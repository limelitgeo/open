// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SearchApi, for Bing Copilot and a second read on the Google AI surfaces.
//
// The reason this provider exists is Bing Copilot, which nothing else in this
// build can reach. It also covers Google AI Overview and AI Mode, which
// DataForSEO already covers, and that overlap is useful rather than wasteful:
// two scrapers reading the same surface and disagreeing is a signal about the
// surface, not noise. Both are scraped targets, both are labelled, and
// neither is averaged with an API answer.
//
// Shapes probed against the live API on 2026-09-12.

const (
	// SearchApiEndpoint is the single search URL; the engine is a parameter.
	SearchApiEndpoint = "https://www.searchapi.io/api/v1/search"

	// The vendor's own engine ids, which are not this project's engine ids.
	searchApiAIMode     = "google_ai_mode"
	searchApiCopilot    = "bing_copilot"
	searchApiAIOverview = "google_ai_overview"
	searchApiGoogle     = "google"
	searchApiStatusOK   = "Success"
)

type searchApiProvider struct {
	apiKey   string
	endpoint string
	client   *http.Client
}

// NewSearchApi builds the provider.
func NewSearchApi(apiKey string) (Provider, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("%w: SEARCHAPI_KEY is not set", ErrAuth)
	}
	return &searchApiProvider{
		apiKey:   strings.TrimSpace(apiKey),
		endpoint: SearchApiEndpoint,
		// A live scrape renders a real page, so it is slow.
		client: &http.Client{Timeout: 3 * time.Minute},
	}, nil
}

func (p *searchApiProvider) Name() string   { return "searchapi" }
func (p *searchApiProvider) Access() Access { return AccessScraped }

// Engines maps to the empty string because a scraped surface has no model to
// choose.
func (p *searchApiProvider) Engines() map[string]string {
	return map[string]string{
		AIOverviewEngine:  "",
		AIModeEngine:      "",
		BingCopilotEngine: "",
	}
}

// searchApiAnswer is the shape every answering engine returns.
type searchApiAnswer struct {
	Metadata struct {
		Status string `json:"status"`
	} `json:"search_metadata"`
	Markdown   string `json:"markdown"`
	TextBlocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"text_blocks"`
	ReferenceLinks []struct {
		Index int    `json:"index"`
		Title string `json:"title"`
		Link  string `json:"link"`
	} `json:"reference_links"`
	// AIOverview appears on a plain google search and carries either the
	// answer or the token needed to fetch it.
	AIOverview *struct {
		Error     string `json:"error"`
		PageToken string `json:"page_token"`
	} `json:"ai_overview"`
	Error any `json:"error"`
}

// Run fetches one answer surface for one query.
func (p *searchApiProvider) Run(ctx context.Context, req Request) (Response, error) {
	switch req.Engine {
	case AIModeEngine:
		return p.fetchAnswer(ctx, req, searchApiAIMode, 1)
	case BingCopilotEngine:
		return p.fetchAnswer(ctx, req, searchApiCopilot, 1)
	case AIOverviewEngine:
		return p.fetchOverview(ctx, req)
	default:
		return Response{}, fmt.Errorf("%w: searchapi reaches ai_overview, ai_mode and bing_copilot, not %s",
			ErrUnsupportedEngine, req.Engine)
	}
}

// fetchOverview handles Google AI Overview, which takes two billable calls.
//
// A plain google search returns an ai_overview block carrying a page_token,
// and the overview itself is fetched with that token. Both calls are counted,
// because a user watching their bill should see what this actually costs.
//
// The block's own error text is not authoritative: probed 2026-09-12 it read
// "An AI Overview is not available for this search" and handed over a token
// that then resolved a 7,000 character answer. So the token is tried whenever
// one is present, and no-answer-surface is decided by what comes back.
func (p *searchApiProvider) fetchOverview(ctx context.Context, req Request) (Response, error) {
	probe, err := p.get(ctx, req, map[string]string{"engine": searchApiGoogle})
	if err != nil {
		return Response{}, err
	}
	if probe.AIOverview == nil || probe.AIOverview.PageToken == "" {
		return Response{}, fmt.Errorf("%w: google showed no AI Overview for this query", ErrNoAnswerSurface)
	}

	answer, err := p.get(ctx, req, map[string]string{
		"engine":     searchApiAIOverview,
		"page_token": probe.AIOverview.PageToken,
	})
	if err != nil {
		return Response{}, err
	}
	return p.buildResponse(answer, req.Engine, 2)
}

// fetchAnswer handles the engines that answer in a single call.
func (p *searchApiProvider) fetchAnswer(ctx context.Context, req Request, engine string, calls int) (Response, error) {
	answer, err := p.get(ctx, req, map[string]string{"engine": engine})
	if err != nil {
		return Response{}, err
	}
	return p.buildResponse(answer, req.Engine, calls)
}

func (p *searchApiProvider) buildResponse(a *searchApiAnswer, engine string, calls int) (Response, error) {
	if a.Metadata.Status != "" && a.Metadata.Status != searchApiStatusOK {
		return Response{}, fmt.Errorf("searchapi: %s", a.Metadata.Status)
	}

	text := strings.TrimSpace(a.Markdown)
	if text == "" {
		var parts []string
		for _, b := range a.TextBlocks {
			if s := strings.TrimSpace(b.Text); s != "" {
				parts = append(parts, s)
			}
		}
		text = strings.Join(parts, "\n\n")
	}
	if strings.TrimSpace(text) == "" {
		// The call succeeded and the surface carried nothing. Not a brand
		// miss: nothing rendered, so nothing could have named anyone.
		return Response{}, fmt.Errorf("%w: the %s surface carried no text", ErrNoAnswerSurface, engine)
	}

	var (
		citations []Citation
		seen      = map[string]bool{}
	)
	for _, r := range a.ReferenceLinks {
		link := strings.TrimSpace(r.Link)
		if link == "" || seen[link] {
			continue
		}
		seen[link] = true
		citations = append(citations, Citation{URL: link, Title: r.Title, Position: len(citations) + 1})
	}

	return Response{
		Text: text,
		// A scraped surface has no model to report, and inventing one would
		// suggest a choice was made about which model answered.
		Model:     "",
		Citations: citations,
		Calls:     calls,
	}, nil
}

// get performs one call and decodes it.
func (p *searchApiProvider) get(ctx context.Context, req Request, params map[string]string) (*searchApiAnswer, error) {
	values := url.Values{}
	for k, v := range params {
		values.Set(k, v)
	}
	// A page_token call carries the query inside the token, and sending q
	// alongside it is rejected.
	if _, tokenCall := params["page_token"]; !tokenCall {
		values.Set("q", req.Prompt)
		if c := strings.TrimSpace(req.LocationCountry); c != "" {
			values.Set("gl", strings.ToLower(c))
		}
		if l := strings.TrimSpace(req.LanguageCode); l != "" {
			values.Set("hl", strings.ToLower(l))
		}
	}

	raw, err := p.fetch(ctx, p.endpoint+"?"+values.Encode())
	if err != nil {
		return nil, err
	}
	var answer searchApiAnswer
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, fmt.Errorf("searchapi: decode response: %w", err)
	}
	// The error field is sometimes a string and sometimes an object, so it is
	// read as any and only reported when it carries something.
	if msg := searchApiErrorText(answer.Error); msg != "" {
		return nil, fmt.Errorf("searchapi: %s", msg)
	}
	return &answer, nil
}

// fetch performs one GET, retrying once on a transient failure.
func (p *searchApiProvider) fetch(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+p.apiKey)

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("searchapi: %w", err)
			continue
		}
		payload, readErr := readLimited(resp)
		if readErr != nil {
			lastErr = fmt.Errorf("searchapi: read response: %w", readErr)
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return payload, nil
		}

		err = httpStatusError("searchapi", resp.StatusCode, string(payload))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = err
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

// searchApiErrorText normalises the error field, which the API returns as a
// string in some shapes and as an object in others.
func searchApiErrorText(v any) string {
	switch e := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(e)
	case map[string]any:
		if msg, ok := e["message"].(string); ok {
			return strings.TrimSpace(msg)
		}
		b, _ := json.Marshal(e)
		return string(b)
	default:
		return ""
	}
}

// Test proves the key with the cheapest call the API offers.
func (p *searchApiProvider) Test(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	values := url.Values{}
	values.Set("engine", searchApiGoogle)
	values.Set("q", "ping")
	values.Set("num", "1")

	_, err := p.fetch(ctx, p.endpoint+"?"+values.Encode())
	return err
}
