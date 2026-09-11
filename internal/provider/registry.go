// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package provider

import (
	"fmt"
	"sort"
	"strings"
)

// Registration is what a provider declares about itself before anyone has
// credentials for it.
//
// The metadata is separate from the constructor on purpose. A target has to
// be validated at config-load time, when no key may be present: "is
// ai_overview:anthropic a real target" must be answerable without building an
// Anthropic client, or a typo in limelit.yaml would surface as a runtime
// failure halfway through a scheduled run.
type Registration struct {
	// Name is the provider segment of a target string.
	Name string
	// Access is fixed for every target this provider serves.
	Access Access
	// Engines maps engine id to default model, empty where a pin is
	// meaningless.
	Engines map[string]string
	// Credentials names the environment variables this provider needs, for
	// the settings screen and for error messages that can say what is
	// missing instead of just refusing.
	Credentials []string
	// New builds the provider. It returns ErrAuth when a required
	// credential is absent, so a missing key and a wrong key report the
	// same way.
	New func(CredentialSource) (Provider, error)
}

// Registry holds the providers this binary knows about.
//
// It is a value, not a package global, so a test can build a registry with
// exactly one stub in it and a future settings screen can present a filtered
// view without mutating global state.
type Registry struct {
	byName map[string]Registration
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Registration)}
}

// Register adds a provider. It panics on a duplicate or an invalid
// registration because both are programmer errors discovered at startup, not
// conditions a running instance can do anything about.
func (r *Registry) Register(reg Registration) {
	if reg.Name == "" {
		panic("provider: registration has no name")
	}
	if !reg.Access.Valid() {
		panic(fmt.Sprintf("provider %q: access %q is neither api nor scraped", reg.Name, reg.Access))
	}
	if len(reg.Engines) == 0 {
		panic(fmt.Sprintf("provider %q: registration lists no engines", reg.Name))
	}
	if reg.New == nil {
		panic(fmt.Sprintf("provider %q: registration has no constructor", reg.Name))
	}
	if _, dup := r.byName[reg.Name]; dup {
		panic(fmt.Sprintf("provider %q: registered twice", reg.Name))
	}
	r.byName[reg.Name] = reg
}

// Lookup returns the registration for a provider name.
func (r *Registry) Lookup(name string) (Registration, bool) {
	reg, ok := r.byName[name]
	return reg, ok
}

// Names returns every registered provider, sorted, for error messages and the
// settings screen.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.byName))
	for name := range r.byName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ProvidersFor returns the providers that can reach an engine, sorted. The
// settings screen uses it to explain what a user's options are for a surface
// they have not configured yet.
func (r *Registry) ProvidersFor(engine string) []string {
	var out []string
	for name, reg := range r.byName {
		if _, ok := reg.Engines[engine]; ok {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// New constructs the provider named in a registration, reading its
// credentials from src.
func (r *Registry) New(name string, src CredentialSource) (Provider, error) {
	reg, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q, known providers are %s", name, strings.Join(r.Names(), ", "))
	}
	if src == nil {
		src = func(string) string { return "" }
	}
	return reg.New(src)
}

// MissingCredentials lists the credentials a provider needs and does not have.
// It exists so the settings screen can say which variable to set rather than
// reporting a bare authentication failure.
func (r *Registry) MissingCredentials(name string, src CredentialSource) []string {
	reg, ok := r.byName[name]
	if !ok {
		return nil
	}
	if src == nil {
		src = func(string) string { return "" }
	}
	var missing []string
	for _, cred := range reg.Credentials {
		if strings.TrimSpace(src(cred)) == "" {
			missing = append(missing, cred)
		}
	}
	return missing
}
