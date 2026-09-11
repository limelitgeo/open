package ui

// View models. These are the only shapes the templates see, which keeps
// formatting decisions in Go where they can be tested, rather than in
// template expressions where they cannot.

// NavItem is one sidebar link.
type NavItem struct {
	// Section starts a labelled group above this item when set.
	Section string
	Label   string
	Href    string
	Count   string
	Current bool
}

// Flash is a one-off message above the page body.
type Flash struct {
	// Kind is info, ok, warn or error, matching the notice styles.
	Kind string
	Text string
}

// PropertyView is the brand as the chrome shows it.
type PropertyView struct {
	Name   string
	Domain string
}

// Base is what every chrome template needs.
type Base struct {
	Title   string
	Version string
	// Theme is "" so the page follows the viewer's system setting. A stored
	// preference writes "light" or "dark" here.
	Theme    string
	Nav      []NavItem
	Property PropertyView
	Flash    *Flash
	LastRun  string
	// CanRun is false until there is something to run: a prompt, a target,
	// and a provider that exists in this build. The button says why.
	CanRun bool
}

// StatView is one number on the overview.
type StatView struct {
	Label string
	Value string
	Sub   string
}

// TargetView is one row in a target table.
type TargetView struct {
	ID          int64
	Spec        string
	EngineLabel string
	Access      string
	Chats       int
}

// PromptView is one row in the prompts table.
type PromptView struct {
	ID       int64
	Text     string
	Category string
	Branded  bool
	Chats    int
}

// CompetitorView is one row in the competitors table.
type CompetitorView struct {
	ID       int64
	Name     string
	Domain   string
	Mentions int
}

// CountsView is the row counts the sidebar shows.
type CountsView struct {
	Prompts     int
	Competitors int
	Targets     int
	Chats       int
}

// OverviewPage is the dashboard.
type OverviewPage struct {
	Base
	WindowDays int
	Counts     CountsView
	Stats      []StatView
	Targets    []TargetView
}

// PromptsPage lists tracked prompts.
type PromptsPage struct {
	Base
	Prompts []PromptView
}

// CompetitorsPage lists tracked competitors.
type CompetitorsPage struct {
	Base
	Competitors []CompetitorView
}

// TrackOption is one provider a user can click to start tracking an engine.
type TrackOption struct {
	Provider string
	Label    string
	Access   string
	Note     string
	// Available is false for a provider that is documented but not built
	// yet. The button is disabled and Reason says why, which is more use
	// than hiding it and leaving the engine looking unreachable.
	Available bool
	Reason    string
}

// EngineCard is one tracked answer engine and how to reach it.
type EngineCard struct {
	ID      string
	Label   string
	Kind    string
	Targets []TargetView
	// Options are the providers a user can click today.
	Options []TrackOption
	// Pending names the documented providers this build does not implement
	// yet, collapsed to one line. Seven disabled buttons per engine would
	// bury the one that works.
	Pending string
}

// CredentialView is one environment variable a provider needs, and where its
// value is coming from.
type CredentialView struct {
	Name string
	// Status is "not set", "set in the environment", or "saved here". The
	// environment always wins, and saying so stops a user editing a field
	// that cannot take effect.
	Status  string
	FromEnv bool
	Saved   bool
}

// ProviderKeyCard is one provider in the keys section.
type ProviderKeyCard struct {
	Name        string
	Label       string
	Access      string
	EngineList  string
	Note        string
	KeyURL      string
	Credentials []CredentialView
	Available   bool
	Reason      string
}

// SettingsPage is targets, keys and limits.
type SettingsPage struct {
	Base
	Engines       []EngineCard
	Providers     []ProviderKeyCard
	Targets       []TargetView
	EngineList    string
	ProviderNames string
	RunsPerDay    int
	RunsToday     int
}

// UpgradePage is the honest boundary with the hosted product.
type UpgradePage struct {
	Base
	CloudFeatures []string
}

// PlaceholderPage stands in for a screen that is not built yet. It says so
// rather than showing an empty frame that looks broken.
type PlaceholderPage struct {
	Base
	Lede     string
	Detail   string
	IssueURL string
}

// WizardBase is the chrome for the setup flow.
type WizardBase struct {
	Title     string
	Version   string
	Theme     string
	Steps     []string
	StepIndex int
	Flash     *Flash
}

// StepNumber is the 1-based step, for "Step 2 of 4".
func (w WizardBase) StepNumber() int { return w.StepIndex + 1 }

// BrandForm is step one.
type BrandForm struct {
	Name    string
	Domain  string
	Aliases string
}

// CompetitorField is one name and domain pair in step two.
type CompetitorField struct {
	Name   string
	Domain string
}

// CompetitorsForm is step two.
type CompetitorsForm struct {
	Category    string
	Competitors []CompetitorField
}

// WizardBrandPage is step one.
type WizardBrandPage struct {
	WizardBase
	Form BrandForm
}

// WizardCompetitorsPage is step two.
type WizardCompetitorsPage struct {
	WizardBase
	Form CompetitorsForm
}

// WizardPromptView is one proposed prompt in step three.
type WizardPromptView struct {
	Text     string
	Category string
	Branded  bool
}

// WizardPromptsPage is step three.
type WizardPromptsPage struct {
	WizardBase
	Prompts []WizardPromptView
}

// ProviderOption is one provider a user could connect in step four.
type ProviderOption struct {
	Name       string
	Access     string
	EngineList string
}

// WizardProviderPage is step four.
type WizardProviderPage struct {
	WizardBase
	Providers []ProviderKeyCard
	// AnyAvailable is false when no provider is built into this binary yet,
	// which turns the step into an honest exit rather than a dead form.
	AnyAvailable bool
}
