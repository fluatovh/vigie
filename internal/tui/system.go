package tui

import (
	"fmt"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"vigie/internal/netdiag"
	"vigie/internal/sysconf"
)

// Onglet Système (phase D) : accès, réseau de gestion, test du réseau, mot de passe root, shell
// root, arrêt et redémarrage. Les modifications passent par /api/system/*, réservé à la console.

func systemItems() []Item {
	return []Item{
		{"Accès et état", &accessView{}},
		{"Réseau (IP)", newNetConfView()},
		{"Tester le réseau", newNetTestView()},
		{"Mot de passe", newPasswordView()},
		{"Shell root", &shellView{}},
		{"Langue", &langView{}},
		{"Alimentation", &powerView{}},
	}
}

type sysNetwork struct {
	Saved    *sysconf.Network `json:"saved"`
	Source   string           `json:"source"` // dhcp, ovf, console
	Hostname string           `json:"hostname"`
	Current  netdiag.Local    `json:"current"`
}

func (a *App) loadSysNet(done func(*sysNetwork)) {
	a.Go(func() (any, error) {
		var n sysNetwork
		err := a.C.Get("/system/network", &n)
		return &n, err
	}, func(v any, err error) {
		if err != nil {
			a.Error(err)
			return
		}
		a.SysNet = v.(*sysNetwork)
		if done != nil {
			done(a.SysNet)
		}
	})
}

func sourceFR(s string) string {
	switch s {
	case "console":
		return "configuration choisie dans cette console"
	case "ovf":
		return "propriétés OVF du déploiement"
	}
	return "DHCP (par défaut)"
}

// currentIPv4 : adresse et préfixe d'eth0.
func currentIPv4(l netdiag.Local) (string, int) {
	for _, i := range l.Ifaces {
		if i.Name == "lo" || i.CIDR == "" {
			continue
		}
		if p, err := netip.ParsePrefix(i.CIDR); err == nil && p.Addr().Is4() {
			return p.Addr().String(), p.Bits()
		}
	}
	return "", 24
}

func ipRunes(r rune) bool { return (r >= '0' && r <= '9') || r == '.' || r == '/' }
func ipListRunes(r rune) bool {
	return ipRunes(r) || r == ',' || r == ' ' || r == ':' || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// ---------- réseau de gestion ----------

type netConfView struct {
	form   Form
	loaded bool
	msg    string
}

func newNetConfView() *netConfView {
	return &netConfView{form: Form{LabelW: 14, Button: "appliquer la configuration", Fields: []*Field{
		{Label: "Mode", Choices: []string{"DHCP", "Statique"}},
		{Label: "Adresse IP", Max: 15, Accept: ipRunes},
		{Label: "Masque", Max: 15, Accept: ipRunes, Hint: "255.255.255.0 ou 24"},
		{Label: "Passerelle", Max: 15, Accept: ipRunes},
		{Label: "DNS", Max: 47, Accept: ipListRunes, Hint: "séparés par des virgules"},
		{Label: "Nom d'hôte", Max: 63, Accept: func(r rune) bool {
			return r < 0x80 && (r == '-' || (r >= '0' && r <= '9') || (r|0x20 >= 'a' && r|0x20 <= 'z'))
		}},
	}}}
}

func (v *netConfView) Help() string { return "Tab champ  flèches choix  Entrée appliquer" }

func (v *netConfView) Enter(a *App) {
	a.loadSysNet(func(n *sysNetwork) {
		if v.loaded {
			return
		}
		v.loaded = true
		f := v.form.Fields
		ip, pfx := currentIPv4(n.Current)
		f[0].Idx = 0
		if n.Source == "ovf" || (n.Saved != nil && n.Saved.Mode == "static") {
			f[0].Idx = 1
		}
		f[1].Value, f[2].Value, f[3].Value = ip, sysconf.Mask(pfx), n.Current.Gateway
		f[4].Value, f[5].Value = strings.Join(n.Current.DNS, ", "), n.Hostname
		if s := n.Saved; s != nil && s.Mode == "static" {
			f[1].Value, f[2].Value, f[3].Value, f[4].Value = s.Address, sysconf.Mask(s.Prefix), s.Gateway, strings.Join(s.DNS, ", ")
		}
	})
}

func (v *netConfView) build() (sysconf.Network, error) {
	f := v.form.Fields
	n := sysconf.Network{Mode: "dhcp", Hostname: f[5].Get()}
	if f[0].Idx == 1 {
		pfx, err := sysconf.ParseMask(f[2].Get())
		if err != nil {
			return n, err
		}
		n.Mode, n.Address, n.Prefix, n.Gateway = "static", f[1].Get(), pfx, f[3].Get()
		for _, d := range strings.FieldsFunc(f[4].Get(), func(r rune) bool { return r == ',' || r == ' ' }) {
			n.DNS = append(n.DNS, d)
		}
	}
	return n, n.Validate()
}

func (v *netConfView) Key(a *App, k Key) bool {
	sub, used := v.form.Key(k)
	if !sub {
		return used
	}
	n, err := v.build()
	if err != nil {
		v.msg = err.Error()
		a.Error(err)
		return true
	}
	v.msg = ""
	desc := "DHCP"
	if n.Mode == "static" {
		desc = fmt.Sprintf("statique %s/%d, passerelle %s, DNS %s", n.Address, n.Prefix, orNone(n.Gateway), orNone(strings.Join(n.DNS, " ")))
	}
	a.Confirm("Réseau de gestion", []string{"Appliquer : " + desc, "Nom d'hôte : " + orNone(n.Hostname),
		"Le réseau redémarre ; si l'adresse change, les sessions web", "et SSH en cours sont coupées. Conservé au redémarrage."}, func() {
		a.Status("Application de la configuration réseau…", stAccent)
		a.Go(func() (any, error) { return nil, a.C.Do2("PUT", "/system/network", n) }, func(_ any, err error) {
			if err != nil {
				a.Error(err)
				return
			}
			a.Status("Configuration réseau appliquée et enregistrée.", stOK)
			a.refreshInfo()
			a.loadSysNet(nil)
		})
	})
	return true
}

func orNone(s string) string {
	if s == "" {
		return "aucun"
	}
	return s
}

func (v *netConfView) Draw(a *App, s *Screen, x, y, w, h int) {
	ip, pfx := currentIPv4(a.Local)
	cur := "aucune adresse"
	if ip != "" {
		cur = fmt.Sprintf("%s/%d", ip, pfx)
	}
	src := ""
	if a.SysNet != nil {
		src = sourceFR(a.SysNet.Source)
	}
	s.Text(x, y, "Actuel : "+cur+", passerelle "+orNone(a.Local.Gateway), stAccent, w)
	s.Text(x, y+1, "DNS "+orNone(strings.Join(a.Local.DNS, " "))+" ; origine : "+src, stNormal, w)
	n := v.form.Draw(s, x, y+3, w, a.Focus)
	row := y + 4 + n
	if v.form.Fields[0].Idx == 0 {
		row += Paragraph(s, x, row, w, "En DHCP, adresse, masque, passerelle et DNS sont fournis par le serveur DHCP ; seul le nom d'hôte est pris en compte.", stDim)
	}
	if v.msg != "" {
		row += Paragraph(s, x, row, w, v.msg, stAlert)
	}
	Paragraph(s, x, row+1, w, "La configuration choisie ici est conservée dans /data et prime au démarrage sur les propriétés OVF du déploiement.", stDim)
}

// ---------- test du réseau (façon « Test Management Network ») ----------

type netTestView struct {
	form    Form
	results []string
	styles  []Style
	running bool
}

func newNetTestView() *netTestView {
	return &netTestView{form: Form{LabelW: 16, Button: "lancer le test", Fields: []*Field{
		{Label: "Adresses", Max: 60, Accept: ipListRunes, Hint: "passerelle et DNS par défaut"},
		{Label: "Nom à résoudre", Max: 60, Accept: hostRunes, Hint: "facultatif"},
	}}}
}

func (v *netTestView) Help() string { return "Tab champ  Entrée tester" }

func (v *netTestView) Enter(a *App) {
	if v.form.Fields[0].Value == "" {
		var t []string
		if a.Local.Gateway != "" {
			t = append(t, a.Local.Gateway)
		}
		t = append(t, a.Local.DNS...)
		v.form.Fields[0].Value = strings.Join(t, ", ")
	}
}

func (v *netTestView) Key(a *App, k Key) bool {
	sub, used := v.form.Key(k)
	if !sub || v.running {
		return used
	}
	targets := strings.FieldsFunc(v.form.Fields[0].Get(), func(r rune) bool { return r == ',' || r == ' ' })
	if name := v.form.Fields[1].Get(); name != "" {
		targets = append(targets, name)
	}
	if len(targets) == 0 {
		a.Status("Aucune adresse à tester.", stWarn)
		return true
	}
	v.results, v.styles, v.running = nil, nil, true
	for _, t := range targets {
		t := t
		v.results = append(v.results, fmt.Sprintf("%-28s en cours…", t))
		v.styles = append(v.styles, stDim)
	}
	a.Go(func() (any, error) {
		type res struct {
			line string
			ok   bool
		}
		var out []res
		for _, t := range targets {
			var r struct {
				IP    string        `json:"ip"`
				Stats netdiag.Stats `json:"stats"`
			}
			err := a.C.Post("/net/ping", map[string]any{"target": t, "count": 3, "interval_ms": 300}, &r)
			switch {
			case err != nil:
				out = append(out, res{fmt.Sprintf("%-28s ÉCHEC : %s", t, err), false})
			case r.Stats.Recv == 0:
				out = append(out, res{fmt.Sprintf("%-28s ÉCHEC : aucune réponse (%s)", t, r.IP), false})
			default:
				name := t
				if r.IP != t {
					name = t + " = " + r.IP
				}
				out = append(out, res{fmt.Sprintf("%-28s OK %d/%d, %s", name, r.Stats.Recv, r.Stats.Sent, ms(r.Stats.Median)), true})
			}
		}
		return out, nil
	}, func(x any, _ error) {
		v.running = false
		v.results, v.styles = nil, nil
		for _, r := range x.([]struct {
			line string
			ok   bool
		}) {
			v.results = append(v.results, r.line)
			if r.ok {
				v.styles = append(v.styles, stOK)
			} else {
				v.styles = append(v.styles, stAlert)
			}
		}
	})
	return true
}

func (v *netTestView) Draw(a *App, s *Screen, x, y, w, h int) {
	s.Text(x, y, "Ping (3 échos) de chaque adresse, puis résolution et ping du nom.", stAccent, w)
	n := v.form.Draw(s, x, y+2, w, a.Focus)
	for i, r := range v.results {
		s.Text(x, y+3+n+i, r, v.styles[i], w)
	}
}

// ---------- mot de passe root ----------

type passwordView struct {
	form Form
	msg  string
}

func newPasswordView() *passwordView {
	pr := func(r rune) bool { return r >= 0x20 && r < 0x7f }
	return &passwordView{form: Form{LabelW: 22, Button: "changer le mot de passe", Fields: []*Field{
		{Label: "Nouveau mot de passe", Max: 64, Accept: pr},
		{Label: "Confirmation", Max: 64, Accept: pr},
	}}}
}

func (v *passwordView) Help() string { return "Tab champ  Entrée valider" }

func (v *passwordView) Key(a *App, k Key) bool {
	sub, used := v.form.Key(k)
	if !sub {
		return used
	}
	p1, p2 := v.form.Fields[0].Value, v.form.Fields[1].Value
	switch {
	case p1 != p2:
		v.msg = "Les deux saisies diffèrent."
	default:
		if err := sysconf.CheckPassword(p1); err != nil {
			v.msg = err.Error()
		} else {
			v.msg = ""
		}
	}
	if v.msg != "" {
		a.Status(v.msg, stAlert)
		return true
	}
	a.Confirm("Mot de passe root", []string{"Remplacer le mot de passe de root et de l'interface web ?", "Les sessions web ouvertes seront fermées ; SSH demandera le nouveau."}, func() {
		a.Go(func() (any, error) { return nil, a.C.Do2("PUT", "/system/password", map[string]string{"password": p1}) }, func(_ any, err error) {
			if err != nil {
				v.msg = err.Error()
				a.Error(err)
				return
			}
			v.form.Fields[0].Value, v.form.Fields[1].Value, v.form.Cur = "", "", 0
			a.Status("Mot de passe changé (root, interface web, API).", stOK)
			a.refreshInfo()
		})
	})
	return true
}

func (v *passwordView) Draw(a *App, s *Screen, x, y, w, h int) {
	s.Text(x, y, "Mot de passe actuel : "+a.Info.Password, stNormal, w)
	n := v.form.Draw(s, x, y+2, w, a.Focus)
	row := y + 3 + n
	if v.msg != "" {
		row += Paragraph(s, x, row, w, v.msg, stAlert) + 1
	}
	Paragraph(s, x, row, w, "Le même mot de passe sert à root (SSH) et à l'interface web. Il reste affiché sur la console, qui donne de toute façon un accès root. Saisie en clair pour vérifier le clavier : la console est en QWERTY ; 8 à 64 caractères ASCII.", stDim)
}

// ---------- shell root ----------

type shellView struct{}

func (v *shellView) Help() string { return "Entrée ouvrir le shell" }

func (v *shellView) Key(a *App, k Key) bool {
	if k.Name != "Enter" {
		return false
	}
	if err := a.Chvt(2); err != nil {
		a.Error(err)
	}
	return true
}

func (v *shellView) Draw(a *App, s *Screen, x, y, w, h int) {
	s.Text(x, y, "Shell root de la console", stAccent, w)
	row := y + 2 + Paragraph(s, x, y+2, w, "Entrée (ou Alt+F2) bascule sur la deuxième console, ouverte en root sans mot de passe : l'accès à la console de la VM vaut un accès physique. Alt+F1 revient ici ; « exit » ferme le shell, qui se rouvre au prochain passage.", stNormal)
	Paragraph(s, x, row+1, w, "En SSH, le mot de passe root est toujours demandé.", stDim)
}

// ---------- arrêt / redémarrage ----------

type powerView struct{}

func (v *powerView) Help() string { return "r redémarrer  a arrêter" }

func (v *powerView) Key(a *App, k Key) bool {
	switch k.R {
	case 'r', 'R':
		a.power("reboot")
		return true
	case 'a', 'A':
		a.power("poweroff")
		return true
	}
	return false
}

func (v *powerView) Draw(a *App, s *Screen, x, y, w, h int) {
	s.Text(x, y, "Arrêt et redémarrage de l'appliance (aussi par F12)", stAccent, w)
	s.Text(x, y+2, " r ", stInput, 0)
	s.Text(x+4, y+2, "redémarrer", stNormal, w-4)
	s.Text(x, y+3, " a ", stInput, 0)
	s.Text(x+4, y+3, "arrêter (la VM s'éteint ; la rallumer depuis vSphere)", stNormal, w-4)
	Paragraph(s, x, y+5, w, "Un test en cours doit d'abord être annulé. La surveillance réseau est enregistrée avant l'arrêt.", stDim)
}

func (a *App) power(action string) {
	label := map[string]string{"reboot": "Redémarrer", "poweroff": "Arrêter"}[action]
	a.Confirm(label+" l'appliance", []string{label + " maintenant ?"}, func() {
		a.Go(func() (any, error) { return nil, a.C.Do2("POST", "/system/power", map[string]string{"action": action}) }, func(_ any, err error) {
			if err != nil {
				a.Error(err)
				return
			}
			a.Status(label+" en cours…", stWarn)
		})
	})
}

// powerMenu : F12, depuis n'importe où (comme la DCUI).
func (a *App) powerMenu() {
	a.modal = &modal{title: "Arrêt / redémarrage", lines: []string{"[r] redémarrer l'appliance", "[a] arrêter l'appliance"},
		keys: map[rune]func(){'r': func() { a.power("reboot") }, 'a': func() { a.power("poweroff") }}}
}

// ---------- accueil (façon DCUI) ----------

func memory() string {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "MemTotal:") {
			f := strings.Fields(l)
			if len(f) >= 2 {
				kb, _ := strconv.ParseFloat(f[1], 64)
				return Tf("%s Gio de mémoire", strings.Replace(strconv.FormatFloat(kb/1024/1024, 'f', 1, 64), ".", decimalSep(), 1))
			}
		}
	}
	return ""
}

func (a *App) drawHome() {
	s := a.Scr
	s.Clear()
	gray := Style{Fg: Black, Bg: White}
	grayB := Style{Fg: Blue, Bg: White, Bold: false}
	yellow := Style{Fg: Black, Bg: Yellow}
	yellowB := Style{Fg: White, Bg: Yellow, Bold: true}
	s.Fill(0, 0, s.W, 11, ' ', gray)
	s.Fill(0, 11, s.W, s.H-12, ' ', yellow)
	s.Text(4, 2, "Vigie "+a.Info.Version, Style{Fg: Black, Bg: White}, 0)
	s.Text(4, 3, "Appliance de diagnostic réseau et de performance des datastores vSphere", grayB, s.W-8)
	host := a.Info.Hostname
	if host == "" {
		host = "vigie"
	}
	s.Text(4, 5, fmt.Sprintf("%s  ·  %d vCPU  ·  %s", host, runtime.NumCPU(), memory()), gray, s.W-8)
	vc := "vCenter : non connecté (stockage : tests locaux, onglet Storage)"
	if a.Info.VSphere.Connected {
		vc = "vCenter : connecté à " + a.Info.VSphere.Server.FullName
	} else if a.Info.Platform == "kvm" {
		vc = "Hyperviseur KVM (Proxmox, OpenStack) : stockage par tests locaux, onglet Storage"
	}
	s.Text(4, 6, vc, gray, s.W-8)
	if a.Info.Live {
		s.Text(4, 8, "Mode live (ISO) : rien n'est conservé au redémarrage.", Style{Fg: Red, Bg: White}, s.W-8)
	}
	if a.InfoErr != "" {
		s.Text(4, 8, "Service vigie injoignable : "+a.InfoErr, Style{Fg: Red, Bg: White}, s.W-8)
	}
	ip := a.IP()
	mode := ""
	if a.SysNet != nil {
		mode = map[string]string{"dhcp": "DHCP", "ovf": "statique (OVF)", "console": "configuré en console"}[a.SysNet.Source]
		if a.SysNet.Saved != nil {
			mode = map[string]string{"dhcp": "DHCP", "static": "statique"}[a.SysNet.Saved.Mode] + ", configuré en console"
		}
	}
	s.Text(4, 12, "Pour gérer cette appliance, ouvrir :", yellow, 0)
	if ip == "" {
		s.Text(6, 13, "adresse IP en attente (DHCP)…", yellowB, 0)
	} else {
		x := s.Text(6, 13, "https://"+ip+"/", yellowB, 0)
		if mode != "" {
			s.Text(6+x+2, 13, "("+mode+")", yellow, 0)
		}
	}
	s.Text(4, 15, "Mot de passe (root et interface web) :", yellow, 0)
	s.Raw(4+width(T("Mot de passe (root et interface web) :"))+1, 15, a.Info.Password, yellowB, 0)
	if fp := a.Info.TLS; fp != "" {
		s.Text(4, 16, "Certificat HTTPS (SHA-256) :", yellow, 0)
		s.Text(6, 17, fp[:min(len(fp), 47)], yellow, 0)
		if len(fp) > 48 {
			s.Text(6, 18, fp[48:], yellow, 0)
		}
	}
	if ip != "" {
		s.Text(4, 20, "SSH : ssh root@"+ip+"   (mot de passe demandé)", yellow, 0)
	}
	s.Fill(0, s.H-1, s.W, 1, ' ', Style{Fg: White, Bg: Black})
	s.Text(1, s.H-1, "<Entrée> Console vigie", Style{Fg: Yellow, Bg: Black, Bold: true}, 0)
	s.Text(26, s.H-1, "<Alt+F2> Shell root", Style{Fg: White, Bg: Black}, 0)
	s.Text(s.W-25, s.H-1, "<F12> Arrêt/redémarrage", Style{Fg: White, Bg: Black, Bold: true}, 0)
	if a.modal != nil {
		a.drawModal()
	}
}

// IdleHome : délai d'inactivité avant le retour à l'accueil.
const IdleHome = 10 * time.Minute
