package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strings"

	"vigie/internal/analysis"
	"vigie/internal/bench"
	"vigie/internal/jobs"
	"vigie/internal/localtest"
	"vigie/internal/testdisk"
)

func storageItems() []Item {
	return []Item{
		{"État du disque", &diskView{}},
		{"Ajouter disque", &addDiskView{}},
		{"Test standard", newTestView(false)},
		{"Test avancé", newTestView(true)},
		{"Résultats", &resultsView{}},
		{"Libérer disque", &releaseView{}},
	}
}

type localState struct {
	testdisk.State
	Expect *testdisk.Expect `json:"expect"`
	Guide  struct {
		AdviseGiB  int64  `json:"advise_gib"`
		MinGiB     int64  `json:"min_gib"`
		Controller string `json:"controller"`
	} `json:"guide"`
}

func (a *App) loadDisk(done func(*localState)) {
	a.Go(func() (any, error) {
		var st localState
		err := a.C.Get("/localdisk", &st)
		return &st, err
	}, func(v any, err error) {
		if err != nil {
			a.Error(err)
			return
		}
		done(v.(*localState))
	})
}

func gib(b int64) string { return Tf("%d Gio", b>>30) }

func checkLines(sc *Scroller, checks []testdisk.Check) {
	sorted := make([]testdisk.Check, 0, len(checks))
	for _, c := range checks { // les contrôles en échec d'abord
		if !c.OK {
			sorted = append(sorted, c)
		}
	}
	for _, c := range checks {
		if c.OK {
			sorted = append(sorted, c)
		}
	}
	for _, c := range sorted {
		mark, st := "OK  ", stOK
		if !c.OK {
			mark, st = "NON ", stAlert
		}
		t := mark + c.Name
		if c.Detail != "" {
			t += " — " + c.Detail
		}
		sc.Add(t, st)
	}
}

// ---------- état du disque ----------

type diskView struct {
	st *localState
	sc Scroller
}

func (v *diskView) Help() string { return "r rafraîchir  ↑↓ défiler" }
func (v *diskView) Enter(a *App) { v.load(a) }

func (v *diskView) load(a *App) {
	a.loadDisk(func(st *localState) { v.st = st })
}

func (v *diskView) Key(a *App, k Key) bool {
	if k.R == 'r' {
		v.load(a)
		return true
	}
	return v.sc.Key(k, 10)
}

func (v *diskView) Draw(a *App, s *Screen, x, y, w, h int) {
	v.sc.Lines = nil
	st := v.st
	switch {
	case st == nil:
		v.sc.Add("Lecture de l'état du disque de test…", stDim)
	case st.Record == nil:
		v.sc.Add("Aucun disque de test.", stAccent)
		v.sc.Add("", stNormal)
		v.sc.Add("Sans vCenter : menu « Ajouter disque », qui explique les paramètres à saisir dans le client vSphere, puis adopte le disque après contrôles et confirmation.", stNormal)
		v.sc.Add("Avec vCenter : l'interface web (onglet Datastore performance) crée et supprime elle-même le disque.", stDim)
		if st.Expect != nil {
			v.sc.Add("", stNormal)
			v.sc.Add("Une demande d'ajout est en cours depuis le "+st.Expect.At.UTC().Format("02/01 15:04")+" UTC : poursuivre dans « Ajouter disque ».", stWarn)
		}
	default:
		r := st.Record
		mode := "créé via vCenter"
		if r.Mode == testdisk.ModeLocal {
			mode = "ajouté à la main (mode local, sans vCenter)"
		}
		ready, rst := "Prêt pour les tests", stOK
		if !st.Ready {
			ready, rst = "Non utilisable", stAlert
		}
		v.sc.Add(ready, rst)
		v.sc.AddKV("Disque", r.FileName, stNormal)
		v.sc.AddKV("Origine", mode, stNormal)
		v.sc.AddKV("Taille", gib(r.SizeBytes), stNormal)
		if r.DatastoreName != "" {
			v.sc.AddKV("Datastore", r.DatastoreName, stNormal)
		}
		if st.Guest != nil && st.Guest.Device != nil {
			d := st.Guest.Device
			v.sc.AddKV("Invité", d.Path+"  "+d.WWID, stNormal)
		}
		if st.Signature != nil {
			pre := "non prérempli"
			if st.Signature.PrefilledBytes > 0 {
				pre = "prérempli " + gib(st.Signature.PrefilledBytes)
			}
			v.sc.Add("Signature  : posée le "+st.Signature.CreatedAt.UTC().Format("02/01/2006 15:04")+" UTC, "+pre, stNormal)
		}
		if st.Error != "" {
			v.sc.Add("Erreur : "+st.Error, stAlert)
		}
		if st.Guest != nil {
			v.sc.Add("", stNormal)
			v.sc.Add("Contrôles de sécurité (refaits avant chaque test) :", stAccent)
			checkLines(&v.sc, st.Guest.Checks)
		}
	}
	v.sc.Draw(s, x, y, w, h)
}

// ---------- ajout d'un disque (assistant) ----------

type addDiskView struct {
	step   int // 0 guide, 1 en attente de l'ajout, 2 détection, 3 candidats, 4 confirmation, 5 terminé
	st     *localState
	expect *testdisk.Expect
	cands  []testdisk.Candidate
	list   List
	form   Form
	sc     Scroller
	result *testdisk.State
	err    string
}

func (v *addDiskView) Help() string {
	switch v.step {
	case 0:
		return "haut/bas défiler  Entrée préparer l'ajout"
	case 1:
		return "Entrée c'est fait  Suppr abandonner"
	case 3:
		return "haut/bas disque  Entrée choisir  r relancer"
	case 4:
		return "Entrée adopter  Suppr annuler"
	}
	return ""
}

func (v *addDiskView) Enter(a *App) {
	a.loadDisk(func(st *localState) {
		v.st = st
		if st.Record == nil && st.Expect != nil && v.step == 0 {
			v.expect, v.step = st.Expect, 1 // demande en cours (console relancée)
		}
	})
}

func (v *addDiskView) Key(a *App, k Key) bool {
	switch v.step {
	case 0:
		if k.Name == "Enter" {
			if v.st != nil && v.st.Record != nil {
				a.Status("Un disque de test est déjà en place : le libérer d'abord.", stWarn)
				return true
			}
			a.Go(func() (any, error) {
				var r struct{ Expect *testdisk.Expect }
				err := a.C.Post("/localdisk/prepare", nil, &r)
				return r.Expect, err
			}, func(x any, err error) {
				if err != nil {
					a.Error(err)
					return
				}
				v.expect, v.step = x.(*testdisk.Expect), 1
			})
			return true
		}
		return v.sc.Key(k, 10)
	case 1:
		switch k.Name {
		case "Enter":
			v.scan(a)
			return true
		case "Delete":
			v.abandon(a)
			return true
		}
		return v.sc.Key(k, 10)
	case 3:
		if v.list.Key(k, len(v.cands), 5) {
			v.sc.Top = 0
			return true
		}
		switch {
		case k.R == 'r':
			v.scan(a)
			return true
		case k.Name == "Delete":
			v.abandon(a)
			return true
		case k.Name == "Enter" && len(v.cands) == 0:
			v.scan(a)
			return true
		case k.Name == "Enter":
			c := v.cands[v.list.Sel]
			if !c.OK {
				a.Status("Ce disque ne passe pas les contrôles : l'appliance n'y écrira pas.", stAlert)
				return true
			}
			v.form = Form{LabelW: 24, Button: "adopter ce disque", Fields: []*Field{
				{Label: "Retapez le nom (" + c.Device.Name + ")", Max: 8},
				{Label: "Datastore (pour mémoire)", Max: 60, Hint: "facultatif"},
			}}
			v.step = 4
			return true
		}
	case 4:
		if k.Name == "Delete" {
			v.step = 3
			return true
		}
		sub, used := v.form.Key(k)
		if sub {
			c := v.cands[v.list.Sel]
			req := map[string]string{"name": c.Device.Name, "confirm": v.form.Fields[0].Get(), "label": v.form.Fields[1].Get()}
			a.Go(func() (any, error) {
				var st testdisk.State
				err := a.C.Post("/localdisk/adopt", req, &st)
				return &st, err
			}, func(x any, err error) {
				if err != nil {
					v.err = err.Error()
					a.Error(err)
					return
				}
				v.result, v.step, v.err = x.(*testdisk.State), 5, ""
				a.Status("Disque adopté : prêt pour les tests.", stOK)
			})
		}
		return used
	case 5:
		if k.Name == "Enter" {
			v.step, v.result = 0, nil
			v.Enter(a)
			return true
		}
	}
	return false
}

func (v *addDiskView) abandon(a *App) {
	a.Confirm("Abandonner l'ajout", []string{"Abandonner la demande d'ajout de disque ?", "Si le disque a déjà été ajouté dans vSphere, le retirer là-bas."}, func() {
		a.Go(func() (any, error) { return nil, a.C.Delete("/localdisk/prepare", nil) }, func(_ any, err error) {
			a.Error(err)
			v.step = 0
		})
	})
}

func (v *addDiskView) scan(a *App) {
	v.step = 2
	a.Go(func() (any, error) {
		var r struct{ Candidates []testdisk.Candidate }
		err := a.C.Post("/localdisk/scan?wait=15", nil, &r)
		return r.Candidates, err
	}, func(x any, err error) {
		if err != nil {
			a.Error(err)
			v.step = 1
			return
		}
		v.cands, v.step = x.([]testdisk.Candidate), 3
		v.list = List{}
		for i, c := range v.cands { // présélectionne le premier disque acceptable
			if c.OK {
				v.list.Sel = i
				break
			}
		}
	})
}

// guide : les paramètres à saisir dans le client vSphere, expliqués (demande utilisateur).
func (v *addDiskView) guide(sc *Scroller, full bool, host string, platform string) {
	advise, minG := int64(testdisk.LocalAdviseGiB), int64(testdisk.LocalMinBytes>>30)
	if platform == "kvm" {
		sc.Add("Dans l'hyperviseur (Proxmox, OpenStack, KVM) : ajouter à la VM un disque vierge, puis :", stNormal)
		sc.AddKV("Taille", fmt.Sprintf("%d Go conseillés (%d Go minimum)", advise, minG), stNormal)
		sc.AddKV("Emplacement", "le stockage à mesurer", stNormal)
		sc.AddKV("Contrôleur", "SCSI (VirtIO SCSI) ; un disque VirtIO Block (vdX) n'est pas pris en charge.", stNormal)
		sc.AddKV("Cache", "aucun (cache=none) : sinon on mesure la RAM de l'hôte, pas le stockage.", stNormal)
		if full {
			sc.AddKV("Mode disque", "pas de snapshot pendant les tests.", stNormal)
			sc.Add("Ajout à chaud possible. Seul un disque neuf, apparu après votre demande et dont vous retapez le nom, sera utilisé ; ses données seront détruites.", stWarn)
		}
		return
	}
	if host == "" {
		host = "la VM"
	} else {
		host = "la VM « " + host + " »"
	}
	sc.Add("Dans le client vSphere : clic droit sur "+host+" > Modifier les paramètres > Ajouter un périphérique > Disque dur, puis :", stNormal)
	if full {
		sc.AddKV("Taille", fmt.Sprintf("%d Go conseillés, %d Go minimum : tout le disque sert de zone de test (plus grand que le cache du serveur = plus réaliste).", advise, minG), stNormal)
		sc.AddKV("Emplacement", "le datastore à mesurer, pas celui de la VM.", stNormal)
		sc.AddKV("Provisionnement", "Thick eager-zeroed conseillé ; thin accepté (préremplissage plus long).", stNormal)
		sc.AddKV("Contrôleur", "SCSI 0 existant (VMware Paravirtual), unité libre ; pas de contrôleur SATA ou NVMe.", stNormal)
		sc.AddKV("Mode disque", "Dépendant (pas « indépendant non persistant ») ; pas de snapshot pendant les tests.", stNormal)
		sc.AddKV("Partage", "aucun (pas de multi-writer) ; IOPS illimitées.", stNormal)
		sc.Add("OK : ajout à chaud, sans redémarrage. Seul un disque neuf, apparu après votre demande et dont vous retapez le nom, sera utilisé ; ses données seront détruites.", stWarn)
		return
	}
	sc.AddKV("Taille", fmt.Sprintf("%d Go conseillés (%d Go minimum)", advise, minG), stNormal)
	sc.AddKV("Emplacement", "le datastore à mesurer", stNormal)
	sc.AddKV("Provisionnement", "Thick eager-zeroed (thin accepté)", stNormal)
	sc.AddKV("Contrôleur", "SCSI 0 existant (VMware Paravirtual)", stNormal)
	sc.AddKV("Mode disque", "Dépendant, sans partage", stNormal)
}

func (v *addDiskView) Draw(a *App, s *Screen, x, y, w, h int) {
	v.sc.Lines = nil
	switch v.step {
	case 0:
		if v.st != nil && v.st.Record != nil {
			s.Text(x, y, "Un disque de test est déjà en place : "+v.st.Record.FileName, stWarn, w)
			Paragraph(s, x, y+2, w, "Pour en ajouter un autre, le libérer d'abord (menu « Libérer disque »).", stNormal)
			return
		}
		s.Text(x, y, "Ajouter un disque de test (sans vCenter)", stAccent, w)
		v.guide(&v.sc, true, a.Info.Hostname, a.Info.Platform)
		v.sc.Draw(s, x, y+1, w, h-2)
		s.Text(x, y+h-1, " Entrée : je vais ajouter le disque, préparer la détection ", Style{Fg: Black, Bg: Green}, w)
	case 1:
		n := 0
		if v.expect != nil {
			n = len(v.expect.Disks)
		}
		s.Text(x, y, fmt.Sprintf("Prêt : %d disque(s) déjà présent(s), ignoré(s).", n), stOK, w)
		s.Text(x, y+1, map[bool]string{true: "Ajoutez maintenant le disque dans l'hyperviseur :", false: "Ajoutez maintenant le disque dans vSphere :"}[a.Info.Platform == "kvm"], stAccent, w)
		v.guide(&v.sc, false, a.Info.Hostname, a.Info.Platform)
		v.sc.Draw(s, x, y+3, w, h-5)
		s.Text(x, y+h-1, " Entrée : c'est fait, détecter le disque ", Style{Fg: Black, Bg: Green}, w)
	case 2:
		s.Text(x, y, "Recherche du nouveau disque (rescan des contrôleurs SCSI, jusqu'à 15 s)…", stAccent, w)
	case 3:
		if len(v.cands) == 0 {
			s.Text(x, y, "Aucun nouveau disque détecté.", stWarn, w)
			Paragraph(s, x, y+2, w, "Vérifier dans vSphere que l'ajout est validé (OK), que le disque est sur le contrôleur SCSI 0 (VMware Paravirtual) et non sur un nouveau contrôleur SATA ou NVMe. Entrée : relancer la détection ; Suppr : abandonner.", stNormal)
			return
		}
		s.Text(x, y, "Disque(s) apparu(s) depuis la demande :", stAccent, w)
		for i, c := range v.cands {
			d := c.Device
			ok, st := T("acceptable"), stOK
			if !c.OK {
				ok, st = T("refusé"), stAlert
			}
			txt := fmt.Sprintf("%-4s %7s  SCSI %d:%d (%s)  %s", d.Name, gib(d.SizeBytes), c.Bus, c.Unit, c.Controller, ok)
			if i == v.list.Sel {
				s.Fill(x, y+1+i, w, 1, ' ', stSel)
				s.Text(x, y+1+i, txt, stSel, w)
			} else {
				s.Text(x, y+1+i, txt, st, w)
			}
		}
		c := v.cands[v.list.Sel]
		row := y + 2 + len(v.cands)
		s.Text(x, row, "Contrôles de "+c.Device.Name+" ("+c.Device.WWID+") :", stAccent, w)
		checkLines(&v.sc, c.Checks)
		v.sc.Draw(s, x, row+1, w, y+h-row-1)
	case 4:
		c := v.cands[v.list.Sel]
		d := c.Device
		s.Text(x, y, "Disque détecté : vérifiez que c'est bien le vôtre.", stAccent, w)
		rows := []string{
			"Nom dans la VM . " + d.Path,
			"Taille ......... " + gib(d.SizeBytes) + fmt.Sprintf(" (%s octets)", num(float64(d.SizeBytes))),
			fmt.Sprintf("Contrôleur ..... SCSI %d (%s), unité %d", c.Bus, c.Controller, c.Unit),
			"Identifiant .... " + d.WWID,
			"Modèle ......... " + strings.TrimSpace(d.Vendor+" "+d.Model),
			"Provisionnement  non visible depuis la VM",
		}
		for i, r := range rows {
			s.Text(x, y+1+i, r, stBright, w)
		}
		n := Paragraph(s, x, y+8, w, "L'appliance écrira une signature de 4 Kio au début du disque ; les tests écriront ensuite sur tout le disque. Toutes ses données seront détruites.", stWarn)
		v.form.Draw(s, x, y+9+n, w, a.Focus)
		if v.err != "" {
			Paragraph(s, x, y+h-2, w, v.err, stAlert)
		}
	case 5:
		r := v.result
		s.Text(x, y, "Disque adopté : prêt pour les tests (menus Test standard et Test avancé).", stOK, w)
		if r != nil && r.Guest != nil {
			checkLines(&v.sc, r.Guest.Checks)
			v.sc.Draw(s, x, y+2, w, h-4)
		}
		s.Text(x, y+h-1, " Entrée : terminer ", Style{Fg: Black, Bg: Green}, w)
	}
}

// ---------- tests locaux ----------

type testView struct {
	adv    bool
	form   Form
	job    follower
	series []float64 // Mo/s (séquentiel) ou IO/s (aléatoire) à la seconde, palier courant
	step   int
	kind   string
	unit   string
	sc     Scroller
	report *localtest.Report
}

func newTestView(adv bool) *testView {
	f := Form{LabelW: 14, Button: "lancer le test", Fields: []*Field{
		{Label: "Écriture séq.", Choices: []string{"80 s", "60 s", "30 s", "120 s"}},
		{Label: "Lecture séq.", Choices: []string{"80 s", "60 s", "30 s", "120 s"}},
		{Label: "Seuils", Choices: []string{"all-flash", "hybride", "disques"}},
	}}
	return &testView{adv: adv, form: f}
}

func (v *testView) Help() string {
	if v.job.Running() {
		return "x annuler le test"
	}
	if v.report != nil {
		return "haut/bas défiler  Entrée relancer"
	}
	return "Tab champ  flèches choix  Entrée lancer"
}

func (v *testView) Enter(a *App) {
	if v.job.ID != "" {
		return
	}
	// un test local déjà en cours (lancé ailleurs) : on le suit
	a.Go(func() (any, error) {
		var r struct{ Current *jobs.Job }
		err := a.C.Get("/jobs?limit=1", &r)
		return r.Current, err
	}, func(x any, err error) {
		if j, _ := x.(*jobs.Job); err == nil && j != nil && j.Profile == "local" && v.job.ID == "" {
			v.attach(a, j.ID)
		}
	})
}

func (v *testView) Leave(a *App) {}

func (v *testView) attach(a *App, id string) {
	v.job = follower{Profile: "local"}
	v.series, v.report = nil, nil
	a.follow(id, &v.job, v.onData)
}

func (v *testView) onData(typ string, data json.RawMessage) {
	if typ != "second" {
		return
	}
	var d struct {
		Step int     `json:"step"`
		Kind string  `json:"kind"`
		T    int     `json:"t"`
		MBps float64 `json:"mbps"`
		IOPS float64 `json:"iops"`
	}
	if json.Unmarshal(data, &d) != nil || d.T <= 0 {
		return
	}
	if d.Step != v.step {
		v.step, v.kind, v.series = d.Step, d.Kind, nil
	}
	if strings.HasPrefix(d.Kind, "seq") {
		v.series, v.unit = append(v.series, d.MBps), "Mo/s"
	} else {
		v.series, v.unit = append(v.series, d.IOPS), "IO/s"
	}
}

func (v *testView) Key(a *App, k Key) bool {
	if v.job.Running() {
		if k.R == 'x' {
			a.cancelJob(&v.job)
			return true
		}
		return false
	}
	if v.report != nil && v.sc.Key(k, 10) {
		return true
	}
	if v.report != nil && k.Name == "Enter" {
		v.report, v.job = nil, follower{}
		return true
	}
	sub, used := v.form.Key(k)
	if sub {
		f := v.form.Fields
		req := map[string]any{"profile": "local", "write_s": atoi(strings.TrimSuffix(f[0].Get(), " s")), "read_s": atoi(strings.TrimSuffix(f[1].Get(), " s")),
			"preset": f[2].Get(), "advanced": v.adv}
		v.series, v.report, v.step = nil, nil, 0
		a.submitJob(req, &v.job, nil, v.onData)
	}
	return used
}

func (v *testView) estimate() string {
	f := v.form.Fields
	sec := atoi(strings.TrimSuffix(f[0].Get(), " s")) + atoi(strings.TrimSuffix(f[1].Get(), " s"))
	if v.adv {
		sec += 12 * 45
	}
	return fmt.Sprintf("Durée estimée : %d min %02d s, plus le préremplissage du disque au premier test.", sec/60, sec%60)
}

func (v *testView) Draw(a *App, s *Screen, x, y, w, h int) {
	if v.job.ID == "" {
		title := "Test standard local : débit séquentiel en écriture puis en lecture"
		if v.adv {
			title = "Test avancé local : débit séquentiel, puis IOPS et latences 4 Kio"
		}
		s.Text(x, y, title, stAccent, w)
		n := v.form.Draw(s, x, y+2, w, a.Focus)
		row := y + 3 + n
		row += Paragraph(s, x, row, w, v.estimate(), stNormal)
		prof := "Profil d'I/O : écriture puis lecture séquentielles, bloc 1 Mio, QD 4, O_DIRECT+O_SYNC, données aléatoires incompressibles renouvelées à chaque passe, sur tout le disque de test."
		if v.adv {
			prof += " Puis 4 Kio aléatoire en lecture, écriture et mixte 70/30, QD 1, 4, 16 et 32, 5 s de chauffe et 40 s de mesure par palier."
		}
		row += 1 + Paragraph(s, x, row+1, w, prof, stDim)
		Paragraph(s, x, row+1, w, "Sans vCenter, seul ce que voit la VM est mesuré : pas de ratio TX/écrit, d'erreurs NIC ni de journaux de l'hôte.", stDim)
		return
	}
	if v.job.Result != nil && v.report == nil && v.job.State == "succeeded" {
		var r localtest.Report
		if json.Unmarshal(v.job.Result, &r) == nil {
			v.report = &r
		}
	}
	if v.report != nil {
		v.drawReport(s, x, y, w, h)
		return
	}
	s.Text(x, y, "Test "+map[bool]string{false: "standard", true: "avancé"}[v.adv]+" local : "+stateFR(v.job.State), stateStyle(v.job.State), w)
	progressBar(s, x, y+1, w, v.job.Progress, v.job.Step)
	if v.job.Err != "" {
		Paragraph(s, x, y+3, w, v.job.Err, stAlert)
	}
	if len(v.series) > 0 {
		BarChart(s, x, y+5, w, h-7, v.series, v.unit, stChart)
	}
}

func (v *testView) drawReport(s *Screen, x, y, w, h int) {
	r := v.report
	v.sc.Lines = nil
	v.sc.Add("Verdict "+string(r.Verdict.Level), levelStyle(string(r.Verdict.Level)))
	for _, t := range r.Verdict.Triggered {
		v.sc.Add("  "+t, stWarn)
	}
	resultLines(&v.sc, r)
	v.sc.Draw(s, x, y, w, h)
}

// resultLines décrit un résultat de test local, avec son profil d'I/O complet.
func resultLines(sc *Scroller, r *localtest.Report) {
	if r.Write != nil && r.Read != nil {
		sc.Add(fmt.Sprintf("Écriture séquentielle %s Mo/s (p99 %s) ; lecture %s Mo/s (p99 %s)", num(r.Write.MBps), ms(r.Write.Latency.P99/1000), num(r.Read.MBps), ms(r.Read.Latency.P99/1000)), stBright)
	}
	for _, u := range r.Useful {
		if u.BlockKiB != 4 {
			continue
		}
		name := map[string]string{"read": "lecture", "write": "écriture", "mixed": "mixte 70/30"}[u.Pattern]
		if u.IOPS > 0 {
			sc.Add(fmt.Sprintf("IOPS utiles %-11s %s IO/s à QD %d (p99 < %s ; max brut %s à QD %d)", name, num(u.IOPS), u.QD, ms(u.CeilingMs), num(u.RawMax), u.RawMaxQD), stBright)
		} else {
			sc.Add(fmt.Sprintf("IOPS utiles %-11s aucun palier sous %s (max brut %s à QD %d)", name, ms(u.CeilingMs), num(u.RawMax), u.RawMaxQD), stWarn)
		}
	}
	if len(r.Points) > 0 {
		sc.Add("", stNormal)
		sc.Add(fmt.Sprintf("%-16s %4s %9s %9s %9s", T("Palier"), "QD", "IO/s", "p50", "p99"), stAccent)
		for i, p := range r.Points {
			p999 := math.NaN()
			if i < len(r.Steps) {
				p999 = r.Steps[i].Latency.P999 / 1000
			}
			_ = p999
			name := T(map[string]string{"read": "lecture 4 Kio", "write": "écriture 4 Kio", "mixed": "mixte 4 Kio"}[p.Pattern])
			sc.Add(fmt.Sprintf("%-16s %4d %9s %9s %9s", name, p.QD, num(p.IOPS), strings.TrimSuffix(ms(p.P50Ms), " ms"), strings.TrimSuffix(ms(p.P99Ms), " ms")), stNormal)
		}
	}
	sc.Add("", stNormal)
	if r.Write != nil {
		sc.Add(T("Profil écriture : ")+profileLine(r.Write.Profile), stDim)
	}
	if r.Read != nil {
		sc.Add(T("Profil lecture : ")+profileLine(r.Read.Profile), stDim)
	}
	if len(r.Steps) > 0 {
		st := r.Steps[0]
		sc.Add(Tf("Profil paliers : aléatoire aligné, bloc 4 Kio, QD 1 à 32, %d s de chauffe + %d s de mesure, zone %s, O_DIRECT+O_SYNC", r.Options.WarmupS, r.Options.MeasureS, gib(st.ZoneBytes)), stDim)
	}
	sc.Add("Disque : "+r.Disk.FileName+map[bool]string{true: ", datastore " + r.Disk.DatastoreName, false: ""}[r.Disk.DatastoreName != ""]+" ; seuils « "+r.Options.Thresholds.Preset+" »", stDim)
	for _, wn := range r.Warnings {
		sc.Add(wn, stDim)
	}
}

// ---------- résultats ----------

type resultsView struct {
	jobs   []jobs.Job
	list   List
	detail *localtest.Report
	sc     Scroller
}

func (v *resultsView) Help() string {
	if v.detail != nil {
		return "haut/bas défiler  Échap liste"
	}
	return "haut/bas choisir  Entrée détail"
}

func (v *resultsView) Enter(a *App) {
	a.Go(func() (any, error) {
		var r struct{ Jobs []jobs.Job }
		err := a.C.Get("/jobs?profile=local&limit=50", &r)
		return r.Jobs, err
	}, func(x any, err error) {
		if err != nil {
			a.Error(err)
			return
		}
		v.jobs = x.([]jobs.Job)
		v.list.Clamp(len(v.jobs))
	})
}

func (v *resultsView) Key(a *App, k Key) bool {
	if v.detail != nil {
		if k.Name == "Esc" {
			v.detail = nil
			return true
		}
		return v.sc.Key(k, 10)
	}
	if v.list.Key(k, len(v.jobs), 10) {
		return true
	}
	if k.Name == "Enter" && len(v.jobs) > 0 {
		id := v.jobs[v.list.Sel].ID
		a.Go(func() (any, error) {
			var j jobs.Job
			err := a.C.Get("/jobs/"+url.PathEscape(id), &j)
			return j, err
		}, func(x any, err error) {
			if err != nil {
				a.Error(err)
				return
			}
			j := x.(jobs.Job)
			var r localtest.Report
			if j.Result == nil || json.Unmarshal(j.Result, &r) != nil || r.Write == nil {
				a.Message("Résultat", []string{"Ce test n'a pas de résultat complet.", j.Error})
				return
			}
			v.detail, v.sc = &r, Scroller{}
		})
		return true
	}
	return false
}

func (v *resultsView) Draw(a *App, s *Screen, x, y, w, h int) {
	if v.detail != nil {
		r := v.detail
		v.sc.Lines = nil
		v.sc.Add(r.Started.UTC().Format("02/01/2006 15:04")+" UTC — verdict "+string(r.Verdict.Level), levelStyle(string(r.Verdict.Level)))
		for _, t := range r.Verdict.Triggered {
			v.sc.Add("  "+t, stWarn)
		}
		resultLines(&v.sc, r)
		v.sc.Draw(s, x, y, w, h)
		return
	}
	s.Raw(x, y, fmt.Sprintf("%-17s %-10s %-10s %s", T("Date (UTC)"), T("État"), T("Verdict"), T("Résultat")), stDim, w)
	if len(v.jobs) == 0 {
		s.Text(x, y+2, "Aucun test local pour l'instant.", stDim, w)
		return
	}
	start, end := v.list.Window(h-1, len(v.jobs))
	for i := start; i < end; i++ {
		j := v.jobs[i]
		lvl, txt := "", ""
		if j.Summary != nil {
			lvl, txt = j.Summary.Level, j.Summary.Text
		}
		line := fmt.Sprintf("%-17s %-10s %-10s %s", j.Created.UTC().Format("02/01/2006 15:04"), T(stateFR(string(j.State))), T(lvl), T(txt))
		row := y + 1 + i - start
		if i == v.list.Sel && a.Focus {
			s.Fill(x, row, w, 1, ' ', stSel)
			s.Raw(x, row, line, stSel, w)
		} else {
			s.Raw(x, row, line, levelStyle(lvl), w)
		}
	}
}

// ---------- libération ----------

type releaseView struct {
	st   *localState
	done string
}

func (v *releaseView) Help() string { return "Entrée libérer" }
func (v *releaseView) Enter(a *App) {
	v.done = ""
	a.loadDisk(func(st *localState) { v.st = st })
}

func (v *releaseView) Key(a *App, k Key) bool {
	if k.Name != "Enter" || v.st == nil || v.st.Record == nil {
		return false
	}
	r := v.st.Record
	if r.Mode != testdisk.ModeLocal {
		a.Message("Disque créé via vCenter", []string{"Ce disque a été créé par l'appliance via vCenter :", "le supprimer depuis l'interface web (Datastore performance)."})
		return true
	}
	a.Confirm("Libérer le disque", []string{"Oublier " + r.FileName + " ?", "Rien n'est effacé ; le retirer ensuite dans vSphere."}, func() {
		a.Go(func() (any, error) { return nil, a.C.Post("/localdisk/release", nil, nil) }, func(_ any, err error) {
			if err != nil {
				a.Error(err)
				return
			}
			v.done = fmt.Sprintf("Disque libéré. Dans vSphere : Modifier les paramètres de la VM, retirer le disque dur de %s sur SCSI 0, unité %d, en cochant « Supprimer les fichiers du datastore ».", gib(r.SizeBytes), r.UnitNumber)
			v.st.Record = nil
		})
	})
	return true
}

func (v *releaseView) Draw(a *App, s *Screen, x, y, w, h int) {
	switch {
	case v.done != "":
		Paragraph(s, x, y, w, v.done, stOK)
	case v.st == nil:
		s.Text(x, y, "Lecture de l'état du disque…", stDim, w)
	case v.st.Record == nil:
		s.Text(x, y, "Aucun disque de test à libérer.", stDim, w)
	default:
		r := v.st.Record
		s.Text(x, y, "Disque en place : "+r.FileName+", "+gib(r.SizeBytes), stAccent, w)
		Paragraph(s, x, y+2, w, "Libérer : l'appliance oublie ce disque sans rien y écrire ni l'effacer. Il faut ensuite le retirer de la VM dans le client vSphere.", stNormal)
		s.Text(x, y+5, " Entrée : libérer le disque ", Style{Fg: Black, Bg: Green}, w)
	}
}

var _ = analysis.OK

// profileLine : profil d'I/O d'un palier, dans la langue de la console (à partir des champs du
// profil, pas du texte français du serveur).
func profileLine(p bench.Profile) string {
	sens := T("écriture")
	switch {
	case p.ReadPct == 100:
		sens = T("lecture")
	case p.ReadPct > 0:
		sens = Tf("mixte %d/%d lecture/écriture", p.ReadPct, 100-p.ReadPct)
	}
	block := Tf("%d Kio", p.BlockKiB)
	if p.BlockKiB >= 1024 && p.BlockKiB%1024 == 0 {
		block = Tf("%d Mio", p.BlockKiB/1024)
	}
	flags := "O_DIRECT"
	if p.Sync {
		flags += "+O_SYNC"
	}
	out := Tf("%s %s, bloc %s, QD %d, %d s, zone %s, %s", sens, T(p.Pattern), block, p.QD, p.Duration, gib(p.ZoneBytes), flags)
	if p.Data != "" {
		out += ", " + T(p.Data)
	}
	return out
}
