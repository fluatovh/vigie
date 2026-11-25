package analysis

import (
	"fmt"
	"sort"
)

// HostRun est le résultat d'un test standard sur un hôte, pour la comparaison (§3.3).
type HostRun struct {
	Host      string  `json:"host"`
	Cycle     int     `json:"cycle"`
	WriteMBps float64 `json:"write_mbps"`
	ReadMBps  float64 `json:"read_mbps"`
	TxRatio   float64 `json:"tx_ratio"` // 0 : non mesurable
	WriteP99  float64 `json:"write_p99_ms"`
	Level     Level   `json:"level"`
}

// HostSummary agrège les passages sur un hôte (médianes).
type HostSummary struct {
	Host      string   `json:"host"`
	Runs      int      `json:"runs"`
	WriteMBps float64  `json:"write_mbps_median"`
	ReadMBps  float64  `json:"read_mbps_median"`
	TxRatio   float64  `json:"tx_ratio_median"`
	WriteP99  float64  `json:"write_p99_ms_median"`
	VsBest    float64  `json:"write_vs_best"` // débit d'écriture du meilleur hôte / celui-ci (1 = le meilleur)
	VsMedian  float64  `json:"write_vs_median_pct"`
	Level     Level    `json:"level"`
	Triggered []string `json:"triggered,omitempty"`
}

// Comparison est le verdict comparatif d'une tournée.
type Comparison struct {
	Hosts     []HostSummary `json:"hosts"`
	Spread    float64       `json:"write_spread"` // meilleur / pire (médianes)
	Level     Level         `json:"level"`
	Sentence  string        `json:"sentence"`
	Triggered []string      `json:"triggered,omitempty"`
}

func med(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

// Compare calcule les médianes par hôte et l'écart de débit d'écriture (seuils ×2 / ×3, §7).
func Compare(runs []HostRun, th Thresholds) Comparison {
	byHost := map[string][]HostRun{}
	var order []string
	for _, r := range runs {
		if _, ok := byHost[r.Host]; !ok {
			order = append(order, r.Host)
		}
		byHost[r.Host] = append(byHost[r.Host], r)
	}
	c := Comparison{Level: OK}
	var best, worst float64
	var medians []float64
	for _, h := range order {
		var w, rd, tx, p99 []float64
		s := HostSummary{Host: h, Runs: len(byHost[h]), Level: OK}
		for _, r := range byHost[h] {
			w = append(w, r.WriteMBps)
			rd = append(rd, r.ReadMBps)
			p99 = append(p99, r.WriteP99)
			if r.TxRatio > 0 {
				tx = append(tx, r.TxRatio)
			}
			if r.Level.rank() > s.Level.rank() {
				s.Level = r.Level
			}
		}
		s.WriteMBps, s.ReadMBps, s.TxRatio, s.WriteP99 = med(w), med(rd), med(tx), med(p99)
		if s.WriteMBps > best {
			best = s.WriteMBps
		}
		if worst == 0 || s.WriteMBps < worst {
			worst = s.WriteMBps
		}
		medians = append(medians, s.WriteMBps)
		c.Hosts = append(c.Hosts, s)
	}
	all := med(medians)
	for i := range c.Hosts {
		h := &c.Hosts[i]
		if h.WriteMBps > 0 {
			h.VsBest = best / h.WriteMBps
		}
		if all > 0 {
			h.VsMedian = (h.WriteMBps - all) / all * 100
		}
		switch {
		case h.VsBest > th.HostSpreadAlert:
			h.Level = Alert
			h.Triggered = append(h.Triggered, fr("débit d'écriture ×%.1f plus lent que le meilleur hôte (seuil ×%.0f)", h.VsBest, th.HostSpreadAlert))
		case h.VsBest > th.HostSpreadWarn:
			if h.Level.rank() < Warning.rank() {
				h.Level = Warning
			}
			h.Triggered = append(h.Triggered, fr("débit d'écriture ×%.1f plus lent que le meilleur hôte (seuil ×%.0f)", h.VsBest, th.HostSpreadWarn))
		}
		if h.Level.rank() > c.Level.rank() {
			c.Level = h.Level
		}
		for _, t := range h.Triggered {
			c.Triggered = append(c.Triggered, h.Host+" : "+t)
		}
	}
	if worst > 0 {
		c.Spread = best / worst
	}
	if len(c.Hosts) >= 2 {
		var slow, fast HostSummary
		for _, h := range c.Hosts {
			if slow.Host == "" || h.WriteMBps < slow.WriteMBps {
				slow = h
			}
			if fast.Host == "" || h.WriteMBps > fast.WriteMBps {
				fast = h
			}
		}
		if c.Spread > th.HostSpreadWarn {
			c.Sentence = fmt.Sprintf("%s sur %s : débit d'écriture %s plus lent que %s.", levelOf(c.Hosts, slow.Host), slow.Host, fr("×%.1f", c.Spread), fast.Host)
		} else {
			c.Sentence = fmt.Sprintf("Les %d hôtes sont homogènes : écart de débit d'écriture %s entre le meilleur et le moins bon.", len(c.Hosts), fr("×%.2f", c.Spread))
		}
	}
	return c
}

func levelOf(hs []HostSummary, host string) string {
	for _, h := range hs {
		if h.Host == host {
			return string(h.Level)
		}
	}
	return ""
}
