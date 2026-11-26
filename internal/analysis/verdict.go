package analysis

import (
	"fmt"
	"strings"
	"sync"
)

// Level est un niveau de verdict.
type Level string

const (
	OK      Level = "OK"
	Warning Level = "ATTENTION"
	Alert   Level = "ALERTE"
	Unknown Level = "INCONNU" // indicateur non mesurable (privilège manquant, pas d'échantillon…)
)

func (l Level) rank() int {
	switch l {
	case Alert:
		return 3
	case Warning:
		return 2
	case OK:
		return 1
	}
	return 0
}

// Thresholds sont les seuils du §7, modifiables (stockés dans /data à partir de la phase 7).
type Thresholds struct {
	Preset              string  `json:"preset"`
	TxRatioWarn         float64 `json:"tx_ratio_warn"`             // > 1,05
	TxRatioAlert        float64 `json:"tx_ratio_alert"`            // > 1,10
	HostSpreadWarn      float64 `json:"host_spread_warn"`          // ×2 (phase 8)
	HostSpreadAlert     float64 `json:"host_spread_alert"`         // ×3 (phase 8)
	WriteMBpsWarn       float64 `json:"write_mbps_warn"`           // < 50
	WriteMBpsAlert      float64 `json:"write_mbps_alert"`          // < 16
	NICErrorsAlert      float64 `json:"nic_errors_alert"`          // > 0
	NFSWarnLinesWarn    int     `json:"nfs_warn_lines_warn"`       // > 0
	NFSWarnLinesAlert   int     `json:"nfs_warn_lines_alert"`      // > 5
	LinkFlapsAlert      int     `json:"link_flaps_alert"`          // > 0
	DSWriteLatWarnMs    float64 `json:"ds_write_latency_warn_ms"`  // > 10
	DSWriteLatAlertMs   float64 `json:"ds_write_latency_alert_ms"` // > 30
	QD1P99WarnMs        float64 `json:"qd1_p99_warn_ms"`           // > 2 (phase 8b)
	QD1P99AlertMs       float64 `json:"qd1_p99_alert_ms"`          // > 10 (phase 8b)
	IOPSMismatchWarnPct float64 `json:"iops_mismatch_warn_pct"`    // > 10 % (phase 8b)
	WorkerCPUWarnPct    float64 `json:"worker_cpu_warn_pct"`       // > 85 % (phase 8b)
	UsefulIOPSCeilingMs float64 `json:"useful_iops_ceiling_ms"`    // 5 ms (phase 8b)
	PhysErrorsAlert     float64 `json:"nic_phys_errors_alert"`     // > 0 : CRC, trame, longueur (esxcli, phase 10)
	RxMissedWarn        float64 `json:"rx_missed_warn"`            // > 1000 : trames manquées par la carte (phase 10)
}

// Presets : les préréglages en vigueur (fournis, éventuellement modifiés dans l'UI, phase 9).
func Presets() map[string]Thresholds {
	out := BuiltinPresets()
	ovMu.RLock()
	defer ovMu.RUnlock()
	for k, v := range overrides {
		if _, ok := out[k]; ok {
			v.Preset = k
			out[k] = v
		}
	}
	return out
}

var (
	ovMu      sync.RWMutex
	overrides map[string]Thresholds
)

// SetOverrides remplace les seuils modifiés (par préréglage) ; nil revient aux valeurs fournies.
func SetOverrides(m map[string]Thresholds) {
	ovMu.Lock()
	defer ovMu.Unlock()
	overrides = map[string]Thresholds{}
	for k, v := range m {
		overrides[k] = v
	}
}

// BuiltinPresets : « all-flash » (défaut du plan), « hybride », « disques », valeurs fournies.
func BuiltinPresets() map[string]Thresholds {
	base := Thresholds{
		Preset: "all-flash", TxRatioWarn: 1.05, TxRatioAlert: 1.10, HostSpreadWarn: 2, HostSpreadAlert: 3,
		WriteMBpsWarn: 50, WriteMBpsAlert: 16, NICErrorsAlert: 0, NFSWarnLinesWarn: 0, NFSWarnLinesAlert: 5,
		LinkFlapsAlert: 0, DSWriteLatWarnMs: 10, DSWriteLatAlertMs: 30, QD1P99WarnMs: 2, QD1P99AlertMs: 10,
		IOPSMismatchWarnPct: 10, WorkerCPUWarnPct: 85, UsefulIOPSCeilingMs: 5, PhysErrorsAlert: 0, RxMissedWarn: 1000,
	}
	hybrid := base
	hybrid.Preset, hybrid.QD1P99WarnMs, hybrid.QD1P99AlertMs, hybrid.DSWriteLatWarnMs, hybrid.DSWriteLatAlertMs, hybrid.UsefulIOPSCeilingMs = "hybride", 5, 20, 15, 40, 10
	disks := base
	disks.Preset, disks.QD1P99WarnMs, disks.QD1P99AlertMs, disks.DSWriteLatWarnMs, disks.DSWriteLatAlertMs, disks.UsefulIOPSCeilingMs = "disques", 15, 50, 25, 60, 20
	return map[string]Thresholds{"all-flash": base, "hybride": hybrid, "disques": disks}
}

// DefaultThresholds renvoie le préréglage « all-flash ».
func DefaultThresholds() Thresholds { return Presets()["all-flash"] }

// Indicator est un indicateur évalué.
type Indicator struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Value    float64 `json:"value"`
	Unit     string  `json:"unit"`
	Level    Level   `json:"level"`
	Why      string  `json:"why"`
	Reliable bool    `json:"reliable"`
}

// Verdict regroupe les indicateurs et le niveau global (le pire).
type Verdict struct {
	Level      Level       `json:"level"`
	Triggered  []string    `json:"triggered"` // indicateurs ATTENTION/ALERTE, avec la raison
	Indicators []Indicator `json:"indicators"`
	Preset     string      `json:"preset"`
}

// Facts sont les mesures d'un test standard sur un hôte.
type Facts struct {
	TxRatio      *Ratio   // écriture ; nil si non mesurable
	RxRatio      *Ratio   // lecture (informatif)
	WriteMBps    float64  // débit d'écriture séquentielle (10^6 o/s)
	NICErrors    *float64 // Σ Δ errorsRx + droppedRx sur les uplinks NFS ; nil si non mesurable
	NFSWarnLines *int     // nouvelles lignes d'avertissement NFS dans vmkernel.log
	LinkFlaps    *int     // événements de lien vmnic
	DSWriteLatMs *float64 // latence d'écriture moyenne vue par l'hôte
	PhysErrors   *float64 // Δ CRC + trame + longueur en réception sur les uplinks (esxcli) ; nil si non lisible
	RxMissed     *float64 // Δ trames manquées + FIFO + débordements en réception (esxcli)
}

// fr formate un nombre à la française (virgule décimale) pour les libellés destinés à l'interface et au rapport.
func fr(format string, a ...any) string {
	return strings.ReplaceAll(fmt.Sprintf(format, a...), ".", ",")
}

// Evaluate applique les seuils.
func Evaluate(f Facts, th Thresholds) Verdict {
	v := Verdict{Level: OK, Preset: th.Preset}
	add := func(ind Indicator) {
		v.Indicators = append(v.Indicators, ind)
		if ind.Level.rank() > v.Level.rank() {
			v.Level = ind.Level
		}
		if ind.Level == Warning || ind.Level == Alert {
			v.Triggered = append(v.Triggered, fmt.Sprintf("%s : %s", ind.Label, ind.Why))
		}
	}

	if f.TxRatio == nil || f.TxRatio.Samples == 0 {
		add(Indicator{Key: "tx_ratio", Label: "Ratio TX/écrit corrigé", Level: Unknown, Why: "non mesurable"})
	} else if f.TxRatio.Samples < MinSamples {
		add(Indicator{Key: "tx_ratio", Label: "Ratio TX/écrit corrigé", Value: f.TxRatio.Value, Level: Unknown,
			Why: fmt.Sprintf("%s non concluant : %d échantillon de 20 s complet, écriture trop courte", fr("%.2f", f.TxRatio.Value), f.TxRatio.Samples)})
	} else {
		r := f.TxRatio
		ind := Indicator{Key: "tx_ratio", Label: "Ratio TX/écrit corrigé", Value: r.Value, Unit: "", Reliable: r.Reliable, Level: OK,
			Why: fr("%.2f (seuils %.2f / %.2f)", r.Value, th.TxRatioWarn, th.TxRatioAlert)}
		switch {
		case r.Value > th.TxRatioAlert:
			ind.Level = Alert
		case r.Value > th.TxRatioWarn:
			ind.Level = Warning
		}
		if !r.Reliable {
			ind.Why += " — peu fiable"
			if ind.Level == Alert {
				ind.Level = Warning // un ratio peu fiable ne déclenche pas seul une alerte
			}
		}
		add(ind)
	}

	wind := Indicator{Key: "write_mbps", Label: "Débit d'écriture", Value: f.WriteMBps, Unit: "Mo/s", Reliable: true, Level: OK,
		Why: fmt.Sprintf("%.0f Mo/s (seuils %.0f / %.0f)", f.WriteMBps, th.WriteMBpsWarn, th.WriteMBpsAlert)}
	switch {
	case f.WriteMBps < th.WriteMBpsAlert:
		wind.Level = Alert
	case f.WriteMBps < th.WriteMBpsWarn:
		wind.Level = Warning
	}
	add(wind)

	if f.NICErrors == nil {
		add(Indicator{Key: "nic_errors", Label: "Erreurs/pertes en réception (vmnic NFS)", Level: Unknown, Why: "non mesurable"})
	} else {
		ind := Indicator{Key: "nic_errors", Label: "Erreurs/pertes en réception (vmnic NFS)", Value: *f.NICErrors, Reliable: true, Level: OK, Why: fmt.Sprintf("%.0f", *f.NICErrors)}
		if *f.NICErrors > th.NICErrorsAlert {
			ind.Level = Alert
		}
		add(ind)
	}

	if f.NFSWarnLines == nil {
		add(Indicator{Key: "nfs_warnings", Label: "Avertissements NFS (vmkernel.log)", Level: Unknown, Why: "journal non lisible (privilège Global.Diagnostics ?)"})
	} else {
		n := *f.NFSWarnLines
		ind := Indicator{Key: "nfs_warnings", Label: "Avertissements NFS (vmkernel.log)", Value: float64(n), Reliable: true, Level: OK, Why: fmt.Sprintf("%d nouvelle(s) ligne(s)", n)}
		switch {
		case n > th.NFSWarnLinesAlert:
			ind.Level = Alert
		case n > th.NFSWarnLinesWarn:
			ind.Level = Warning
		}
		add(ind)
	}

	if f.LinkFlaps == nil {
		add(Indicator{Key: "link_flaps", Label: "Changements d'état de lien", Level: Unknown, Why: "événements non lisibles"})
	} else {
		ind := Indicator{Key: "link_flaps", Label: "Changements d'état de lien", Value: float64(*f.LinkFlaps), Reliable: true, Level: OK, Why: fmt.Sprintf("%d événement(s)", *f.LinkFlaps)}
		if *f.LinkFlaps > th.LinkFlapsAlert {
			ind.Level = Alert
		}
		add(ind)
	}

	if f.PhysErrors == nil {
		add(Indicator{Key: "nic_phys_errors", Label: "Erreurs physiques en réception (CRC, trame, longueur)", Level: Unknown, Why: "esxcli non autorisé ou indisponible"})
	} else {
		n := *f.PhysErrors
		ind := Indicator{Key: "nic_phys_errors", Label: "Erreurs physiques en réception (CRC, trame, longueur)", Value: n, Reliable: true, Level: OK,
			Why: fmt.Sprintf("%.0f pendant le test (seuil %.0f)", n, th.PhysErrorsAlert)}
		if n > th.PhysErrorsAlert {
			ind.Level = Alert
		}
		add(ind)
	}
	if f.RxMissed != nil {
		n := *f.RxMissed
		ind := Indicator{Key: "rx_missed", Label: "Trames manquées par la carte (anneau RX plein)", Value: n, Reliable: true, Level: OK,
			Why: fmt.Sprintf("%.0f pendant le test (seuil %.0f)", n, th.RxMissedWarn)}
		if n > th.RxMissedWarn {
			ind.Level = Warning
		}
		add(ind)
	}

	if f.DSWriteLatMs == nil {
		add(Indicator{Key: "ds_write_latency", Label: "Latence d'écriture datastore (hôte)", Level: Unknown, Why: "compteur indisponible"})
	} else {
		l := *f.DSWriteLatMs
		ind := Indicator{Key: "ds_write_latency", Label: "Latence d'écriture datastore (hôte)", Value: l, Unit: "ms", Reliable: true, Level: OK,
			Why: fr("%.1f ms (seuils %.0f / %.0f)", l, th.DSWriteLatWarnMs, th.DSWriteLatAlertMs)}
		switch {
		case l > th.DSWriteLatAlertMs:
			ind.Level = Alert
		case l > th.DSWriteLatWarnMs:
			ind.Level = Warning
		}
		add(ind)
	}
	return v
}
