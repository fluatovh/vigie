package tui

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"vigie/internal/netdiag"
)

// View est un écran du panneau de droite.
type View interface {
	Draw(a *App, s *Screen, x, y, w, h int)
	Key(a *App, k Key) bool // true : touche consommée
	Help() string
}

// Entering est appelé quand la vue devient visible (chargement des données).
type Entering interface{ Enter(a *App) }

// Leaving est appelé quand la vue est quittée (arrêt d'un flux).
type Leaving interface{ Leave(a *App) }

// Ticking reçoit un tic par seconde quand elle est visible.
type Ticking interface{ Tick(a *App) }

// Item est une entrée du menu d'un onglet.
type Item struct {
	Label string
	View  View
}

// Tab est un onglet : un menu à gauche, la vue sélectionnée à droite.
type Tab struct {
	Name  string
	Key   string
	Items []Item
	Sel   int
}

type consoleInfo struct {
	Language string `json:"language"`
	Live     bool   `json:"live"`
	Platform string `json:"platform"`
	Version  string `json:"version"`
	Hostname string `json:"hostname"`
	Password string `json:"password"`
	TLS      string `json:"tls_fingerprint"`
	VSphere  struct {
		Connected bool `json:"connected"`
		Server    struct {
			FullName string `json:"full_name"`
		} `json:"server"`
	} `json:"vsphere"`
}

type modal struct {
	title string
	lines []string
	yes   func()
	info  bool            // simple message : n'importe quelle touche ferme
	keys  map[rune]func() // choix par touche (F12 : r / a)
}

// App est l'application console.
type App struct {
	C        *Client
	Scr      *Screen
	Tabs     []*Tab
	TabIdx   int
	Focus    bool // true : le panneau a le focus, sinon le menu
	Info     consoleInfo
	InfoErr  string
	Local    netdiag.Local
	Job      *jobBrief
	post     chan func()
	status   string
	statusSt Style
	statusAt time.Time
	modal    *modal
	lastInfo time.Time
	Now      func() time.Time
	Home     bool        // écran d'accueil façon DCUI
	lastKey  time.Time   // retour à l'accueil après IdleHome d'inactivité
	SysNet   *sysNetwork // configuration réseau de gestion (accueil, onglet Système)
	Chvt     func(n int) error
	Font     func(lang string) // charge la police de la langue (console réelle)
	langSet  bool
}

type jobBrief struct {
	ID       string  `json:"id"`
	Profile  string  `json:"profile"`
	State    string  `json:"state"`
	Progress float64 `json:"progress"`
	Step     string  `json:"step"`
}

// NewApp construit l'application (onglets Network, Storage, Accès).
func NewApp(c *Client, w, h int) *App {
	a := &App{C: c, Scr: NewScreen(w, h), post: make(chan func(), 256), Now: time.Now, Home: true}
	a.lastKey = a.Now()
	a.Chvt = func(int) error { return errors.New("changement de console indisponible") }
	a.Tabs = []*Tab{
		{Name: "Network", Key: "F1", Items: networkItems()},
		{Name: "Storage", Key: "F2", Items: storageItems()},
		{Name: "Système", Key: "F3", Items: systemItems()},
	}
	return a
}

// Post exécute f dans la boucle principale (depuis une goroutine).
func (a *App) Post(f func()) {
	select {
	case a.post <- f:
	default: // boucle saturée : on perd une mise à jour d'affichage, jamais un état final
		go func() { a.post <- f }()
	}
}

// Go lance work en arrière-plan puis done dans la boucle principale.
func (a *App) Go(work func() (any, error), done func(any, error)) {
	go func() {
		v, err := work()
		a.Post(func() { done(v, err) })
	}()
}

// Status affiche un message temporaire sur l'avant-dernière ligne.
func (a *App) Status(msg string, st Style) {
	a.status, a.statusSt, a.statusAt = msg, st, a.Now()
}

// Error affiche une erreur.
func (a *App) Error(err error) {
	if err != nil {
		a.Status("Erreur : "+err.Error(), stAlert)
	}
}

// Confirm ouvre une boîte de confirmation ([o] oui / [Échap] non).
func (a *App) Confirm(title string, lines []string, yes func()) {
	a.modal = &modal{title: title, lines: lines, yes: yes}
}

// Message ouvre une boîte d'information.
func (a *App) Message(title string, lines []string) {
	a.modal = &modal{title: title, lines: lines, info: true}
}

func (a *App) tab() *Tab { return a.Tabs[a.TabIdx] }

func (a *App) view() View {
	t := a.tab()
	if len(t.Items) == 0 {
		return nil
	}
	return t.Items[t.Sel].View
}

func (a *App) switchView(f func()) {
	if l, ok := a.view().(Leaving); ok {
		l.Leave(a)
	}
	f()
	if e, ok := a.view().(Entering); ok {
		e.Enter(a)
	}
}

// Start charge l'état initial (l'accueil s'affiche d'abord).
func (a *App) Start() {
	a.refreshInfo()
	if e, ok := a.view().(Entering); ok && !a.Home {
		e.Enter(a)
	}
}

func (a *App) refreshInfo() {
	a.lastInfo = a.Now()
	a.Go(func() (any, error) {
		var info consoleInfo
		var loc netdiag.Local
		var jobs struct {
			Current *jobBrief `json:"current"`
		}
		var sn sysNetwork
		err := a.C.Get("/console", &info)
		if err == nil {
			_ = a.C.Get("/net/local", &loc)
			_ = a.C.Get("/jobs?limit=1", &jobs)
			_ = a.C.Get("/system/network", &sn)
		}
		return []any{info, loc, jobs.Current, &sn}, err
	}, func(v any, err error) {
		if err != nil {
			a.InfoErr = err.Error()
			return
		}
		r := v.([]any)
		a.InfoErr = ""
		a.Info, a.Local = r[0].(consoleInfo), r[1].(netdiag.Local)
		if !a.langSet {
			a.langSet = true
			a.setLanguage(a.Info.Language)
		}
		a.Job, _ = r[2].(*jobBrief)
		if sn := r[3].(*sysNetwork); sn.Source != "" {
			a.SysNet = sn
		}
	})
}

// IP principale de l'appliance.
func (a *App) IP() string {
	for _, i := range a.Local.Ifaces {
		if i.Up && i.CIDR != "" && !strings.HasPrefix(i.CIDR, "127.") && i.Name != "lo" {
			return strings.SplitN(i.CIDR, "/", 2)[0]
		}
	}
	return ""
}

// HandleKey traite une touche.
func (a *App) HandleKey(k Key) {
	a.lastKey = a.Now()
	if a.modal != nil {
		m := a.modal
		if m.info {
			a.modal = nil
			return
		}
		if m.keys != nil {
			if f, ok := m.keys[k.R]; ok {
				a.modal = nil
				f()
			} else if k.Name == "Esc" {
				a.modal = nil
			}
			return
		}
		switch {
		case k.R == 'o' || k.R == 'O' || k.R == 'y' || k.R == 'Y':
			a.modal = nil
			if m.yes != nil {
				m.yes()
			}
		case k.Name == "Esc" || k.R == 'n' || k.R == 'N':
			a.modal = nil
		}
		return
	}
	if k.Name == "F12" {
		a.powerMenu()
		return
	}
	if a.Home {
		switch k.Name {
		case "Enter", "F2":
			a.leaveHome()
		case "F1", "F3":
			a.leaveHome()
			a.switchView(func() { a.TabIdx, a.Focus = int(k.Name[1]-'1'), false })
		}
		return
	}
	switch k.Name {
	case "F1", "F2", "F3":
		i := int(k.Name[1] - '1')
		if i < len(a.Tabs) && i != a.TabIdx {
			a.switchView(func() { a.TabIdx, a.Focus = i, false })
		}
		return
	case "Ctrl-L":
		a.Scr.Invalidate()
		return
	}
	if a.Focus {
		if v := a.view(); v != nil && v.Key(a, k) {
			return
		}
		if k.Name == "Esc" || k.Name == "Left" {
			a.Focus = false
		}
		return
	}
	t := a.tab()
	switch k.Name {
	case "Up":
		if t.Sel > 0 {
			a.switchView(func() { t.Sel-- })
		}
	case "Down":
		if t.Sel < len(t.Items)-1 {
			a.switchView(func() { t.Sel++ })
		}
	case "Home":
		a.switchView(func() { t.Sel = 0 })
	case "End":
		a.switchView(func() { t.Sel = len(t.Items) - 1 })
	case "Enter", "Right", "Tab":
		a.Focus = true
	case "BackTab":
		a.switchView(func() { a.TabIdx, a.Focus = (a.TabIdx+len(a.Tabs)-1)%len(a.Tabs), false })
	case "Esc":
		a.goHome()
	}
	if k.Name == "Tab" && len(t.Items) == 1 {
		a.Focus = true
	}
}

func (a *App) leaveHome() {
	a.Home = false
	if e, ok := a.view().(Entering); ok {
		e.Enter(a)
	}
}

// goHome revient à l'écran d'accueil (les flux de la vue courante sont arrêtés).
func (a *App) goHome() {
	if l, ok := a.view().(Leaving); ok {
		l.Leave(a)
	}
	a.Home, a.Focus, a.modal = true, false, nil
}

// Tick : une fois par seconde.
func (a *App) Tick() {
	if !a.Home && a.modal == nil && a.Now().Sub(a.lastKey) > IdleHome {
		a.goHome()
	}
	if a.Now().Sub(a.lastInfo) > 5*time.Second {
		a.refreshInfo()
	}
	if a.Now().Unix()%60 == 0 {
		a.Scr.Invalidate() // redessin complet périodique (écriture parasite sur la console)
	}
	if t, ok := a.view().(Ticking); ok {
		t.Tick(a)
	}
	if a.status != "" && a.Now().Sub(a.statusAt) > 12*time.Second {
		a.status = ""
	}
}

const menuW = 18

// Draw compose l'écran complet.
func (a *App) Draw() {
	if a.Home {
		a.drawHome()
		return
	}
	s := a.Scr
	s.Clear()
	// ligne 0 : identité et mot de passe (toujours visible, demande utilisateur)
	s.Fill(0, 0, s.W, 1, ' ', stTitle)
	ip := a.IP()
	if ip == "" {
		ip = "IP en attente"
	}
	ver := a.Info.Version
	if len(ver) > 14 {
		ver = ver[:14]
	}
	x := s.Text(0, 0, " vigie "+ver+" ", stTitle, 0)
	x += s.Text(x, 0, "│ "+a.Info.Hostname+" ", stTitle, 18)
	x += s.Text(x, 0, "│ "+ip+" ", stTitle, 20)
	if a.Info.Password != "" {
		lbl := "│ mot de passe "
		if x+len(lbl)+len(a.Info.Password)+1 > s.W {
			lbl = "│ mdp "
		}
		x += s.Text(x, 0, lbl, stTitle, 0)
		s.Raw(x, 0, a.Info.Password, stPass, 0)
	} else if a.InfoErr != "" {
		s.Text(x, 0, "│ service vigie injoignable", Style{Fg: Red, Bg: Cyan, Bold: true}, 0)
	}
	// ligne 1 : onglets et job en cours
	x = 0
	for i, t := range a.Tabs {
		st := stTabOff
		if i == a.TabIdx {
			st = stTabOn
		}
		x += s.Raw(x, 1, " "+t.Key+" "+T(t.Name)+" ", st, 0)
		x++
	}
	if j := a.Job; j != nil && j.State != "" && j.State != "succeeded" && j.State != "failed" && j.State != "cancelled" {
		txt := Tf("job %s %s %%", j.Profile, strconv.Itoa(int(j.Progress)))
		s.Raw(s.W-width(txt)-1, 1, txt, stWarn, 0)
	} else if a.Info.VSphere.Connected {
		txt := T("vCenter connecté")
		s.Raw(s.W-width(txt)-1, 1, txt, stOK, 0)
	}
	// menu
	t := a.tab()
	top := 2
	bodyH := s.H - 4
	for i := 0; i < bodyH; i++ {
		s.Set(menuW, top+i, '│', stGrid)
	}
	for i, it := range t.Items {
		st := stNormal
		if i == t.Sel {
			st = stSelOff
			if !a.Focus {
				st = stSel
			}
		}
		s.Fill(0, top+i, menuW, 1, ' ', st)
		s.Text(1, top+i, it.Label, st, menuW-2)
	}
	// panneau
	if v := a.view(); v != nil {
		v.Draw(a, s, menuW+2, top, s.W-menuW-3, bodyH)
	}
	// avant-dernière ligne : message
	if a.status != "" {
		s.Text(0, s.H-2, " "+a.status, a.statusSt, 0)
	}
	// dernière ligne : aide
	s.Fill(0, s.H-1, s.W, 1, ' ', stHelp)
	help := "haut/bas menu  Entrée ouvrir  Échap accueil"
	if a.Focus {
		help = "Échap menu"
		if v := a.view(); v != nil && v.Help() != "" {
			help = v.Help() + "  Échap menu"
		}
	}
	s.Text(1, s.H-1, help, stHelp, s.W-24)
	s.Text(s.W-22, s.H-1, "F1-F3 onglets  F12", stHelp, 0)
	if a.modal != nil {
		a.drawModal()
	}
}

func (a *App) drawModal() {
	s, m := a.Scr, a.modal
	w := 20
	lines := make([]string, len(m.lines))
	for i, l := range m.lines {
		lines[i] = T(l)
		w = max(w, width(lines[i])+4)
	}
	w = min(w, s.W-4)
	h := len(m.lines) + 4
	x, y := (s.W-w)/2, max(2, (s.H-h)/2)
	st := Style{Fg: White, Bg: Blue, Bold: true}
	s.Fill(x, y, w, h, ' ', st)
	s.Box(x, y, w, h, m.title, st)
	for i, l := range lines {
		s.Raw(x+2, y+1+i, l, st, w-4)
	}
	foot := "[o] oui    [Échap] non"
	if m.info {
		foot = "une touche pour fermer"
	}
	if m.keys != nil {
		foot = "[Échap] annuler"
	}
	foot = T(foot)
	s.Raw(x+w-width(foot)-2, y+h-2, foot, Style{Fg: Yellow, Bg: Blue, Bold: true}, 0)
}

// Run est la boucle principale.
func (a *App) Run(keys <-chan Key, out io.Writer) error {
	a.Start()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	a.Draw()
	if err := a.Scr.Flush(out); err != nil {
		return err
	}
	for {
		select {
		case k, ok := <-keys:
			if !ok {
				return nil
			}
			a.HandleKey(k)
		case f := <-a.post:
			f()
		case <-tick.C:
			a.Tick()
		}
		// regroupe les mises à jour en attente avant de redessiner
		for drained := false; !drained; {
			select {
			case f := <-a.post:
				f()
			default:
				drained = true
			}
		}
		a.Draw()
		if err := a.Scr.Flush(out); err != nil {
			return err
		}
	}
}
