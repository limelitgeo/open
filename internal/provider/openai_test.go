// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// newOpenAIAgainst builds a provider pointed at a fake, so every test here
// runs with no network and no key.
func newOpenAIAgainst(t *testing.T, h http.Handler) (*openAIProvider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	p, err := NewOpenAI("sk-test")
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}
	op := p.(*openAIProvider)
	op.endpoint = srv.URL + "/v1/responses"
	op.models = srv.URL + "/v1/models"
	return op, srv
}

func serveFixture(t *testing.T, name string) http.Handler {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
}

func TestOpenAIParsesARealAnswer(t *testing.T) {
	// The fixture is a recorded Responses API answer, so this asserts against
	// the shape OpenAI actually returns rather than one invented here.
	p, _ := newOpenAIAgainst(t, serveFixture(t, "openai_answer.json"))

	resp, err := p.Run(context.Background(), Request{
		Engine: ChatGPTEngine, Prompt: "What are the best AI visibility tracking tools?", Online: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(resp.Text) < 500 {
		t.Errorf("answer is %d characters, want the whole body", len(resp.Text))
	}
	if resp.Model == "" {
		t.Error("the model that actually answered was not recorded")
	}
	if len(resp.Citations) != 3 {
		t.Fatalf("got %d citations, want the 3 in the fixture", len(resp.Citations))
	}
	for i, c := range resp.Citations {
		if c.Position != i+1 {
			t.Errorf("citation %d has position %d", i, c.Position)
		}
		if !strings.HasPrefix(c.URL, "http") {
			t.Errorf("citation %d url = %q", i, c.URL)
		}
	}
	if resp.InputTokens == 0 || resp.OutputTokens == 0 {
		t.Errorf("usage = %d/%d, want the numbers from the response", resp.InputTokens, resp.OutputTokens)
	}
	if resp.Calls != 1 {
		t.Errorf("calls = %d", resp.Calls)
	}
}

func TestOpenAICapturesTheFanOutWithoutLeakingItIntoTheAnswer(t *testing.T) {
	// The recorded answer contains a web_search_call item carrying the exact
	// searches the model ran. Those are collected as fan-out, and they must
	// never end up inside the answer text: the matcher searches that text for
	// brand names, and a query naming a competitor would be recorded as a
	// mention the reader never saw.
	raw, err := os.ReadFile("testdata/openai_answer.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "web_search_call") {
		t.Fatal("the fixture no longer exercises this: it has no web_search_call item")
	}

	p, _ := newOpenAIAgainst(t, serveFixture(t, "openai_answer.json"))
	resp, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Prompt: "q", Online: true})
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]any
	json.Unmarshal(raw, &fixture)
	for _, item := range fixture["output"].([]any) {
		m := item.(map[string]any)
		if m["type"] != "web_search_call" {
			continue
		}
		query, _ := m["action"].(map[string]any)["query"].(string)
		if query != "" && strings.Contains(resp.Text, query) {
			t.Error("a fan-out query leaked into the answer text")
		}
	}
	if len(resp.FanOut) == 0 {
		t.Error("the fan-out was not captured")
	}
	for _, q := range resp.FanOut {
		if strings.TrimSpace(q) == "" {
			t.Error("an empty fan-out query was recorded")
		}
	}
}

func TestOpenAIAnswerWithNoCitations(t *testing.T) {
	// An ungrounded answer is a normal answer. Treating it as a failure would
	// drop a real measurement of what the model said.
	body := `{"model":"chat-latest","status":"completed",
		"usage":{"input_tokens":12,"output_tokens":34},
		"output":[{"type":"message","content":[{"type":"output_text","text":"Acme and Globex lead the category.","annotations":[]}]}]}`
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))

	resp, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Prompt: "q"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "Acme and Globex lead the category." {
		t.Errorf("text = %q", resp.Text)
	}
	if len(resp.Citations) != 0 {
		t.Errorf("citations = %+v, want none", resp.Citations)
	}
}

func TestOpenAIDeduplicatesRepeatedSources(t *testing.T) {
	// One source cited three times is one source. Counting it three times
	// would inflate every citation share it contributes to.
	body := `{"model":"chat-latest","status":"completed","usage":{"input_tokens":1,"output_tokens":1},
		"output":[{"type":"message","content":[{"type":"output_text","text":"one two three",
		"annotations":[
			{"type":"url_citation","url":"https://acme.com/a","title":"A"},
			{"type":"url_citation","url":"https://acme.com/a","title":"A again"},
			{"type":"url_citation","url":"https://globex.com/b","title":"B"},
			{"type":"file_citation","url":"https://ignored.example"}
		]}]}]}`
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))

	resp, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Prompt: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Citations) != 2 {
		t.Fatalf("got %d citations, want the repeat collapsed and the non-url annotation skipped: %+v", len(resp.Citations), resp.Citations)
	}
	if resp.Citations[0].URL != "https://acme.com/a" || resp.Citations[1].Position != 2 {
		t.Errorf("citations = %+v, want encounter order", resp.Citations)
	}
}

func TestOpenAIAuthFailure(t *testing.T) {
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Incorrect API key provided"}}`))
	}))

	_, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Prompt: "q"})
	if !errors.Is(err, ErrAuth) {
		t.Errorf("Run error = %v, want ErrAuth", err)
	}
	if err := p.Test(context.Background()); !errors.Is(err, ErrAuth) {
		t.Errorf("Test error = %v, want ErrAuth", err)
	}
}

func TestOpenAIMissingKeyIsAuthFailure(t *testing.T) {
	// A missing key and a wrong key report the same way, so the settings
	// screen has one message to show.
	if _, err := NewOpenAI("   "); !errors.Is(err, ErrAuth) {
		t.Errorf("NewOpenAI with no key = %v, want ErrAuth", err)
	}
}

func TestOpenAIRateLimitIsRetriedOnce(t *testing.T) {
	// The runner owns backoff and the daily ceiling, so a provider that
	// retried on its own would blow through both.
	var calls int32
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"model":"chat-latest","status":"completed","usage":{"input_tokens":1,"output_tokens":1},
			"output":[{"type":"message","content":[{"type":"output_text","text":"recovered","annotations":[]}]}]}`))
	}))

	resp, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Prompt: "q"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "recovered" {
		t.Errorf("text = %q", resp.Text)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("made %d attempts, want exactly one retry", got)
	}
}

func TestOpenAIGivesUpAfterTheRetry(t *testing.T) {
	var calls int32
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))

	_, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Prompt: "q"})
	if !errors.Is(err, ErrRateLimited) {
		t.Errorf("error = %v, want ErrRateLimited", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("made %d attempts, want 2", got)
	}
}

func TestOpenAIReportsAnExhaustedAccountAsQuotaNotRateLimit(t *testing.T) {
	// OpenAI sends insufficient_quota with the same 429 as a burst limit.
	// The two need different actions, so they must be different errors, and
	// an empty account is not worth a second call.
	var calls int32
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"You have no credits remaining.","type":"insufficient_quota","param":null,"code":"credit_balance_exhausted"}}`))
	}))

	_, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Prompt: "q"})
	if !errors.Is(err, ErrQuota) {
		t.Errorf("error = %v, want ErrQuota", err)
	}
	if errors.Is(err, ErrRateLimited) {
		t.Error("an exhausted account was reported as a rate limit")
	}
	if !strings.Contains(err.Error(), "credit_balance_exhausted") {
		t.Errorf("the error does not carry the vendor's code: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("made %d attempts, want 1", got)
	}
}

func TestOpenAIDoesNotRetryABadRequest(t *testing.T) {
	// A 400 will fail again identically, so a second call only spends time.
	var calls int32
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"model not found"}}`))
	}))

	_, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Prompt: "q"})
	if err == nil {
		t.Fatal("a 400 was accepted")
	}
	if !strings.Contains(err.Error(), "model not found") {
		t.Errorf("error = %v, want the vendor's own message kept", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("made %d attempts, want 1", got)
	}
}

func TestOpenAIEmptyAnswerIsAnError(t *testing.T) {
	// Storing an empty body as an answer would count the brand as absent
	// from something that was never said.
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"model":"chat-latest","status":"incomplete","usage":{},"output":[]}`))
	}))
	if _, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Prompt: "q"}); err == nil {
		t.Error("an empty response was accepted as an answer")
	}
}

func TestOpenAIRefusesAnotherEngine(t *testing.T) {
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	_, err := p.Run(context.Background(), Request{Engine: AIOverviewEngine, Prompt: "q"})
	if !errors.Is(err, ErrUnsupportedEngine) {
		t.Errorf("error = %v, want ErrUnsupportedEngine", err)
	}
}

func TestOpenAIRequestShape(t *testing.T) {
	var got openAIRequest
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk-test" {
			t.Errorf("Authorization = %q", auth)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"model":"chat-latest","status":"completed","usage":{"input_tokens":1,"output_tokens":1},
			"output":[{"type":"message","content":[{"type":"output_text","text":"ok","annotations":[]}]}]}`))
	}))

	if _, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Prompt: "best crm", Online: true}); err != nil {
		t.Fatal(err)
	}
	if got.Input != "best crm" {
		t.Errorf("the prompt was not sent verbatim: %q", got.Input)
	}
	if got.Model != OpenAIDefaultModel {
		t.Errorf("model = %q", got.Model)
	}
	if len(got.Tools) != 1 || got.Tools[0]["type"] != "web_search" {
		t.Errorf("tools = %+v, want web_search for an online request", got.Tools)
	}
	// A self-hosted tool should not leave a copy of what it asked on someone
	// else's server.
	if got.Store {
		t.Error("store was true, so the prompt would be kept in OpenAI's history")
	}
}

func TestOpenAIOfflineRequestOmitsWebSearch(t *testing.T) {
	var got openAIRequest
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"model":"chat-latest","status":"completed","usage":{},
			"output":[{"type":"message","content":[{"type":"output_text","text":"ok","annotations":[]}]}]}`))
	}))
	if _, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Prompt: "q"}); err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 0 {
		t.Errorf("tools = %+v, want none without the online flag", got.Tools)
	}
}

func TestOpenAIModelPinIsHonoured(t *testing.T) {
	// Pinning is how a user holds the instrument still against alias drift.
	var got openAIRequest
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"model":"gpt-5.5","status":"completed","usage":{},
			"output":[{"type":"message","content":[{"type":"output_text","text":"ok","annotations":[]}]}]}`))
	}))
	resp, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Model: "gpt-5.5", Prompt: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "gpt-5.5" {
		t.Errorf("sent model %q", got.Model)
	}
	if resp.Model != "gpt-5.5" {
		t.Errorf("recorded model %q", resp.Model)
	}
}

func TestOpenAITestUsesTheCheapestCall(t *testing.T) {
	// Pressing Test in Settings must not spend tokens.
	var path string
	p, _ := newOpenAIAgainst(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Write([]byte(`{"data":[]}`))
	}))
	if err := p.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if path != "/v1/models" {
		t.Errorf("Test called %q, want the models listing", path)
	}
}

func TestOpenAIIsInTheDefaultRegistry(t *testing.T) {
	reg := Default()
	entry, ok := reg.Lookup("openai")
	if !ok {
		t.Fatal("openai is not registered in the default registry")
	}
	if entry.Access != AccessAPI {
		t.Errorf("access = %q", entry.Access)
	}
	if _, ok := entry.Engines[ChatGPTEngine]; !ok {
		t.Error("openai does not claim chatgpt")
	}
	if _, err := reg.New("openai", StaticCredentials(nil)); !errors.Is(err, ErrAuth) {
		t.Errorf("constructing with no key = %v, want ErrAuth", err)
	}
	p, err := reg.New("openai", StaticCredentials(map[string]string{"OPENAI_API_KEY": "sk-test"}))
	if err != nil {
		t.Fatalf("constructing with a key: %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("name = %q", p.Name())
	}
}

func TestOpenAICatalogMatchesTheImplementation(t *testing.T) {
	// The settings screen offers what the catalog says; the registry decides
	// what actually runs. A disagreement is a button that does nothing.
	entry, ok := CatalogEntryFor("openai")
	if !ok {
		t.Fatal("openai is not in the catalog")
	}
	reg, _ := Default().Lookup("openai")
	if entry.Access != reg.Access {
		t.Errorf("catalog access %q, registry %q", entry.Access, reg.Access)
	}
	if len(entry.Engines) != len(reg.Engines) {
		t.Errorf("catalog reaches %v, registry reaches %v", entry.Engines, reg.Engines)
	}
	for engine, model := range entry.Engines {
		if reg.Engines[engine] != model {
			t.Errorf("engine %q: catalog default %q, registry %q", engine, model, reg.Engines[engine])
		}
	}
}
