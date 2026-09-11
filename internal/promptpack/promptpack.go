// Package promptpack fills a starter set of tracked prompts from what the
// setup wizard already knows: the brand, its category phrase and its
// competitors.
//
// The pack is a fixed template list, not a model call. That is a deliberate
// line: generating prompts from a model is a hosted feature, and it would
// also mean the first thing a new instance did was spend money before the
// user had seen a single number.
//
// The templates are the questions a buyer actually types. Each one is a
// different shape of intent, because a set of twelve rewordings of "best X"
// measures one thing twelve times.
package promptpack

import (
	"strings"
)

// Input is what the wizard collected.
type Input struct {
	// Brand is the property name, for the head-to-head and branded prompts.
	Brand string
	// Category is the phrase a buyer would use, for example "AI visibility
	// tracking" or "CRM for startups". It is used verbatim.
	Category string
	// Competitors are display names, used for alternatives and head-to-head.
	Competitors []string
}

// Prompt is one generated prompt.
type Prompt struct {
	Text     string
	Category string
	// Branded marks a prompt that names the property. It is kept, because
	// what an engine says about you when asked directly is worth reading,
	// but it is excluded from the headline visibility number.
	Branded bool
}

// Categories used for the generated prompts, so the grid can be grouped
// before anyone has written a tag.
const (
	CategoryDiscovery  = "discovery"
	CategoryComparison = "comparison"
	CategoryUseCase    = "use case"
	CategoryBrand      = "brand"
)

// Build returns the starter prompts. The result is deterministic for the
// same input, so a user who reruns the wizard sees the same list rather than
// a reshuffled one.
func Build(in Input) []Prompt {
	category := strings.TrimSpace(in.Category)
	brand := strings.TrimSpace(in.Brand)
	if category == "" {
		return nil
	}

	var out []Prompt
	add := func(text, cat string, branded bool) {
		text = strings.Join(strings.Fields(text), " ")
		if text != "" {
			out = append(out, Prompt{Text: text, Category: cat, Branded: branded})
		}
	}

	// Discovery: the question with no brand in it at all. This is where
	// visibility is won or lost, and it is the only shape that measures
	// whether an engine reaches for you unprompted.
	add("What are the best "+category+" tools?", CategoryDiscovery, false)
	add("Which "+category+" tool should I use?", CategoryDiscovery, false)
	add("What is the best "+category+" tool for a small team?", CategoryDiscovery, false)
	add("Which "+category+" tools are worth paying for?", CategoryDiscovery, false)
	add("What are the top "+category+" tools right now?", CategoryDiscovery, false)

	// Use case: how a buyer actually phrases the problem, rather than the
	// category label a vendor uses.
	article := indefiniteArticle(category)
	add("How do I choose "+article+" "+category+" tool?", CategoryUseCase, false)
	add("What should I look for in "+article+" "+category+" tool?", CategoryUseCase, false)
	add("Is there a free or open source "+category+" tool?", CategoryUseCase, false)

	// Comparison: one per competitor, capped so the pack stays a starter set
	// rather than a bill.
	competitors := trimAll(in.Competitors)
	for i, c := range competitors {
		if i >= 3 {
			break
		}
		add(c+" alternatives", CategoryComparison, false)
	}
	if brand != "" && len(competitors) > 0 {
		add(brand+" vs "+competitors[0], CategoryComparison, true)
	}

	// Brand: what an engine says when asked directly. Branded, so it is
	// visible in the grid and absent from the headline.
	if brand != "" {
		add("What is "+brand+"?", CategoryBrand, true)
		add("Is "+brand+" any good?", CategoryBrand, true)
	}

	return out
}

// IsBranded reports whether text names the property or one of its aliases.
// The wizard uses it on prompts a user typed by hand, so a hand-written
// branded prompt is tagged the same way a generated one is.
func IsBranded(text string, names []string) bool {
	lower := strings.ToLower(text)
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}

// indefiniteArticle picks "a" or "an" for a category phrase. A generated
// prompt that reads "a AI visibility tool" tells a user the list was written
// by a template, which is exactly the impression the first screen should not
// leave.
//
// The rule is the written-vowel one, with the two exceptions that break it in
// this vocabulary: "a UX tool" and "a European..." are both correct because
// they start with a consonant SOUND.
func indefiniteArticle(phrase string) string {
	phrase = strings.TrimSpace(phrase)
	if phrase == "" {
		return "a"
	}
	lower := strings.ToLower(phrase)
	if strings.HasPrefix(lower, "u") || strings.HasPrefix(lower, "eu") {
		return "a"
	}
	switch lower[0] {
	case 'a', 'e', 'i', 'o':
		return "an"
	}
	return "a"
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
