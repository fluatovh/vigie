package web

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Les textes français produits par le serveur s'affichent dans l'interface web, traduite par le
// catalogue web/i18n.tsv. Tout littéral Go en français doit donc y figurer (variables comprises),
// sinon il reste en français dans les autres langues.
// Exclus : le rapport client (français uniquement, documenté), la console (testée dans tui),
// les journaux système (log.*, préfixe « paquet: »), les fragments concaténés et le texte multiligne.
var (
	frText   = regexp.MustCompile(`[éèêàçùôî]|\b(le|la|les|des|du|une|sur|avec|sans|pas|aucun|est|disque|hôte|seuils|écriture|lecture)\b|Mo/s|Gio|Kio|Mio|\bGo\b| : `)
	goVerb   = regexp.MustCompile(`%(\[\d+\])?[-+# 0-9.]*[a-zA-Z]`)
	logTag   = regexp.MustCompile(`^[a-z]+: `)
	skipDirs = []string{"tui", "sysconf"}
)

func normVerbs(s string) string {
	return goVerb.ReplaceAllString(strings.ReplaceAll(s, "%%", "\x00"), "%s")
}

func TestServerTextsInCatalog(t *testing.T) {
	f, err := os.Open("../../web/i18n.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	keys := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		if l := sc.Text(); l != "" && !strings.HasPrefix(l, "#") {
			k, _, _ := strings.Cut(l, "\t")
			keys[normVerbs(k)] = true
		}
	}
	var missing []string
	err = filepath.Walk("..", func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			for _, d := range skipDirs {
				if fi.Name() == d {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		// rapport en français, sauf la validation de la personnalisation (erreurs affichées dans Réglages)
		if strings.Contains(p, "/report/") && !strings.HasSuffix(p, "/branding.go") {
			return nil
		}
		fs := token.NewFileSet()
		file, err := parser.ParseFile(fs, p, nil, 0)
		if err != nil {
			return err
		}
		skip := map[ast.Node]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if s, ok := c.Fun.(*ast.SelectorExpr); ok {
					if x, ok := s.X.(*ast.Ident); ok && x.Name == "log" {
						for _, a := range c.Args {
							skip[a] = true
						}
					}
				}
			}
			bl, ok := n.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING || skip[bl] {
				return true
			}
			v, err := strconv.Unquote(bl.Value)
			if err != nil || !frText.MatchString(v) || strings.Contains(v, "\n") || logTag.MatchString(v) {
				return true
			}
			if strings.TrimSpace(v) != v || strings.HasPrefix(v, ", ") { // fragment d'une concaténation
				return true
			}
			if !keys[normVerbs(v)] {
				missing = append(missing, fs.Position(bl.Pos()).String()+"\t"+v)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Errorf("%d texte(s) français absent(s) de web/i18n.tsv :\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
