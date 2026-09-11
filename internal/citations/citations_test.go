package citations

import "testing"

func classifier() *Classifier {
	return NewClassifier([]string{"acme.com"}, []string{"globex.com", "https://www.initech.com/pricing"})
}

func TestHostNormalisation(t *testing.T) {
	// The same function normalises what a user typed into the competitors
	// form and what an engine returned as a link. If those disagree, a
	// competitor's own citations are not recognised as theirs.
	for raw, want := range map[string]string{
		"acme.com":                      "acme.com",
		"ACME.com":                      "acme.com",
		"www.acme.com":                  "acme.com",
		"https://acme.com":              "acme.com",
		"https://www.acme.com/pricing":  "acme.com",
		"http://acme.com:8080/x?y=1#z":  "acme.com",
		"https://help.acme.com/article": "help.acme.com",
		"acme.com.":                     "acme.com",
		"https://acme.com?utm=openai":   "acme.com",
		"":                              "",
		"not a url":                     "",
		"localhost":                     "",
	} {
		if got := Host(raw); got != want {
			t.Errorf("Host(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSiteGroupsSubdomains(t *testing.T) {
	// A top-sources table should read like a list of companies, not a list of
	// subdomains.
	for host, want := range map[string]string{
		"acme.com":             "acme.com",
		"help.acme.com":        "acme.com",
		"blog.help.acme.com":   "acme.com",
		"acme.co.uk":           "acme.co.uk",
		"help.acme.co.uk":      "acme.co.uk",
		"acme.com.au":          "acme.com.au",
		"en.wikipedia.org":     "wikipedia.org",
		"news.ycombinator.com": "ycombinator.com",
		"":                     "",
	} {
		if got := Site(host); got != want {
			t.Errorf("Site(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestClassifyEveryType(t *testing.T) {
	c := classifier()
	cases := []struct {
		url  string
		want string
	}{
		{"https://acme.com/pricing", TypeOwn},
		{"https://www.acme.com/blog/post", TypeOwn},
		{"https://help.acme.com/faq", TypeOwn},
		{"https://globex.com/compare", TypeCompetitor},
		{"https://www.initech.com/", TypeCompetitor},
		{"https://www.reddit.com/r/seo/comments/x", TypeSocial},
		{"https://g2.com/products/acme/reviews", TypeSocial},
		{"https://en.wikipedia.org/wiki/Thing", TypeInformational},
		{"https://nist.gov/report", TypeInformational},
		{"https://cs.stanford.edu/paper", TypeInformational},
		{"https://ox.ac.uk/research", TypeInformational},
		{"https://techcrunch.com/2026/09/11/story", TypeOther},
	}
	raws := make([]Raw, 0, len(cases))
	for _, tc := range cases {
		raws = append(raws, Raw{URL: tc.url})
	}
	got := c.Classify(raws)
	if len(got) != len(cases) {
		t.Fatalf("classified %d of %d", len(got), len(cases))
	}
	for i, tc := range cases {
		if got[i].SourceType != tc.want {
			t.Errorf("%s classified as %q, want %q", tc.url, got[i].SourceType, tc.want)
		}
	}
}

func TestWwwAndTrailingSlashGroupTogether(t *testing.T) {
	c := classifier()
	got := c.Classify([]Raw{
		{URL: "https://acme.com"},
		{URL: "https://www.acme.com/"},
		{URL: "http://ACME.com/pricing"},
	})
	sites := Hosts(got)
	if len(sites) != 1 || sites[0] != "acme.com" {
		t.Errorf("three spellings produced sites %v, want one", sites)
	}
	for _, cit := range got {
		if cit.SourceType != TypeOwn {
			t.Errorf("%s classified as %q", cit.URL, cit.SourceType)
		}
	}
}

func TestOriginalURLIsKept(t *testing.T) {
	// A user has to be able to open exactly what the model saw, tracking
	// parameters and all.
	c := classifier()
	const raw = "https://ahrefs.com/brand-radar?utm_source=openai"
	got := c.Classify([]Raw{{URL: raw}})
	if got[0].URL != raw {
		t.Errorf("stored URL = %q, want it verbatim", got[0].URL)
	}
	if got[0].Host != "ahrefs.com" {
		t.Errorf("host = %q", got[0].Host)
	}
}

func TestPositionsRenumberOverWhatSurvives(t *testing.T) {
	// A dropped junk URL must not leave a hole in the numbering, or position
	// 3 would mean different things in different answers.
	c := classifier()
	got := c.Classify([]Raw{
		{URL: "https://acme.com/a"},
		{URL: "not a url at all"},
		{URL: "https://globex.com/b"},
	})
	if len(got) != 2 {
		t.Fatalf("kept %d citations", len(got))
	}
	if got[0].Position != 1 || got[1].Position != 2 {
		t.Errorf("positions = %d, %d", got[0].Position, got[1].Position)
	}
}

func TestOwnDomainWinsOverAMisconfiguredCompetitor(t *testing.T) {
	// Counting your own site as a rival's would be the most misleading row on
	// the page.
	c := NewClassifier([]string{"acme.com"}, []string{"acme.com"})
	got := c.Classify([]Raw{{URL: "https://acme.com/x"}})
	if got[0].SourceType != TypeOwn {
		t.Errorf("classified as %q", got[0].SourceType)
	}
}

func TestReclassifyAfterAddingACompetitor(t *testing.T) {
	// Adding a competitor has to change what their existing citations mean,
	// or the source mix stays wrong until the next run.
	before := NewClassifier([]string{"acme.com"}, nil)
	raw := []Raw{{URL: "https://globex.com/compare"}}
	if got := before.Classify(raw); got[0].SourceType != TypeOther {
		t.Errorf("before tracking, classified as %q, want other", got[0].SourceType)
	}
	after := NewClassifier([]string{"acme.com"}, []string{"globex.com"})
	if got := after.Classify(raw); got[0].SourceType != TypeCompetitor {
		t.Errorf("after tracking, classified as %q, want competitor", got[0].SourceType)
	}
}

func TestEmptyInput(t *testing.T) {
	if got := classifier().Classify(nil); got != nil {
		t.Errorf("nil input produced %+v", got)
	}
	if got := Hosts(nil); got != nil {
		t.Errorf("Hosts(nil) = %v", got)
	}
}
