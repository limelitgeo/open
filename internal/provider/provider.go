// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

// Package provider is the boundary between this project and the services that
// actually answer a prompt: vendor model APIs and consumer-surface scrapers.
//
// Everything downstream of a provider is identical for every provider. The
// mention matcher, the citation classifier and the metrics do not know or
// care which one produced a row, which is what lets an api target and a
// scraped target of the same engine be compared honestly instead of averaged.
//
// The rules a provider follows are in docs/providers.md. Two are enforced by
// the shape of this file: nothing here converts usage to currency, and no
// provider is given a database handle.
package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
)

// Access separates vendor APIs from consumer surfaces reached by scraping.
//
// It travels with every row that descends from a target because the two
// measure different things: a model API with web search on is not the answer
// a person sees in the consumer product, and presenting them as one number
// would be the single most misleading thing this tool could do.
type Access string

const (
	// AccessAPI is the vendor's model, called directly, billed per token.
	AccessAPI Access = "api"
	// AccessScraped is the consumer surface, fetched through a scraping
	// service, billed per request.
	AccessScraped Access = "scraped"
)

// Valid reports whether a is one of the two known modes.
func (a Access) Valid() bool { return a == AccessAPI || a == AccessScraped }

func (a Access) String() string { return string(a) }

// Request is one prompt asked of one engine.
type Request struct {
	// Engine is the id from package engines.
	Engine string
	// Model is the optional pin from the target. Empty means the provider's
	// default for this engine.
	Model string
	// Online asks for web search where the provider treats it as a switch.
	// Scraped surfaces are always online and ignore it.
	Online bool
	// Prompt is the question, verbatim as the user wrote it.
	Prompt string
	// LocationCountry is an ISO 3166 alpha-2 code, optional. Geo-sensitive
	// surfaces answer differently by country.
	LocationCountry string
	// LanguageCode is a BCP 47 base language, optional.
	LanguageCode string
}

// Citation is one source an engine attributed, exactly as the engine gave it.
//
// No normalisation happens here. urlnorm and the citation classifier run once,
// afterwards, for every provider alike; a provider that cleaned up its own
// URLs would make its rows quietly incomparable with the rest.
type Citation struct {
	// URL is the link as the engine returned it.
	URL string
	// Title is the engine's own label for the source, when it gave one.
	Title string
	// Position is 1-based, in the order the engine listed them.
	Position int
}

// Response is one answer.
type Response struct {
	// Text is the answer body, which is what the mention matcher reads.
	Text string
	// Model is what actually answered, as the provider reported it. It can
	// differ from the requested pin, and that difference is worth storing:
	// a silently swapped model explains a step change in the numbers.
	Model string
	// Citations are the sources the engine attributed, in its order.
	Citations []Citation
	// FanOut is the searches the engine ran while grounding this answer, in
	// the order it ran them, deduplicated.
	//
	// It is the closest thing to seeing the question the engine actually
	// asked on your behalf, and it is often not the question the user typed.
	// Providers that do not expose it leave this empty; that is an absence
	// of evidence, not evidence the engine searched for nothing.
	FanOut []string
	// InputTokens and OutputTokens are zero for scraped providers, which do
	// not bill by token.
	InputTokens  int
	OutputTokens int
	// Calls is how many billable requests this answer cost. One for an API
	// call; a scraper that submits and then polls may report more.
	Calls int
}

// Provider asks one engine one prompt.
//
// Implementations are stateless: credentials arrive at construction, nothing
// reads the database, and nothing retries beyond a single transient attempt.
// The runner owns backoff, concurrency and the runs_per_day guard, because
// those are instance-wide decisions and a provider cannot see the instance.
type Provider interface {
	// Name is the provider segment of a target string.
	Name() string
	// Access is fixed per provider.
	Access() Access
	// Engines maps each engine id this provider can reach to its default
	// model, or to the empty string where a model pin is meaningless (a
	// scraped surface has no model to choose).
	Engines() map[string]string
	// Run asks one engine one prompt.
	Run(ctx context.Context, req Request) (Response, error)
	// Test checks credentials with the cheapest call the provider offers.
	// The settings screen calls it when a key is saved, so a wrong key is
	// reported at the moment it is pasted rather than at the next run.
	Test(ctx context.Context) error
}

// The typed errors every provider maps its failures onto. The runner reads
// these, not error strings, to decide what to do next.
var (
	// ErrAuth is a rejected or missing credential. Retrying will not help,
	// and the settings screen shows it against the offending key.
	ErrAuth = errors.New("provider: credentials rejected")

	// ErrRateLimited is a temporary refusal. The runner backs off.
	ErrRateLimited = errors.New("provider: rate limited")

	// ErrQuota is a vendor account with no credit or quota left. Vendors
	// send it with the same 429 as a rate limit, but it is a billing state,
	// not a burst: retrying will not help, and every answer that hour fails
	// the same way. The demo lost a whole engine to one that read as "rate
	// limited" 47 times. It is named so the failed rows say what to fix.
	ErrQuota = errors.New("provider: no credit or quota left")

	// ErrUnsupportedEngine means this provider cannot reach that engine.
	// Target validation catches this before a run, so seeing it at runtime
	// means a registration and an implementation disagree.
	ErrUnsupportedEngine = errors.New("provider: engine not supported")

	// ErrNoAnswerSurface means the surface did not render for this query.
	//
	// This is NOT a miss for the brand, and the distinction is the whole
	// reason the error exists: a Google query that produced no AI Overview
	// says nothing about who the overview would have named. Chats recorded
	// with this status are excluded from every metric denominator. Counting
	// them as answers that ignored the brand would drag visibility toward
	// zero for reasons that have nothing to do with the brand.
	ErrNoAnswerSurface = errors.New("provider: answer surface did not render")
)

// CredentialSource reads one credential by environment-variable name.
// config.Credential satisfies it, and tests pass a map-backed stub.
type CredentialSource func(name string) string

// StaticCredentials builds a CredentialSource from a map, for tests and for
// the settings store.
func StaticCredentials(m map[string]string) CredentialSource {
	return func(name string) string { return m[name] }
}

// truncateBody trims an error body for an error message. A provider's own
// wording is usually the most useful thing available, and the whole body is
// usually far more than anyone wants in a log line.
func truncateBody(body string) string {
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

// readLimited reads a response body under a cap and closes it.
//
// The cap matters: these are third-party responses and a scraped results page
// can be very large, so an unbounded read hands a remote service the ability
// to exhaust this process's memory.
func readLimited(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}
