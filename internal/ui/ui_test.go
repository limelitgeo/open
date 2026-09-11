// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package ui

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/limelitgeo/open/internal/config"
	"github.com/limelitgeo/open/internal/provider"
	"github.com/limelitgeo/open/internal/provider/providertest"
	"github.com/limelitgeo/open/internal/runner"
	"github.com/limelitgeo/open/internal/secrets"
	"github.com/limelitgeo/open/internal/store"
)

func newApp(t *testing.T, reg *provider.Registry) (*App, *store.DB, http.Handler) {
	t.Helper()
	return newAppWithRunner(t, reg, nil)
}

// newAppWithRunner builds the dashboard with a runner attached, for the tests
// that press Run.
func newAppWithRunner(t *testing.T, reg *provider.Registry, run *runner.Runner) (*App, *store.DB, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "limelit.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	t.Setenv("LIMELIT_SECRET", "test-secret")
	keys, err := secrets.Open(dir)
	if err != nil {
		t.Fatalf("secrets.Open: %v", err)
	}
	if run == nil {
		run = runner.New(db, reg, provider.StaticCredentials(nil), slog.New(slog.NewTextHandler(io.Discard, nil)))
	}
	app, err := New(db, reg, keys, run, slog.New(slog.NewTextHandler(io.Discard, nil)), "test", &config.Config{})
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	mux := http.NewServeMux()
	app.Routes(mux)
	return app, db, mux
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func post(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestEveryTemplateParses is the reason NewRenderer returns an error rather
// than lazily parsing: a broken template must fail at startup, not the first
// time a user opens that page.
func TestEveryTemplateParses(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	for page := range pages {
		if _, ok := r.sets[page]; !ok {
			t.Errorf("page %q was not parsed", page)
		}
	}
}

func TestFreshInstanceGoesToTheWizard(t *testing.T) {
	// The whole first run is this redirect. An empty dashboard would be the
	// first thing a new user had to interpret.
	_, _, h := newApp(t, provider.NewRegistry())
	rec := get(t, h, "/")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET / = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/setup" {
		t.Errorf("redirected to %q", got)
	}
	for _, page := range []string{"/overview", "/prompts", "/competitors", "/settings", "/upgrade"} {
		if rec := get(t, h, page); rec.Header().Get("Location") != "/setup" {
			t.Errorf("GET %s before setup redirected to %q", page, rec.Header().Get("Location"))
		}
	}
}

func TestWizardWalksEndToEnd(t *testing.T) {
	_, db, h := newApp(t, provider.NewRegistry())
	ctx := context.Background()

	// Step 1. The domain arrives the way a person pastes it, with a scheme
	// and a www, and has to be stored as the bare identity.
	if rec := post(t, h, "/setup/brand", url.Values{
		"name": {"Acme"}, "domain": {"https://www.acme.com/pricing"}, "aliases": {"Acme Inc, acme.io"},
	}); rec.Header().Get("Location") != "/setup/competitors" {
		t.Fatalf("step 1 went to %q", rec.Header().Get("Location"))
	}
	prop, err := db.Property(ctx)
	if err != nil {
		t.Fatalf("property was not saved: %v", err)
	}
	if prop.Domain != "acme.com" {
		t.Errorf("domain stored as %q, want the bare identity", prop.Domain)
	}
	if len(prop.Aliases) != 2 {
		t.Errorf("aliases = %v", prop.Aliases)
	}

	// Step 2. A row with a domain and no name is still a competitor.
	if rec := post(t, h, "/setup/competitors", url.Values{
		"category":          {"AI visibility tracking"},
		"competitor_name":   {"Globex", "", ""},
		"competitor_domain": {"globex.com", "https://initech.com/pricing", ""},
	}); rec.Header().Get("Location") != "/setup/prompts" {
		t.Fatalf("step 2 went to %q", rec.Header().Get("Location"))
	}
	comps, _ := db.Competitors(ctx)
	if len(comps) != 2 {
		t.Fatalf("saved %d competitors, want 2", len(comps))
	}
	if comps[1].Domain != "initech.com" || comps[1].Name == "" {
		t.Errorf("competitor with no name saved as %+v", comps[1])
	}

	// Step 3 renders the pack, and the article agrees with the category.
	rec := get(t, h, "/setup/prompts")
	body := rec.Body.String()
	if !strings.Contains(body, "an AI visibility tracking tool") {
		t.Error("step 3 did not render the pack with a correct article")
	}
	if !strings.Contains(body, "Globex alternatives") {
		t.Error("step 3 did not use the competitor")
	}

	// Submitting with one prompt unticked. The categories submitted alongside
	// cannot be matched by index once a box in the middle is cleared, which
	// is the bug this asserts against.
	if rec := post(t, h, "/setup/prompts", url.Values{
		"prompt": {
			"What are the best AI visibility tracking tools?",
			"Globex alternatives",
			"What is Acme?",
		},
		"extra": {"Which tool tracks brand mentions in ChatGPT?\n\n"},
	}); rec.Header().Get("Location") != "/setup/provider" {
		t.Fatalf("step 3 went to %q", rec.Header().Get("Location"))
	}

	prompts, _ := db.Prompts(ctx, true)
	if len(prompts) != 4 {
		t.Fatalf("saved %d prompts, want 3 ticked plus 1 typed", len(prompts))
	}
	byText := map[string]store.Prompt{}
	for _, p := range prompts {
		byText[p.Text] = p
	}
	if got := byText["Globex alternatives"].Category; got != "comparison" {
		t.Errorf("category survived unticking as %q, want comparison", got)
	}
	if !byText["What is Acme?"].Branded {
		t.Error("a prompt naming the brand was not tagged branded")
	}
	if byText["What are the best AI visibility tracking tools?"].Branded {
		t.Error("a discovery prompt was tagged branded")
	}
	if byText["Which tool tracks brand mentions in ChatGPT?"].Text == "" {
		t.Error("the hand-typed prompt was dropped")
	}

	// Step 4 with no providers compiled in offers the honest exit.
	rec = get(t, h, "/setup/provider")
	if !strings.Contains(rec.Body.String(), "No provider is implemented in this build yet") {
		t.Error("step 4 did not say why there is nothing to connect")
	}
	// Even with nothing to connect, the step still says where the keys will
	// come from, so a user can go and get one while they wait.
	if !strings.Contains(rec.Body.String(), "https://openrouter.ai/keys") {
		t.Error("step 4 does not link a vendor key page")
	}
	if rec := post(t, h, "/setup/finish", nil); !strings.HasPrefix(rec.Header().Get("Location"), "/overview") {
		t.Errorf("finish went to %q", rec.Header().Get("Location"))
	}
}

func TestBrandStepRejectsAMissingDomain(t *testing.T) {
	// The domain is how a citation is recognised as the property's own, so a
	// brand without one cannot be tracked.
	_, db, h := newApp(t, provider.NewRegistry())
	rec := post(t, h, "/setup/brand", url.Values{"name": {"Acme"}, "domain": {"  "}})
	if rec.Code != http.StatusOK {
		t.Fatalf("a rejected step redirected instead of re-rendering: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "domain") {
		t.Error("the error did not mention the domain")
	}
	if !strings.Contains(rec.Body.String(), `value="Acme"`) {
		t.Error("the form did not keep what was already typed")
	}
	if _, err := db.Property(context.Background()); err == nil {
		t.Error("a property was saved despite the error")
	}
}

func TestRunButtonIsDisabledUntilItCanRun(t *testing.T) {
	// Offering a Run button that produces nothing would be the first thing
	// this product got wrong.
	_, db, h := newApp(t, provider.NewRegistry())
	ctx := context.Background()
	if err := db.SaveProperty(ctx, store.Property{Name: "Acme", Domain: "acme.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddPrompt(ctx, store.Prompt{Text: "best crm", Active: true}); err != nil {
		t.Fatal(err)
	}
	body := get(t, h, "/overview").Body.String()
	if !strings.Contains(body, "disabled") {
		t.Error("Run now was enabled with no target and no provider")
	}
	if !strings.Contains(body, "Add a target and a provider key first") {
		t.Error("the disabled button does not say what is missing")
	}
}

func TestAddTargetQuotesTheParserError(t *testing.T) {
	// The parser's message names the way out. Replacing it with "invalid
	// target" would throw away the only useful part.
	_, _, h := newApp(t, providertest.Registry())
	seedProperty(t, h)

	rec := post(t, h, "/settings/targets/add", url.Values{"spec": {"ai_overview:openai"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("a rejected target redirected instead of re-rendering: %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "cannot reach ai_overview") {
		t.Error("the parser error was not shown")
	}
	if !strings.Contains(body, "dataforseo") {
		t.Error("the error did not name a provider that can reach it")
	}
}

func TestAddTargetStoresAccessMode(t *testing.T) {
	// Access travels with the target because an api answer and a scraped
	// answer measure different surfaces.
	_, db, h := newApp(t, providertest.Registry())
	seedProperty(t, h)

	for spec, want := range map[string]string{
		"chatgpt:openai:online":  "api",
		"ai_overview:dataforseo": "scraped",
	} {
		if rec := post(t, h, "/settings/targets/add", url.Values{"spec": {spec}}); rec.Code != http.StatusSeeOther {
			t.Fatalf("adding %q = %d", spec, rec.Code)
		}
		targets, _ := db.Targets(context.Background(), false)
		var found bool
		for _, tg := range targets {
			if tg.Spec == spec {
				found = true
				if tg.Access != want {
					t.Errorf("target %q stored access %q, want %q", spec, tg.Access, want)
				}
			}
		}
		if !found {
			t.Errorf("target %q was not stored", spec)
		}
	}
}

func TestCompetitorNeedsADomain(t *testing.T) {
	_, db, h := newApp(t, provider.NewRegistry())
	seedProperty(t, h)

	rec := post(t, h, "/competitors/add", url.Values{"name": {"Globex"}, "domain": {""}})
	if !strings.Contains(rec.Header().Get("Location"), "competitor-nodom") {
		t.Errorf("a competitor with no domain was accepted, redirect was %q", rec.Header().Get("Location"))
	}
	comps, _ := db.Competitors(context.Background())
	if len(comps) != 0 {
		t.Errorf("stored %d competitors", len(comps))
	}
}

func TestPromptAddedByHandIsTaggedBranded(t *testing.T) {
	_, db, h := newApp(t, provider.NewRegistry())
	seedProperty(t, h)

	post(t, h, "/prompts/add", url.Values{"text": {"Is Acme better than Globex?"}})
	prompts, _ := db.Prompts(context.Background(), true)
	if len(prompts) != 1 {
		t.Fatalf("stored %d prompts", len(prompts))
	}
	if !prompts[0].Branded {
		t.Error("a hand-typed prompt naming the brand was not tagged branded")
	}
}

func TestFlashCodesCannotInjectText(t *testing.T) {
	// Flash copy lives in Go, so a link cannot put words on the page.
	_, _, h := newApp(t, provider.NewRegistry())
	seedProperty(t, h)

	body := get(t, h, "/overview?flash=%3Cscript%3Ealert(1)%3C/script%3E").Body.String()
	if strings.Contains(body, "alert(1)") {
		t.Error("an unknown flash code reached the page")
	}
	if !strings.Contains(get(t, h, "/overview?flash=setup-done").Body.String(), "Setup complete") {
		t.Error("a known flash code did not render")
	}
}

func TestUpgradeScreenShowsTheWholeBoundary(t *testing.T) {
	// The upgrade screen is the only place in the product that lists what is
	// hosted-only, so a user learns the boundary before hunting for a
	// missing feature.
	_, _, h := newApp(t, provider.NewRegistry())
	seedProperty(t, h)
	body := get(t, h, "/upgrade").Body.String()
	for _, feature := range cloudFeatures {
		if !strings.Contains(body, feature) {
			t.Errorf("the upgrade screen does not mention %q", feature)
		}
	}
}

func TestPlaceholderScreensSayWhatIsMissing(t *testing.T) {
	// An empty frame reads as broken. These say which issue builds them.
	_, _, h := newApp(t, provider.NewRegistry())
	seedProperty(t, h)
	for _, path := range []string{"/chats", "/citations"} {
		body := get(t, h, path).Body.String()
		if !strings.Contains(body, "Not built yet") {
			t.Errorf("%s does not say it is unbuilt", path)
		}
		if !strings.Contains(body, "github.com/limelitgeo/open/issues") {
			t.Errorf("%s does not link the issue", path)
		}
	}
}

func TestRunStartsAPassAndReturnsImmediately(t *testing.T) {
	// A pass takes as long as the slowest engine, so holding the request open
	// would look like a hung browser.
	reg := provider.NewRegistry()
	provider.RegisterStub(reg, provider.StubConfig{Answer: "Acme leads."})
	_, db, h := newApp(t, reg)
	seedProperty(t, h)

	ctx := context.Background()
	if _, err := db.AddPrompt(ctx, store.Prompt{Text: "best crm", Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTarget(ctx, store.Target{
		Spec: "chatgpt:stub", Engine: "chatgpt", Provider: provider.StubName, Access: "api",
	}); err != nil {
		t.Fatal(err)
	}

	rec := post(t, h, "/run", nil)
	if !strings.Contains(rec.Header().Get("Location"), "run-started") {
		t.Fatalf("Run redirected to %q", rec.Header().Get("Location"))
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, _ := db.Counts(ctx); c.Chats > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("pressing Run produced no answer")
}

func TestStaticStylesheetIsServed(t *testing.T) {
	_, _, h := newApp(t, provider.NewRegistry())
	rec := get(t, h, "/static/app.css")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /static/app.css = %d", rec.Code)
	}
	css := rec.Body.String()
	// Every themed token must exist in both places: one declared only in
	// light renders as invisible text in dark, which looks fine until
	// somebody switches.
	if !strings.Contains(css, "prefers-color-scheme: dark") {
		t.Error("the stylesheet has no dark theme")
	}
	// The brand ramp is deliberately invariant: the mark is white on it in
	// both themes, so it must not flip. Everything that carries text or a
	// surface must.
	for _, token := range []string{"--ink:", "--ink-2:", "--surface:", "--rule:", "--bg-app:"} {
		if strings.Count(css, token) < 2 {
			t.Errorf("token %s is declared in only one theme, so it renders wrong in the other", token)
		}
	}
	// Verified in a browser at 375px: without this the fixed sidebar squeezes
	// the content column to about a hundred pixels.
	if !strings.Contains(css, "max-width: 860px") {
		t.Error("the stylesheet has no narrow-screen breakpoint")
	}
}

// TestNoEmDashesInUserFacingCopy enforces the house rule. Code comments are
// exempt, so this reads the rendered pages rather than the source.
func TestNoEmDashesInUserFacingCopy(t *testing.T) {
	_, _, h := newApp(t, providertest.Registry())
	seedProperty(t, h)
	for _, path := range []string{
		"/overview", "/prompts", "/competitors", "/settings", "/upgrade",
		"/chats", "/citations", "/setup", "/setup/competitors", "/setup/prompts", "/setup/provider",
	} {
		body := get(t, h, path).Body.String()
		for _, bad := range []string{"—", "&mdash;"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains an em dash", path)
			}
		}
	}
}

func seedProperty(t *testing.T, h http.Handler) {
	t.Helper()
	post(t, h, "/setup/brand", url.Values{"name": {"Acme"}, "domain": {"acme.com"}})
}
