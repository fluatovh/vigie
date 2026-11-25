// Package report produit le rapport client du §5.4 : un fichier HTML autonome (données, uPlot et
// CSS en ligne, aucune requête réseau), imprimable en A4, à partir des résultats d'un job (ou de
// deux pour un avant/après). Aucune mesure n'est refaite : tout vient du résultat persisté.
// Les phrases viennent de gabarits fixes (text.go), jamais d'une génération libre.
package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"vigie/internal/advanced"
	"vigie/internal/analysis"
	"vigie/internal/jobs"
	"vigie/internal/localtest"
	"vigie/internal/standard"
	"vigie/internal/tour"
)

// Page est le modèle rendu par le gabarit (une section par page imprimée).
type Page struct {
	Title     string
	Kind      string // standard, advanced, tour, compare
	Brand     Branding
	LogoURL   string // data URI contrôlée par ValidateLogo
	Subject   string
	RunDate   string
	Generated string
	Version   string
	JobIDs    []string
	Comment   []string
	Sections  []Section
	Charts    map[string]Chart
}

// HTML est un fragment déjà échappé (SVG produit par charts.go). On n'utilise pas html/template :
// sa réflexion empêche l'éditeur de liens d'élaguer les méthodes inutilisées (+5,7 Mo de binaire).
type HTML string

// Section est une page du rapport.
type Section struct {
	ID      string
	Title   string
	Verdict *VerdictBox
	Tiles   []Tile
	Lead    []string
	Blocks  []Block
}

// Block est un élément de section, rendu dans l'ordre.
type Block struct {
	Kind    string // h3, text, note, chart, svg, table, kv, list
	Text    string
	ChartID string
	SVG     HTML
	Table   *Table
	KV      []KV
	List    []string
}

// Tile est une tuile de chiffre titre.
type Tile struct {
	Value, Label, Note string
	Delta, DeltaClass  string
}

// VerdictBox est le bandeau de verdict.
type VerdictBox struct {
	Level, Class, Text string
}

// Table est un tableau simple ; Num marque les colonnes numériques (alignées à droite).
type Table struct {
	Head []string
	Num  []bool
	Rows [][]Cell
}

// Cell est une cellule de tableau.
type Cell struct {
	Text, Class string
}

// KV est une ligne clé / valeur.
type KV struct {
	K, V string
}

// Chart décrit un graph uPlot (tracé par report.js).
type Chart struct {
	Type   string        `json:"type"` // line, hist
	XLabel string        `json:"x"`
	YLabel string        `json:"y"`
	X      []float64     `json:"xs"`
	Series []ChartSeries `json:"series"`
	LogX   bool          `json:"logx,omitempty"`
	Height int           `json:"height"`
}

// ChartSeries est une série ; null = pas de valeur.
type ChartSeries struct {
	Label  string     `json:"label"`
	Color  string     `json:"color"`
	Dash   bool       `json:"dash,omitempty"`
	Values []*float64 `json:"values"`
}

// run normalise le résultat d'un job standard, avancé ou d'une tournée.
type run struct {
	job  *jobs.Job
	std  []*standard.Report // répétitions du test standard
	adv  *advanced.Report
	tour *tour.Report
}

// ErrNoReport : le job n'a pas de rapport client (profil ou état).
var ErrNoReport = errors.New("pas de rapport client pour ce job")

func load(j *jobs.Job) (*run, error) {
	if j == nil {
		return nil, ErrNoReport
	}
	if j.State != jobs.Succeeded || len(j.Result) == 0 {
		return nil, fmt.Errorf("%w : job %s dans l'état %s, sans résultat complet", ErrNoReport, j.ID, j.State)
	}
	r := &run{job: j}
	switch j.Profile {
	case "standard":
		var v struct {
			Runs []*standard.Report `json:"runs"`
		}
		if err := json.Unmarshal(j.Result, &v); err != nil {
			return nil, fmt.Errorf("résultat illisible : %w", err)
		}
		for _, x := range v.Runs {
			if x != nil && x.Write != nil && x.Read != nil {
				r.std = append(r.std, x)
			}
		}
		if len(r.std) == 0 {
			return nil, fmt.Errorf("%w : aucune mesure complète", ErrNoReport)
		}
	case "advanced":
		var v advanced.Report
		if err := json.Unmarshal(j.Result, &v); err != nil {
			return nil, fmt.Errorf("résultat illisible : %w", err)
		}
		if v.Standard == nil || v.Standard.Write == nil || v.Standard.Read == nil {
			return nil, fmt.Errorf("%w : test standard absent du résultat", ErrNoReport)
		}
		r.adv = &v
	case "tour":
		var v tour.Report
		if err := json.Unmarshal(j.Result, &v); err != nil {
			return nil, fmt.Errorf("résultat illisible : %w", err)
		}
		var legs []tour.Leg
		for _, l := range v.Legs {
			if l.Report != nil && l.Report.Write != nil && l.Report.Read != nil {
				legs = append(legs, l)
			}
		}
		if len(legs) == 0 {
			return nil, fmt.Errorf("%w : aucun passage complet", ErrNoReport)
		}
		v.Legs = legs
		r.tour = &v
	default:
		return nil, fmt.Errorf("%w : profil %s (rapport pour standard, advanced ou tour)", ErrNoReport, j.Profile)
	}
	return r, nil
}

// main est la mesure de référence (première répétition, test standard de l'avancé, premier passage).
func (r *run) main() *standard.Report {
	switch {
	case r.adv != nil:
		return r.adv.Standard
	case r.tour != nil:
		return r.tour.Legs[0].Report
	}
	return r.std[0]
}

// reports renvoie toutes les mesures standard du job.
func (r *run) reports() []*standard.Report {
	switch {
	case r.adv != nil:
		return []*standard.Report{r.adv.Standard}
	case r.tour != nil:
		var out []*standard.Report
		for _, l := range r.tour.Legs {
			out = append(out, l.Report)
		}
		return out
	}
	return r.std
}

func (r *run) kind() string {
	switch {
	case r.adv != nil:
		return "advanced"
	case r.tour != nil:
		return "tour"
	}
	return "standard"
}

// head regroupe les chiffres titres.
type head struct {
	Write, Read, WriteP99 float64 // médianes sur les répétitions / passages
	TxRatio               float64 // 0 : non mesurable
	TxReliable            bool
	Reps                  int
	Useful                *analysis.Useful // 4 Kio mixte 70/30 (avancé)
	QD1                   map[string]analysis.Point
	Ceiling               float64
	Level                 analysis.Level
	Triggered             []string
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	if len(c)%2 == 1 {
		return c[len(c)/2]
	}
	return (c[len(c)/2-1] + c[len(c)/2]) / 2
}

var rank = map[analysis.Level]int{analysis.Unknown: 0, analysis.OK: 1, analysis.Warning: 2, analysis.Alert: 3}

func (r *run) head() head {
	h := head{QD1: map[string]analysis.Point{}, Level: analysis.OK}
	var w, rd, p, tx []float64
	reliable := true
	for _, s := range r.reports() {
		w = append(w, s.Write.MBps)
		rd = append(rd, s.Read.MBps)
		p = append(p, s.Write.Latency.P99/1000)
		if s.TxRatio != nil && s.TxRatio.Samples > 0 {
			tx = append(tx, s.TxRatio.Value)
			reliable = reliable && s.TxRatio.Reliable
		}
	}
	h.Write, h.Read, h.WriteP99, h.TxRatio, h.Reps = median(w), median(rd), median(p), median(tx), len(w)
	h.TxReliable = len(tx) > 0 && reliable
	switch {
	case r.adv != nil:
		h.Level, h.Triggered = r.adv.Verdict.Level, r.adv.Verdict.Triggered
		h.Ceiling = r.adv.Options.CeilingMs
		for i := range r.adv.Useful {
			u := r.adv.Useful[i]
			if u.Pattern == "mixed" && u.BlockKiB == 4 {
				h.Useful = &u
			}
		}
		for _, pt := range r.adv.Points {
			if pt.QD == 1 && pt.BlockKiB == 4 {
				h.QD1[pt.Pattern] = pt
			}
		}
	case r.tour != nil:
		h.Level = r.tour.Comparison.Level
		h.Triggered = append(h.Triggered, r.tour.Comparison.Triggered...)
		for _, hs := range r.tour.Comparison.Hosts {
			if rank[hs.Level] > rank[h.Level] {
				h.Level = hs.Level
			}
			for _, t := range hs.Triggered {
				h.Triggered = append(h.Triggered, hs.Host+" : "+t)
			}
		}
	default:
		for i, s := range r.std {
			if i == 0 || rank[s.Verdict.Level] > rank[h.Level] {
				h.Level = s.Verdict.Level
			}
			for _, t := range s.Verdict.Triggered {
				if len(r.std) > 1 {
					t = fmt.Sprintf("répétition %d : %s", i+1, t)
				}
				h.Triggered = append(h.Triggered, t)
			}
		}
	}
	return h
}

// hostLabel : hôte(s) mesuré(s).
func (r *run) hostLabel() string {
	if r.tour != nil {
		n := len(r.tour.Comparison.Hosts)
		if n == 0 {
			n = len(r.tour.Options.Hosts)
		}
		return fmt.Sprintf("%d hôtes", n)
	}
	return r.main().Host.Name
}

// Summarize résume un résultat de job pour l'historique (nil si pas de rapport).
func Summarize(profile string, result json.RawMessage) *jobs.Summary {
	if profile == "local" {
		var l localtest.Report
		if json.Unmarshal(result, &l) != nil || l.Write == nil || l.Read == nil {
			return nil
		}
		s := &jobs.Summary{Host: "local (sans vCenter)", Datastore: l.Disk.DatastoreName, Level: string(l.Verdict.Level), Text: MBps(l.Write.MBps) + " / " + MBps(l.Read.MBps)}
		for _, u := range l.Useful {
			if u.Pattern == "mixed" && u.BlockKiB == 4 && u.IOPS > 0 {
				s.Text += " · " + IOPS(u.IOPS)
			}
		}
		return s
	}
	r, err := load(&jobs.Job{Profile: profile, Result: result, State: jobs.Succeeded})
	if err != nil {
		return nil
	}
	h := r.head()
	s := &jobs.Summary{Host: r.hostLabel(), Datastore: r.main().Datastore.Name, Level: string(h.Level),
		Text: MBps(h.Write) + " / " + MBps(h.Read)}
	if h.Useful != nil && h.Useful.IOPS > 0 {
		s.Text += " · " + IOPS(h.Useful.IOPS)
	}
	if r.tour != nil {
		s.Text = "écart ×" + dec(r.tour.Comparison.Spread, 2)
	}
	return s
}
