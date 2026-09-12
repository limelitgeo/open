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

// Perplexity, through the OpenAI-compatible chat completions endpoint.
//
// Perplexity is always grounded: the Sonar models search before answering and
// there is no switch to turn that off, so the online flag on a target is
// accepted and ignored rather than being an error. Saying "this provider does
// not support offline" would suggest offline is a thing you could ask for.
//
// It returns the cleanest citations of any provider here: search_results
// carries a real URL and a real page title for every source, needing no
// reconstruction. Shapes probed against the live API on 2026-09-11.

const (
	// PerplexityEndpoint is the chat completions URL.
	PerplexityEndpoint = "https://api.perplexity.ai/chat/completions"

	// PerplexityDefaultModel is the cheapest grounded Sonar model, which is
	// the right default for a tool that asks the same prompts every day.
	PerplexityDefaultModel = "sonar"

	// perplexityTestTokens is the API's own floor for max_tokens.
	perplexityTestTokens = 16

	// perplexityMaxTokens bounds the answer. A truncated answer loses the
	// brands named at its end, so a response that stops on length is
	// rejected rather than stored.
	perplexityMaxTokens = 1500
)

type perplexityProvider struct {
	apiKey   string
	model    string
	endpoint string
	client   *http.Client
}

// NewPerplexity builds the provider.
func NewPerplexity(apiKey string) (Provider, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("%w: PERPLEXITY_API_KEY is not set", ErrAuth)
	}
	return &perplexityProvider{
		apiKey:   strings.TrimSpace(apiKey),
		model:    PerplexityDefaultModel,
		endpoint: PerplexityEndpoint,
		client:   &http.Client{Timeout: 2 * time.Minute},
	}, nil
}

func (p *perplexityProvider) Name() string   { return "perplexity" }
func (p *perplexityProvider) Access() Access { return AccessAPI }

func (p *perplexityProvider) Engines() map[string]string {
	return map[string]string{PerplexityEngine: PerplexityDefaultModel}
}

type perplexityRequest struct {
	Model     string             `json:"model"`
	Messages  []anthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens,omitempty"`
}

type perplexityResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	// Citations is the legacy bare-URL list, kept as a fallback.
	Citations []string `json:"citations"`
	// SearchResults is the richer list, with a title per source.
	SearchResults []struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	} `json:"search_results"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Run asks Perplexity one prompt.
func (p *perplexityProvider) Run(ctx context.Context, req Request) (Response, error) {
	if req.Engine != PerplexityEngine {
		return Response{}, fmt.Errorf("%w: perplexity reaches perplexity, not %s", ErrUnsupportedEngine, req.Engine)
	}
	model := req.Model
	if model == "" {
		model = p.model
	}

	raw, err := postJSON(ctx, p.client, p.endpoint, map[string]string{
		"Authorization": "Bearer " + p.apiKey,
	}, perplexityRequest{
		Model:     model,
		Messages:  []anthropicMessage{{Role: "user", Content: req.Prompt}},
		MaxTokens: perplexityMaxTokens,
	}, "perplexity")
	if err != nil {
		return Response{}, err
	}

	var parsed perplexityResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, fmt.Errorf("perplexity: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Response{}, fmt.Errorf("perplexity: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return Response{}, fmt.Errorf("perplexity: no choices in the response")
	}
	choice := parsed.Choices[0]
	if choice.FinishReason == "length" {
		return Response{}, fmt.Errorf("perplexity: answer hit the %d token ceiling and would be missing its ending", perplexityMaxTokens)
	}
	if strings.TrimSpace(choice.Message.Content) == "" {
		return Response{}, fmt.Errorf("perplexity: no answer text in a %q response", choice.FinishReason)
	}

	var (
		citations []Citation
		seen      = map[string]bool{}
	)
	for _, r := range parsed.SearchResults {
		if r.URL == "" || seen[r.URL] {
			continue
		}
		seen[r.URL] = true
		citations = append(citations, Citation{URL: r.URL, Title: r.Title, Position: len(citations) + 1})
	}
	// Older responses carry only the bare URL list. Reading it after
	// search_results rather than instead of it means a shape change on
	// Perplexity's side degrades the titles rather than losing the sources.
	for _, u := range parsed.Citations {
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		citations = append(citations, Citation{URL: u, Position: len(citations) + 1})
	}

	return Response{
		Text:  choice.Message.Content,
		Model: parsed.Model,
		// Perplexity does not expose the searches it ran, so fan-out stays
		// empty here. That is an absence of evidence, not evidence it ran
		// none: it is always grounded.
		Citations:    citations,
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
		Calls:        1,
	}, nil
}

// Test proves the key with the smallest completion the API will accept.
// Perplexity has no free models endpoint, so this is the cheapest call
// available and it does cost a fraction of a cent.
//
// perplexityTestTokens is 16 because the API refuses anything smaller:
// "max_tokens must be at least 16". Probed 2026-09-11.
func (p *perplexityProvider) Test(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	_, err := postJSON(ctx, p.client, p.endpoint, map[string]string{
		"Authorization": "Bearer " + p.apiKey,
	}, perplexityRequest{
		Model:     p.model,
		Messages:  []anthropicMessage{{Role: "user", Content: "ping"}},
		MaxTokens: perplexityTestTokens,
	}, "perplexity")
	return err
}

// postJSON sends one JSON request, retrying once on a transient failure, and
// maps the status onto the typed errors the runner branches on.
//
// Shared by the providers whose auth is a single header. The runner owns
// backoff, concurrency and the daily ceiling, so this stops at one retry.
func postJSON(ctx context.Context, client *http.Client, url string, headers map[string]string, body any, who string) ([]byte, error) {
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

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(encoded)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("content-type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", who, err)
			continue
		}
		payload, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("%s: read response: %w", who, readErr)
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return payload, nil
		}

		err = httpStatusError(who, resp.StatusCode, string(payload))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = err
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

// httpStatusError maps an HTTP status onto the typed errors the runner reads.
func httpStatusError(who string, status int, body string) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return fmt.Errorf("%w: %s rejected the key", ErrAuth, who)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", ErrRateLimited, who)
	case status >= 500:
		return fmt.Errorf("%w: %s returned %d", ErrRateLimited, who, status)
	default:
		return fmt.Errorf("%s: http %d%s", who, status, truncateBody(body))
	}
}
