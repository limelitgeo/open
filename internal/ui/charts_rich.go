// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package ui

// The richer charts: the competitor race, the share-of-voice donut, the
// visibility-by-engine bars, the citation mix, and the fan-out word cloud.
//
// All server-rendered SVG, all reading CSS custom properties, so they follow
// the theme and never carry a colour of their own. Every one of them is drawn
// from real rows: nothing here fills a gap, extends a line, or invents a
// series to make a chart look fuller than the data is.

import (
	"fmt"
	"html/template"
	"math"
	"sort"
	"strings"
)

// seriesClass assigns a stable colour class to a brand.
//
// Alphabetical over the full set of names, never by rank or by the filtered
// subset, so a brand keeps its colour across every screen and every week. The
// property is always the brand colour and never takes a series slot.
func seriesClass(name string, all []string, isOwn bool) string {
	if isOwn {
		return "s-own"
	}
	sorted := make([]string, 0, len(all))
	for _, n := range all {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	idx := 0
	for i, n := range sorted {
		if n == name {
			idx = i
			break
		}
	}
	return fmt.Sprintf("s-%d", idx%8+1)
}

// RaceSeries is one brand's line on the competitor race.
type RaceSeries struct {
	Name   string
	IsOwn  bool
	Class  string
	Points []TrendPoint
	// Last is the most recent value, for the legend.
	Last float64
}

// RaceChart draws every tracked brand's visibility over time on one chart.
//
// This is the chart that was missing. One line at zero says nothing; the same
// zero drawn under three competitors at 82, 71 and 57 says exactly where you
// stand. The property's line is drawn last and heavier so it stays on top and
// reads without the legend, which is the redundant encoding a colourblind
// reader needs.
func RaceChart(series []RaceSeries) template.HTML {
	if len(series) == 0 {
		return ""
	}
	n := 0
	for _, s := range series {
		if len(s.Points) > n {
			n = len(s.Points)
		}
	}
	if n == 0 {
		return ""
	}

	// Own last, so it draws on top.
	ordered := make([]RaceSeries, 0, len(series))
	var own *RaceSeries
	for i := range series {
		if series[i].IsOwn {
			own = &series[i]
			continue
		}
		ordered = append(ordered, series[i])
	}
	if own != nil {
		ordered = append(ordered, *own)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart chart-race" viewBox="0 0 %.0f %.0f" preserveAspectRatio="xMidYMid meet" role="img" aria-label="%s">`,
		trendW, trendH, template.HTMLEscapeString(raceLabel(series)))

	for _, pct := range []float64{0, 25, 50, 75, 100} {
		y := trendY(pct)
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" aria-hidden="true"/>`, trendPadX, y, trendW-trendPadX, y)
		fmt.Fprintf(&b, `<text class="axis axis-y" x="%.1f" y="%.1f" text-anchor="end">%.0f</text>`, trendPadX-3, y+3, pct)
	}

	x := func(i, total int) float64 {
		if total == 1 {
			return trendW / 2
		}
		return trendPadX + (trendW-2*trendPadX)*float64(i)/float64(total-1)
	}

	for _, s := range ordered {
		if len(s.Points) == 0 {
			continue
		}
		cls := "series " + s.Class
		if s.IsOwn {
			cls += " series-own"
		}
		if len(s.Points) > 1 {
			var d strings.Builder
			for i, p := range s.Points {
				cmd := "L"
				if i == 0 {
					cmd = "M"
				}
				fmt.Fprintf(&d, "%s%.1f %.1f ", cmd, x(i, len(s.Points)), trendY(p.Value))
			}
			fmt.Fprintf(&b, `<path class="%s line" d="%s"/>`, cls, strings.TrimSpace(d.String()))
		}
		for i, p := range s.Points {
			r := 3.0
			if s.IsOwn {
				r = 4.5
			}
			if len(s.Points) == 1 {
				r += 1.5
			}
			fmt.Fprintf(&b, `<circle class="%s dot" cx="%.1f" cy="%.1f" r="%.1f"><title>%s, %s: %s%% of %s</title></circle>`,
				cls, x(i, len(s.Points)), trendY(p.Value), r,
				template.HTMLEscapeString(s.Name), template.HTMLEscapeString(p.Day), pct(p.Value), answersWord(p.N))
		}
	}

	first, last := series[0].Points[0].Day, series[0].Points[len(series[0].Points)-1].Day
	if n == 1 {
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, trendW/2, trendH-8, template.HTMLEscapeString(first))
	} else {
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f">%s</text>`, trendPadX, trendH-8, template.HTMLEscapeString(first))
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, trendW-trendPadX, trendH-8, template.HTMLEscapeString(last))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func raceLabel(series []RaceSeries) string {
	parts := make([]string, 0, len(series))
	for _, s := range series {
		parts = append(parts, fmt.Sprintf("%s %s%%", s.Name, pct(s.Last)))
	}
	return "Visibility over time by brand. Latest: " + strings.Join(parts, ", ") + "."
}

// DonutSlice is one brand's share.
type DonutSlice struct {
	Name  string
	IsOwn bool
	Class string
	Share float64
}

// Donut draws share of voice as parts of a whole.
//
// A donut is right here and wrong for visibility. Share of voice is
// genuinely a partition: every tracked brand's mentions add to 100%, so the
// arcs mean something. Visibility is not a partition (every brand can be at
// 80% at once) and a pie of it would lie. The centre carries the property's
// own share so the one number the reader came for is not hidden in a slice.
func Donut(slices []DonutSlice, ownShare float64, hasOwnData bool) template.HTML {
	const size, r, stroke = 160.0, 62.0, 22.0
	cx, cy := size/2, size/2
	circ := 2 * math.Pi * r

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart chart-donut" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`,
		size, size, template.HTMLEscapeString(donutLabel(slices)))
	fmt.Fprintf(&b, `<circle class="donut-track" cx="%.1f" cy="%.1f" r="%.1f" fill="none" stroke-width="%.1f"/>`, cx, cy, r, stroke)

	offset := 0.0
	total := 0.0
	for _, s := range slices {
		total += s.Share
	}
	for _, s := range slices {
		if s.Share <= 0 || total <= 0 {
			continue
		}
		frac := s.Share / total
		length := frac * circ
		// A hairline gap between arcs, so adjacent slices of similar colour
		// stay separable. Skipped when the slice is the whole ring.
		gap := 2.0
		if frac >= 0.999 {
			gap = 0
		}
		cls := "arc " + s.Class
		if s.IsOwn {
			cls += " series-own"
		}
		fmt.Fprintf(&b,
			`<circle class="%s" cx="%.1f" cy="%.1f" r="%.1f" fill="none" stroke-width="%.1f" stroke-dasharray="%.2f %.2f" stroke-dashoffset="%.2f" transform="rotate(-90 %.1f %.1f)"><title>%s: %s%% of all mentions</title></circle>`,
			cls, cx, cy, r, stroke, math.Max(length-gap, 0), circ-math.Max(length-gap, 0), -offset, cx, cy,
			template.HTMLEscapeString(s.Name), pct(s.Share))
		offset += length
	}

	if hasOwnData {
		fmt.Fprintf(&b, `<text class="donut-value" x="%.1f" y="%.1f" text-anchor="middle">%s%%</text>`, cx, cy-2, pct(ownShare))
		fmt.Fprintf(&b, `<text class="donut-label" x="%.1f" y="%.1f" text-anchor="middle">your share</text>`, cx, cy+15)
	} else {
		fmt.Fprintf(&b, `<text class="donut-value" x="%.1f" y="%.1f" text-anchor="middle">0%%</text>`, cx, cy-2)
		fmt.Fprintf(&b, `<text class="donut-label" x="%.1f" y="%.1f" text-anchor="middle">never named</text>`, cx, cy+15)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func donutLabel(slices []DonutSlice) string {
	parts := make([]string, 0, len(slices))
	for _, s := range slices {
		parts = append(parts, fmt.Sprintf("%s %s%%", s.Name, pct(s.Share)))
	}
	return "Share of voice: " + strings.Join(parts, ", ") + "."
}

// EngineGroup is one engine's column of bars.
type EngineGroup struct {
	Engine  string
	Access  string
	Answers int
	Bars    []EngineBar
}

// EngineBar is one brand on one engine.
type EngineBar struct {
	Name       string
	IsOwn      bool
	Class      string
	Visibility float64
	Mentions   int
}

// EngineBars draws visibility by engine, one group per engine and one bar per
// brand: where each of you wins. It needs no time depth, so it is the richest
// chart on a first-day install.
func EngineBars(groups []EngineGroup) template.HTML {
	if len(groups) == 0 {
		return ""
	}
	bars := 0
	for _, g := range groups {
		if len(g.Bars) > bars {
			bars = len(g.Bars)
		}
	}
	if bars == 0 {
		return ""
	}

	// padBottom leaves room for two label lines under the baseline, plus a
	// gap so the 2px stub a measured zero draws does not touch the text.
	const h, padTop, padBottom, padX = 212.0, 12.0, 46.0, 10.0
	groupW := 92.0
	w := padX*2 + groupW*float64(len(groups))
	plotH := h - padTop - padBottom
	barW := math.Min(14, (groupW-16)/float64(bars))
	y := func(v float64) float64 { return padTop + plotH*(1-clampPct(v)/100) }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart chart-engines" viewBox="0 0 %.0f %.0f" preserveAspectRatio="xMidYMid meet" role="img" aria-label="Visibility by engine, one bar per brand">`, w, h)
	for _, pct := range []float64{0, 50, 100} {
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" aria-hidden="true"/>`, padX, y(pct), w-padX, y(pct))
	}

	for gi, g := range groups {
		gx := padX + groupW*float64(gi)
		total := barW * float64(len(g.Bars))
		start := gx + (groupW-total)/2
		for bi, bar := range g.Bars {
			bx := start + barW*float64(bi)
			top := y(bar.Visibility)
			height := y(0) - top
			cls := "bar " + bar.Class
			if bar.IsOwn {
				cls += " series-own"
			}
			// A measured zero is drawn as a 2px stub rather than nothing, so
			// "you were not named here" is visible rather than blank.
			if height < 2 {
				top, height = y(0)-2, 2
				cls += " bar-zero"
			}
			fmt.Fprintf(&b, `<rect class="%s" x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2"><title>%s on %s: %s%% of %s</title></rect>`,
				cls, bx+1, top, barW-2, height,
				template.HTMLEscapeString(bar.Name), template.HTMLEscapeString(engineLabel(g.Engine)), pct(bar.Visibility), answersWord(g.Answers))
		}
		label := engineLabel(g.Engine)
		if len(label) > 13 {
			label = label[:12] + "."
		}
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, gx+groupW/2, h-24, template.HTMLEscapeString(label))
		fmt.Fprintf(&b, `<text class="axis axis-sub axis-%s" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, g.Access, gx+groupW/2, h-9, g.Access)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// MixDay is one day's citation mix.
type MixDay struct {
	Day    string
	Counts map[string]int
	Total  int
}

// mixOrder is the stacking order, bottom to top. Own first so your share sits
// on the baseline where it can be read without a legend.
var mixOrder = []string{"own", "competitor", "informational", "social", "other"}

// CitationMixChart draws citations by source type per day as 100% stacked
// bars: who the engines trust, and whether that is changing.
//
// Stacked bars rather than a stacked area, because the area needs enough days
// to read as a shape and lies about the days between measurements. A bar per
// measured day says exactly what was measured and nothing about what was not.
func CitationMixChart(days []MixDay) template.HTML {
	if len(days) == 0 {
		return ""
	}
	const h, padTop, padBottom, padX = 170.0, 10.0, 26.0, 10.0
	slot := math.Max(36, math.Min(90, 680/float64(len(days))))
	w := padX*2 + slot*float64(len(days))
	plotH := h - padTop - padBottom
	barW := math.Min(48, slot-10)

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart chart-mix" viewBox="0 0 %.0f %.0f" preserveAspectRatio="xMidYMid meet" role="img" aria-label="Cited sources by type per day, as a share of each day's citations">`, w, h)

	for di, d := range days {
		if d.Total == 0 {
			continue
		}
		x := padX + slot*float64(di) + (slot-barW)/2
		yCursor := padTop + plotH
		for _, kind := range mixOrder {
			n := d.Counts[kind]
			if n == 0 {
				continue
			}
			height := plotH * float64(n) / float64(d.Total)
			yCursor -= height
			fmt.Fprintf(&b, `<rect class="mix mix-%s" x="%.1f" y="%.1f" width="%.1f" height="%.1f"><title>%s, %s: %d of %d citations</title></rect>`,
				kind, x, yCursor, barW, math.Max(height-1, 0.5),
				template.HTMLEscapeString(d.Day), kind, n, d.Total)
		}
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, x+barW/2, h-8, template.HTMLEscapeString(shortDay(d.Day)))
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func shortDay(day string) string {
	if len(day) == 10 {
		return day[5:]
	}
	return day
}

// CloudWord is one term in the fan-out cloud.
type CloudWord struct {
	Term  string
	Count int
	// Size is the font size in px, from the count.
	Size int
	// Weight thins the least frequent words so the frequent ones read first.
	Weight int
}

// WordCloud sizes the terms the engines actually searched for.
//
// Square-root scaling, because a linear scale makes the top word a poster and
// the rest illegible, and a log scale flattens the difference the reader came
// to see. Frequency is encoded once, as size. Encoding it a second time as
// colour or opacity would say the same thing twice while looking like it said
// two things.
func WordCloud(words []CloudWord) []CloudWord {
	if len(words) == 0 {
		return nil
	}
	maxN, minN := words[0].Count, words[0].Count
	for _, w := range words {
		if w.Count > maxN {
			maxN = w.Count
		}
		if w.Count < minN {
			minN = w.Count
		}
	}
	const minPx, maxPx = 12.0, 30.0
	out := make([]CloudWord, len(words))
	for i, w := range words {
		t := 1.0
		if maxN > minN {
			t = (math.Sqrt(float64(w.Count)) - math.Sqrt(float64(minN))) / (math.Sqrt(float64(maxN)) - math.Sqrt(float64(minN)))
		}
		out[i] = CloudWord{
			Term: w.Term, Count: w.Count,
			Size:   int(math.Round(minPx + t*(maxPx-minPx))),
			Weight: 500 + int(math.Round(t*2))*100,
		}
	}
	// Alphabetical, so the layout is stable between renders and a word does
	// not jump when its count changes by one.
	sort.Slice(out, func(i, j int) bool { return out[i].Term < out[j].Term })
	return out
}
