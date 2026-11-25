package report

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strings"

	"vigie/internal/bench"
)

// Palette (thème clair uniquement : le rapport est fait pour être imprimé).
var palette = []string{"#2563eb", "#d97706", "#059669", "#7c3aed", "#dc2626", "#0891b2", "#65a30d", "#db2777"}

func color(i int) string { return palette[i%len(palette)] }

func fp(v float64) *float64 { return &v }

// lineSeries est une série à la seconde (débit) à tracer avec uPlot.
type lineSeries struct {
	Label string
	Color string
	Dash  bool
	R     *bench.Result
}

// throughputChart : débit à la seconde (Mo/s), axe x en secondes de mesure.
func throughputChart(series []lineSeries) Chart {
	maxT := 0
	for _, s := range series {
		for _, sec := range s.R.Seconds {
			if sec.T > maxT {
				maxT = sec.T
			}
		}
	}
	c := Chart{Type: "line", XLabel: "seconde de mesure", YLabel: "Mo/s", Height: 230}
	for t := 1; t <= maxT; t++ {
		c.X = append(c.X, float64(t))
	}
	for _, s := range series {
		vals := make([]*float64, maxT)
		for _, sec := range s.R.Seconds {
			if sec.T > 0 && sec.T <= maxT {
				vals[sec.T-1] = fp(math.Round(float64(sec.Bytes)/1e4) / 100)
			}
		}
		c.Series = append(c.Series, ChartSeries{Label: s.Label, Color: s.Color, Dash: s.Dash, Values: vals})
	}
	return c
}

// perSecond : moyenne, minimum et maximum à la seconde (Mo/s) d'un palier.
func perSecond(r *bench.Result) (mean, min, max float64) {
	n := 0
	for _, s := range r.Seconds {
		if s.T <= 0 {
			continue
		}
		v := float64(s.Bytes) / 1e6
		if n == 0 || v < min {
			min = v
		}
		if v > max {
			max = v
		}
		n++
	}
	return r.MBps, min, max
}

// histSeries est une distribution de latences à tracer.
type histSeries struct {
	Label string
	Color string
	Dash  bool
	R     *bench.Result
}

// rebin regroupe l'histogramme HDR en 10 classes par décade (échelle log), en % des I/O.
func rebin(b [][2]uint64) map[int]float64 {
	out := map[int]float64{}
	var total float64
	for _, x := range b {
		total += float64(x[1])
	}
	if total == 0 {
		return out
	}
	for _, x := range b {
		us := float64(x[0]) / 1000
		if us < 1 {
			us = 1
		}
		i := int(math.Floor(10 * math.Log10(us)))
		out[i] += 100 * float64(x[1]) / total
	}
	return out
}

// histChart : distribution des latences, x en ms (échelle log), y en % des I/O par classe.
func histChart(series []histSeries) Chart {
	bins := make([]map[int]float64, len(series))
	lo, hi := math.MaxInt, math.MinInt
	for i, s := range series {
		bins[i] = rebin(s.R.Buckets)
		for k := range bins[i] {
			lo, hi = min(lo, k), max(hi, k)
		}
	}
	c := Chart{Type: "hist", XLabel: "latence (ms, échelle log)", YLabel: "% des I/O", LogX: true, Height: 230}
	if lo > hi {
		return c
	}
	lo, hi = lo-1, hi+1
	for k := lo; k <= hi; k++ {
		c.X = append(c.X, math.Pow(10, (float64(k)+0.5)/10)/1000)
	}
	for i, s := range series {
		vals := make([]*float64, 0, hi-lo+1)
		for k := lo; k <= hi; k++ {
			vals = append(vals, fp(math.Round(bins[i][k]*1000)/1000))
		}
		c.Series = append(c.Series, ChartSeries{Label: s.Label, Color: s.Color, Dash: s.Dash, Values: vals})
	}
	return c
}

// ---------- SVG (graphs dessinés côté serveur : courbe IOPS/latence et barres) ----------
// Pas d'attribut style (CSP) : couleurs en attributs de présentation, typographie par classes.

type xyPoint struct {
	X, Y float64
	Tip  string
}

type xySeries struct {
	Label  string
	Color  string
	Dash   bool
	Points []xyPoint
}

// niceMax arrondit vers le haut à 1, 2 ou 5 × 10^n et renvoie le pas de graduation.
func niceMax(v float64) (float64, float64) {
	if v <= 0 {
		return 1, 0.2
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if v <= m*p {
			top := m * p
			step := top / 5
			if m == 2.5 {
				step = top / 5
			}
			return top, step
		}
	}
	return 10 * p, 2 * p
}

func axisNum(v float64) string {
	switch {
	case v == 0:
		return "0"
	case math.Abs(v) >= 1000:
		return group(int64(math.Round(v)))
	case math.Abs(v) >= 10:
		return dec(v, 0)
	case math.Abs(v) >= 1:
		s := dec(v, 1)
		return strings.TrimSuffix(s, ",0")
	}
	return strings.TrimRight(strings.TrimRight(dec(v, 2), "0"), ",")
}

const svgW, svgPadL, svgPadR, svgPadT, svgPadB = 680.0, 62.0, 34.0, 30.0, 42.0

// svgXY trace des courbes y(x) reliant les points dans l'ordre donné, avec une ligne horizontale
// optionnelle (plafond). Chaque point porte une info-bulle (survol).
func svgXY(series []xySeries, xLabel, yLabel string, hline float64, hlineLabel string, h float64) HTML {
	var mx, my float64
	for _, s := range series {
		for _, p := range s.Points {
			mx, my = math.Max(mx, p.X), math.Max(my, p.Y)
		}
	}
	if hline > 0 {
		my = math.Max(my, hline*1.15)
	}
	xTop, xStep := niceMax(mx * 1.04)
	yTop, yStep := niceMax(my * 1.04)
	pw, ph := svgW-svgPadL-svgPadR, h-svgPadT-svgPadB
	X := func(v float64) float64 { return svgPadL + v/xTop*pw }
	Y := func(v float64) float64 { return svgPadT + ph - v/yTop*ph }
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="xy" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s en fonction de %s">`, svgW, h, html.EscapeString(yLabel), html.EscapeString(xLabel))
	for v := 0.0; v <= yTop+yStep/2; v += yStep {
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, svgPadL, Y(v), svgPadL+pw, Y(v))
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, svgPadL-6, Y(v)+4, axisNum(v))
	}
	for v := 0.0; v <= xTop+xStep/2; v += xStep {
		fmt.Fprintf(&b, `<line class="grid" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, X(v), svgPadT, X(v), svgPadT+ph)
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, X(v), svgPadT+ph+16, axisNum(v))
	}
	fmt.Fprintf(&b, `<text class="axis" x="%.1f" y="%.1f" text-anchor="middle">%s</text>`, svgPadL+pw/2, h-6, html.EscapeString(xLabel))
	fmt.Fprintf(&b, `<text class="axis" x="14" y="%.1f" text-anchor="middle" transform="rotate(-90 14 %.1f)">%s</text>`, svgPadT+ph/2, svgPadT+ph/2, html.EscapeString(yLabel))
	if hline > 0 {
		fmt.Fprintf(&b, `<line class="ceiling" x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke-dasharray="6 4"/>`, svgPadL, Y(hline), svgPadL+pw, Y(hline))
		fmt.Fprintf(&b, `<text class="tick ceil" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, svgPadL+pw-4, Y(hline)-5, html.EscapeString(hlineLabel))
	}
	lx := svgPadL
	for _, s := range series {
		dash := ""
		if s.Dash {
			dash = ` stroke-dasharray="5 4"`
		}
		var pts []string
		for _, p := range s.Points {
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", X(p.X), Y(p.Y)))
		}
		fmt.Fprintf(&b, `<polyline fill="none" stroke="%s" stroke-width="2"%s points="%s"/>`, s.Color, dash, strings.Join(pts, " "))
		for _, p := range s.Points {
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="4" fill="%s"><title>%s</title></circle>`, X(p.X), Y(p.Y), s.Color, html.EscapeString(s.Label+" — "+p.Tip))
		}
		// légende en tête
		fmt.Fprintf(&b, `<line x1="%.1f" y1="12" x2="%.1f" y2="12" stroke="%s" stroke-width="3"%s/>`, lx, lx+18, s.Color, dash)
		fmt.Fprintf(&b, `<text class="legend" x="%.1f" y="16">%s</text>`, lx+22, html.EscapeString(s.Label))
		lx += 30 + 6.2*float64(len([]rune(s.Label)))
	}
	b.WriteString(`</svg>`)
	return HTML(b.String())
}

type bar struct {
	Label string
	Value float64
	Text  string
	Color string
	Tip   string
}

// svgBars : barres horizontales (une par hôte), valeur affichée au bout.
func svgBars(title string, bars []bar) HTML {
	const rowH, labelW, top = 24.0, 130.0, 24.0
	h := top + rowH*float64(len(bars)) + 8
	var mx float64
	for _, b := range bars {
		mx = math.Max(mx, b.Value)
	}
	if mx <= 0 {
		mx = 1
	}
	pw := svgW - labelW - 110
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="bars" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, svgW, h, html.EscapeString(title))
	fmt.Fprintf(&b, `<text class="axis" x="0" y="14">%s</text>`, html.EscapeString(title))
	for i, x := range bars {
		y := top + rowH*float64(i)
		w := math.Max(1, x.Value/mx*pw)
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f" text-anchor="end">%s</text>`, labelW-8, y+15, html.EscapeString(x.Label))
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" rx="2" fill="%s"><title>%s</title></rect>`, labelW, y+3, w, rowH-8, x.Color, html.EscapeString(x.Tip))
		fmt.Fprintf(&b, `<text class="tick" x="%.1f" y="%.1f">%s</text>`, labelW+w+6, y+15, html.EscapeString(x.Text))
	}
	b.WriteString(`</svg>`)
	return HTML(b.String())
}

// sortedPoints renvoie les clés d'une map triées (rendu déterministe).
func sortedKeys[V any](m map[string]V) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}
