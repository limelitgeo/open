// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package ui

// Charts, drawn as SVG on the server.
//
// No charting library and no JavaScript, because this project ships as one
// binary with no build step and adding a bundler to draw three shapes would
// cost more than it is worth. Everything here emits an SVG string the
// templates drop inline, so the charts inherit the page's CSS custom
// properties and follow light and dark without a second palette.
//
// Two rules this file follows.
//
// No colour is emitted from Go. A hex string baked into markup cannot be
// theme-aware, and for the grid the readable ink flips at a different step in
// each theme. Go decides which bin a value lands in; CSS decides what a bin
// looks like.
//
// The degenerate cases come first. A new install has one day of data, or
// none. A series with one point has no line to draw, and the usual way that
// fails is a divide by len(points)-1.

import (
	"fmt"
	"html/template"
	"math"
	"strings"
)

// Trend geometry. The viewBox is fixed and the SVG scales inside its
// container.
const (
	trendW, trendH = 720.0, 180.0
	trendPadX      = 10.0
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
// turns a move from 2% to 3% into a visual doubling, which for a visibility
// metric is the most misleading thing a chart can do.
//
// Gaps are never filled. metrics.Series returns only the days that actually
// ran, and drawing a line across a day nobody measured would show a period
// that was never observed.
func TrendChart(points []TrendPoint) template.HTML {
	if len(points) == 0 {
		return ""
	}

	var b strings.Builder
	// xMidYMid meet, not none. Stretching the viewBox shears the axis glyphs
	// and scales the stroke anisotropically, so a wide container gets fat
	// horizontal strokes and squashed text.
	fmt.Fprintf(&b, `<svg class="chart chart-trend" viewBox="0 0 %.0f %.0f" preserveAspectRatio="xMidYMid meet" role="img" aria-label="%s">`,
		trendW, trendH, template.HTMLEscapeString(trendLabel(points)))

	// Gridlines carry no information a screen reader needs; the label above
	// already states the shape.
	for _, pct := range []float64{0, 25, 50, 75, 100} {
		y := trendY(pct)
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" aria-hidden="true"/>`,
			trendPadX, y, trendW-trendPadX, y)
	}

	x := func(i int) float64 {
		if len(points) == 1 {
			// A single point sits in the middle. At the left edge it reads as
			// the start of a line that failed to draw.
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
		for i := 1; i < len(points); i++ {
			fmt.Fprintf(&area, "L%.1f %.1f ", x(i), trendY(points[i].Value))
		}
		fmt.Fprintf(&area, "L%.1f %.1f L%.1f %.1f Z", x(len(points)-1), trendY(0), x(0), trendY(0))

		fmt.Fprintf(&b, `<path class="area" d="%s"/>`, strings.TrimSpace(area.String()))
		fmt.Fprintf(&b, `<path class="line" d="%s"/>`, strings.TrimSpace(line.String()))
	}

	// Dots last, so they sit above the line. With one point this is the whole
	// chart and it has to look deliberate.
	dotEvery := 1
	if len(points) > 45 {
		dotEvery = len(points) / 45
	}
	for i, p := range points {
		if len(points) > 1 && i%dotEvery != 0 && i != len(points)-1 {
			continue
		}
		r := 3.0
		if len(points) == 1 {
			r = 5.0
		}
		fmt.Fprintf(&b, `<circle class="dot" cx="%.1f" cy="%.1f" r="%.1f"><title>%s: %s of %s</title></circle>`,
			x(i), trendY(p.Value), r, template.HTMLEscapeString(p.Day), pct(p.Value)+"%", answersWord(p.N))
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

// trendLabel describes the series for a screen reader, which cannot see it.
func trendLabel(points []TrendPoint) string {
	if len(points) == 1 {
		return fmt.Sprintf("Visibility on %s: %s%% of %s. One day of history.",
			points[0].Day, pct(points[0].Value), answersWord(points[0].N))
	}
	first, last := points[0], points[len(points)-1]
	return fmt.Sprintf("Visibility across %d days, %s to %s. From %s%% to %s%%.",
		len(points), first.Day, last.Day, pct(first.Value), pct(last.Value))
}

func trendY(v float64) float64 {
	usable := trendH - trendPadTop - trendPadBottom
	return trendPadTop + usable*(1-clampPct(v)/100)
}

// Sparkline is a bare line for a table cell: no axes, no labels, no scale.
// It answers which way this is going and nothing more.
func Sparkline(values []float64) template.HTML {
	if len(values) < 3 {
		// Two points is a segment, not a trend, and it implies a shape the
		// data does not support.
		return ""
	}
	const w, h, pad = 68.0, 20.0, 3.0

	var d strings.Builder
	for i, v := range values {
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		x := pad + (w-2*pad)*float64(i)/float64(len(values)-1)
		y := pad + (h-2*pad)*(1-clampPct(v)/100)
		fmt.Fprintf(&d, "%s%.1f %.1f ", cmd, x, y)
	}

	last := values[len(values)-1]
	lastX := w - pad
	lastY := pad + (h-2*pad)*(1-clampPct(last)/100)

	return template.HTML(fmt.Sprintf(
		`<svg class="spark" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" preserveAspectRatio="xMidYMid meet" aria-hidden="true">`+
			`<path class="spark-line" d="%s" fill="none"/>`+
			`<circle class="spark-dot" cx="%.1f" cy="%.1f" r="2"/>`+
			`</svg>`,
		w, h, w, h, strings.TrimSpace(d.String()), lastX, lastY))
}

// CellMinN is the sample below which a grid cell cannot reach the top of the
// heat ramp.
//
// One answer that named you is 100%, and painting it the same as five of five
// would let a single lucky answer look like dominance. Below this the fill is
// capped one step short, and the cell still prints its own fraction so the
// reader can see exactly what it rests on.
const CellMinN = 5

// cellBin picks the heat class for one grid cell.
//
// It returns a class, never a colour. The readable ink over each step differs
// by theme and flips at a different step in each, which CSS can express and a
// Go string cannot.
func cellBin(answers int, percent float64) string {
	if answers == 0 {
		return "hm-none"
	}
	// A measured zero is its own bin, drawn at full strength. Greying it is
	// the most common way a real finding reads as a broken widget.
	if percent <= 0 {
		return "hm-0"
	}
	bin := 1
	switch {
	case percent >= 80:
		bin = 5
	case percent >= 60:
		bin = 4
	case percent >= 40:
		bin = 3
	case percent >= 20:
		bin = 2
	}
	if answers < CellMinN && bin > 4 {
		bin = 4
	}
	return fmt.Sprintf("hm-%d", bin)
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
