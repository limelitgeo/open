// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

// Package engines is the single registry of the answer engines this project
// tracks. Every surface that needs an engine list resolves it here rather
// than restating one of its own, because a list restated in three places
// drifts in three directions.
//
// Engine ids are Limelit Cloud's. That is not cosmetic: `limelit upgrade`
// pushes chats keyed by these ids into Cloud, and a rename here would mean a
// lossy import.
package engines

import "sort"

// Kind separates engines answered by a chat-completion model from search
// products whose answer panel may not render at all.
//
// The distinction is user-visible. A chat engine that returns no brand
// mention means the brand was absent from the answer, which is a visibility
// signal. A search surface can be absent altogether, which is not a signal
// about the brand at all and must stay out of the denominator.
type Kind string

const (
	// KindChat is an engine answered by a chat-completion model.
	KindChat Kind = "chat"
	// KindSearchSurface is a search product's generated answer panel.
	KindSearchSurface Kind = "search_surface"
)

// Engine ids, as stored on every chat row.
const (
	ChatGPT     = "chatgpt"
	Claude      = "claude"
	Perplexity  = "perplexity"
	Gemini      = "gemini"
	AIOverview  = "ai_overview"
	AIMode      = "ai_mode"
	BingCopilot = "bing_copilot"
)

// Engine is one tracked answer surface.
type Engine struct {
	// ID is the slug persisted on chat rows and accepted in a target.
	ID string
	// Label is the user-facing name.
	Label string
	// Kind decides whether an absent answer is a signal or a non-event.
	Kind Kind
}

var all = []Engine{
	{ID: ChatGPT, Label: "ChatGPT", Kind: KindChat},
	{ID: Claude, Label: "Claude", Kind: KindChat},
	{ID: Perplexity, Label: "Perplexity", Kind: KindChat},
	{ID: Gemini, Label: "Gemini", Kind: KindChat},
	{ID: AIOverview, Label: "Google AI Overview", Kind: KindSearchSurface},
	{ID: AIMode, Label: "Google AI Mode", Kind: KindSearchSurface},
	{ID: BingCopilot, Label: "Bing Copilot", Kind: KindSearchSurface},
}

var byID = func() map[string]Engine {
	m := make(map[string]Engine, len(all))
	for _, e := range all {
		m[e.ID] = e
	}
	return m
}()

// All returns every engine in display order.
func All() []Engine {
	out := make([]Engine, len(all))
	copy(out, all)
	return out
}

// Lookup returns the engine with this id.
func Lookup(id string) (Engine, bool) {
	e, ok := byID[id]
	return e, ok
}

// Known reports whether id names an engine.
func Known(id string) bool {
	_, ok := byID[id]
	return ok
}

// IDs returns every engine id, sorted, for error messages that have to list
// the valid options.
func IDs() []string {
	out := make([]string, 0, len(all))
	for _, e := range all {
		out = append(out, e.ID)
	}
	sort.Strings(out)
	return out
}
