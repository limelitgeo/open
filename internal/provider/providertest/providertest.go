// Package providertest builds a registry that mirrors the provider table in
// docs/providers.md, backed by stubs.
//
// It exists so the parser, the runner and the metrics can be tested against
// the real target strings a user will write, before the real providers are
// implemented and without keys or network afterwards. It is a test helper:
// nothing in cmd/ imports it, so these declarations can never be mistaken for
// a working provider in a deployment.
//
// When a real provider lands, its entry here stays as the offline double and
// its engine list must match the real registration. TestCatalogMatchesDocs
// keeps both honest against the documentation.
package providertest

import (
	"github.com/limelitgeo/open/internal/engines"
	"github.com/limelitgeo/open/internal/provider"
)

// Entry is one documented provider.
type Entry struct {
	Name        string
	Access      provider.Access
	Engines     map[string]string
	Credentials []string
}

// Catalog is the provider table from docs/providers.md, in code.
func Catalog() []Entry {
	return []Entry{
		// API providers: the vendor's model with web search on.
		{
			Name:        "openai",
			Access:      provider.AccessAPI,
			Engines:     map[string]string{engines.ChatGPT: "gpt-5.5"},
			Credentials: []string{"OPENAI_API_KEY"},
		},
		{
			Name:        "anthropic",
			Access:      provider.AccessAPI,
			Engines:     map[string]string{engines.Claude: "claude-sonnet-5"},
			Credentials: []string{"ANTHROPIC_API_KEY"},
		},
		{
			Name:        "perplexity",
			Access:      provider.AccessAPI,
			Engines:     map[string]string{engines.Perplexity: "sonar"},
			Credentials: []string{"PERPLEXITY_API_KEY"},
		},
		{
			Name:        "google",
			Access:      provider.AccessAPI,
			Engines:     map[string]string{engines.Gemini: "gemini-2.5-flash"},
			Credentials: []string{"GOOGLE_API_KEY"},
		},
		{
			Name:   "openrouter",
			Access: provider.AccessAPI,
			Engines: map[string]string{
				engines.ChatGPT:    "openai/gpt-5.5",
				engines.Claude:     "anthropic/claude-sonnet-5",
				engines.Gemini:     "google/gemini-2.5-flash",
				engines.Perplexity: "perplexity/sonar",
			},
			Credentials: []string{"OPENROUTER_API_KEY"},
		},

		// Scraped providers: the consumer surface. No model to pin, so every
		// engine maps to the empty default.
		{
			Name:   "dataforseo",
			Access: provider.AccessScraped,
			Engines: map[string]string{
				engines.AIOverview: "",
				engines.AIMode:     "",
				engines.ChatGPT:    "",
				engines.Gemini:     "",
				engines.Perplexity: "",
			},
			Credentials: []string{"DATAFORSEO_LOGIN", "DATAFORSEO_PASSWORD"},
		},
		{
			Name:   "searchapi",
			Access: provider.AccessScraped,
			Engines: map[string]string{
				engines.AIOverview:  "",
				engines.AIMode:      "",
				engines.BingCopilot: "",
			},
			Credentials: []string{"SEARCHAPI_KEY"},
		},
		{
			Name:   "cloro",
			Access: provider.AccessScraped,
			Engines: map[string]string{
				engines.ChatGPT:    "",
				engines.AIMode:     "",
				engines.Perplexity: "",
				engines.Gemini:     "",
			},
			Credentials: []string{"CLORO_API_KEY"},
		},
		{
			Name:   "brightdata",
			Access: provider.AccessScraped,
			Engines: map[string]string{
				engines.ChatGPT:     "",
				engines.AIMode:      "",
				engines.AIOverview:  "",
				engines.Perplexity:  "",
				engines.Gemini:      "",
				engines.BingCopilot: "",
			},
			Credentials: []string{"BRIGHTDATA_API_TOKEN"},
		},
		{
			Name:   "oxylabs",
			Access: provider.AccessScraped,
			Engines: map[string]string{
				engines.ChatGPT:    "",
				engines.AIMode:     "",
				engines.AIOverview: "",
				engines.Perplexity: "",
			},
			Credentials: []string{"OXYLABS_USERNAME", "OXYLABS_PASSWORD"},
		},
		{
			Name:   "olostep",
			Access: provider.AccessScraped,
			Engines: map[string]string{
				engines.ChatGPT:    "",
				engines.AIMode:     "",
				engines.Perplexity: "",
			},
			Credentials: []string{"OLOSTEP_API_KEY"},
		},
	}
}

// Registry returns a registry holding every documented provider, each backed
// by a stub that answers with canned text.
func Registry() *provider.Registry {
	reg := provider.NewRegistry()
	for _, e := range Catalog() {
		entry := e
		reg.Register(provider.Registration{
			Name:        entry.Name,
			Access:      entry.Access,
			Engines:     entry.Engines,
			Credentials: entry.Credentials,
			New: func(src provider.CredentialSource) (provider.Provider, error) {
				return provider.NewStub(provider.StubConfig{
					Access:  entry.Access,
					Engines: entry.Engines,
					Answer:  "Canned answer from the " + entry.Name + " double.",
				}), nil
			},
		})
	}
	return reg
}
