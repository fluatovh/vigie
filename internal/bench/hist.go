// Package bench est le moteur de mesure : préremplissage et motifs d'I/O en O_DIRECT sur le disque
// de test, latence par I/O, débit à la seconde, histogrammes, usage CPU. Il s'exécute dans le
// sous-processus `vigie worker` (CPU 1..n-1) et parle JSON au processus serveur.
package bench

import (
	"math"
	"math/bits"
)

// Hist est un histogramme de latences de type HDR : pour chaque puissance de deux, 64 sous-paniers
// (précision environ 1,6 %). Plage : 1 µs à 2^40 ns (environ 18 min). Valeurs en nanosecondes.
type Hist struct {
	buckets [41 * 64]uint64
	Count   uint64
	Sum     uint64
	Min     uint64
	Max     uint64
}

const histSub = 64

func (h *Hist) index(ns uint64) int {
	if ns < 1024 {
		return int(ns * histSub / 1024)
	}
	exp := bits.Len64(ns) - 1 // 10..40
	if exp > 40 {
		exp = 40
		ns = 1<<41 - 1
	}
	sub := (ns >> (uint(exp) - 6)) & (histSub - 1) // 6 bits sous le bit de poids fort
	return (exp-10+1)*histSub + int(sub)
}

func (h *Hist) value(i int) uint64 {
	if i < histSub {
		return uint64(i+1)*1024/histSub - 1 // borne haute du panier linéaire (16 ns)
	}
	exp := uint(i/histSub) + 9
	sub := uint64(i % histSub)
	return (1 << exp) + sub<<(exp-6) + (1 << (exp - 6)) - 1 // borne haute du panier
}

// Add enregistre une latence en nanosecondes.
func (h *Hist) Add(ns uint64) {
	h.buckets[h.index(ns)]++
	h.Count++
	h.Sum += ns
	if h.Count == 1 || ns < h.Min {
		h.Min = ns
	}
	if ns > h.Max {
		h.Max = ns
	}
}

// Merge ajoute un autre histogramme.
func (h *Hist) Merge(o *Hist) {
	for i := range h.buckets {
		h.buckets[i] += o.buckets[i]
	}
	if o.Count > 0 {
		if h.Count == 0 || o.Min < h.Min {
			h.Min = o.Min
		}
		if o.Max > h.Max {
			h.Max = o.Max
		}
	}
	h.Count += o.Count
	h.Sum += o.Sum
}

// Reset vide l'histogramme.
func (h *Hist) Reset() { *h = Hist{} }

// Percentile renvoie la valeur (ns) au percentile p (0-100), ou 0 si vide. Le max exact est
// renvoyé pour p >= 100.
func (h *Hist) Percentile(p float64) uint64 {
	if h.Count == 0 {
		return 0
	}
	if p >= 100 {
		return h.Max
	}
	target := uint64(math.Ceil(p / 100 * float64(h.Count)))
	if target == 0 {
		target = 1
	}
	var acc uint64
	for i, c := range h.buckets {
		acc += c
		if acc >= target {
			v := h.value(i)
			if v > h.Max {
				v = h.Max
			}
			if v < h.Min {
				v = h.Min
			}
			return v
		}
	}
	return h.Max
}

// Mean renvoie la moyenne en ns.
func (h *Hist) Mean() float64 {
	if h.Count == 0 {
		return 0
	}
	return float64(h.Sum) / float64(h.Count)
}

// Latency résume un histogramme en microsecondes.
type Latency struct {
	P50  float64 `json:"p50_us"`
	P95  float64 `json:"p95_us"`
	P99  float64 `json:"p99_us"`
	P999 float64 `json:"p999_us"`
	Max  float64 `json:"max_us"`
	Mean float64 `json:"mean_us"`
}

// Summary résume l'histogramme.
func (h *Hist) Summary() Latency {
	us := func(ns uint64) float64 { return float64(ns) / 1000 }
	return Latency{P50: us(h.Percentile(50)), P95: us(h.Percentile(95)), P99: us(h.Percentile(99)), P999: us(h.Percentile(99.9)), Max: us(h.Max), Mean: h.Mean() / 1000}
}

// Buckets exporte les paniers non vides (borne haute en ns, effectif) pour les graphs de distribution.
func (h *Hist) Buckets() [][2]uint64 {
	var out [][2]uint64
	for i, c := range h.buckets {
		if c > 0 {
			out = append(out, [2]uint64{h.value(i), c})
		}
	}
	return out
}
