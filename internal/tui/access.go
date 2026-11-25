package tui

import (
	"fmt"
	"strings"
)

// accessView : comment joindre l'appliance (mot de passe compris) et son état.
type accessView struct{}

func (v *accessView) Help() string           { return "" }
func (v *accessView) Key(a *App, k Key) bool { return false }
func (v *accessView) Enter(a *App)           { a.refreshInfo() }

func (v *accessView) Draw(a *App, s *Screen, x, y, w, h int) {
	ip := a.IP()
	row := y
	put := func(k, val string, st Style) {
		s.Text(x, row, k, stNormal, 15)
		s.Text(x+15, row, val, st, w-15)
		row++
	}
	if a.InfoErr != "" {
		put("Service vigie", "injoignable : "+a.InfoErr, stAlert)
		row++
	}
	put("Appliance", a.Info.Hostname+", vigie "+a.Info.Version, stBright)
	if ip == "" {
		put("Adresse IP", "en attente (DHCP)…", stWarn)
	} else {
		var all []string
		for _, i := range a.Local.Ifaces {
			if i.Name != "lo" && i.CIDR != "" {
				all = append(all, i.Name+" "+i.CIDR)
			}
		}
		put("Adresse IP", strings.Join(all, ", "), stBright)
		put("Interface web", "https://"+ip+"/", stAccent)
		put("SSH", "ssh root@"+ip, stNormal)
	}
	put("Mot de passe", a.Info.Password, stPass)
	row++
	fp := a.Info.TLS
	if fp != "" {
		row += Paragraph(s, x, row, w, "Certificat HTTPS de l'appliance (SHA-256), à comparer à celui que montre le navigateur :", stNormal)
		s.Text(x+2, row, fp[:min(len(fp), 47)], stBright, w-2)
		if len(fp) > 48 {
			s.Text(x+2, row+1, fp[48:], stBright, w-2)
		}
		row += 3
	}
	if a.Info.VSphere.Connected {
		put("vCenter", "connecté : "+a.Info.VSphere.Server.FullName, stOK)
	} else {
		put("vCenter", "non connecté (mode local : onglet Storage)", stNormal)
	}
	if j := a.Job; j != nil {
		put("Job en cours", fmt.Sprintf("%s, %.0f %% — %s", j.Profile, j.Progress, j.Step), stWarn)
	}
	row++
	Paragraph(s, x, row, w, "Le mot de passe sert à l'interface web et à root en SSH. En console, root est ouvert sans mot de passe : Alt+F2 (shell), Alt+F1 pour revenir. Adresse IP et mot de passe : menus Réseau (IP) et Mot de passe de l'onglet Système.", stDim)
}
