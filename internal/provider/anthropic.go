// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Anthropic, through the Messages API with the server-side web_search tool.
//
// Claude is the one engine in this project that can only be reached by API.
// There is no anonymous consumer surface to scrape: claude.ai requires an
// account for every conversation, so no scraping vendor sells it and none
// ever will. Every other engine here can be measured both ways; this one
// cannot, and a target that asks for scraped claude is rejected at parse time
// rather than quietly answered with an API call.
//
// Shapes below were probed against the live API on 2026-09-11 rather than
// taken from documentation, because the failure mode of guessing is a
// provider that returns an empty answer and reports the brand as absent.

const (
	// AnthropicEndpoint is the Messages API URL.
	AnthropicEndpoint = "https://api.anthropic.com/v1/messages"
	// anthropicModelsEndpoint proves a key without spending tokens.
	anthropicModelsEndpoint = "https://api.anthropic.com/v1/models"

	// anthropicVersion is the dated API contract, not a model version. It
	// pins the request and response shape this file parses.
	anthropicVersion = "2023-06-01"

	// anthropicSearchTool is the dated server-side web search tool. Unlike a
	// model alias this one is a wire contract, so it is pinned: a new dated
	// tool is a new shape, and picking it up silently would change what
	// citations look like.
	anthropicSearchTool = "web_search_20250305"

	// AnthropicDefaultModel is the current Sonnet alias. Verified present in
	// GET /v1/models on 2026-09-11. The same trade as OpenAI applies and is
	// argued there: the unversioned alias drifts rather than breaking, and
	// every answer stores the model the API reported so drift stays visible.
	AnthropicDefaultModel = "claude-sonnet-5"

	// anthropicMaxTokens has to cover thinking, the search calls, and then
	// the answer. Probed at 1024 the answer stopped on max_tokens mid
	// sentence, and a truncated answer silently loses the brand mentions
	// near its end, which reads as a brand that was not talked about. This
	// is set well clear of that, and a response that still truncates is
	// rejected rather than stored.
	anthropicMaxTokens = 6000

	// anthropicMaxSearches bounds what one answer can spend on grounding.
	anthropicMaxSearches = 5
)

type anthropicProvider struct {
	apiKey   string
	model    string
	endpoint string
	models   string
	client   *http.Client
}

// NewAnthropic builds the provider. A missing key is ErrAuth, so a missing
// key and a wrong key report the same way.
func NewAnthropic(apiKey string) (Provider, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("%w: ANTHROPIC_API_KEY is not set", ErrAuth)
	}
	return &anthropicProvider{
		apiKey:   strings.TrimSpace(apiKey),
		model:    AnthropicDefaultModel,
		endpoint: AnthropicEndpoint,
		models:   anthropicModelsEndpoint,
		client:   &http.Client{Timeout: 3 * time.Minute},
	}, nil
}

func (p *anthropicProvider) Name() string   { return "anthropic" }
func (p *anthropicProvider) Access() Access { return AccessAPI }

func (p *anthropicProvider) Engines() map[string]string {
	return map[string]string{ClaudeEngine: AnthropicDefaultModel}
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicTool struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	MaxUses int    `json:"max_uses,omitempty"`
}

type anthropicResponse struct {
	Model      string             `json:"model"`
	StopReason string             `json:"stop_reason"`
	Content    []anthropicContent `json:"content"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
	// Input carries the search query on a server_tool_use block. That is
	// query fan-out: the searches the model ran while grounding.
	Input struct {
		Query string `json:"query"`
	} `json:"input"`
	Citations []struct {
		Type  string `json:"type"`
		URL   string `json:"url"`
		Title string `json:"title"`
	} `json:"citations"`
}

// Run asks Claude one prompt.
func (p *anthropicProvider) Run(ctx context.Context, req Request) (Response, error) {
	if req.Engine != ClaudeEngine {
		return Response{}, fmt.Errorf("%w: anthropic reaches claude, not %s", ErrUnsupportedEngine, req.Engine)
	}
	model := req.Model
	if model == "" {
		model = p.model
	}

	body := anthropicRequest{
		Model:     model,
		MaxTokens: anthropicMaxTokens,
		Messages:  []anthropicMessage{{Role: "user", Content: req.Prompt}},
	}
	if req.Online {
		body.Tools = []anthropicTool{{Type: anthropicSearchTool, Name: "web_search", MaxUses: anthropicMaxSearches}}
	}

	raw, err := p.post(ctx, body)
	if err != nil {
		return Response{}, err
	}

	var parsed anthropicResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, fmt.Errorf("anthropic: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Response{}, fmt.Errorf("anthropic: %s", parsed.Error.Message)
	}

	// A truncated answer is rejected, not stored. Its tail is missing, and
	// the brands named in that tail would be recorded as absent. A failed
	// chat is excluded from every denominator; a truncated one would not be.
	if parsed.StopReason == "max_tokens" {
		return Response{}, fmt.Errorf("anthropic: answer hit the %d token ceiling and would be missing its ending", anthropicMaxTokens)
	}

	text, citations, fanout := readAnthropicContent(parsed.Content)
	if strings.TrimSpace(text) == "" {
		return Response{}, fmt.Errorf("anthropic: no answer text in a %q response", parsed.StopReason)
	}

	return Response{
		Text:         text,
		Model:        parsed.Model,
		Citations:    citations,
		FanOut:       fanout,
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
		Calls:        1,
	}, nil
}

// readAnthropicContent walks the content blocks and separates the answer from
// everything around it.
//
// Claude returns the answer as many small text blocks interleaved with
// thinking, server_tool_use and web_search_tool_result blocks. Only the text
// blocks are the answer. A thinking block concatenated into the answer would
// be searched for brand names and would report mentions the reader never saw,
// which is worse than missing one.
func readAnthropicContent(blocks []anthropicContent) (string, []Citation, []string) {
	var (
		text      strings.Builder
		citations []Citation
		fanout    []string
		seenURL   = map[string]bool{}
		seenQuery = map[string]bool{}
	)
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
			for _, c := range b.Citations {
				if c.URL == "" || seenURL[c.URL] {
					continue
				}
				seenURL[c.URL] = true
				citations = append(citations, Citation{
					URL:      c.URL,
					Title:    c.Title,
					Position: len(citations) + 1,
				})
			}
		case "server_tool_use":
			q := strings.TrimSpace(b.Input.Query)
			if q != "" && !seenQuery[q] {
				seenQuery[q] = true
				fanout = append(fanout, q)
			}
		}
		// thinking and web_search_tool_result blocks are deliberately
		// skipped: neither is the answer the user would have read.
	}
	return text.String(), citations, fanout
}

// Test proves the key with the cheapest authenticated call Anthropic offers.
func (p *anthropicProvider) Test(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.models, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("anthropic: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	return anthropicStatusError(resp.StatusCode, "")
}

// post sends one request, retrying once on a transient failure. The runner
// owns backoff and the daily ceiling, so this stops at one.
func (p *anthropicProvider) post(ctx context.Context, body any) ([]byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, strings.NewReader(string(encoded)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("x-api-key", p.apiKey)
		req.Header.Set("anthropic-version", anthropicVersion)
		req.Header.Set("content-type", "application/json")

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("anthropic: %w", err)
			continue
		}
		payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("anthropic: read response: %w", readErr)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			return payload, nil
		}
		err = anthropicStatusError(resp.StatusCode, string(payload))
		// Only a rate limit or a server fault is worth a second attempt. A
		// rejected key will be rejected again.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = err
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

// anthropicStatusError maps an HTTP status onto the typed errors the runner
// reads.
func anthropicStatusError(code int, body string) error {
	switch {
	case code == http.StatusOK:
		return nil
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("%w: anthropic rejected the key", ErrAuth)
	case code == http.StatusTooManyRequests:
		return fmt.Errorf("%w: anthropic", ErrRateLimited)
	case code >= 500:
		return fmt.Errorf("%w: anthropic returned %d", ErrRateLimited, code)
	default:
		return fmt.Errorf("anthropic: http %d%s", code, truncateBody(body))
	}
}
