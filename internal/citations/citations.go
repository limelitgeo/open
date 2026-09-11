// Package citations turns the sources an engine attributed into classified
// rows: which host, which site, and whose it is.
//
// Classification is the difference between "47 citations" and "6 of them
// yours, 19 a competitor's, 22 places you could be". The first number is
// trivia; the second is a worklist.
package citations

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Source types, matching the citation.source_type constraint.
const (
	TypeOwn           = "own"
	TypeCompetitor    = "competitor"
	TypeSocial        = "social"
	TypeInformational = "informational"
	TypeOther         = "other"
)

// Citation is one classified source.
type Citation struct {
	// URL is the link as the engine gave it, kept so a user can open exactly
	// what the model saw.
	URL string
	// Host is the normalised hostname, with any leading www dropped.
	Host string
	// Site is the registrable site, so pages on help.acme.com and
	// www.acme.com group under one company.
	Site  string
	Title string
	// Position is 1-based, in the order the engine listed them.
	Position   int
	SourceType string
}

// Classifier assigns a source type using the brands this instance tracks.
type Classifier struct {
	ownSites        map[string]bool
	competitorSites map[string]bool
}

// NewClassifier builds a classifier from the property's domains and the
// tracked competitors' domains.
func NewClassifier(ownDomains, competitorDomains []string) *Classifier {
	c := &Classifier{
		ownSites:        map[string]bool{},
		competitorSites: map[string]bool{},
	}
	for _, d := range ownDomains {
		if site := Site(Host(d)); site != "" {
			c.ownSites[site] = true
		}
	}
	for _, d := range competitorDomains {
		site := Site(Host(d))
		// The property's own domain wins if a competitor is misconfigured
		// with it, because counting your own site as a rival's would be the
		// most misleading row on the page.
		if site != "" && !c.ownSites[site] {
			c.competitorSites[site] = true
		}
	}
	return c
}

// Classify turns raw citations into classified rows, dropping anything that
// is not a usable URL and renumbering positions over what survives.
func (c *Classifier) Classify(raw []Raw) []Citation {
	var out []Citation
	for _, r := range raw {
		host := Host(r.URL)
		if host == "" {
			continue
		}
		site := Site(host)
		out = append(out, Citation{
			URL:        r.URL,
			Host:       host,
			Site:       site,
			Title:      r.Title,
			Position:   len(out) + 1,
			SourceType: c.typeFor(site, host),
		})
	}
	return out
}

// Raw is a citation as a provider reported it.
type Raw struct {
	URL      string
	Title    string
	Position int
}

func (c *Classifier) typeFor(site, host string) string {
	switch {
	case c.ownSites[site]:
		return TypeOwn
	case c.competitorSites[site]:
		return TypeCompetitor
	case socialSites[site]:
		return TypeSocial
	case isInformational(site, host):
		return TypeInformational
	default:
		return TypeOther
	}
}

// socialSites is short on purpose. It exists to separate "a person said this
// on a forum" from "a publication wrote this", not to be a directory of every
// social network. Anything missing lands in other, which is honest.
var socialSites = map[string]bool{
	"reddit.com":           true,
	"x.com":                true,
	"twitter.com":          true,
	"linkedin.com":         true,
	"youtube.com":          true,
	"facebook.com":         true,
	"instagram.com":        true,
	"tiktok.com":           true,
	"quora.com":            true,
	"medium.com":           true,
	"substack.com":         true,
	"github.com":           true,
	"news.ycombinator.com": true,
	"stackoverflow.com":    true,
	"producthunt.com":      true,
	"g2.com":               true,
	"capterra.com":         true,
	"trustpilot.com":       true,
}

// informationalSites are reference sources: the ones an engine reaches for to
// define a thing rather than to recommend one.
var informationalSites = map[string]bool{
	"wikipedia.org":  true,
	"wikidata.org":   true,
	"britannica.com": true,
	"arxiv.org":      true,
	"nih.gov":        true,
	"who.int":        true,
}

// informationalSuffixes are the domain endings that are institutional by
// definition, so they need no list.
var informationalSuffixes = []string{".gov", ".edu", ".ac.uk", ".edu.au", ".gov.uk", ".int"}

func isInformational(site, host string) bool {
	if informationalSites[site] {
		return true
	}
	for _, suffix := range informationalSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// Host normalises a URL or a bare domain to a hostname: lowercased, with the
// scheme, any credentials, the port, a leading www and any path removed.
//
// It takes bare domains as well as URLs because the same function normalises
// what a user typed into the competitors form and what an engine returned as
// a link, and those two have to agree or a competitor's own citations would
// not be recognised as theirs.
func Host(raw string) string {
	s := strings.TrimSpace(strings.ToLower(raw))
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimSuffix(host, ".")
	if host == "" || !strings.Contains(host, ".") {
		return ""
	}
	if invalidHost.MatchString(host) {
		return ""
	}
	return host
}

var invalidHost = regexp.MustCompile(`[^a-z0-9.\-]`)

// multiLevelTLDs are the public suffixes with two labels that appear often
// enough here to matter. A full public-suffix list would be a dependency and
// a data file to keep current; this covers the cases that show up in AI
// citations, and a miss only makes the site one label too specific, which
// groups slightly more narrowly rather than wrongly.
var multiLevelTLDs = map[string]bool{
	"co.uk": true, "org.uk": true, "ac.uk": true, "gov.uk": true,
	"com.au": true, "net.au": true, "org.au": true, "edu.au": true,
	"co.nz": true, "co.jp": true, "co.in": true, "co.za": true,
	"com.br": true, "com.mx": true, "com.sg": true, "com.hk": true,
	"co.kr": true, "com.tr": true, "com.cn": true,
}

// Site reduces a hostname to the registrable site, so help.acme.com,
// www.acme.com and acme.com all group under acme.com.
//
// Grouping by site rather than host is what makes a top-sources table read
// like a list of companies instead of a list of subdomains.
func Site(host string) string {
	if host == "" {
		return ""
	}
	parts := strings.Split(host, ".")
	if len(parts) <= 2 {
		return host
	}
	return registrable(parts)
}

func registrable(parts []string) string {
	n := len(parts)
	if n >= 3 {
		candidate := parts[n-2] + "." + parts[n-1]
		if multiLevelTLDs[candidate] {
			return strings.Join(parts[n-3:], ".")
		}
	}
	return strings.Join(parts[n-2:], ".")
}

// Hosts returns the distinct sites in a set of citations, sorted, which is
// what the dashboard groups the sources table by.
func Hosts(in []Citation) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range in {
		if c.Site != "" && !seen[c.Site] {
			seen[c.Site] = true
			out = append(out, c.Site)
		}
	}
	sort.Strings(out)
	return out
}
