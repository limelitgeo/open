// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// DataForSEO, for the two Google surfaces that have no API at all.
//
// Google AI Overview and AI Mode cannot be reached any other way. There is no
// vendor endpoint to call, no key to buy from Google, and no supported way to
// ask for the answer a person sees. A scraping vendor is the only route, and
// that is why this provider exists even though every other one here is a
// direct API.
//
// This is the first provider whose Access is scraped, and the distinction is
// load bearing: an answer from here is what a person actually sees on a
// Google results page, which is a different measurement from a model API with
// web search turned on. The two are never averaged.
//
// THE MOST IMPORTANT BEHAVIOUR IN THIS FILE is what happens when Google shows
// no AI Overview for a query. That is common and it is not a miss for the
// brand: nothing rendered, so nothing could have named anyone. It returns
// ErrNoAnswerSurface, and the runner records a chat that every metric
// denominator excludes. Treating it as an answer that ignored the brand would
// drag visibility toward zero for reasons that have nothing to do with the
// brand, and it would do so most for exactly the queries where an overview is
// rare.
//
// Shapes probed against the live API on 2026-09-11.

const (
	// dataForSEOOrganicEndpoint carries the AI Overview inside a normal
	// results page, which is where a person meets it.
	dataForSEOOrganicEndpoint = "https://api.dataforseo.com/v3/serp/google/organic/live/advanced"
	// dataForSEOAIModeEndpoint is the dedicated AI Mode surface.
	dataForSEOAIModeEndpoint = "https://api.dataforseo.com/v3/serp/google/ai_mode/live/advanced"
	// dataForSEOUserEndpoint proves credentials and costs nothing.
	dataForSEOUserEndpoint = "https://api.dataforseo.com/v3/appendix/user_data"

	// dataForSEODefaultLocation is the United States. DataForSEO location
	// codes for countries are 2000 plus the ISO 3166-1 numeric code.
	dataForSEODefaultLocation = 2840

	// dataForSEOOK is the vendor's own success code, which it returns inside
	// a 200 response. An HTTP 200 here does not mean the task succeeded.
	dataForSEOOK = 20000
)

type dataForSEOProvider struct {
	auth     string
	organic  string
	aiMode   string
	userInfo string
	client   *http.Client
}

// NewDataForSEO builds the provider from a login and a password, which is the
// pair DataForSEO issues instead of a single API key.
func NewDataForSEO(login, password string) (Provider, error) {
	login, password = strings.TrimSpace(login), strings.TrimSpace(password)
	if login == "" || password == "" {
		return nil, fmt.Errorf("%w: DATAFORSEO_LOGIN and DATAFORSEO_PASSWORD must both be set", ErrAuth)
	}
	return &dataForSEOProvider{
		auth:     base64.StdEncoding.EncodeToString([]byte(login + ":" + password)),
		organic:  dataForSEOOrganicEndpoint,
		aiMode:   dataForSEOAIModeEndpoint,
		userInfo: dataForSEOUserEndpoint,
		// A live SERP call fetches a real results page, so it is slow.
		client: &http.Client{Timeout: 3 * time.Minute},
	}, nil
}

func (p *dataForSEOProvider) Name() string   { return "dataforseo" }
func (p *dataForSEOProvider) Access() Access { return AccessScraped }

// Engines maps to the empty string because a scraped surface has no model to
// choose. A target that pins one is rejected at parse time.
func (p *dataForSEOProvider) Engines() map[string]string {
	return map[string]string{AIOverviewEngine: "", AIModeEngine: ""}
}

type dataForSEOTask struct {
	Keyword      string `json:"keyword"`
	LocationCode int    `json:"location_code"`
	LanguageCode string `json:"language_code"`
	Device       string `json:"device,omitempty"`
	// LoadAsyncAIOverview asks DataForSEO to wait for an overview that Google
	// renders in a second request. Without it a rendered overview is
	// frequently reported as absent, which would read as a brand miss.
	LoadAsyncAIOverview bool `json:"load_async_ai_overview,omitempty"`
}

type dataForSEOEnvelope struct {
	StatusCode    int    `json:"status_code"`
	StatusMessage string `json:"status_message"`
	Tasks         []struct {
		StatusCode    int    `json:"status_code"`
		StatusMessage string `json:"status_message"`
		Result        []struct {
			// Items stays raw because a results page mixes shapes: a
			// related_searches item carries items as a list of strings while
			// an ai_overview carries objects. Decoding every item into one
			// struct fails on the first page that has both.
			Items []json.RawMessage `json:"items"`
		} `json:"result"`
	} `json:"tasks"`
}

type dataForSEOItem struct {
	Type     string `json:"type"`
	Markdown string `json:"markdown"`
	Text     string `json:"text"`
	Items    []struct {
		Type       string                `json:"type"`
		Text       string                `json:"text"`
		References []dataForSEOReference `json:"references"`
	} `json:"items"`
	References []dataForSEOReference `json:"references"`
}

type dataForSEOReference struct {
	Source string `json:"source"`
	Domain string `json:"domain"`
	URL    string `json:"url"`
	Title  string `json:"title"`
}

// Run fetches one Google AI surface for one query.
func (p *dataForSEOProvider) Run(ctx context.Context, req Request) (Response, error) {
	var endpoint string
	switch req.Engine {
	case AIOverviewEngine:
		endpoint = p.organic
	case AIModeEngine:
		endpoint = p.aiMode
	default:
		return Response{}, fmt.Errorf("%w: dataforseo reaches ai_overview and ai_mode, not %s", ErrUnsupportedEngine, req.Engine)
	}

	location, err := dataForSEOLocation(req.LocationCountry)
	if err != nil {
		return Response{}, err
	}
	language := strings.TrimSpace(strings.ToLower(req.LanguageCode))
	if language == "" {
		language = "en"
	}

	task := dataForSEOTask{
		Keyword:      req.Prompt,
		LocationCode: location,
		LanguageCode: language,
	}
	if req.Engine == AIOverviewEngine {
		task.Device = "desktop"
		task.LoadAsyncAIOverview = true
	}

	// The API takes an array of tasks. One at a time here: batching would
	// make a single failure ambiguous across several prompts.
	raw, err := postJSON(ctx, p.client, endpoint, map[string]string{
		"Authorization": "Basic " + p.auth,
	}, []dataForSEOTask{task}, "dataforseo")
	if err != nil {
		return Response{}, err
	}

	var parsed dataForSEOEnvelope
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, fmt.Errorf("dataforseo: decode response: %w", err)
	}
	// A 200 with a failure code inside is the normal way this API reports a
	// problem, so the envelope is checked rather than the HTTP status.
	if parsed.StatusCode != dataForSEOOK {
		return Response{}, dataForSEOEnvelopeError(parsed.StatusCode, parsed.StatusMessage)
	}
	if len(parsed.Tasks) == 0 {
		return Response{}, fmt.Errorf("dataforseo: no task in the response")
	}
	t := parsed.Tasks[0]
	if t.StatusCode != dataForSEOOK {
		return Response{}, dataForSEOEnvelopeError(t.StatusCode, t.StatusMessage)
	}

	overview := findAIOverview(t.Result)
	if overview == nil {
		// The page rendered and carried no AI surface. Not a brand miss.
		return Response{}, fmt.Errorf("%w: google showed no %s for this query", ErrNoAnswerSurface, req.Engine)
	}

	text := strings.TrimSpace(overview.Markdown)
	if text == "" {
		var parts []string
		for _, el := range overview.Items {
			if s := strings.TrimSpace(el.Text); s != "" {
				parts = append(parts, s)
			}
		}
		text = strings.Join(parts, "\n\n")
	}
	if strings.TrimSpace(text) == "" {
		// An overview block with no readable text is not an answer either.
		return Response{}, fmt.Errorf("%w: the %s block carried no text", ErrNoAnswerSurface, req.Engine)
	}

	return Response{
		Text: text,
		// A scraped surface has no model to report. Leaving this empty is the
		// honest answer; inventing "google" would suggest a model was chosen.
		Model:     "",
		Citations: dataForSEOCitations(overview),
		// Google does not publish the searches behind an AI Overview.
		Calls: 1,
	}, nil
}

// dataForSEOCitations collects the references, top level first and then the
// per-element ones, deduplicated by URL and renumbered.
func dataForSEOCitations(item *dataForSEOItem) []Citation {
	var (
		out  []Citation
		seen = map[string]bool{}
	)
	add := func(refs []dataForSEOReference) {
		for _, r := range refs {
			url := strings.TrimSpace(r.URL)
			if url == "" || seen[url] {
				continue
			}
			seen[url] = true
			title := r.Title
			if title == "" {
				title = r.Source
			}
			out = append(out, Citation{URL: url, Title: title, Position: len(out) + 1})
		}
	}
	add(item.References)
	for _, el := range item.Items {
		add(el.References)
	}
	return out
}

// Test proves the credentials with the free account endpoint, so pressing
// Test in Settings costs nothing.
func (p *dataForSEOProvider) Test(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.userInfo, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Basic "+p.auth)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("dataforseo: %w", err)
	}
	defer resp.Body.Close()
	if err := httpStatusError("dataforseo", resp.StatusCode, ""); err != nil {
		return err
	}

	var parsed dataForSEOEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("dataforseo: decode response: %w", err)
	}
	if parsed.StatusCode != dataForSEOOK {
		return dataForSEOEnvelopeError(parsed.StatusCode, parsed.StatusMessage)
	}
	return nil
}

// dataForSEOEnvelopeError maps the vendor's own status code, which arrives
// inside a 200, onto the typed errors the runner reads.
func dataForSEOEnvelopeError(code int, message string) error {
	switch code {
	case 40100, 40101, 40200:
		return fmt.Errorf("%w: dataforseo (%d %s)", ErrAuth, code, message)
	case 40202, 40203, 40204:
		// Out of funds. Retrying will not help and the message says why.
		return fmt.Errorf("%w: dataforseo (%d %s)", ErrAuth, code, message)
	case 40429, 50401:
		return fmt.Errorf("%w: dataforseo (%d %s)", ErrRateLimited, code, message)
	default:
		if code >= 50000 {
			return fmt.Errorf("%w: dataforseo (%d %s)", ErrRateLimited, code, message)
		}
		return fmt.Errorf("dataforseo: %d %s", code, message)
	}
}

// dataForSEOCountries maps ISO 3166-1 alpha-2 onto DataForSEO location codes,
// which are 2000 plus the ISO 3166-1 numeric code.
//
// Only the markets a user is likely to track are listed. An unknown country
// is an error rather than a silent fall back to the United States: measuring
// the wrong country and labelling it the right one is exactly the kind of
// quiet wrongness this project exists to avoid.
var dataForSEOCountries = map[string]int{
	"US": 2840, "GB": 2826, "CA": 2124, "AU": 2036, "NZ": 2554, "IE": 2372,
	"DE": 2276, "FR": 2250, "ES": 2724, "IT": 2380, "NL": 2528, "BE": 2056,
	"CH": 2756, "AT": 2040, "SE": 2752, "NO": 2578, "DK": 2208, "FI": 2246,
	"PL": 2616, "PT": 2620, "CZ": 2203, "GR": 2300, "RO": 2642,
	"IN": 2356, "SG": 2702, "JP": 2392, "KR": 2410, "HK": 2344, "MY": 2458,
	"PH": 2608, "TH": 2764, "ID": 2360, "VN": 2704,
	"BR": 2076, "MX": 2484, "AR": 2032, "CL": 2152, "CO": 2170,
	"ZA": 2710, "AE": 2784, "SA": 2682, "IL": 2376, "TR": 2792,
}

// dataForSEOLocation resolves the target country, defaulting to the United
// States when none was asked for.
func dataForSEOLocation(country string) (int, error) {
	country = strings.ToUpper(strings.TrimSpace(country))
	if country == "" {
		return dataForSEODefaultLocation, nil
	}
	if code, ok := dataForSEOCountries[country]; ok {
		return code, nil
	}
	known := make([]string, 0, len(dataForSEOCountries))
	for c := range dataForSEOCountries {
		known = append(known, c)
	}
	sort.Strings(known)
	return 0, fmt.Errorf("dataforseo: no location for country %q; known: %s", country, strings.Join(known, " "))
}

// findAIOverview picks the AI surface out of a mixed results page.
//
// Each item is probed for its type before being decoded, so a shape this
// provider does not understand is skipped rather than failing the whole
// answer. A results page carries organic results, people-also-ask and related
// searches alongside the overview, and their shapes differ.
func findAIOverview(results []struct {
	Items []json.RawMessage `json:"items"`
}) *dataForSEOItem {
	for _, result := range results {
		for _, raw := range result.Items {
			var probe struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(raw, &probe); err != nil || probe.Type != "ai_overview" {
				continue
			}
			var item dataForSEOItem
			if err := json.Unmarshal(raw, &item); err != nil {
				continue
			}
			return &item
		}
	}
	return nil
}
