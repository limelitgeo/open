// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package ui

// Charts, drawn as SVG on the server.
//
// No charting library and no JavaScript, because this project ships as one
// binary with no build step and adding a bundler to draw four shapes would
// cost more than it is worth. Everything here emits an SVG string that the
// templates drop inline, which also means the charts inherit the page's CSS
// custom properties and follow light and dark without a second palette.
//
// The degenerate cases are the point of this file. A brand new install has
// one day of data, or none. A series with one point cannot have a line
// between points, and the usual way that fails is a divide by len(points)-1.
// Every function here is written for the empty and single-point cases first.

import (
	"fmt"
	"html/template"
	"math"
	"strings"
)

// Chart geometry. The viewBox is fixed and the SVG scales to its container,
// so one set of numbers works at every width.
const (
	trendW, trendH = 720.0, 180.0
	trendPadX      = 8.0
	trendPadTop    = 14.0
	trendPadBottom = 26.0
)

// TrendPoint is one day on the visibility trend.
type TrendPoint struct {
	Day   string
	Value float64
	N     int
}

// TrendChart draws visibility over time.
//
// The y axis is pinned to 0-100 rather than fitted to the data. A fitted axis
// makes a move from 2% to 3% look like a doubling, which for a visibility
// metric is the most misleading thing a chart can do.
func TrendChart(points []TrendPoint) template.HTML {
	if len(points) == 0 {
		return ""
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="chart chart-trend" viewBox="0 0 %.0f %.0f" preserveAspectRatio="none" role="img" aria-label="Visibility over time">`, trendW, trendH)

	// Gridlines at 0, 25, 50, 75, 100 give the eye a scale without a legend.
	for _, pct := range []float64{0, 25, 50, 75, 100} {
		y := trendY(pct)
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, trendPadX, y, trendW-trendPadX, y)
	}

	x := func(i int) float64 {
		if len(points) == 1 {
			// One point sits in the middle rather than at the left edge,
			// where it would read as the start of a line that is missing.
			return trendW / 2
		}
		span := trendW - 2*trendPadX
		return trendPadX + span*float64(i)/float64(len(points)-1)
	}

	if len(points) > 1 {
		var line, area strings.Builder
		for i, p := range points {
			cmd := "L"
			if i == 0 {
				cmd = "M"
			}
			fmt.Fprintf(&line, "%s%.1f %.1f ", cmd, x(i), trendY(p.Value))
		}
		fmt.Fprintf(&area, "M%.1f %.1f ", x(0), trendY(points[0].Value))
		for i, p := range points[1:] {
			fmt.Fprintf(&area, "L%.1f %.1f ", x(i+1), trendY(p.Value))
		}
		fmt.Fprintf(&area, "L%.1f %.1f L%.1f %.1f Z", x(len(points)-1), trendY(0), x(0), trendY(0))

		fmt.Fprintf(&b, `<path class="area" d="%s"/>`, strings.TrimSpace(area.String()))
		fmt.Fprintf(&b, `<path class="line" d="%s"/>`, strings.TrimSpace(line.String()))
	}

	// Dots last so they sit above the line. With one point this is the whole
	// chart, and it has to look deliberate rather than broken.
	for i, p := range points {
		r := 3.0
		if len(points) == 1 {
			r = 5.0
		}
		fmt.Fprintf(&b, `<circle class="dot" cx="%.1f" cy="%.1f" r="%.1f"><title>%s: %.0f%% of %d answers</title></circle>`,
			x(i), trendY(p.Value), r, template.HTMLEscapeString(p.Day), p.Value, p.N)
	}

	if len(points) == 1 {
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`,
			x(0), trendH-8, template.HTMLEscapeString(points[0].Day))
	} else {
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f">%s</text>`, trendPadX, trendH-8, template.HTMLEscapeString(points[0].Day))
		fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, trendW-trendPadX, trendH-8, template.HTMLEscapeString(points[len(points)-1].Day))
	}

	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func trendY(pct float64) float64 {
	usable := trendH - trendPadTop - trendPadBottom
	return trendPadTop + usable*(1-pct/100)
}

// Sparkline is a bare line for a table cell: no axes, no labels, no scale.
// It answers "which way is this going" and nothing more.
func Sparkline(values []float64) template.HTML {
	if len(values) < 2 {
		return ""
	}
	const w, h, pad = 68.0, 20.0, 2.0
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="spark" viewBox="0 0 %.0f %.0f" preserveAspectRatio="none" aria-hidden="true">`, w, h)
	for i, v := range values {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		x := pad + (w-2*pad)*float64(i)/float64(len(values)-1)
		y := pad + (h-2*pad)*(1-clampPct(v)/100)
		fmt.Fprintf(&b, "%s%.1f %.1f ", cmd, x, y)
	}
	// Wrapped so the path data is one attribute rather than loose text.
	out := strings.TrimSpace(b.String())
	return template.HTML(out + `" fill="none"/></svg>`)
}

// BarRow is one brand in the share-of-voice ranking.
type BarRow struct {
	Label string
	// Value is the percentage the bar fills to.
	Value float64
	// Detail is the small print under the label.
	Detail string
	// IsOwn draws the row in the brand colour and keeps it visible at zero.
	IsOwn bool
	// Rank is 1-based, or 0 when the brand does not appear at all.
	Rank int
}

// heatColor maps a percentage onto the grid's colour ramp.
//
// A sequential single-hue ramp, not a red-to-green one. Red and green carry a
// judgement ("bad", "good") that this number does not support: 20% visibility
// is excellent in some categories and terrible in others. It is also the
// ramp that fails hardest for the ~8% of men with red-green colour blindness.
// Intensity alone says "more" without saying "good".
func heatColor(pct float64, n int) string {
	if n == 0 {
		return "var(--cell-empty)"
	}
	switch {
	case pct <= 0:
		return "var(--cell-0)"
	case pct < 20:
		return "var(--cell-1)"
	case pct < 40:
		return "var(--cell-2)"
	case pct < 60:
		return "var(--cell-3)"
	case pct < 80:
		return "var(--cell-4)"
	default:
		return "var(--cell-5)"
	}
}

// heatInk picks readable text for a heat cell. The ramp darkens as it goes,
// so the top two steps need light text and the rest need dark.
func heatInk(pct float64, n int) string {
	if n == 0 || pct < 60 {
		return "var(--ink)"
	}
	return "#fff"
}

func clampPct(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
