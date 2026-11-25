package report

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Arrondis lisibles du §5.4 : 48 000 IO/s, 2 310 Mo/s, 0,42 ms ; deux décimales sous 10 ms.
// Séparateur de milliers : espace insécable fine (U+202F), décimale : virgule.

const nbsp = " "
const thin = " "

// group formate un entier avec séparateur de milliers.
func group(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteString(thin)
		}
		b.WriteRune(c)
	}
	if neg {
		return "−" + b.String()
	}
	return b.String()
}

// dec formate avec n décimales et une virgule.
func dec(v float64, n int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "n/d"
	}
	s := strconv.FormatFloat(v, 'f', n, 64)
	ip, fp, _ := strings.Cut(s, ".")
	i, _ := strconv.ParseInt(ip, 10, 64)
	out := group(i)
	if strings.HasPrefix(ip, "-") && i == 0 {
		out = "−0"
	}
	if fp != "" {
		out += "," + fp
	}
	return out
}

// sig3 arrondit à trois chiffres significatifs (au-delà de 1 000).
func sig3(v float64) float64 {
	if v < 1000 {
		return math.Round(v)
	}
	p := math.Pow(10, math.Floor(math.Log10(v))-2)
	return math.Round(v/p) * p
}

// MBps : « 2 310 Mo/s ».
func MBps(v float64) string { return group(int64(math.Round(v))) + nbsp + "Mo/s" }

// IOPS : « 48 200 IO/s » (trois chiffres significatifs).
func IOPS(v float64) string { return group(int64(sig3(v))) + nbsp + "IO/s" }

// Ms : deux décimales sous 10 ms, une sous 100 ms, entier au-delà.
func Ms(v float64) string { return msNum(v) + nbsp + "ms" }

func msNum(v float64) string {
	switch {
	case v < 10:
		return dec(v, 2)
	case v < 100:
		return dec(v, 1)
	}
	return dec(v, 0)
}

// Ratio : trois décimales.
func Ratio(v float64) string { return dec(v, 3) }

// Pct : « +12,3 % ».
func Pct(v float64) string {
	sign := "+"
	if v < 0 {
		sign = "−"
		v = -v
	}
	return sign + dec(v, 1) + nbsp + "%"
}

// Size : tailles en Gio / Mio (puissances de deux).
func Size(b int64) string {
	switch {
	case b >= 1<<30:
		return dec(float64(b)/(1<<30), 2) + nbsp + "Gio"
	case b >= 1<<20:
		return dec(float64(b)/(1<<20), 0) + nbsp + "Mio"
	}
	return group(b) + nbsp + "o"
}

// BlockSize : « 1 Mio », « 4 Kio ».
func BlockSize(kib int) string {
	if kib >= 1024 && kib%1024 == 0 {
		return strconv.Itoa(kib/1024) + nbsp + "Mio"
	}
	return strconv.Itoa(kib) + nbsp + "Kio"
}

// Secs : durée lisible.
func Secs(s float64) string {
	d := time.Duration(s * float64(time.Second)).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%d%ss", int(d.Seconds()), nbsp)
	}
	m := int(d.Minutes())
	r := int(d.Seconds()) - 60*m
	if r == 0 {
		return fmt.Sprintf("%d%smin", m, nbsp)
	}
	return fmt.Sprintf("%d%smin%s%02d%ss", m, nbsp, nbsp, r, nbsp)
}

var mois = []string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}

// Date : « 18 novembre 2026 à 14 h 05 » (heure locale de l'appliance, UTC par défaut).
func Date(t time.Time) string {
	if t.IsZero() {
		return "date inconnue"
	}
	t = t.In(loc)
	return fmt.Sprintf("%d %s %d à %d%sh%s%02d UTC", t.Day(), mois[t.Month()-1], t.Year(), t.Hour(), nbsp, nbsp, t.Minute())
}

// DateShort : « 18/11/2026 14:05 ».
func DateShort(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(loc).Format("02/01/2006 15:04") + " UTC"
}

// loc : fuseau d'affichage. L'appliance tourne en UTC ; on le dit dans la méthodologie.
var loc = time.UTC

// MsCeil : seuil lisible (« 5 ms », « 2,5 ms »).
func MsCeil(v float64) string {
	if v == float64(int64(v)) {
		return dec(v, 0) + nbsp + "ms"
	}
	return Ms(v)
}
