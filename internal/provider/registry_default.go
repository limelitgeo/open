package provider

// Default returns the registry a running instance uses.
//
// A provider appears here when its implementation lands, not when it is
// documented. An instance that lists a target for a provider that is not yet
// implemented fails target validation at startup with a message naming what
// IS available, which is the honest failure: the alternative is a target that
// validates and then never produces a row.
//
// docs/providers.md is the plan; this function is the truth.
func Default() *Registry {
	reg := NewRegistry()
	// Providers register here as they are implemented.
	return reg
}
