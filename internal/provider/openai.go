package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAI, through the Responses API with the built-in web_search tool.
//
// Responses rather than Chat Completions, because it is the only shape that
// returns the answer AND the sources the model attributed, as url_citation
// annotations with offsets into the text. Chat Completions would leave this
// project extracting URLs out of prose with a regular expression, which is
// how a measurement tool starts disagreeing with itself.
//
// What this provider deliberately does NOT read: the web_search_call items,
// which carry the exact searches the model ran while grounding. That is query
// fan-out, and it is a Limelit Cloud feature. Parsing it here and dropping it
// on the floor would invite someone to wire it up by accident.

const (
	// OpenAIEndpoint is the Responses API URL.
	OpenAIEndpoint = "https://api.openai.com/v1/responses"
	// openAIModelsEndpoint is the cheapest authenticated call OpenAI offers,
	// which is what Test uses: it proves the key without spending tokens.
	openAIModelsEndpoint = "https://api.openai.com/v1/models"

	// OpenAIDefaultModel is the alias OpenAI serves in the ChatGPT product,
	// so it is the closest an API call gets to what a person sees.
	//
	// The choice between this and a pinned version is a real trade and it is
	// worth knowing which way it fails. Versioned aliases get retired: probed
	// 2026-09-11, gpt-5.5-chat-latest already returns 404 while plain
	// chat-latest answers. A pin therefore breaks loudly one day, for
	// everyone, until they upgrade. The unversioned alias never 404s; it
	// quietly starts answering as a different model instead, and for a
	// measurement product that reads as a change in the market rather than a
	// change in the instrument.
	//
	// The unversioned alias wins here because a self-hosted install may go
	// months without an update, and a tool that stops working is worse than
	// one that drifts. The drift is made visible rather than hidden: every
	// answer stores the model the API reported, so a step change in the
	// numbers can be checked against a change in that column. Pin a version
	// with a target like chatgpt:openai:gpt-5.5:online when you want the
	// instrument held still.
	OpenAIDefaultModel = "chat-latest"

	// openAIMaxOutputTokens has to cover the answer AND whatever the model
	// spends on tool calls before it. Too low truncates the answer, and a
	// truncated answer silently loses the brand mentions near its end.
	openAIMaxOutputTokens = 2000
)

type openAIProvider struct {
	apiKey   string
	model    string
	endpoint string
	models   string
	client   *http.Client
}

// NewOpenAI builds the provider. A missing key is ErrAuth, so a missing key
// and a wrong key report the same way.
func NewOpenAI(apiKey string) (Provider, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("%w: OPENAI_API_KEY is not set", ErrAuth)
	}
	return &openAIProvider{
		apiKey:   strings.TrimSpace(apiKey),
		model:    OpenAIDefaultModel,
		endpoint: OpenAIEndpoint,
		models:   openAIModelsEndpoint,
		// Web search makes these calls slow: a grounded answer took about
		// forty seconds when this was written. A short timeout would report
		// a working key as broken.
		client: &http.Client{Timeout: 3 * time.Minute},
	}, nil
}

func (p *openAIProvider) Name() string   { return "openai" }
func (p *openAIProvider) Access() Access { return AccessAPI }

func (p *openAIProvider) Engines() map[string]string {
	return map[string]string{ChatGPTEngine: OpenAIDefaultModel}
}

type openAIRequest struct {
	Model           string              `json:"model"`
	Input           string              `json:"input"`
	Tools           []map[string]string `json:"tools,omitempty"`
	MaxOutputTokens int                 `json:"max_output_tokens,omitempty"`
	// Store false keeps the prompt out of OpenAI's dashboard history. A
	// self-hosted tool should not quietly leave a copy of what it asked on
	// somebody else's server.
	Store bool `json:"store"`
}

type openAIResponse struct {
	Model  string            `json:"model"`
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
	Usage  struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

type openAIMessageItem struct {
	Type    string `json:"type"`
	Content []struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Annotations []struct {
			Type  string `json:"type"`
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"annotations"`
	} `json:"content"`
}

// Run asks ChatGPT's model one prompt.
func (p *openAIProvider) Run(ctx context.Context, req Request) (Response, error) {
	if req.Engine != ChatGPTEngine {
		return Response{}, fmt.Errorf("%w: openai reaches chatgpt, not %s", ErrUnsupportedEngine, req.Engine)
	}
	model := req.Model
	if model == "" {
		model = p.model
	}

	body := openAIRequest{
		Model:           model,
		Input:           req.Prompt,
		MaxOutputTokens: openAIMaxOutputTokens,
		Store:           false,
	}
	if req.Online {
		body.Tools = []map[string]string{{"type": "web_search"}}
	}

	raw, err := p.post(ctx, p.endpoint, body)
	if err != nil {
		return Response{}, err
	}

	var parsed openAIResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Response{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Response{}, fmt.Errorf("openai: %s", parsed.Error.Message)
	}

	text, citations := p.readOutput(parsed.Output)
	if strings.TrimSpace(text) == "" {
		// An empty body with a non-terminal status means the answer never
		// arrived. Storing it as an answer would count the brand as absent
		// from something that was never said.
		return Response{}, fmt.Errorf("openai: no answer text in a %q response", parsed.Status)
	}

	return Response{
		Text:         text,
		Model:        parsed.Model,
		Citations:    citations,
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
		Calls:        1,
	}, nil
}

// readOutput pulls the answer and its attributed sources out of the output
// items. Anything that is not a message is skipped, which includes the
// web_search_call items carrying the fan-out.
func (p *openAIProvider) readOutput(items []json.RawMessage) (string, []Citation) {
	var (
		text      strings.Builder
		citations []Citation
		seen      = map[string]bool{}
	)
	for _, item := range items {
		var msg openAIMessageItem
		if err := json.Unmarshal(item, &msg); err != nil || msg.Type != "message" {
			continue
		}
		for _, c := range msg.Content {
			text.WriteString(c.Text)
			for _, a := range c.Annotations {
				if a.Type != "url_citation" || a.URL == "" {
					continue
				}
				// One source cited three times is one source. Counting it
				// three times would inflate every citation share it
				// contributes to.
				if seen[a.URL] {
					continue
				}
				seen[a.URL] = true
				citations = append(citations, Citation{
					URL:      a.URL,
					Title:    a.Title,
					Position: len(citations) + 1,
				})
			}
		}
	}
	return text.String(), citations
}

// Test proves the key with the cheapest authenticated call OpenAI offers, so
// pressing Test in Settings costs nothing.
func (p *openAIProvider) Test(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.models, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("openai: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	return openAIStatusError(resp.StatusCode, "")
}

// post sends one request, retrying once on a transient failure. The runner
// owns backoff and the daily ceiling, so this stops at one.
func (p *openAIProvider) post(ctx context.Context, url string, body any) ([]byte, error) {
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

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("openai: %w", err)
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("openai: read response: %w", readErr)
			continue
		}

		if err := openAIStatusError(resp.StatusCode, string(raw)); err != nil {
			// Auth and a rejected request will fail again identically, so
			// only a rate limit or a server fault is worth a second try.
			if errors.Is(err, ErrRateLimited) || resp.StatusCode >= 500 {
				lastErr = err
				continue
			}
			return nil, err
		}
		return raw, nil
	}
	return nil, lastErr
}

// openAIStatusError maps an HTTP status onto the typed errors the runner
// branches on. Everything else keeps the body, trimmed, because OpenAI's own
// message is usually the most useful thing available.
func openAIStatusError(status int, body string) error {
	switch {
	case status >= 200 && status < 300:
		return nil
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return fmt.Errorf("%w: openai rejected the key", ErrAuth)
	case status == http.StatusTooManyRequests:
		return fmt.Errorf("%w: openai", ErrRateLimited)
	default:
		return fmt.Errorf("openai: http %d%s", status, openAITrim(body))
	}
}

func openAITrim(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	const max = 300
	if len(body) > max {
		body = body[:max] + "..."
	}
	return ": " + body
}
