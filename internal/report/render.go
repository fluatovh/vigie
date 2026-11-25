package report

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"time"

	webui "vigie/web"
)

//go:embed assets/report.css assets/report.js
var assets embed.FS

// CSP renvoie la politique du rapport : rien d'autre que le contenu en ligne marqué du nonce et
// les images en data URI. default-src 'none' garantit qu'aucune requête réseau ne part du fichier.
func CSP(nonce string) string {
	return "default-src 'none'; script-src 'nonce-" + nonce + "'; style-src 'nonce-" + nonce + "'; img-src data:; " +
		"base-uri 'none'; form-action 'none'"
}

func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawStdEncoding.EncodeToString(b)
}

func read(fsys fs.FS, name string) (string, error) {
	b, err := fs.ReadFile(fsys, name)
	return string(b), err
}

// esc échappe un texte pour le contenu HTML et les valeurs d'attribut entre guillemets.
func esc(s string) string { return html.EscapeString(s) }

// inScript neutralise toute fermeture de balise dans un script en ligne.
func inScript(s string) string { return strings.ReplaceAll(s, "</", `<\/`) }

// Render écrit le rapport HTML autonome et renvoie le nonce utilisé (pour l'en-tête CSP).
// Tout texte venant des données passe par esc ; seuls les SVG de charts.go (déjà échappés), le CSS,
// le JS embarqués et le JSON des graphs (json.Marshal échappe <, > et &) sont insérés tels quels.
func Render(w io.Writer, p *Page) (string, error) {
	nonce := newNonce()
	css, err := read(assets, "assets/report.css")
	if err != nil {
		return "", err
	}
	ucss, err := read(webui.FS, "vendor/uPlot.min.css")
	if err != nil {
		return "", err
	}
	js, err := read(assets, "assets/report.js")
	if err != nil {
		return "", err
	}
	ujs, err := read(webui.FS, "vendor/uPlot.iife.min.js")
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(p.Charts)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	pf := func(f string, a ...any) { fmt.Fprintf(&b, f, a...) }
	n := esc(nonce)
	pf(`<!doctype html>
<html lang="fr">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="%s">
<meta name="generator" content="vigie %s">
<title>%s</title>
<style nonce="%s">%s
%s</style>
</head>
<body class="kind-%s">
<div class="toolbar noprint"><span>Rapport autonome : il s'ouvre hors ligne et s'imprime en A4 (« Enregistrer au format PDF »).</span><button id="print" type="button">Imprimer / PDF</button></div>
`, esc(CSP(nonce)), esc(p.Version), esc(p.Title), n, inScript(ucss), inScript(css), esc(p.Kind))
	client := "Rapport de performance"
	if p.Brand.Client != "" {
		client = p.Brand.Client
	}
	if p.Brand.Site != "" {
		client += " — " + p.Brand.Site
	}
	var jobs []string
	for _, id := range p.JobIDs {
		jobs = append(jobs, "job "+id)
	}
	for i, s := range p.Sections {
		pf(`<section class="page" id="%s">
<header class="pagehead"><div class="brand">`, esc(s.ID))
		if logo := templateURL(p.LogoURL); logo != "" {
			pf(`<img class="logo" src="%s" alt="logo">`, esc(logo))
		}
		pf(`<div><div class="client">%s</div><div class="subject">%s</div></div></div><div class="pageno">%d / %d</div></header>
<h2>%s</h2>
`, esc(client), esc(p.Subject), i+1, len(p.Sections), esc(s.Title))
		if i == 0 {
			pf(`<div class="meta"><div><b>Mesure :</b> %s</div>`, esc(p.RunDate))
			if p.Brand.Author != "" {
				pf(`<div><b>Intervenant :</b> %s</div>`, esc(p.Brand.Author))
			}
			pf(`<div><b>Rapport généré le</b> %s par vigie %s</div></div>
`, esc(p.Generated), esc(p.Version))
			for _, c := range p.Comment {
				pf("<p class=\"comment\">%s</p>\n", esc(c))
			}
		}
		if v := s.Verdict; v != nil {
			pf("<div class=\"verdict %s\"><span class=\"lvl\">%s</span><span>%s</span></div>\n", esc(v.Class), esc(v.Level), esc(v.Text))
		}
		if len(s.Tiles) > 0 {
			b.WriteString(`<div class="tiles">`)
			for _, t := range s.Tiles {
				pf(`<div class="tile"><div class="v">%s</div><div class="l">%s</div>`, esc(t.Value), esc(t.Label))
				if t.Delta != "" {
					pf(`<div class="delta %s">%s</div>`, esc(t.DeltaClass), esc(t.Delta))
				}
				pf(`<div class="n">%s</div></div>`, esc(t.Note))
			}
			b.WriteString("</div>\n")
		}
		for _, l := range s.Lead {
			pf("<p class=\"lead\">%s</p>\n", esc(l))
		}
		for _, bl := range s.Blocks {
			renderBlock(&b, bl)
		}
		pf("<footer class=\"pagefoot\">vigie %s · %s · Mo/s = 10⁶ octets/s</footer>\n</section>\n", esc(p.Version), esc(strings.Join(jobs, ", ")))
	}
	pf(`<script type="application/json" id="vigie-data">%s</script>
<script nonce="%s">/* uPlot 1.6.32 — licence MIT, (c) Leon Sorokin, https://github.com/leeoniya/uPlot */
%s</script>
<script nonce="%s">%s</script>
</body>
</html>
`, inScript(string(data)), n, inScript(ujs), n, inScript(js))
	_, err = w.Write(b.Bytes())
	return nonce, err
}

func renderBlock(b *bytes.Buffer, bl Block) {
	switch bl.Kind {
	case "h3":
		fmt.Fprintf(b, "<h3>%s</h3>\n", esc(bl.Text))
	case "text":
		fmt.Fprintf(b, "<p>%s</p>\n", esc(bl.Text))
	case "note":
		fmt.Fprintf(b, "<p class=\"note\">%s</p>\n", esc(bl.Text))
	case "chart":
		fmt.Fprintf(b, "<div class=\"chart\" data-chart=\"%s\"></div>\n", esc(bl.ChartID))
	case "svg":
		fmt.Fprintf(b, "<div class=\"svgchart\">%s</div>\n", string(bl.SVG))
	case "list":
		b.WriteString("<ul>")
		for _, l := range bl.List {
			fmt.Fprintf(b, "<li>%s</li>", esc(l))
		}
		b.WriteString("</ul>\n")
	case "kv":
		b.WriteString(`<dl class="kv">`)
		for _, kv := range bl.KV {
			fmt.Fprintf(b, "<dt>%s</dt><dd>%s</dd>", esc(kv.K), esc(kv.V))
		}
		b.WriteString("</dl>\n")
	case "table":
		t := bl.Table
		if t == nil {
			return
		}
		num := func(i int) bool { return i < len(t.Num) && t.Num[i] }
		b.WriteString("<table><thead><tr>")
		for i, h := range t.Head {
			if num(i) {
				fmt.Fprintf(b, `<th class="r">%s</th>`, esc(h))
			} else {
				fmt.Fprintf(b, "<th>%s</th>", esc(h))
			}
		}
		b.WriteString("</tr></thead><tbody>")
		for _, row := range t.Rows {
			b.WriteString("<tr>")
			for i, c := range row {
				cls := c.Class
				if num(i) {
					cls = strings.TrimSpace(cls + " r")
				}
				if cls != "" {
					fmt.Fprintf(b, `<td class="%s">%s</td>`, esc(cls), esc(c.Text))
				} else {
					fmt.Fprintf(b, "<td>%s</td>", esc(c.Text))
				}
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</tbody></table>\n")
	}
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// FileName propose un nom de fichier pour le téléchargement.
func FileName(p *Page, at time.Time) string {
	ds := ""
	if i := strings.LastIndex(p.Title, "datastore "); i >= 0 {
		ds = p.Title[i+len("datastore "):]
	}
	kind := "rapport"
	if p.Kind == "compare" {
		kind = "avant-apres"
	}
	n := fmt.Sprintf("vigie-%s-%s-%s.html", kind, ds, at.UTC().Format("20060102-1504"))
	return strings.Trim(unsafeName.ReplaceAllString(n, "_"), "_")
}
