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

// Google AI Studio, through the Gemini API with the google_search tool.
//
// AI Studio rather than Vertex deliberately. Both reach the same models, but
// Vertex wants a Google Cloud project, an enabled API, a service account and
// a downloaded JSON key, which is four steps and a billing account before a
// marketer sees a number. AI Studio is one key from one page. An open-source
// tool that a person cannot start is not an open-source tool.
//
// ONE IMPORTANT QUIRK, and it would be a silent measurement bug if missed.
// Gemini does not return the source URL. groundingChunks[].web.uri is a
// vertexaisearch.cloud.google.com redirect that resolves only for a while,
// and the actual identity of the source is in web.title, which carries the
// bare domain ("zapier.com"). Storing the redirect would classify every
// citation from every Gemini answer as one Google domain: your own site would
// never be recognised as cited, and one host would dominate every source
// list. So the domain is promoted to the URL, and the redirect is dropped.
//
// The cost of that is real and worth stating: Gemini citations are domain
// grained, not page grained. You learn that zapier.com was cited, not which
// page. Probed against the live API on 2026-09-11.

const (
	// GoogleEndpointTemplate is the generateContent URL, model interpolated.
	GoogleEndpointTemplate = "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent"

	// GoogleDefaultModel is the fast, cheap Gemini that AI Studio serves.
	GoogleDefaultModel = "gemini-2.5-flash"

	// googleRedirectHost is the wrapper every grounding uri points at.
	googleRedirectHost = "vertexaisearch.cloud.google.com"
)

type googleProvider struct {
	apiKey   string
	model    string
	endpoint string
	client   *http.Client
}

// NewGoogle builds the provider.
func NewGoogle(apiKey string) (Provider, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("%w: GOOGLE_API_KEY is not set", ErrAuth)
	}
	return &googleProvider{
		apiKey:   strings.TrimSpace(apiKey),
		model:    GoogleDefaultModel,
		endpoint: GoogleEndpointTemplate,
		client:   &http.Client{Timeout: 2 * time.Minute},
	}, nil
}

func (p *googleProvider) Name() string   { return "google" }
func (p *googleProvider) Access() Access { return AccessAPI }

func (p *googleProvider) Engines() map[string]string {
	return map[string]string{GeminiEngine: GoogleDefaultModel}
}

type googleRequest struct {
	Contents []googleContent  `json:"contents"`
	Tools    []map[string]any `json:"tools,omitempty"`
}

type googleContent struct {
	Parts []googlePart `json:"parts"`
}

type googlePart struct {
	Text string `json:"text"`
}

type googleResponse struct {
	ModelVersion string `json:"modelVersion"`
	Candidates   []struct {
		FinishReason string        `json:"finishReason"`
		Content      googleContent `json:"content"`
		Grounding    struct {
			WebSearchQueries []string `json:"webSearchQueries"`
			GroundingChunks  []struct {
				Web struct {
					URI   string `json:"uri"`
					Title string `json:"title"`
				} `json:"web"`
			} `json:"groundingChunks"`
		} `json:"groundingMetadata"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Run asks Gemini one prompt.
func (p *googleProvider) Run(ctx context.Context, req Request) (Response, error) {
	if req.Engine != GeminiEngine {
		return Response{}, fmt.Errorf("%w: google reaches gemini, not %s", ErrUnsupportedEngine, req.Engine)
	}
	model := req.Model
	if model == "" {
		model = p.model
	}

	body := googleRequest{Contents: []googleContent{{Parts: []googlePart{{Text: req.Prompt}}}}}
	if req.Online {
		body.Tools = []map[string]any{{"google_search": map[string]any{}}}
	}

	// The key rides in a header rather than the query string so it cannot
	// end up in a proxy log or an error message that quotes the URL.
	raw, err := postJSON(ctx, p.client, fmt.Sprintf(p.endpoint, url.PathEscape(model)), map[string]string{
		"x-goog-api-key": p.apiKey,
	}, body, "google")
	if err != nil {
		return Response{}, err
	}

	var parsed googleResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, fmt.Errorf("google: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Response{}, fmt.Errorf("google: %s", parsed.Error.Message)
	}
	if len(parsed.Candidates) == 0 {
		return Response{}, fmt.Errorf("google: no candidates in the response")
	}
	cand := parsed.Candidates[0]
	if cand.FinishReason == "MAX_TOKENS" {
		return Response{}, fmt.Errorf("google: answer hit the token ceiling and would be missing its ending")
	}

	var text strings.Builder
	for _, part := range cand.Content.Parts {
		text.WriteString(part.Text)
	}
	if strings.TrimSpace(text.String()) == "" {
		return Response{}, fmt.Errorf("google: no answer text in a %q response", cand.FinishReason)
	}

	var (
		citations []Citation
		seen      = map[string]bool{}
	)
	for _, chunk := range cand.Grounding.GroundingChunks {
		site := googleSourceURL(chunk.Web.Title, chunk.Web.URI)
		if site == "" || seen[site] {
			continue
		}
		seen[site] = true
		citations = append(citations, Citation{
			URL:      site,
			Title:    chunk.Web.Title,
			Position: len(citations) + 1,
		})
	}

	return Response{
		Text:         text.String(),
		Model:        parsed.ModelVersion,
		Citations:    citations,
		FanOut:       dedupeQueries(cand.Grounding.WebSearchQueries),
		InputTokens:  parsed.UsageMetadata.PromptTokenCount,
		OutputTokens: parsed.UsageMetadata.CandidatesTokenCount,
		Calls:        1,
	}, nil
}

// googleSourceURL turns a grounding chunk into something the citation
// classifier can read.
//
// title is a bare domain in every response observed, so it becomes the URL.
// If it ever is not a domain, the redirect is used instead: a citation
// attributed to Google is wrong, but it is visibly wrong, which beats
// silently dropping a source.
func googleSourceURL(title, uri string) string {
	title = strings.TrimSpace(strings.ToLower(title))
	if looksLikeDomain(title) {
		return "https://" + title
	}
	if strings.Contains(uri, googleRedirectHost) || uri == "" {
		return uri
	}
	return uri
}

// looksLikeDomain is deliberately strict: a hostname, nothing else. A page
// title that happens to contain a dot must not be turned into a URL.
func looksLikeDomain(s string) bool {
	if s == "" || strings.ContainsAny(s, " /\\?#@:") {
		return false
	}
	dot := strings.LastIndex(s, ".")
	if dot <= 0 || dot == len(s)-1 {
		return false
	}
	for _, r := range s[dot+1:] {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return len(s)-dot-1 >= 2
}

func dedupeQueries(in []string) []string {
	var (
		out  []string
		seen = map[string]bool{}
	)
	for _, q := range in {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] {
			continue
		}
		seen[q] = true
		out = append(out, q)
	}
	return out
}

// Test proves the key by listing models, which costs no tokens.
func (p *googleProvider) Test(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://generativelanguage.googleapis.com/v1beta/models", nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-goog-api-key", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("google: %w", err)
	}
	defer resp.Body.Close()

	return httpStatusError("google", resp.StatusCode, "")
}
