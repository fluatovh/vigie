package tui

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// ---------- formulaire ----------

// Field est un champ de formulaire : texte libre, ou liste de choix (← →) si Choices est défini.
type Field struct {
	Label   string
	Value   string
	Choices []string
	Idx     int
	Max     int
	Accept  func(r rune) bool // caractères acceptés (nil : imprimables)
	Hint    string
}

// Get renvoie la valeur saisie ou choisie.
func (f *Field) Get() string {
	if f.Choices != nil {
		return f.Choices[f.Idx]
	}
	return strings.TrimSpace(f.Value)
}

// Form est une liste de champs ; Entrée valide.
type Form struct {
	Fields []*Field
	Cur    int
	Button string
	LabelW int
}

// Key : submitted vaut true quand l'utilisateur valide (Entrée).
func (f *Form) Key(k Key) (submitted, consumed bool) {
	if len(f.Fields) == 0 {
		return k.Name == "Enter", k.Name == "Enter"
	}
	fd := f.Fields[f.Cur]
	switch k.Name {
	case "Enter":
		return true, true
	case "Up", "BackTab":
		if f.Cur > 0 {
			f.Cur--
		}
		return false, true
	case "Down", "Tab":
		if f.Cur < len(f.Fields)-1 {
			f.Cur++
		}
		return false, true
	case "Left":
		if fd.Choices != nil {
			fd.Idx = (fd.Idx + len(fd.Choices) - 1) % len(fd.Choices)
			return false, true
		}
		return false, fd.Value != ""
	case "Right":
		if fd.Choices != nil {
			fd.Idx = (fd.Idx + 1) % len(fd.Choices)
		}
		return false, true
	case "Backspace":
		if fd.Choices == nil && fd.Value != "" {
			r := []rune(fd.Value)
			fd.Value = string(r[:len(r)-1])
		}
		return false, true
	case "Delete":
		if fd.Choices == nil {
			fd.Value = ""
		}
		return false, true
	case "":
		if fd.Choices != nil {
			if k.R == ' ' {
				fd.Idx = (fd.Idx + 1) % len(fd.Choices)
			}
			return false, true
		}
		ok := unicode.IsPrint(k.R)
		if fd.Accept != nil {
			ok = fd.Accept(k.R)
		}
		max := fd.Max
		if max == 0 {
			max = 60
		}
		if ok && len([]rune(fd.Value)) < max {
			fd.Value += string(k.R)
		}
		return false, true
	}
	return false, false
}

// Draw dessine le formulaire ; renvoie le nombre de lignes.
func (f *Form) Draw(s *Screen, x, y, w int, focus bool) int {
	lw := f.LabelW
	if lw == 0 {
		lw = 14
	}
	row := y
	for i, fd := range f.Fields {
		s.Text(x, row, fd.Label, stNormal, lw)
		fw := w - lw - 1
		val := fd.Value
		st := stNormal
		if fd.Choices != nil {
			val = "◄ " + fd.Choices[fd.Idx] + " ►"
			if !cp437['◄'] {
				val = "< " + fd.Choices[fd.Idx] + " >"
			}
		}
		if focus && i == f.Cur {
			st = stInput
			if fd.Choices == nil {
				val += "_"
			}
		}
		if fd.Choices == nil {
			fw = min(fw, max(fd.Max, 16)+1)
			s.Fill(x+lw+1, row, fw, 1, ' ', Style{Fg: White, Bg: Blue})
			if r := []rune(val); len(r) > fw {
				val = string(r[len(r)-fw:])
			}
			s.Raw(x+lw+1, row, val, st, fw)
		} else {
			s.Raw(x+lw+1, row, strings.Replace(val, fd.Choices[fd.Idx], T(fd.Choices[fd.Idx]), 1), st, fw)
		}
		if fd.Hint != "" && focus && i == f.Cur {
			s.Text(x+lw+fw+3, row, fd.Hint, stDim, w-lw-fw-3)
		}
		row++
	}
	if f.Button != "" {
		row++
		st := stAccent
		if focus {
			st = Style{Fg: Black, Bg: Green}
		}
		s.Raw(x, row, " "+Tf("Entrée : %s", T(f.Button))+" ", st, w)
		row++
	}
	return row - y
}

func digits(r rune) bool { return r >= '0' && r <= '9' }

func hostRunes(r rune) bool {
	return r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(".-:_", r))
}

// ---------- liste ----------

// List gère la sélection et le défilement d'une liste de n éléments.
type List struct {
	Sel, Top int
}

// Key déplace la sélection.
func (l *List) Key(k Key, n, page int) bool {
	switch k.Name {
	case "Up":
		l.Sel--
	case "Down":
		l.Sel++
	case "PgUp":
		l.Sel -= page
	case "PgDn":
		l.Sel += page
	case "Home":
		l.Sel = 0
	case "End":
		l.Sel = n - 1
	default:
		return false
	}
	l.Clamp(n)
	return true
}

// Clamp borne la sélection.
func (l *List) Clamp(n int) {
	if l.Sel >= n {
		l.Sel = n - 1
	}
	if l.Sel < 0 {
		l.Sel = 0
	}
}

// Window renvoie les indices visibles [start, end) pour h lignes.
func (l *List) Window(h, n int) (int, int) {
	if l.Sel < l.Top {
		l.Top = l.Sel
	}
	if l.Sel >= l.Top+h {
		l.Top = l.Sel - h + 1
	}
	if l.Top > n-h {
		l.Top = max(0, n-h)
	}
	return l.Top, min(n, l.Top+h)
}

// ---------- graphs en caractères ----------
// Résolution verticale doublée par les demi-blocs ▄ et █ (présents dans la police VGA).

// axisLabel formate une graduation courte.
func axisLabel(v float64) string {
	return strings.Replace(axisLabelRaw(v), ".", decimalSep(), 1)
}

func axisLabelRaw(v float64) string {
	switch {
	case v >= 10000:
		return strconv.FormatFloat(v/1000, 'f', 0, 64) + "k"
	case v >= 100:
		return strconv.FormatFloat(v, 'f', 0, 64)
	case v >= 10:
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// niceTop arrondit un maximum vers le haut (1, 2, 5 × 10^n).
func niceTop(v float64) float64 {
	if v <= 0 || math.IsNaN(v) {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if v <= m*p {
			return m * p
		}
	}
	return 10 * p
}

// BarChart trace une série (une colonne par valeur, la plus récente à droite) ; NaN = perte.
// Renvoie le maximum de l'échelle.
func BarChart(s *Screen, x, y, w, h int, vals []float64, unit string, st Style) float64 {
	const axisW = 7
	pw := w - axisW
	if pw < 4 || h < 2 {
		return 0
	}
	if len(vals) > pw {
		vals = vals[len(vals)-pw:]
	}
	mx := 0.0
	for _, v := range vals {
		if !math.IsNaN(v) {
			mx = math.Max(mx, v)
		}
	}
	top := niceTop(mx * 1.05)
	s.Text(x, y, fmt.Sprintf("%6s", axisLabel(top)), stDim, axisW-1)
	s.Text(x, y+h/2, fmt.Sprintf("%6s", axisLabel(top/2)), stDim, axisW-1)
	s.Text(x, y+h-1, fmt.Sprintf("%6s", "0"), stDim, axisW-1)
	if unit != "" {
		s.Text(x, y+h, fmt.Sprintf("%6s", unit), stDim, axisW-1)
	}
	for j := 0; j < h; j++ {
		s.Set(x+axisW-1, y+j, '┤', stGrid)
	}
	half := '▄'
	if !supported(half) {
		half = '█' // police sans demi-bloc (Lat2-Terminus16) : résolution d'une ligne
	}
	for i, v := range vals {
		cx := x + axisW + i
		if math.IsNaN(v) {
			s.Set(cx, y+h-1, 'x', stAlert)
			continue
		}
		units := int(math.Round(v / top * float64(2*h)))
		if v > 0 && units == 0 {
			units = 1
		}
		for j := 0; j < h; j++ {
			lvl := units - 2*(h-1-j) // demi-cellules remplies dans cette rangée (en partant du bas)
			switch {
			case lvl >= 2:
				s.Set(cx, y+j, '█', st)
			case lvl == 1:
				s.Set(cx, y+j, half, st)
			}
		}
	}
	for i := len(vals); i < pw; i++ {
		s.Set(x+axisW+i, y+h-1, '·', stGrid)
	}
	return top
}

// SmokePoint est une colonne du graph de surveillance.
type SmokePoint struct {
	Sent, Recv    int
	P10, Med, P90 float64
	Max           float64
}

// SmokeChart trace la surveillance façon smokeping : fumée p10–p90 (░), médiane colorée selon la
// perte (vert : 0 %, jaune : < 5 %, rouge au-delà), x pour une perte totale, rien sans mesure.
func SmokeChart(s *Screen, x, y, w, h int, pts []SmokePoint) {
	const axisW = 7
	pw := w - axisW
	if pw < 4 || h < 2 {
		return
	}
	cols := groupSmoke(pts, pw)
	var all []float64
	for _, p := range cols {
		if p.Recv > 0 {
			all = append(all, p.P90)
		}
	}
	mx := 1.0
	if len(all) > 0 {
		sort.Float64s(all)
		mx = all[len(all)*95/100] // l'échelle ignore les 5 % de pics extrêmes
	}
	top := niceTop(mx * 1.15)
	s.Text(x, y, fmt.Sprintf("%6s", axisLabel(top)), stDim, axisW-1)
	s.Text(x, y+h/2, fmt.Sprintf("%6s", axisLabel(top/2)), stDim, axisW-1)
	s.Text(x, y+h-1, fmt.Sprintf("%6s", "0"), stDim, axisW-1)
	s.Text(x, y+h, fmt.Sprintf("%6s", "ms"), stDim, axisW-1)
	for j := 0; j < h; j++ {
		s.Set(x+axisW-1, y+j, '┤', stGrid)
	}
	row := func(v float64) int { // rangée (0 = haut) d'une valeur
		r := h - 1 - int(math.Floor(v/top*float64(h)))
		return max(0, min(h-1, r))
	}
	for i, p := range cols {
		cx := x + axisW + i
		if p.Sent == 0 {
			continue
		}
		if p.Recv == 0 {
			s.Set(cx, y+h-1, 'x', stAlert)
			continue
		}
		for j := row(p.P90); j <= row(p.P10); j++ {
			s.Set(cx, y+j, '░', Style{Fg: White, Bg: Black})
		}
		loss := 1 - float64(p.Recv)/float64(p.Sent)
		st := stOK
		switch {
		case loss >= 0.05:
			st = stAlert
		case loss > 0:
			st = stWarn
		}
		ch := '■'
		s.Set(cx, y+row(p.Med), ch, st)
		if p.Med > top {
			s.Set(cx, y, '^', st)
		}
	}
}

// groupSmoke regroupe les points si plus nombreux que les colonnes.
func groupSmoke(pts []SmokePoint, cols int) []SmokePoint {
	if len(pts) <= cols {
		return pts
	}
	per := int(math.Ceil(float64(len(pts)) / float64(cols)))
	var out []SmokePoint
	for i := 0; i < len(pts); i += per {
		g := SmokePoint{}
		var meds []float64
		for _, p := range pts[i:min(len(pts), i+per)] {
			g.Sent += p.Sent
			g.Recv += p.Recv
			if p.Recv > 0 {
				meds = append(meds, p.Med)
				if g.P10 == 0 || p.P10 < g.P10 {
					g.P10 = p.P10
				}
				g.P90 = math.Max(g.P90, p.P90)
				g.Max = math.Max(g.Max, p.Max)
			}
		}
		if len(meds) > 0 {
			sort.Float64s(meds)
			g.Med = meds[len(meds)/2]
		}
		out = append(out, g)
	}
	return out
}

// ms formate une durée en ms (« 0,42 ms »).
func ms(v float64) string {
	if math.IsNaN(v) || v < 0 {
		return "—"
	}
	f := 2
	if v >= 100 {
		f = 0
	} else if v >= 10 {
		f = 1
	}
	return strings.Replace(strconv.FormatFloat(v, 'f', f, 64), ".", decimalSep(), 1) + " ms"
}

// num formate un entier avec des espaces de milliers.
func num(v float64) string {
	s := strconv.FormatInt(int64(math.Round(v)), 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 && c != '-' {
			b.WriteString(thousandsSep())
		}
		b.WriteRune(c)
	}
	return b.String()
}

// pct formate un pourcentage.
func pct(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", decimalSep(), 1) + " %"
}

// wrap coupe un texte en lignes de w colonnes au plus.
func wrap(text string, w int) []string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		if width(para) <= w {
			out = append(out, para) // tient : gardé tel quel (alignement des tableaux)
			continue
		}
		line := ""
		for _, word := range strings.Split(para, " ") { // l'espace insécable lie les chiffres
			if word == "" {
				continue
			}
			if line != "" && width(line)+1+width(word) > w {
				out = append(out, line)
				line = ""
			}
			if line != "" {
				line += " "
			}
			line += word
		}
		out = append(out, line)
	}
	return out
}

// Paragraph écrit un texte coupé ; renvoie le nombre de lignes.
func Paragraph(s *Screen, x, y, w int, text string, st Style) int {
	lines := wrap(T(text), w)
	for i, l := range lines {
		s.Raw(x, y+i, l, st, w)
	}
	return len(lines)
}
