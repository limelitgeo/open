package engines

import "testing"

func TestLookupKnownEngines(t *testing.T) {
	// These ids are Limelit Cloud's and are persisted on every chat row. A
	// rename would make `limelit upgrade` a lossy import, so the test pins
	// them as literals rather than referring to the constants.
	for _, id := range []string{"chatgpt", "claude", "perplexity", "gemini", "ai_overview", "ai_mode", "bing_copilot"} {
		e, ok := Lookup(id)
		if !ok {
			t.Errorf("engine %q is not registered", id)
			continue
		}
		if e.ID != id {
			t.Errorf("Lookup(%q).ID = %q", id, e.ID)
		}
		if e.Label == "" {
			t.Errorf("engine %q has no label", id)
		}
		if e.Kind != KindChat && e.Kind != KindSearchSurface {
			t.Errorf("engine %q has kind %q", id, e.Kind)
		}
	}
}

func TestSearchSurfacesAreMarked(t *testing.T) {
	// Kind decides whether an absent answer is a signal. A search surface can
	// fail to render, which says nothing about the brand; a chat engine always
	// answers something, so silence about the brand is real.
	surfaces := map[string]Kind{
		ChatGPT:     KindChat,
		Claude:      KindChat,
		Perplexity:  KindChat,
		Gemini:      KindChat,
		AIOverview:  KindSearchSurface,
		AIMode:      KindSearchSurface,
		BingCopilot: KindSearchSurface,
	}
	for id, want := range surfaces {
		e, _ := Lookup(id)
		if e.Kind != want {
			t.Errorf("engine %q kind = %q, want %q", id, e.Kind, want)
		}
	}
}

func TestUnknownEngine(t *testing.T) {
	if Known("bard") {
		t.Error("Known(\"bard\") = true")
	}
	if _, ok := Lookup(""); ok {
		t.Error("the empty id resolved to an engine")
	}
}

func TestIDsIsSortedAndComplete(t *testing.T) {
	ids := IDs()
	if len(ids) != len(All()) {
		t.Fatalf("IDs returned %d, All returned %d", len(ids), len(All()))
	}
	for i := 1; i < len(ids); i++ {
		if ids[i-1] > ids[i] {
			t.Fatalf("IDs is not sorted: %v", ids)
		}
	}
}

func TestAllReturnsACopy(t *testing.T) {
	first := All()
	first[0].Label = "mutated"
	if All()[0].Label == "mutated" {
		t.Error("All() handed out the package's own slice")
	}
}
