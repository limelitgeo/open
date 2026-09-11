// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package ui

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/limelitgeo/open/internal/config"
	"github.com/limelitgeo/open/internal/credentials"
	"github.com/limelitgeo/open/internal/engines"
	"github.com/limelitgeo/open/internal/provider"
	"github.com/limelitgeo/open/internal/store"
	"github.com/limelitgeo/open/internal/target"
)

// Credential status strings. The environment always wins over a stored value,
// and saying which one is in force stops a user editing a field that cannot
// take effect.
const (
	credNotSet = "not set"
	credEnv    = "set in the environment"
	credSaved  = "saved here"
)

// buildSettings assembles the settings page: what is tracked, what could be,
// and where each key comes from.
func (a *App) buildSettings(r *http.Request, base Base) (SettingsPage, error) {
	ctx := r.Context()
	stored, err := a.db.Targets(ctx, false)
	if err != nil {
		return SettingsPage{}, err
	}
	today, err := a.db.RunsToday(ctx)
	if err != nil {
		return SettingsPage{}, err
	}

	byEngine := map[string][]TargetView{}
	var flat []TargetView
	for _, t := range stored {
		v := TargetView{ID: t.ID, Spec: t.Spec, Access: t.Access}
		if e, ok := engines.Lookup(t.Engine); ok {
			v.EngineLabel = e.Label
		}
		byEngine[t.Engine] = append(byEngine[t.Engine], v)
		flat = append(flat, v)
	}

	page := SettingsPage{
		Base:          base,
		Targets:       flat,
		EngineList:    strings.Join(engines.IDs(), ", "),
		ProviderNames: strings.Join(a.registry.Names(), ", "),
		RunsPerDay:    a.runsPerDay(ctx),
		RunsToday:     today,
	}

	for _, e := range engines.All() {
		card := EngineCard{ID: e.ID, Label: e.Label, Targets: byEngine[e.ID]}
		switch e.Kind {
		case engines.KindChat:
			card.Kind = "chat engine"
		default:
			card.Kind = "search surface"
		}
		var pending []string
		for _, c := range provider.CatalogFor(e.ID) {
			if _, built := a.registry.Lookup(c.Name); !built {
				pending = append(pending, c.Label)
				continue
			}
			card.Options = append(card.Options, TrackOption{
				Provider:  c.Name,
				Label:     c.Label,
				Access:    string(c.Access),
				Note:      c.Note,
				Available: true,
			})
		}
		card.Pending = strings.Join(pending, ", ")
		page.Engines = append(page.Engines, card)
	}

	page.Providers = a.providerCards(ctx)
	return page, nil
}

// providerCards lists every documented provider with where to get its key and
// where its current value is coming from.
func (a *App) providerCards(ctx context.Context) []ProviderKeyCard {
	var out []ProviderKeyCard
	for _, c := range provider.Catalog() {
		labels := make([]string, 0, len(c.Engines))
		for id := range c.Engines {
			if e, ok := engines.Lookup(id); ok {
				labels = append(labels, e.Label)
			}
		}
		sort.Strings(labels)

		card := ProviderKeyCard{
			Name:       c.Name,
			Label:      c.Label,
			Access:     string(c.Access),
			EngineList: strings.Join(labels, ", "),
			Note:       c.Note,
			KeyURL:     c.KeyURL,
		}
		if _, built := a.registry.Lookup(c.Name); built {
			card.Available = true
		} else {
			card.Reason = "not built yet"
		}
		for _, name := range c.Credentials {
			card.Credentials = append(card.Credentials, a.credentialView(ctx, name))
		}
		out = append(out, card)
	}
	return out
}

func (a *App) credentialView(ctx context.Context, name string) CredentialView {
	v := CredentialView{Name: name, Status: credNotSet}
	if config.Credential(name) != "" {
		v.FromEnv, v.Status = true, credEnv
		return v
	}
	if stored, err := a.db.Setting(ctx, credentialPrefix+name); err == nil && stored != "" {
		v.Saved, v.Status = true, credSaved
	}
	return v
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
	page, err := a.buildSettings(r, base)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.write(w, r, "settings", page)
}

// settingsWithFlash re-renders settings carrying a message too specific to be
// a flash code, such as a parser error quoted back to the user.
func (a *App) settingsWithFlash(w http.ResponseWriter, r *http.Request, flash Flash) {
	base, _, err := a.base(r, "Settings", "settings")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	base.Flash = &flash
	page, err := a.buildSettings(r, base)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.write(w, r, "settings", page)
}

// trackEngine is the click-select path: an engine and a provider, rather than
// a target string a user has to learn the grammar for.
func (a *App) trackEngine(w http.ResponseWriter, r *http.Request) {
	engine := strings.TrimSpace(r.FormValue("engine"))
	name := strings.TrimSpace(r.FormValue("provider"))

	entry, ok := provider.CatalogEntryFor(name)
	if !ok {
		a.settingsWithFlash(w, r, Flash{Kind: "error", Text: "That provider is not one this build knows about."})
		return
	}
	// The online flag is meaningful for an API provider and implied for a
	// scraped one, so the click-select path sets it rather than asking a
	// user to know the difference.
	spec := engine + ":" + name
	if entry.Access == provider.AccessAPI {
		spec += ":online"
	}
	a.addTargetSpec(w, r, spec)
}

// addTarget is the advanced path: a raw target string.
func (a *App) addTarget(w http.ResponseWriter, r *http.Request) {
	a.addTargetSpec(w, r, strings.TrimSpace(r.FormValue("spec")))
}

func (a *App) addTargetSpec(w http.ResponseWriter, r *http.Request, spec string) {
	parsed, err := target.Parse(spec)
	if err == nil {
		err = parsed.Validate(a.registry)
	}
	if err != nil {
		// The parser's message names the way out, so it is shown verbatim
		// rather than replaced with a generic "invalid target".
		a.settingsWithFlash(w, r, Flash{Kind: "error", Text: err.Error()})
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

// saveKeys stores the credentials for one provider, encrypted.
func (a *App) saveKeys(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.fail(w, r, err)
		return
	}
	name := strings.TrimSpace(r.FormValue("provider"))
	entry, ok := provider.CatalogEntryFor(name)
	if !ok {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	saved, err := a.storeCredentials(r, entry)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if saved == 0 {
		a.settingsWithFlash(w, r, Flash{Kind: "warn", Text: "Nothing was saved: the field was empty."})
		return
	}
	http.Redirect(w, r, "/settings?flash=key-saved", http.StatusSeeOther)
}

// storeCredentials seals and stores whatever fields the form carried for one
// provider, and reports how many landed.
func (a *App) storeCredentials(r *http.Request, entry provider.CatalogEntry) (int, error) {
	var saved int
	for _, cred := range entry.Credentials {
		value := strings.TrimSpace(r.FormValue("cred_" + cred))
		if value == "" {
			continue
		}
		sealed, err := a.keys.Seal(value)
		if err != nil {
			return saved, err
		}
		if err := a.db.SetSetting(r.Context(), credentialPrefix+cred, sealed); err != nil {
			return saved, err
		}
		saved++
	}
	return saved, nil
}

// credentials reads a credential the way the runner does: the environment
// first, then the encrypted settings store. Shared, so the Test button proves
// the same value a run would use rather than a different one.
func (a *App) credentials(ctx context.Context) provider.CredentialSource {
	return credentials.Source(ctx, a.db, a.keys, a.log)
}

// testKeys presses the provider's own cheapest authenticated call, so a wrong
// key is reported at the moment it is pasted rather than at the next run.
func (a *App) testKeys(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("provider"))
	entry, ok := provider.CatalogEntryFor(name)
	if !ok {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	ctx := r.Context()

	if missing := a.registry.MissingCredentials(name, a.credentials(ctx)); len(missing) > 0 {
		a.settingsWithFlash(w, r, Flash{Kind: "error", Text: strings.Join(missing, " and ") + " is not set yet."})
		return
	}
	p, err := a.registry.New(name, a.credentials(ctx))
	if err != nil {
		a.settingsWithFlash(w, r, Flash{Kind: "error", Text: entry.Label + " did not accept the key: " + err.Error()})
		return
	}
	if err := p.Test(ctx); err != nil {
		a.settingsWithFlash(w, r, Flash{Kind: "error", Text: entry.Label + " did not accept the key: " + err.Error()})
		return
	}
	a.settingsWithFlash(w, r, Flash{Kind: "ok", Text: entry.Label + " accepted the key."})
}

func (a *App) saveLimits(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("runs_per_day")))
	if err != nil || n <= 0 {
		a.settingsWithFlash(w, r, Flash{Kind: "error", Text: "The run ceiling has to be a positive number."})
		return
	}
	if err := a.db.SetSetting(r.Context(), settingRunsPerDay, strconv.Itoa(n)); err != nil {
		a.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings?flash=limits-saved", http.StatusSeeOther)
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
