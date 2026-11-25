package netdiag

import (
	"math"
	"sort"
	"time"
)

// Point est une mesure consolidée façon smokeping : perte, distribution du RTT (ms).
type Point struct {
	T    int64   `json:"t"` // début de l'intervalle (secondes Unix)
	Sent int     `json:"s"`
	Recv int     `json:"r"`
	Min  float64 `json:"min"`
	P10  float64 `json:"p10"`
	P25  float64 `json:"p25"`
	Med  float64 `json:"med"`
	P75  float64 `json:"p75"`
	P90  float64 `json:"p90"`
	Max  float64 `json:"max"`
}

// FromRTTs construit un point à partir d'une salve (NaN = perdu).
func FromRTTs(t time.Time, rtts []float64) Point {
	p := Point{T: t.Unix(), Sent: len(rtts)}
	var ok []float64
	for _, r := range rtts {
		if !math.IsNaN(r) {
			ok = append(ok, r)
		}
	}
	p.Recv = len(ok)
	if len(ok) == 0 {
		return p
	}
	sort.Float64s(ok)
	p.Min, p.Max = ok[0], ok[len(ok)-1]
	p.P10, p.P25, p.Med, p.P75, p.P90 = Percentile(ok, 10), Percentile(ok, 25), Percentile(ok, 50), Percentile(ok, 75), Percentile(ok, 90)
	return p
}

// Consolidate regroupe des points en un seul (moyenne des quantiles pondérée par les réponses,
// min des min, max des max, somme des envois et réceptions), comme une consolidation RRD AVERAGE.
func Consolidate(t int64, pts []Point) Point {
	c := Point{T: t}
	var w float64
	first := true
	for _, p := range pts {
		c.Sent += p.Sent
		c.Recv += p.Recv
		if p.Recv == 0 {
			continue
		}
		f := float64(p.Recv)
		c.P10 += p.P10 * f
		c.P25 += p.P25 * f
		c.Med += p.Med * f
		c.P75 += p.P75 * f
		c.P90 += p.P90 * f
		w += f
		if first || p.Min < c.Min {
			c.Min = p.Min
		}
		if p.Max > c.Max {
			c.Max = p.Max
		}
		first = false
	}
	if w > 0 {
		c.P10, c.P25, c.Med, c.P75, c.P90 = c.P10/w, c.P25/w, c.Med/w, c.P75/w, c.P90/w
	}
	return c
}

// Tier est un niveau de résolution d'un RRD.
type Tier struct {
	Step   int64   `json:"step"` // secondes
	Rows   int     `json:"rows"` // capacité
	Points []Point `json:"points"`
}

// RRD : 1 min sur 48 h, 5 min sur 14 jours, 1 h sur 1 an (§11 du plan).
type RRD struct {
	Tiers []Tier `json:"tiers"`
	// tampon de consolidation par niveau (points du niveau précédent en attente)
	Pending [][]Point `json:"pending"`
}

// NewRRD crée un RRD vide.
func NewRRD() *RRD {
	r := &RRD{Tiers: []Tier{{Step: 60, Rows: 2880}, {Step: 300, Rows: 4032}, {Step: 3600, Rows: 8760}}}
	r.Pending = make([][]Point, len(r.Tiers))
	return r
}

// Add insère un point de résolution 1 min et propage les consolidations.
func (r *RRD) Add(p Point) {
	if len(r.Pending) != len(r.Tiers) {
		r.Pending = make([][]Point, len(r.Tiers))
	}
	p.T -= p.T % r.Tiers[0].Step
	r.push(0, p)
}

func (r *RRD) push(i int, p Point) {
	t := &r.Tiers[i]
	if n := len(t.Points); n > 0 && t.Points[n-1].T >= p.T {
		t.Points[n-1] = p // même intervalle : remplacement
	} else {
		t.Points = append(t.Points, p)
	}
	if len(t.Points) > t.Rows {
		t.Points = t.Points[len(t.Points)-t.Rows:]
	}
	if i+1 >= len(r.Tiers) {
		return
	}
	next := r.Tiers[i+1].Step
	bucket := p.T - p.T%next
	pend := r.Pending[i+1]
	if len(pend) > 0 && pend[0].T-pend[0].T%next != bucket {
		// l'intervalle supérieur précédent est complet : on le consolide
		r.push(i+1, Consolidate(pend[0].T-pend[0].T%next, pend))
		pend = nil
	}
	r.Pending[i+1] = append(pend, p)
}

// Range renvoie les points du niveau le plus fin couvrant [from, to].
func (r *RRD) Range(from, to time.Time) (int64, []Point) {
	f, t := from.Unix(), to.Unix()
	for _, tier := range r.Tiers {
		if len(tier.Points) == 0 {
			continue
		}
		if tier.Points[0].T <= f || len(tier.Points) < tier.Rows {
			return tier.Step, slice(tier.Points, f, t)
		}
	}
	last := r.Tiers[len(r.Tiers)-1]
	return last.Step, slice(last.Points, f, t)
}

func slice(pts []Point, f, t int64) []Point {
	out := []Point{}
	for _, p := range pts {
		if p.T >= f && p.T <= t {
			out = append(out, p)
		}
	}
	return out
}
