package provider

// Default returns the registry a running instance uses.
//
// A provider appears here when its implementation lands, not when it is
// documented. An instance that lists a target for a provider that is not yet
// implemented fails target validation at startup with a message naming what
// IS available, which is the honest failure: the alternative is a target that
// validates and then never produces a row.
//
// Catalog() is the plan, and the settings screen reads it so a user can see
// and prepare for what is coming. This function is the truth.
func Default() *Registry {
	reg := NewRegistry()

	reg.Register(Registration{
		Name:        "openai",
		Access:      AccessAPI,
		Engines:     map[string]string{ChatGPTEngine: OpenAIDefaultModel},
		Credentials: []string{"OPENAI_API_KEY"},
		New: func(src CredentialSource) (Provider, error) {
			return NewOpenAI(src("OPENAI_API_KEY"))
		},
	})

	return reg
}
