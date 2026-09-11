package ui

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/limelitgeo/open/internal/config"
	"github.com/limelitgeo/open/internal/engines"
	"github.com/limelitgeo/open/internal/promptpack"
	"github.com/limelitgeo/open/internal/provider"
	"github.com/limelitgeo/open/internal/secrets"
	"github.com/limelitgeo/open/internal/store"
	"github.com/limelitgeo/open/internal/target"
)

// Settings keys the dashboard writes.
const (
	settingCategory   = "category"
	settingRunsPerDay = "runs_per_day"
	credentialPrefix  = "credential:"
)

// cloudFeatures is the hosted-only list. It is the same list as the README's,
// and the upgrade screen is the only place in the product that shows it, so a
// user learns the boundary before they go looking for a missing feature.
var cloudFeatures = []string{
	"Query fan-out capture and rewrite analysis",
	"Prompt generation from your site, personas, competitor suggestion",
	"Discovery of brands you did not list",
	"Sentiment and framing, hallucination guard, correction drafts",
	"Perception audits",
	"Google Search Console and GA4",
	"Agents, sheets and blocks",
	"Portfolios and multi-brand, white-label, digest emails",
}

// App serves the dashboard.
type App struct {
	db       *store.DB
	registry *provider.Registry
	keys     *secrets.Keyring
	views    *Renderer
	log      *slog.Logger
	version  string
	cfg      *config.Config
}

// New builds the dashboard handler set.
func New(db *store.DB, registry *provider.Registry, keys *secrets.Keyring, log *slog.Logger, version string, cfg *config.Config) (*App, error) {
	views, err := NewRenderer()
	if err != nil {
		return nil, err
	}
	return &App{db: db, registry: registry, keys: keys, views: views, log: log, version: version, cfg: cfg}, nil
}

// Routes registers every dashboard route on mux.
func (a *App) Routes(mux *http.ServeMux) {
	mux.Handle("GET /static/", StaticHandler())

	mux.HandleFunc("GET /{$}", a.home)
	mux.HandleFunc("GET /overview", a.overview)
	mux.HandleFunc("GET /prompts", a.prompts)
	mux.HandleFunc("POST /prompts/add", a.addPrompt)
	mux.HandleFunc("POST /prompts/delete", a.deletePrompt)
	mux.HandleFunc("GET /competitors", a.competitors)
	mux.HandleFunc("POST /competitors/add", a.addCompetitor)
	mux.HandleFunc("POST /competitors/delete", a.deleteCompetitor)
	mux.HandleFunc("GET /chats", a.chats)
	mux.HandleFunc("GET /citations", a.citations)
	mux.HandleFunc("GET /settings", a.settings)
	mux.HandleFunc("POST /settings/targets/add", a.addTarget)
	mux.HandleFunc("POST /settings/targets/delete", a.deleteTarget)
	mux.HandleFunc("POST /settings/limits", a.saveLimits)
	mux.HandleFunc("GET /upgrade", a.upgrade)
	mux.HandleFunc("POST /run", a.run)

	mux.HandleFunc("GET /setup", a.wizardBrand)
	mux.HandleFunc("POST /setup/brand", a.saveBrand)
	mux.HandleFunc("GET /setup/competitors", a.wizardCompetitors)
	mux.HandleFunc("POST /setup/competitors", a.saveCompetitors)
	mux.HandleFunc("GET /setup/prompts", a.wizardPrompts)
	mux.HandleFunc("POST /setup/prompts", a.savePrompts)
	mux.HandleFunc("GET /setup/provider", a.wizardProvider)
	mux.HandleFunc("POST /setup/provider", a.saveProvider)
	mux.HandleFunc("POST /setup/finish", a.finishSetup)
}

// flashes maps a short code to the message it shows. Keeping the copy here
// rather than in the query string means a link cannot inject text into the
// page, and the wording stays in one place.
var flashes = map[string]Flash{
	"setup-done":        {Kind: "ok", Text: "Setup complete. Add a provider key in Settings when you are ready to run."},
	"setup-ready":       {Kind: "ok", Text: "Setup complete. Press Run now to fetch your first answers."},
	"prompt-added":      {Kind: "ok", Text: "Prompt added."},
	"prompt-removed":    {Kind: "ok", Text: "Prompt removed. Answers already recorded are kept."},
	"prompt-empty":      {Kind: "error", Text: "That prompt was empty."},
	"competitor-added":  {Kind: "ok", Text: "Competitor added. New answers will be searched for it."},
	"competitor-nodom":  {Kind: "error", Text: "A competitor needs a domain: it is how the same company is recognised across citations."},
	"competitor-remove": {Kind: "ok", Text: "Competitor removed. Answers already recorded keep their mentions."},
	"target-added":      {Kind: "ok", Text: "Target added."},
	"target-removed":    {Kind: "ok", Text: "Target removed."},
	"limits-saved":      {Kind: "ok", Text: "Run ceiling saved."},
	"key-saved":         {Kind: "ok", Text: "Key saved on this machine."},
	"run-unbuilt":       {Kind: "warn", Text: "The evaluation runner is not built yet. It is the next thing being built."},
}

func (a *App) flash(r *http.Request) *Flash {
	code := r.URL.Query().Get("flash")
	if code == "" {
		return nil
	}
	if f, ok := flashes[code]; ok {
		return &f
	}
	// An unknown code is a stale or hand-edited link, not a message.
	return nil
}

// base fills the chrome: the sidebar counts, the property, and whether Run is
// possible at all.
func (a *App) base(r *http.Request, title, current string) (Base, store.Counts, error) {
	ctx := r.Context()
	counts, err := a.db.Counts(ctx)
	if err != nil {
		return Base{}, counts, err
	}
	prop, err := a.db.Property(ctx)
	if err != nil && err != store.ErrNotFound {
		return Base{}, counts, err
	}

	b := Base{
		Title:    title,
		Version:  a.version,
		Property: PropertyView{Name: prop.Name, Domain: prop.Domain},
		Flash:    a.flash(r),
		// Run needs something to ask, somewhere to ask it, and a provider
		// that exists in this build. Enabling the button without all three
		// would produce a spinner and no explanation.
		CanRun: counts.Prompts > 0 && counts.Targets > 0 && len(a.registry.Names()) > 0,
	}
	if counts.LastChatAt != "" {
		b.LastRun = counts.LastChatAt
	}
	b.Nav = []NavItem{
		{Label: "Overview", Href: "/overview", Current: current == "overview"},
		{Label: "Prompts", Href: "/prompts", Count: count(counts.Prompts), Current: current == "prompts"},
		{Label: "Competitors", Href: "/competitors", Count: count(counts.Competitors), Current: current == "competitors"},
		{Section: "Evidence", Label: "Answers", Href: "/chats", Count: count(counts.Chats), Current: current == "chats"},
		{Label: "Citations", Href: "/citations", Current: current == "citations"},
		{Section: "Instance", Label: "Settings", Href: "/settings", Count: count(counts.Targets), Current: current == "settings"},
		{Label: "Limelit Cloud", Href: "/upgrade", Current: current == "upgrade"},
	}
	return b, counts, nil
}

func count(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func (a *App) configured(ctx context.Context) bool {
	_, err := a.db.Property(ctx)
	return err == nil
}

// home sends a fresh instance into the wizard. That redirect is the whole
// first-run experience: there is no empty dashboard to be confused by.
func (a *App) home(w http.ResponseWriter, r *http.Request) {
	if !a.configured(r.Context()) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/overview", http.StatusSeeOther)
}

func (a *App) overview(w http.ResponseWriter, r *http.Request) {
	if !a.configured(r.Context()) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	base, counts, err := a.base(r, "Overview", "overview")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	targets, err := a.db.Targets(r.Context(), false)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	page := OverviewPage{
		Base:       base,
		WindowDays: 30,
		Counts:     CountsView{Prompts: counts.Prompts, Competitors: counts.Competitors, Targets: counts.Targets, Chats: counts.Chats},
	}
	for _, t := range targets {
		label := t.Engine
		if e, ok := engines.Lookup(t.Engine); ok {
			label = e.Label
		}
		page.Targets = append(page.Targets, TargetView{ID: t.ID, Spec: t.Spec, EngineLabel: label, Access: t.Access})
	}
	a.write(w, r, "overview", page)
}

func (a *App) prompts(w http.ResponseWriter, r *http.Request) {
	if !a.configured(r.Context()) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	base, _, err := a.base(r, "Prompts", "prompts")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rows, err := a.db.Prompts(r.Context(), true)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	page := PromptsPage{Base: base}
	for _, p := range rows {
		page.Prompts = append(page.Prompts, PromptView{ID: p.ID, Text: p.Text, Category: p.Category, Branded: p.Branded})
	}
	a.write(w, r, "prompts", page)
}

func (a *App) addPrompt(w http.ResponseWriter, r *http.Request) {
	text := strings.TrimSpace(r.FormValue("text"))
	if text == "" {
		http.Redirect(w, r, "/prompts?flash=prompt-empty", http.StatusSeeOther)
		return
	}
	prop, _ := a.db.Property(r.Context())
	_, err := a.db.AddPrompt(r.Context(), store.Prompt{
		Text:    text,
		Branded: promptpack.IsBranded(text, prop.Names()),
		Active:  true,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/prompts?flash=prompt-added", http.StatusSeeOther)
}

func (a *App) deletePrompt(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err := a.db.DeletePrompt(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/prompts?flash=prompt-removed", http.StatusSeeOther)
}

func (a *App) competitors(w http.ResponseWriter, r *http.Request) {
	if !a.configured(r.Context()) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	base, _, err := a.base(r, "Competitors", "competitors")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rows, err := a.db.Competitors(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	page := CompetitorsPage{Base: base}
	for _, c := range rows {
		page.Competitors = append(page.Competitors, CompetitorView{ID: c.ID, Name: c.Name, Domain: c.Domain})
	}
	a.write(w, r, "competitors", page)
}

func (a *App) addCompetitor(w http.ResponseWriter, r *http.Request) {
	domain := store.NormalizeDomain(r.FormValue("domain"))
	if domain == "" {
		http.Redirect(w, r, "/competitors?flash=competitor-nodom", http.StatusSeeOther)
		return
	}
	_, err := a.db.AddCompetitor(r.Context(), store.Competitor{
		Name:   strings.TrimSpace(r.FormValue("name")),
		Domain: domain,
	})
	if err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/competitors?flash=competitor-added", http.StatusSeeOther)
}

func (a *App) deleteCompetitor(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err := a.db.DeleteCompetitor(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/competitors?flash=competitor-remove", http.StatusSeeOther)
}

// chats and citations are the evidence screens. They are placeholders until
// there are answers to show, and they say which issue builds them rather than
// rendering an empty frame that reads as broken.
func (a *App) chats(w http.ResponseWriter, r *http.Request) {
	a.placeholder(w, r, "Answers", "chats",
		"Every answer an engine gave, with your brand and your competitors highlighted where they appear.",
		"Answers arrive once the evaluation runner is built. This screen then shows each one in full: the text, the mentions with their rank, and every source cited.",
		"https://github.com/limelitgeo/open/issues/22")
}

func (a *App) citations(w http.ResponseWriter, r *http.Request) {
	a.placeholder(w, r, "Citations", "citations",
		"The domains and pages the engines trust when they answer about your category.",
		"Citations are classified as your own, a competitor, social, informational or other. The screen lands with the evidence work.",
		"https://github.com/limelitgeo/open/issues/22")
}

func (a *App) placeholder(w http.ResponseWriter, r *http.Request, title, current, lede, detail, issue string) {
	if !a.configured(r.Context()) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	base, _, err := a.base(r, title, current)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.write(w, r, "placeholder", PlaceholderPage{Base: base, Lede: lede, Detail: detail, IssueURL: issue})
}

func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	if !a.configured(r.Context()) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	base, _, err := a.base(r, "Settings", "settings")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	targets, err := a.db.Targets(r.Context(), false)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	today, err := a.db.RunsToday(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	page := SettingsPage{
		Base:          base,
		EngineList:    strings.Join(engines.IDs(), ", "),
		ProviderNames: strings.Join(a.registry.Names(), ", "),
		RunsPerDay:    a.runsPerDay(r.Context()),
		RunsToday:     today,
	}
	for _, t := range targets {
		page.Targets = append(page.Targets, TargetView{ID: t.ID, Spec: t.Spec, Access: t.Access})
	}
	a.write(w, r, "settings", page)
}

func (a *App) runsPerDay(ctx context.Context) int {
	if v, err := a.db.Setting(ctx, settingRunsPerDay); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if a.cfg != nil && a.cfg.Limits.RunsPerDay > 0 {
		return a.cfg.Limits.RunsPerDay
	}
	return config.DefaultRunsPerDay
}

func (a *App) addTarget(w http.ResponseWriter, r *http.Request) {
	spec := strings.TrimSpace(r.FormValue("spec"))
	parsed, err := target.Parse(spec)
	if err == nil {
		err = parsed.Validate(a.registry)
	}
	if err != nil {
		// The parser's message names the way out, so it is shown verbatim
		// rather than replaced with a generic "invalid target".
		a.rerender(w, r, "/settings", Flash{Kind: "error", Text: err.Error()})
		return
	}
	access, _ := parsed.Access(a.registry)
	if _, err := a.db.AddTarget(r.Context(), store.Target{
		Spec: parsed.String(), Engine: parsed.Engine, Provider: parsed.Provider,
		Model: parsed.Model, Online: parsed.Online, Access: string(access),
	}); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings?flash=target-added", http.StatusSeeOther)
}

func (a *App) deleteTarget(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err := a.db.DeleteTarget(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings?flash=target-removed", http.StatusSeeOther)
}

func (a *App) saveLimits(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("runs_per_day")))
	if err != nil || n <= 0 {
		a.rerender(w, r, "/settings", Flash{Kind: "error", Text: "The run ceiling has to be a positive number."})
		return
	}
	if err := a.db.SetSetting(r.Context(), settingRunsPerDay, strconv.Itoa(n)); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings?flash=limits-saved", http.StatusSeeOther)
}

func (a *App) upgrade(w http.ResponseWriter, r *http.Request) {
	if !a.configured(r.Context()) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	base, _, err := a.base(r, "Limelit Cloud", "upgrade")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.write(w, r, "upgrade", UpgradePage{Base: base, CloudFeatures: cloudFeatures})
}

func (a *App) run(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/overview?flash=run-unbuilt", http.StatusSeeOther)
}

// providerOptions lists what a user could connect, sorted, with the engines
// each one reaches spelled out rather than left as a provider name.
func (a *App) providerOptions() []ProviderOption {
	var out []ProviderOption
	for _, name := range a.registry.Names() {
		reg, _ := a.registry.Lookup(name)
		labels := make([]string, 0, len(reg.Engines))
		for id := range reg.Engines {
			if e, ok := engines.Lookup(id); ok {
				labels = append(labels, e.Label)
			}
		}
		sort.Strings(labels)
		out = append(out, ProviderOption{Name: name, Access: string(reg.Access), EngineList: strings.Join(labels, ", ")})
	}
	return out
}

func (a *App) write(w http.ResponseWriter, r *http.Request, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.views.Render(w, page, data); err != nil {
		// The response is already partly written, so this cannot become a
		// clean 500. Log it loudly instead of pretending it rendered.
		a.log.Error("render failed", "page", page, "error", err)
	}
}

// rerender re-renders a page with a flash that is too specific to be a code,
// such as a parser error quoted back to the user.
func (a *App) rerender(w http.ResponseWriter, r *http.Request, path string, flash Flash) {
	switch path {
	case "/settings":
		base, _, err := a.base(r, "Settings", "settings")
		if err != nil {
			a.fail(w, r, err)
			return
		}
		base.Flash = &flash
		targets, _ := a.db.Targets(r.Context(), false)
		today, _ := a.db.RunsToday(r.Context())
		page := SettingsPage{
			Base: base, EngineList: strings.Join(engines.IDs(), ", "),
			ProviderNames: strings.Join(a.registry.Names(), ", "),
			RunsPerDay:    a.runsPerDay(r.Context()), RunsToday: today,
		}
		for _, t := range targets {
			page.Targets = append(page.Targets, TargetView{ID: t.ID, Spec: t.Spec, Access: t.Access})
		}
		a.write(w, r, "settings", page)
	default:
		http.Redirect(w, r, path, http.StatusSeeOther)
	}
}

func (a *App) fail(w http.ResponseWriter, r *http.Request, err error) {
	a.log.Error("dashboard error", "path", r.URL.Path, "error", err)
	http.Error(w, "Something went wrong. The server log has the detail.", http.StatusInternalServerError)
}
