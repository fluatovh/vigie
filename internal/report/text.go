package report

import (
	"fmt"
	"strings"

	"vigie/internal/analysis"
	"vigie/internal/bench"
)

// Gabarits de phrases (§5.4) : texte fixe, seules les valeurs changent.

func levelClass(l analysis.Level) string {
	switch l {
	case analysis.OK:
		return "ok"
	case analysis.Warning:
		return "warn"
	case analysis.Alert:
		return "alert"
	}
	return "unknown"
}

// status : OK / à surveiller / anomalie (section santé du chemin).
func status(l analysis.Level) string {
	switch l {
	case analysis.OK:
		return "OK"
	case analysis.Warning:
		return "À surveiller"
	case analysis.Alert:
		return "Anomalie"
	}
	return "Non mesuré"
}

func joinFR(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], " ; ") + " ; " + items[len(items)-1]
}

func sentVerdict(l analysis.Level, subject string, triggered []string) string {
	t := strings.TrimSuffix(joinFR(triggered), ".")
	switch l {
	case analysis.OK:
		return fmt.Sprintf("Verdict OK : %s est sain, aucun indicateur n'a été déclenché.", subject)
	case analysis.Warning:
		return fmt.Sprintf("Verdict ATTENTION : %s présente des points à surveiller (%s).", subject, t)
	case analysis.Alert:
		return fmt.Sprintf("Verdict ALERTE : %s présente une anomalie (%s).", subject, t)
	}
	return fmt.Sprintf("Verdict INCONNU : les indicateurs de %s n'ont pas pu être mesurés.", subject)
}

func sentThroughput(w, r float64, p bench.Profile, reps int) string {
	s := fmt.Sprintf("Le datastore soutient %s en écriture séquentielle et %s en lecture séquentielle (bloc %s, QD %d).",
		MBps(w), MBps(r), BlockSize(p.BlockKiB), p.QD)
	if reps > 1 {
		s += fmt.Sprintf(" Valeurs médianes sur %d mesures.", reps)
	}
	return s
}

func patternFR(p string) string {
	switch p {
	case "read":
		return "lecture"
	case "write":
		return "écriture"
	}
	return "mixte 70/30"
}

// usefulWhat : « lecture aléatoire 4 Kio », « 4 Kio aléatoire mixte 70/30 ».
func usefulWhat(pattern string, bs int) string {
	switch pattern {
	case "read":
		return "lecture aléatoire " + BlockSize(bs)
	case "write":
		return "écriture aléatoire " + BlockSize(bs)
	}
	return BlockSize(bs) + " aléatoire mixte 70/30"
}

func sentUseful(u analysis.Useful) string {
	if u.IOPS > 0 {
		s := fmt.Sprintf("Le datastore soutient %s en %s avec une latence p99 inférieure à %s (QD %d).",
			IOPS(u.IOPS), usefulWhat(u.Pattern, u.BlockKiB), MsCeil(u.CeilingMs), u.QD)
		if u.RawMax > u.IOPS*1.1 {
			s += fmt.Sprintf(" Le maximum brut atteint %s à QD %d, mais avec une latence p99 de %s.", IOPS(u.RawMax), u.RawMaxQD, Ms(u.RawMaxP99))
		}
		return s
	}
	return fmt.Sprintf("En %s, aucun palier ne reste sous %s de latence p99 ; le maximum brut est de %s à QD %d (p99 %s).",
		usefulWhat(u.Pattern, u.BlockKiB), MsCeil(u.CeilingMs), IOPS(u.RawMax), u.RawMaxQD, Ms(u.RawMaxP99))
}

func sentQD1(qd1 map[string]analysis.Point) string {
	w, okw := qd1["write"]
	r, okr := qd1["read"]
	switch {
	case okw && okr:
		return fmt.Sprintf("Une écriture 4 Kio isolée (QD1) prend %s en médiane et %s au 99e centile ; une lecture, %s et %s.",
			Ms(w.P50Ms), Ms(w.P99Ms), Ms(r.P50Ms), Ms(r.P99Ms))
	case okw:
		return fmt.Sprintf("Une écriture 4 Kio isolée (QD1) prend %s en médiane et %s au 99e centile.", Ms(w.P50Ms), Ms(w.P99Ms))
	case okr:
		return fmt.Sprintf("Une lecture 4 Kio isolée (QD1) prend %s en médiane et %s au 99e centile.", Ms(r.P50Ms), Ms(r.P99Ms))
	}
	return ""
}

func sentDistribution(name string, l bench.Latency) string {
	return fmt.Sprintf("%s : la moitié des I/O se terminent en moins de %s, 99 %% en moins de %s et 99,9 %% en moins de %s ; la plus lente a pris %s.",
		name, Ms(l.P50/1000), Ms(l.P99/1000), Ms(l.P999/1000), Ms(l.Max/1000))
}

// explain : une phrase d'explication par indicateur (section santé du chemin).
var explain = map[string]string{
	"tx_ratio":         "Octets émis par les vmnics NFS rapportés aux octets écrits, bruit de fond retiré. Au-delà de 1, l'hôte réémet des données : retransmissions TCP ou trafic parasite.",
	"write_mbps":       "Débit d'écriture séquentielle mesuré par l'appliance, comparé aux planchers du préréglage.",
	"nic_errors":       "Erreurs et pertes comptées en réception sur les vmnics du chemin NFS pendant la mesure. Toute erreur physique est une anomalie.",
	"nfs_warnings":     "Avertissements NFS apparus dans vmkernel.log pendant la mesure (serveur lent ou injoignable, connexion perdue).",
	"link_flaps":       "Changements d'état des liens signalés par vCenter pendant la mesure.",
	"nic_phys_errors":  "Erreurs CRC, de trame et de longueur reçues par les vmnics du chemin pendant le test (esxcli via l'API) : câble, optique, port du switch ou MTU incohérent.",
	"rx_missed":        "Trames que la carte n'a pas pu recevoir faute de place dans son anneau de réception : saturation de l'hôte ou anneau trop petit, pas un défaut physique.",
	"ds_write_latency": "Latence moyenne d'écriture vue par l'hôte sur ce datastore pendant l'écriture séquentielle.",
	"qd1_p99_write":    "Latence d'une écriture 4 Kio isolée au 99e centile : l'aller-retour VM → hôte → NFS → disque.",
	"qd1_p99_read":     "Latence d'une lecture 4 Kio isolée au 99e centile.",
	"iops_mismatch":    "Écart entre les IOPS comptées par l'appliance et celles que vSphere voit sur le disque virtuel ; au-delà de 10 %, le palier est à vérifier.",
	"worker_cpu":       "Charge CPU du générateur d'I/O ; au-delà de 85 %, la mesure est limitée par l'appliance et non par le stockage.",
	"host_spread":      "Écart de débit d'écriture entre le meilleur et le moins bon hôte (médianes).",
}

func indicatorValue(ind analysis.Indicator) string {
	v := ind.Why
	if !ind.Reliable && ind.Level != analysis.Unknown {
		v += " (peu fiable)"
	}
	return v
}

// limits : limites assumées (§9), reprises dans la méthodologie.
var limits = []string{
	"Pas de compteur de retransmissions TCP du vmkernel : le ratio TX/écrit corrigé est un indicateur indirect, sensible au trafic des autres VMs sur la même vmnic.",
	"Comparaison entre hôtes séquentielle et non simultanée : l'alternance réduit, sans l'annuler, l'effet d'une charge variable du serveur.",
	"Granularité de 20 s des compteurs vSphere : les micro-coupures plus courtes sont lissées.",
	"Les débits et IOPS incluent l'effet du cache du serveur NFS tant que la zone de test y tient : c'est ce que voit une VM, pas une mesure des disques du serveur.",
	"Les erreurs d'émission de l'hôte (vues par le switch) restent invisibles sans accès au switch.",
	"L'appliance mesure le chemin de l'hôte où elle tourne ; les autres hôtes ne sont couverts que par une tournée.",
}

const unitsText = "Mo/s = 10⁶ octets par seconde (convention des constructeurs de stockage) ; Kio et Mio (2¹⁰ et 2²⁰ octets) pour les tailles de bloc ; Gio pour la zone de test. IO/s : opérations terminées par seconde. p50, p99, p99,9 : 50 %, 99 % et 99,9 % des I/O se terminent en moins de cette durée."
