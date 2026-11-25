// Package analysis transforme les échantillons PerformanceManager (20 s) et le journal d'écriture à
// la seconde du worker en indicateurs : ratio TX/écrit corrigé de la ligne de base, erreurs NIC,
// latence datastore, et verdicts OK / ATTENTION / ALERTE selon les seuils du §7 du plan.
// Le paquet est pur (aucun accès réseau) : tout est testable sur des séries synthétiques.
package analysis

import (
	"math"
	"sort"
	"time"
)

// Sample est un échantillon de compteur couvrant [Start, End] en heure serveur. Value est dans
// l'unité du compteur (KBps pour net.bytesTx, ms pour les latences, nombre pour les erreurs…).
type Sample struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Value float64   `json:"value"`
}

// Tick est une seconde du journal du worker, déjà convertie en heure serveur : [At-1s, At].
type Tick struct {
	At    time.Time `json:"at"`
	Bytes int64     `json:"bytes"`
}

// Window est un intervalle en heure serveur.
type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Duration renvoie la durée de la fenêtre.
func (w Window) Duration() time.Duration { return w.End.Sub(w.Start) }

// Inside garde les échantillons entièrement contenus dans la fenêtre (§3.1 : bords jetés).
func Inside(samples []Sample, w Window) []Sample {
	var out []Sample
	for _, s := range samples {
		if !s.Start.Before(w.Start) && !s.End.After(w.End) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// BytesIn compte les octets du journal tombant dans [start, end], au prorata pour les secondes à
// cheval sur une borne.
func BytesIn(ticks []Tick, start, end time.Time) float64 {
	var total float64
	for _, t := range ticks {
		ts, te := t.At.Add(-time.Second), t.At
		lo, hi := maxTime(ts, start), minTime(te, end)
		if !hi.After(lo) {
			continue
		}
		total += float64(t.Bytes) * hi.Sub(lo).Seconds()
	}
	return total
}

// Stats calcule moyenne et écart-type des valeurs.
func Stats(samples []Sample) (mean, stddev float64) {
	if len(samples) == 0 {
		return 0, 0
	}
	for _, s := range samples {
		mean += s.Value
	}
	mean /= float64(len(samples))
	if len(samples) > 1 {
		var v float64
		for _, s := range samples {
			v += (s.Value - mean) * (s.Value - mean)
		}
		stddev = math.Sqrt(v / float64(len(samples)-1))
	}
	return mean, stddev
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
