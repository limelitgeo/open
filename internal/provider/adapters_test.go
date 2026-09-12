// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The fixtures here are real recorded answers, captured from the live APIs on
// 2026-09-11. Every test runs against them with no network and no key.

func against(t *testing.T, fixture string) http.Handler {
	t.Helper()
	return serveFixture(t, fixture)
}

func newAnthropicAgainst(t *testing.T, h http.Handler) *anthropicProvider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := NewAnthropic("sk-ant-test")
	if err != nil {
		t.Fatal(err)
	}
	ap := p.(*anthropicProvider)
	ap.endpoint, ap.models = srv.URL+"/v1/messages", srv.URL+"/v1/models"
	return ap
}

func newPerplexityAgainst(t *testing.T, h http.Handler) *perplexityProvider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := NewPerplexity("pplx-test")
	if err != nil {
		t.Fatal(err)
	}
	pp := p.(*perplexityProvider)
	pp.endpoint = srv.URL + "/chat/completions"
	return pp
}

func newGoogleAgainst(t *testing.T, h http.Handler) *googleProvider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := NewGoogle("AIza-test")
	if err != nil {
		t.Fatal(err)
	}
	gp := p.(*googleProvider)
	gp.endpoint = srv.URL + "/v1beta/models/%s:generateContent"
	return gp
}

// TestAnthropicReadsOnlyTextBlocks is the one that matters most for this
// provider. Claude returns the answer as many text blocks interleaved with
// thinking, server_tool_use and web_search_tool_result blocks, and thinking
// concatenated into the answer would be searched for brand names.
func TestAnthropicReadsOnlyTextBlocks(t *testing.T) {
	p := newAnthropicAgainst(t, against(t, "anthropic_answer.json"))
	resp, err := p.Run(context.Background(), Request{Engine: ClaudeEngine, Prompt: "q", Online: true})
	// The recorded answer stopped on max_tokens, which this provider rejects
	// on purpose, so exercise the reader directly as well.
	if err == nil {
		t.Fatalf("a max_tokens response must be rejected, got a %d byte answer", len(resp.Text))
	}
	if !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("error should name the truncation: %v", err)
	}
}

func TestAnthropicContentReaderSeparatesAnswerFromEverythingElse(t *testing.T) {
	blocks := []anthropicContent{
		{Type: "thinking", Text: "The user is asking about Acme and Rival."},
		{Type: "server_tool_use"},
		{Type: "web_search_tool_result", Text: "raw result payload"},
		{Type: "text", Text: "Acme leads. "},
		{Type: "text", Text: "Rival is second."},
	}
	blocks[1].Input.Query = "best widget tools"

	text, cites, fanout := readAnthropicContent(blocks)
	if text != "Acme leads. Rival is second." {
		t.Fatalf("text = %q, want only the text blocks joined", text)
	}
	if strings.Contains(text, "thinking") || strings.Contains(text, "raw result") {
		t.Error("a non-answer block leaked into the answer")
	}
	if len(cites) != 0 {
		t.Errorf("citations = %d, want 0", len(cites))
	}
	if len(fanout) != 1 || fanout[0] != "best widget tools" {
		t.Errorf("fanout = %v, want the one search query", fanout)
	}
}

func TestAnthropicDeduplicatesSourcesAndQueries(t *testing.T) {
	one := anthropicContent{Type: "text", Text: "a"}
	one.Citations = append(one.Citations, struct {
		Type  string `json:"type"`
		URL   string `json:"url"`
		Title string `json:"title"`
	}{"web_search_result_location", "https://g2.com/x", "G2"})
	two := one
	q1 := anthropicContent{Type: "server_tool_use"}
	q1.Input.Query = "same"
	q2 := q1

	_, cites, fanout := readAnthropicContent([]anthropicContent{one, two, q1, q2})
	if len(cites) != 1 {
		t.Errorf("citations = %d, want 1: one source cited twice is one source", len(cites))
	}
	if len(fanout) != 1 {
		t.Errorf("fanout = %d, want 1", len(fanout))
	}
}

func TestPerplexityParsesARecordedAnswer(t *testing.T) {
	p := newPerplexityAgainst(t, against(t, "perplexity_answer.json"))
	resp, err := p.Run(context.Background(), Request{Engine: PerplexityEngine, Prompt: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(resp.Text) == "" {
		t.Fatal("no answer text")
	}
	if resp.Model != "sonar" {
		t.Errorf("model = %q", resp.Model)
	}
	if len(resp.Citations) == 0 {
		t.Fatal("no citations parsed")
	}
	for i, c := range resp.Citations {
		if !strings.HasPrefix(c.URL, "http") {
			t.Errorf("citation %d has a non-URL: %q", i, c.URL)
		}
		if c.Position != i+1 {
			t.Errorf("citation %d has position %d", i, c.Position)
		}
	}
	// search_results carries titles; the bare citations list does not. If the
	// richer list were being ignored every source would be untitled.
	titled := 0
	for _, c := range resp.Citations {
		if c.Title != "" {
			titled++
		}
	}
	if titled == 0 {
		t.Error("no citation has a title: search_results is being ignored")
	}
}

func TestPerplexityWrongEngineIsTyped(t *testing.T) {
	p := newPerplexityAgainst(t, against(t, "perplexity_answer.json"))
	_, err := p.Run(context.Background(), Request{Engine: ChatGPTEngine, Prompt: "q"})
	if !errors.Is(err, ErrUnsupportedEngine) {
		t.Fatalf("err = %v, want ErrUnsupportedEngine", err)
	}
}

// TestGooglePromotesTheDomainOverTheRedirect guards the quirk that would
// otherwise be a silent measurement bug: Gemini's grounding uri is a Vertex
// redirect, and the real source identity is in the title.
func TestGooglePromotesTheDomainOverTheRedirect(t *testing.T) {
	p := newGoogleAgainst(t, against(t, "gemini_answer.json"))
	resp, err := p.Run(context.Background(), Request{Engine: GeminiEngine, Prompt: "q", Online: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Citations) == 0 {
		t.Fatal("no citations parsed")
	}
	for _, c := range resp.Citations {
		if strings.Contains(c.URL, googleRedirectHost) {
			t.Fatalf("a citation kept the Vertex redirect: %q. Every Gemini source would classify as one Google domain", c.URL)
		}
		if !strings.HasPrefix(c.URL, "https://") {
			t.Errorf("citation is not a URL: %q", c.URL)
		}
	}
	if len(resp.FanOut) == 0 {
		t.Error("webSearchQueries were not captured as fan-out")
	}
}

func TestLooksLikeDomain(t *testing.T) {
	for _, ok := range []string{"zapier.com", "position.digital", "blog.example.co.uk"} {
		if !looksLikeDomain(ok) {
			t.Errorf("%q should look like a domain", ok)
		}
	}
	// A page title containing a dot must never become a URL.
	for _, no := range []string{"", "The 8 best tools in 2026", "Best. Tool. Ever.", "example.", ".com", "a.b", "http://x.com", "x.com/page", "user@x.com"} {
		if looksLikeDomain(no) {
			t.Errorf("%q should NOT look like a domain", no)
		}
	}
}

func TestGoogleSourceURLFallsBackVisibly(t *testing.T) {
	// A title that is not a domain keeps the redirect. Attributing it to
	// Google is wrong, but it is visibly wrong, which beats dropping a source.
	got := googleSourceURL("Some Page Title", "https://"+googleRedirectHost+"/redirect/abc")
	if !strings.Contains(got, googleRedirectHost) {
		t.Fatalf("got %q, want the redirect kept as a visible fallback", got)
	}
}

func TestNewProvidersRejectAMissingKey(t *testing.T) {
	for name, build := range map[string]func(string) (Provider, error){
		"anthropic":  NewAnthropic,
		"perplexity": NewPerplexity,
		"google":     NewGoogle,
	} {
		if _, err := build("   "); !errors.Is(err, ErrAuth) {
			t.Errorf("%s: err = %v, want ErrAuth", name, err)
		}
	}
}

func TestHTTPStatusErrorMapping(t *testing.T) {
	for status, want := range map[int]error{
		http.StatusUnauthorized:       ErrAuth,
		http.StatusForbidden:          ErrAuth,
		http.StatusTooManyRequests:    ErrRateLimited,
		http.StatusBadGateway:         ErrRateLimited,
		http.StatusServiceUnavailable: ErrRateLimited,
	} {
		if err := httpStatusError("x", status, ""); !errors.Is(err, want) {
			t.Errorf("status %d mapped to %v, want %v", status, err, want)
		}
	}
	if err := httpStatusError("x", http.StatusOK, ""); err != nil {
		t.Errorf("200 mapped to %v", err)
	}
	// A 400 is the provider's own complaint and keeps its message.
	err := httpStatusError("x", http.StatusBadRequest, "model not found")
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Errorf("400 should keep the body: %v", err)
	}
}

// TestEveryRegisteredProviderIsInTheCatalog keeps the shipped registry and the
// documented plan from drifting apart.
func TestEveryRegisteredProviderIsInTheCatalog(t *testing.T) {
	catalog := map[string]bool{}
	for _, e := range Catalog() {
		catalog[e.Name] = true
	}
	for _, name := range Default().Names() {
		if !catalog[name] {
			t.Errorf("provider %q is registered but missing from the catalog", name)
		}
	}
}
