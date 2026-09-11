package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	// A fresh instance has no limelit.yaml and boots into the wizard, so a
	// missing file must produce a usable zero config rather than an error.
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load on a missing file: %v", err)
	}
	if cfg.Configured() {
		t.Error("an empty config reported itself as configured")
	}
	if cfg.Limits.RunsPerDay != DefaultRunsPerDay {
		t.Errorf("RunsPerDay = %d, want the default %d", cfg.Limits.RunsPerDay, DefaultRunsPerDay)
	}
	if cfg.Schedule != "off" {
		t.Errorf("Schedule = %q, want %q", cfg.Schedule, "off")
	}
}

func TestLoadFullConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "limelit.yaml")
	body := `
property:
  name: Acme
  domain: acme.com
  aliases: [Acme Inc, acme.io]
competitors:
  - { name: Globex, domain: globex.com }
  - { name: Initech, domain: initech.com, category: legacy }
targets:
  - chatgpt:openai:gpt-5.5:online
  - ai_overview:dataforseo
limits:
  runs_per_day: 50
schedule: daily
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Configured() {
		t.Fatalf("a complete config reported itself unconfigured: %v", cfg.Validate())
	}
	if cfg.Property.Name != "Acme" || cfg.Property.Domain != "acme.com" {
		t.Errorf("property = %+v", cfg.Property)
	}
	if len(cfg.Property.Aliases) != 2 {
		t.Errorf("aliases = %v, want 2", cfg.Property.Aliases)
	}
	if len(cfg.Competitors) != 2 || cfg.Competitors[1].Category != "legacy" {
		t.Errorf("competitors = %+v", cfg.Competitors)
	}
	if len(cfg.Targets) != 2 {
		t.Errorf("targets = %v, want 2", cfg.Targets)
	}
	if cfg.Limits.RunsPerDay != 50 {
		t.Errorf("RunsPerDay = %d, want 50 from the file", cfg.Limits.RunsPerDay)
	}
	if cfg.Schedule != "daily" {
		t.Errorf("Schedule = %q", cfg.Schedule)
	}
}

func TestValidateNamesEveryProblem(t *testing.T) {
	// One pass should report everything wrong, so a user fixes the file once
	// instead of discovering problems one restart at a time.
	cfg := &Config{Competitors: []Competitor{{Name: "Globex"}}}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate accepted an empty config")
	}
	for _, want := range []string{"property.name", "property.domain", "Globex", "targets is empty"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate error %q does not mention %q", err, want)
		}
	}
}

func TestCompetitorWithoutDomainIsRejected(t *testing.T) {
	// Domain is the identity key, matching Limelit Cloud: name-only rows would
	// collide with citation-based domain tracking.
	cfg := &Config{
		Property:    Property{Name: "Acme", Domain: "acme.com"},
		Competitors: []Competitor{{Name: "Globex"}},
		Targets:     []string{"chatgpt:openai"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("a competitor with no domain was accepted")
	}
}

func TestDataDirRespectsEnv(t *testing.T) {
	t.Setenv("LIMELIT_DATA_DIR", "/var/lib/limelit")
	if got := DataDir(); got != "/var/lib/limelit" {
		t.Errorf("DataDir() = %q", got)
	}
	if got := DatabasePath(); got != "/var/lib/limelit/limelit.db" {
		t.Errorf("DatabasePath() = %q", got)
	}
}

func TestDataDirDefault(t *testing.T) {
	t.Setenv("LIMELIT_DATA_DIR", "")
	if got := DataDir(); got != "data" {
		t.Errorf("DataDir() = %q, want %q", got, "data")
	}
}

func TestCredentialTrimsAndReadsEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "  sk-test  ")
	if got := Credential("OPENAI_API_KEY"); got != "sk-test" {
		t.Errorf("Credential = %q, want trimmed", got)
	}
	if got := Credential("LIMELIT_NO_SUCH_VAR"); got != "" {
		t.Errorf("Credential for an unset var = %q, want empty", got)
	}
}
