package tui

import (
	"unicode"

	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	webui "vigie/web"
)

// Langues de la console (DCUI et TUI). Le texte source du code est en français ; il est traduit au
// moment de l'affichage (Screen.Text, Paragraph, Scroller, boîtes) à partir de web/i18n.tsv (commun
// à la console et à l'interface web) :
// une ligne par texte source, colonnes fr, en, es, it, pl, de. Les textes variables sont des
// gabarits (« %d/%d reçus ») : la chaîne affichée est reconnue, ses morceaux variables sont
// eux-mêmes traduits, puis replacés dans le gabarit de la langue. Les textes envoyés par le serveur
// (en français) passent par le même mécanisme.

// Langues proposées, dans l'ordre du menu ; l'anglais est la langue par défaut.
var Languages = []struct{ Code, Name string }{
	{"en", "English"}, {"fr", "Français"}, {"es", "Español"}, {"it", "Italiano"}, {"pl", "Polski"}, {"de", "Deutsch"},
}

// DefaultLang : langue par défaut de la console.
const DefaultLang = "en"

var catalogTSV = webui.Catalog

var col = map[string]int{"fr": 0, "en": 1, "es": 2, "it": 3, "pl": 4, "de": 5}

type pattern struct {
	re     *regexp.Regexp
	prefix string // texte littéral avant la première variable (filtre rapide)
	lit    int    // longueur du texte fixe : départage les gabarits de même préfixe
	out    [6]string
}

type catalogT struct {
	exact    map[string][6]string
	formats  map[string][6]string // gabarits bruts (Tf), variables d'origine conservées
	patterns []pattern
}

var (
	cat      = loadCatalog(catalogTSV)
	langMu   sync.Mutex
	lang     = DefaultLang
	cache    = map[string]string{}
	verbRe   = regexp.MustCompile(`%(\[\d+\])?[-+# 0-9.]*[a-zA-Z]`)
	missing  map[string]bool // tests : textes affichés sans traduction
	tracking bool
)

func loadCatalog(tsv string) *catalogT {
	c := &catalogT{exact: map[string][6]string{}, formats: map[string][6]string{}}
	for n, line := range strings.Split(tsv, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 6 {
			panic(fmt.Sprintf("catalog.tsv ligne %d : %d colonnes au lieu de 6", n+1, len(f)))
		}
		var row [6]string
		copy(row[:], f)
		c.formats[row[0]] = row
		if !verbRe.MatchString(strings.ReplaceAll(row[0], "%%", "")) {
			for i := range row {
				row[i] = strings.ReplaceAll(row[i], "%%", "%")
			}
			c.exact[row[0]] = row
			continue
		}
		// gabarit : variables → groupes de capture ; sorties normalisées en %s
		var re strings.Builder
		re.WriteString("^")
		src := strings.ReplaceAll(row[0], "%%", "\x00") // « %% sur » n'est pas un verbe « % s »
		prefix, first := "", true
		for {
			loc := verbRe.FindStringIndex(src)
			lit := src
			if loc != nil {
				lit = src[:loc[0]]
			}
			lit = strings.ReplaceAll(lit, "\x00", "%")
			if first {
				prefix, first = lit, false
			}
			re.WriteString(regexp.QuoteMeta(lit))
			if loc == nil {
				break
			}
			re.WriteString("(.+?)")
			src = src[loc[1]:]
		}
		re.WriteString("$")
		p := pattern{re: regexp.MustCompile(re.String()), prefix: prefix, lit: len(verbRe.ReplaceAllString(strings.ReplaceAll(row[0], "%%", "%"), ""))}
		for i, s := range row {
			p.out[i] = strings.ReplaceAll(verbRe.ReplaceAllStringFunc(strings.ReplaceAll(s, "%%", "\x00"), func(v string) string {
				if m := regexp.MustCompile(`^%(\[\d+\])`).FindStringSubmatch(v); m != nil {
					return "%" + m[1] + "s"
				}
				return "%s"
			}), "\x00", "%%")
		}
		c.patterns = append(c.patterns, p)
	}
	// du plus précis au plus général : « Profil écriture : %s » avant « %s %s, bloc… »
	// à préfixe égal, le texte fixe le plus long (le gabarit le plus précis) d'abord
	sort.SliceStable(c.patterns, func(i, j int) bool {
		pi, pj := c.patterns[i], c.patterns[j]
		if len(pi.prefix) != len(pj.prefix) {
			return len(pi.prefix) > len(pj.prefix)
		}
		return pi.lit > pj.lit
	})
	return c
}

// SetLang change la langue de la console ("" ou inconnue : anglais).
func SetLang(code string) {
	if _, ok := col[code]; !ok {
		code = DefaultLang
	}
	langMu.Lock()
	lang, cache = code, map[string]string{}
	langMu.Unlock()
}

// Lang renvoie la langue courante.
func Lang() string {
	langMu.Lock()
	defer langMu.Unlock()
	return lang
}

// T traduit un texte (source française) dans la langue courante ; inconnu : inchangé.
func T(s string) string {
	if s == "" {
		return s
	}
	langMu.Lock()
	l := lang
	if v, ok := cache[s]; ok {
		langMu.Unlock()
		return v
	}
	langMu.Unlock()
	out := translate(s, col[l], 0)
	langMu.Lock()
	if len(cache) > 20000 {
		cache = map[string]string{}
	}
	cache[s] = out
	langMu.Unlock()
	return out
}

// Tf traduit un gabarit puis le remplit (les traductions gardent les mêmes variables, dans le même
// ordre ou indexées %[n]). Gabarit inconnu : formaté puis traduit comme un texte ordinaire.
func Tf(format string, a ...any) string {
	if row, ok := cat.formats[format]; ok {
		return fmt.Sprintf(row[col[Lang()]], a...)
	}
	return T(fmt.Sprintf(format, a...))
}

func translate(s string, c, depth int) string {
	// texte exact entouré d'espaces : avant les gabarits génériques (« %s : %s »)
	if trimmed := strings.TrimSpace(s); trimmed != s && trimmed != "" {
		if row, ok := cat.exact[trimmed]; ok {
			i := strings.Index(s, trimmed)
			return s[:i] + row[c] + s[i+len(trimmed):]
		}
	}
	if out, ok := lookup(s, c, depth); ok {
		return out
	}
	// espaces de tête et de fin conservés (alignements, boutons)
	if trimmed := strings.TrimSpace(s); trimmed != s && trimmed != "" {
		if out, ok := lookup(trimmed, c, depth); ok {
			i := strings.Index(s, trimmed)
			return s[:i] + out + s[i+len(trimmed):]
		}
	}
	if tracking && depth == 0 && strings.IndexFunc(s, unicode.IsLetter) >= 0 {
		langMu.Lock()
		missing[s] = true
		langMu.Unlock()
	}
	return s
}

func lookup(s string, c, depth int) (string, bool) {
	if row, ok := cat.exact[s]; ok {
		return row[c], true
	}
	if depth >= 6 { // gabarits imbriqués : verdict > indicateur > valeur
		return "", false
	}
	for _, p := range cat.patterns {
		if !strings.HasPrefix(s, p.prefix) {
			continue
		}
		m := p.re.FindStringSubmatch(s)
		if m == nil {
			continue
		}
		args := make([]any, len(m)-1)
		for i, v := range m[1:] {
			args[i] = translate(v, c, depth+1)
		}
		return fmt.Sprintf(p.out[c], args...), true
	}
	return "", false
}

// needsTranslation : heuristique des tests (texte avec un mot français de 4 lettres ou plus).
var frWord = regexp.MustCompile(`(?i)\b(le|la|les|des|du|une?|et|ou|pour|avec|sans|dans|sur|par|est|sont|aucun|aucune)\b|[éèêàùçô]`)

func needsTranslation(s string) bool { return frWord.MatchString(s) }

// sépareurs numériques de la langue courante
func decimalSep() string {
	if Lang() == "en" {
		return "."
	}
	return ","
}

func thousandsSep() string {
	switch Lang() {
	case "en":
		return ","
	case "de", "es", "it":
		return "."
	}
	return " "
}
