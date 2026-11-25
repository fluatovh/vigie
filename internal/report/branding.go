package report

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/png" // décodage de l'en-tête PNG du logo
	"io"
	"strings"
	"unicode/utf8"
)

// Branding est la personnalisation du rapport (stockée dans /data).
type Branding struct {
	Client  string `json:"client"`
	Site    string `json:"site"`
	Author  string `json:"author"`  // intervenant
	Comment string `json:"comment"` // commentaire libre en tête de rapport
	Logo    string `json:"logo"`    // data:image/png;base64,… ou data:image/svg+xml;base64,…
}

// MaxLogoBytes : taille maximale du logo décodé (§5.4).
const MaxLogoBytes = 200 << 10

// Validate contrôle les champs et le logo.
func (b *Branding) Validate() error {
	for _, f := range []struct {
		name, v string
		max     int
	}{{"client", b.Client, 120}, {"site", b.Site, 120}, {"intervenant", b.Author, 120}, {"commentaire", b.Comment, 4000}} {
		if !utf8.ValidString(f.v) {
			return fmt.Errorf("%s : texte invalide", f.name)
		}
		if utf8.RuneCountInString(f.v) > f.max {
			return fmt.Errorf("%s : %d caractères au maximum", f.name, f.max)
		}
	}
	b.Client, b.Site, b.Author = strings.TrimSpace(b.Client), strings.TrimSpace(b.Site), strings.TrimSpace(b.Author)
	if b.Logo == "" {
		return nil
	}
	logo, err := ValidateLogo(b.Logo)
	if err != nil {
		return err
	}
	b.Logo = logo
	return nil
}

// ValidateLogo accepte un PNG ou un SVG en data URI base64, 200 Kio au maximum. Un SVG qui
// contient quoi que ce soit d'actif (script, gestionnaire d'événement, objet étranger, référence
// externe, entité) est refusé : on ne « répare » pas un fichier douteux, on le rejette.
func ValidateLogo(uri string) (string, error) {
	mime, payload, ok := strings.Cut(strings.TrimPrefix(uri, "data:"), ";base64,")
	if !ok || !strings.HasPrefix(uri, "data:") {
		return "", errors.New("logo : data URI base64 attendue (PNG ou SVG)")
	}
	if base64.StdEncoding.DecodedLen(len(payload)) > MaxLogoBytes+3 {
		return "", errors.New("logo : 200 Kio au maximum")
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", errors.New("logo : base64 invalide")
	}
	if len(raw) > MaxLogoBytes {
		return "", errors.New("logo : 200 Kio au maximum")
	}
	switch mime {
	case "image/png":
		cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
		if err != nil || format != "png" {
			return "", errors.New("logo : PNG illisible")
		}
		if cfg.Width > 4000 || cfg.Height > 4000 {
			return "", errors.New("logo : 4000 × 4000 pixels au maximum")
		}
	case "image/svg+xml":
		if err := checkSVG(raw); err != nil {
			return "", fmt.Errorf("logo SVG refusé : %w", err)
		}
	default:
		return "", errors.New("logo : PNG ou SVG uniquement")
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

var svgForbidden = map[string]bool{"script": true, "foreignobject": true, "iframe": true, "embed": true, "object": true,
	"handler": true, "listener": true, "animate": true, "set": true, "animatemotion": true, "animatetransform": true}

func checkSVG(raw []byte) error {
	d := xml.NewDecoder(bytes.NewReader(raw))
	d.Strict = true
	root := true
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("XML invalide (%v)", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			if strings.Contains(strings.ToUpper(string(t)), "ENTITY") {
				return errors.New("déclaration d'entité")
			}
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if root {
				if name != "svg" {
					return errors.New("l'élément racine n'est pas <svg>")
				}
				root = false
			}
			if svgForbidden[name] {
				return fmt.Errorf("élément <%s> interdit", t.Name.Local)
			}
			for _, a := range t.Attr {
				an := strings.ToLower(a.Name.Local)
				v := strings.ToLower(strings.TrimSpace(a.Value))
				if strings.HasPrefix(an, "on") {
					return fmt.Errorf("attribut %s interdit", a.Name.Local)
				}
				if (an == "href" || an == "src") && !strings.HasPrefix(v, "#") && !strings.HasPrefix(v, "data:image/png") && !strings.HasPrefix(v, "data:image/jpeg") {
					return errors.New("référence externe interdite")
				}
				if strings.Contains(v, "javascript:") || (strings.Contains(v, "url(") && !strings.Contains(v, "url(#")) {
					return errors.New("référence externe ou script dans un attribut")
				}
			}
		case xml.CharData:
			v := strings.ToLower(string(t))
			if strings.Contains(v, "@import") || strings.Contains(v, "javascript:") {
				return errors.New("import ou script dans le contenu")
			}
		}
	}
	if root {
		return errors.New("document vide")
	}
	return nil
}

// templateURL : le logo n'atteint le gabarit qu'après ValidateLogo (data URI image contrôlée).
func templateURL(s string) string {
	if strings.HasPrefix(s, "data:image/png;base64,") || strings.HasPrefix(s, "data:image/svg+xml;base64,") {
		return s
	}
	return ""
}
