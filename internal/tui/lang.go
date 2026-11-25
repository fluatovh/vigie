package tui

// Écran Système > Langue : la langue de la console, enregistrée par le serveur dans /data.

type langView struct {
	list List
}

func (v *langView) Help() string { return "haut/bas choisir  Entrée appliquer" }

func (v *langView) Enter(a *App) {
	for i, l := range Languages {
		if l.Code == Lang() {
			v.list.Sel = i
		}
	}
}

func (v *langView) Key(a *App, k Key) bool {
	if v.list.Key(k, len(Languages), len(Languages)) {
		return true
	}
	if k.Name != "Enter" {
		return false
	}
	code := Languages[v.list.Sel].Code
	a.Go(func() (any, error) {
		return nil, a.C.Do2("PUT", "/system/language", map[string]string{"language": code})
	}, func(_ any, err error) {
		if err != nil {
			a.Error(err)
			return
		}
		a.setLanguage(code)
		a.Status("Langue appliquée et enregistrée.", stOK)
	})
	return true
}

func (v *langView) Draw(a *App, s *Screen, x, y, w, h int) {
	s.Text(x, y, "Langue de la console", stAccent, w)
	for i, l := range Languages {
		st := stNormal
		mark := "  "
		if l.Code == Lang() {
			mark = "* "
		}
		if i == v.list.Sel && a.Focus {
			st = stSel
			s.Fill(x, y+2+i, 24, 1, ' ', st)
		}
		s.Raw(x, y+2+i, mark+l.Name, st, 24)
	}
	Paragraph(s, x, y+3+len(Languages), w, "La langue s'applique à la console (accueil et menus). Elle est conservée dans /data.", stDim)
}

// setLanguage change la langue, recharge la police si besoin et redessine tout.
func (a *App) setLanguage(code string) {
	SetLang(code)
	if a.Font != nil {
		a.Font(code)
	}
	a.Scr.Invalidate()
}
