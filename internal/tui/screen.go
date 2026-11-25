// Package tui est l'interface texte de la console de l'appliance (tty1, phase T) : entièrement au
// clavier, 80 × 25 caractères (mode texte VGA de la console VMware, 720 × 400 pixels), sans
// dépendance externe. Elle parle à l'API du serveur par le socket Unix de la console.
package tui

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Couleurs ANSI (8 couleurs de la console Linux ; Bold éclaircit l'avant-plan).
type Color uint8

const (
	Black Color = iota
	Red
	Green
	Yellow
	Blue
	Magenta
	Cyan
	White
	Default Color = 9
)

// Style d'une cellule.
type Style struct {
	Fg, Bg Color
	Bold   bool
}

var (
	stNormal = Style{Fg: White, Bg: Black}
	stDim    = Style{Fg: White, Bg: Black} // la console n'a pas d'atténué : texte normal
	stBright = Style{Fg: White, Bg: Black, Bold: true}
	stTitle  = Style{Fg: Black, Bg: Cyan}
	stTabOn  = Style{Fg: White, Bg: Blue, Bold: true}
	stTabOff = Style{Fg: White, Bg: Black}
	stSel    = Style{Fg: Black, Bg: White}
	stSelOff = Style{Fg: White, Bg: Blue}
	stOK     = Style{Fg: Green, Bg: Black, Bold: true}
	stWarn   = Style{Fg: Yellow, Bg: Black, Bold: true}
	stAlert  = Style{Fg: Red, Bg: Black, Bold: true}
	stAccent = Style{Fg: Cyan, Bg: Black, Bold: true}
	stInput  = Style{Fg: White, Bg: Blue, Bold: true}
	stHelp   = Style{Fg: Black, Bg: White}
	stKey    = Style{Fg: Blue, Bg: White, Bold: false}
	stChart  = Style{Fg: Cyan, Bg: Black, Bold: true}
	stChart2 = Style{Fg: Yellow, Bg: Black, Bold: true}
	stGrid   = Style{Fg: Blue, Bg: Black, Bold: true}
	stPass   = Style{Fg: Yellow, Bg: Cyan, Bold: true}
)

// Cell est un caractère à l'écran.
type Cell struct {
	R rune
	S Style
}

// Screen est un tampon W × H ; Flush n'envoie que les cellules modifiées.
type Screen struct {
	W, H  int
	cells []Cell
	prev  []Cell
	full  bool
}

// NewScreen crée un écran vide.
func NewScreen(w, h int) *Screen {
	s := &Screen{W: w, H: h, cells: make([]Cell, w*h), prev: make([]Cell, w*h), full: true}
	s.Clear()
	return s
}

// Clear efface le tampon.
func (s *Screen) Clear() {
	for i := range s.cells {
		s.cells[i] = Cell{' ', stNormal}
	}
}

// Invalidate force un rafraîchissement complet au prochain Flush.
func (s *Screen) Invalidate() { s.full = true }

// Set écrit une cellule (hors écran : ignorée).
func (s *Screen) Set(x, y int, r rune, st Style) {
	if x < 0 || y < 0 || x >= s.W || y >= s.H {
		return
	}
	s.cells[y*s.W+x] = Cell{vga(r), st}
}

// Text écrit une chaîne traduite dans la langue de la console (voir i18n.go) à partir de (x, y),
// tronquée à max colonnes (max <= 0 : jusqu'au bord). Renvoie le nombre de colonnes écrites.
func (s *Screen) Text(x, y int, str string, st Style, max int) int {
	return s.Raw(x, y, T(str), st, max)
}

// Raw écrit une chaîne telle quelle (saisies, mots de passe, adresses, texte déjà traduit).
func (s *Screen) Raw(x, y int, str string, st Style, max int) int {
	if max <= 0 {
		max = s.W - x
	}
	n := 0
	for _, r := range str {
		if n >= max {
			break
		}
		for _, rr := range expand(r) {
			if n >= max {
				break
			}
			s.Set(x+n, y, rr, st)
			n++
		}
	}
	return n
}

// Fill remplit un rectangle.
func (s *Screen) Fill(x, y, w, h int, r rune, st Style) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			s.Set(i, j, r, st)
		}
	}
}

// Box trace un cadre simple avec un titre.
func (s *Screen) Box(x, y, w, h int, title string, st Style) {
	if w < 2 || h < 2 {
		return
	}
	for i := x + 1; i < x+w-1; i++ {
		s.Set(i, y, '─', st)
		s.Set(i, y+h-1, '─', st)
	}
	for j := y + 1; j < y+h-1; j++ {
		s.Set(x, j, '│', st)
		s.Set(x+w-1, j, '│', st)
	}
	s.Set(x, y, '┌', st)
	s.Set(x+w-1, y, '┐', st)
	s.Set(x, y+h-1, '└', st)
	s.Set(x+w-1, y+h-1, '┘', st)
	if title != "" {
		s.Raw(x+2, y, " "+T(title)+" ", st, w-4)
	}
}

// String renvoie le contenu (tests) : une ligne par rangée, espaces de fin retirés.
func (s *Screen) String() string {
	var b strings.Builder
	for y := 0; y < s.H; y++ {
		var line strings.Builder
		for x := 0; x < s.W; x++ {
			line.WriteRune(s.cells[y*s.W+x].R)
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// At renvoie une cellule (tests).
func (s *Screen) At(x, y int) Cell { return s.cells[y*s.W+x] }

func sgr(st Style) string {
	b := "0"
	if st.Bold {
		b = "1"
	}
	return fmt.Sprintf("\x1b[%s;%d;%dm", b, 30+int(st.Fg), 40+int(st.Bg))
}

// Flush envoie les différences au terminal.
func (s *Screen) Flush(w io.Writer) error {
	var b bytes.Buffer
	b.WriteString("\x1b[?25l")
	if s.full {
		b.WriteString("\x1b[0m\x1b[2J")
	}
	cur := Style{Fg: 255}
	lastX, lastY := -1, -1
	for y := 0; y < s.H; y++ {
		for x := 0; x < s.W; x++ {
			i := y*s.W + x
			c := s.cells[i]
			if !s.full && c == s.prev[i] {
				continue
			}
			if y == s.H-1 && x == s.W-1 {
				continue // la dernière cellule ferait défiler l'écran
			}
			if x != lastX+1 || y != lastY {
				fmt.Fprintf(&b, "\x1b[%d;%dH", y+1, x+1)
			}
			if c.S != cur {
				b.WriteString(sgr(c.S))
				cur = c.S
			}
			var buf [4]byte
			n := utf8.EncodeRune(buf[:], c.R)
			b.Write(buf[:n])
			lastX, lastY = x, y
			s.prev[i] = c
		}
	}
	s.full = false
	_, err := w.Write(b.Bytes())
	return err
}

// ---------- jeu de caractères ----------
// La police VGA de la console ne contient que les glyphes de la page de codes 437 : on remplace
// tout le reste par un équivalent lisible (É existe, À et Ê non ; pas de flèches ni de tirets longs).

var cp437 = func() map[rune]bool {
	m := map[rune]bool{}
	for _, r := range "ÇüéâäàåçêëèïîìÄÅÉæÆôöòûùÿÖÜ¢£¥ƒáíóúñÑªº¿⌐¬½¼¡«»░▒▓│┤╡╢╖╕╣║╗╝╜╛┐└┴┬├─┼╞╟╚╔╩╦╠═╬╧╨╤╥╙╘╒╓╫╪┘┌█▄▌▐▀αßΓπΣσµτΦΘΩδ∞φε∩≡±≥≤⌠⌡÷≈°∙·√ⁿ²■" {
		m[r] = true
	}
	return m
}()

var translit = map[rune]string{
	'À': "A", 'Â': "A", 'È': "E", 'Ê': "E", 'Ë': "E", 'Î': "I", 'Ï': "I", 'Ô': "O", 'Ù': "U", 'Û': "U", 'Œ': "OE", 'œ': "oe",
	'—': "-", '–': "-", '…': "...", '→': "->", '←': "<-", '↑': "^", '↓': "v", '’': "'", '‘': "'", '“': "\"", '”': "\"",
	' ': " ", ' ': " ", '✔': "+", '✘': "x", '×': "x", '−': "-", '⁶': "6", '¹': "1", '⁰': "0", '•': "·",
	// polonais et majuscules absentes de la police VGA (quand eurlatgr n'est pas chargée)
	'ą': "a", 'ć': "c", 'ę': "e", 'ł': "l", 'ń': "n", 'ś': "s", 'ź': "z", 'ż': "z", 'Ą': "A", 'Ć': "C", 'Ę': "E", 'Ł': "L",
	'Ń': "N", 'Ó': "O", 'Ś': "S", 'Ź': "Z", 'Ż': "Z", 'Á': "A", 'Í': "I", 'Ú': "U", 'Ì': "I", 'Ò': "O", '„': "\"", '≥': ">=", '≤': "<=",
}

func expand(r rune) []rune {
	if supported(r) {
		return []rune{r}
	}
	if t, ok := translit[r]; ok {
		return []rune(t)
	}
	return []rune{'?'}
}

func vga(r rune) rune {
	if r < 0x20 {
		return ' '
	}
	if supported(r) {
		return r
	}
	if t, ok := translit[r]; ok && len([]rune(t)) == 1 {
		return []rune(t)[0]
	}
	return '?'
}

// width : largeur à l'écran d'une chaîne (après translittération).
func width(s string) int {
	n := 0
	for _, r := range s {
		n += len(expand(r))
	}
	return n
}
