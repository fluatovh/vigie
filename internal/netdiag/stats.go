// Package netdiag regroupe les outils de diagnostic réseau de l'appliance (phase N) : ping ICMP
// natif et statistiques, surveillance continue façon smokeping avec consolidation de type RRD,
// traceroute, table ARP et tests de débit iperf3 entre appliances.
package netdiag

import (
	"math"
	"sort"
)

// Stats résume une série de pings (millisecondes).
type Stats struct {
	Sent    int     `json:"sent"`
	Recv    int     `json:"recv"`
	LossPct float64 `json:"loss_pct"`
	Min     float64 `json:"min_ms"`
	Median  float64 `json:"median_ms"`
	Avg     float64 `json:"avg_ms"`
	Max     float64 `json:"max_ms"`
	P90     float64 `json:"p90_ms"`
	StdDev  float64 `json:"stddev_ms"`
	Jitter  float64 `json:"jitter_ms"` // moyenne des écarts absolus entre RTT consécutifs (RFC 3550, simplifié)
}

// Compute calcule les statistiques ; rtts est dans l'ordre d'émission (NaN = perdu).
func Compute(rtts []float64) Stats {
	s := Stats{Sent: len(rtts)}
	var ok []float64
	var prev float64
	havePrev := false
	var jsum float64
	jn := 0
	for _, r := range rtts {
		if math.IsNaN(r) {
			continue
		}
		ok = append(ok, r)
		if havePrev {
			jsum += math.Abs(r - prev)
			jn++
		}
		prev, havePrev = r, true
	}
	s.Recv = len(ok)
	if s.Sent > 0 {
		s.LossPct = 100 * float64(s.Sent-s.Recv) / float64(s.Sent)
	}
	if len(ok) == 0 {
		return s
	}
	sort.Float64s(ok)
	s.Min, s.Max = ok[0], ok[len(ok)-1]
	s.Median = Percentile(ok, 50)
	s.P90 = Percentile(ok, 90)
	for _, r := range ok {
		s.Avg += r
	}
	s.Avg /= float64(len(ok))
	for _, r := range ok {
		s.StdDev += (r - s.Avg) * (r - s.Avg)
	}
	if len(ok) > 1 {
		s.StdDev = math.Sqrt(s.StdDev / float64(len(ok)-1))
	} else {
		s.StdDev = 0
	}
	if jn > 0 {
		s.Jitter = jsum / float64(jn)
	}
	return s
}

// Percentile sur des valeurs triées (interpolation linéaire).
func Percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := p / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
