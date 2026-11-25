package analysis

import (
	"fmt"
	"time"
)

// KBps est l'unité des compteurs réseau vSphere (net.bytesTx/bytesRx/transmitted/received) :
// kilooctets par seconde, 1 Ko = 1024 octets.
const KBps = 1024.0

// RatioInput : échantillons d'un uplink (ou somme des uplinks) pendant la ligne de base et pendant
// la phase mesurée, plus le journal d'octets écrits (ou lus) du worker.
type RatioInput struct {
	Baseline []Sample // débit du compteur pendant la ligne de base (KBps)
	Bench    []Sample // débit du compteur pendant la fenêtre mesurée (KBps)
	Window   Window   // fenêtre mesurée (heure serveur)
	Ticks    []Tick   // octets par seconde du worker (heure serveur)
}

// Ratio est le ratio octets réseau / octets utiles, corrigé du trafic de fond (§3.2).
type Ratio struct {
	Value          float64   `json:"value"`            // (TX_bench − TX_fond) / octets_utiles
	NetBytes       float64   `json:"net_bytes"`        // TX_bench
	BackgroundB    float64   `json:"background_bytes"` // TX_fond
	UsefulBytes    float64   `json:"useful_bytes"`     // octets écrits (ou lus) sur les mêmes intervalles
	Samples        int       `json:"samples"`          // échantillons complets retenus
	Covered        float64   `json:"covered_s"`        // durée couverte par ces échantillons
	BaselineMean   float64   `json:"baseline_kbps"`
	BaselineStdDev float64   `json:"baseline_stddev_kbps"`
	Reliable       bool      `json:"reliable"`
	Notes          []string  `json:"notes,omitempty"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
}

// MaxBaselineCV : au-delà de 20 % d'écart-type relatif sur la ligne de base, le ratio est « peu fiable ».
const MaxBaselineCV = 0.20

// MinSamples : en dessous de 2 échantillons de 20 s entièrement contenus dans la fenêtre, le ratio
// n'est pas concluant (constaté sur le lab : 1 échantillon donne 1,06 à 1,10 là où 3 donnent 1,04).
const MinSamples = 2

// MinCoherentRatio : en dessous, le ratio est physiquement impossible (moins d'octets émis qu'écrits).
const MinCoherentRatio = 0.97

// ComputeRatio applique la formule du §3.2 en ne gardant que les échantillons entièrement contenus
// dans la fenêtre ; les octets utiles sont recomptés exactement sur ces mêmes intervalles.
func ComputeRatio(in RatioInput) Ratio {
	r := Ratio{Reliable: true}
	bench := Inside(in.Bench, in.Window)
	r.Samples = len(bench)
	if len(bench) == 0 {
		r.Reliable = false
		r.Notes = append(r.Notes, "aucun échantillon de 20 s entièrement contenu dans la fenêtre mesurée")
		return r
	}
	r.From, r.To = bench[0].Start, bench[len(bench)-1].End
	for _, s := range bench {
		d := s.End.Sub(s.Start).Seconds()
		r.NetBytes += s.Value * KBps * d
		r.Covered += d
		r.UsefulBytes += BytesIn(in.Ticks, s.Start, s.End)
	}
	if r.Samples < MinSamples {
		r.Reliable = false
		r.Notes = append(r.Notes, fmt.Sprintf("fenêtre trop courte : %d échantillon de 20 s complet (minimum %d, soit au moins 60 s d'écriture ; 80 s recommandés)", r.Samples, MinSamples))
	}
	r.BaselineMean, r.BaselineStdDev = Stats(in.Baseline)
	r.BackgroundB = r.BaselineMean * KBps * r.Covered
	if len(in.Baseline) < 2 {
		r.Reliable = false
		r.Notes = append(r.Notes, fmt.Sprintf("ligne de base trop courte (%d échantillon(s))", len(in.Baseline)))
	} else if r.BaselineMean > 0 && r.BaselineStdDev/r.BaselineMean > MaxBaselineCV {
		r.Reliable = false
		r.Notes = append(r.Notes, fmt.Sprintf("trafic de fond instable (écart-type %.0f %% de la moyenne) : choisir une plage plus calme ou allonger l'écriture", 100*r.BaselineStdDev/r.BaselineMean))
	}
	if r.UsefulBytes <= 0 {
		r.Reliable = false
		r.Notes = append(r.Notes, "aucun octet utile sur les intervalles retenus")
		return r
	}
	r.Value = (r.NetBytes - r.BackgroundB) / r.UsefulBytes
	if r.Value < MinCoherentRatio {
		// l'hôte ne peut pas émettre moins que ce qu'il écrit : le trafic de fond a varié entre la
		// ligne de base et la mesure (constaté sur le lab avec 60 s d'écriture : 0,835)
		r.Reliable = false
		r.Notes = append(r.Notes, fmt.Sprintf("ratio %.2f incohérent (< %.2f) : trafic de fond variable entre la ligne de base et la mesure", r.Value, MinCoherentRatio))
	}
	if r.BackgroundB > r.NetBytes*0.5 {
		r.Notes = append(r.Notes, "le trafic de fond dépasse la moitié du trafic mesuré : ratio sensible au bruit")
	}
	return r
}
