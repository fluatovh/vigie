package tui

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"vigie/internal/auth"
	"vigie/internal/jobs"
	"vigie/internal/store"
	"vigie/internal/sysconf"
	"vigie/internal/testdisk"
	"vigie/internal/web"
)

func TestDecoder(t *testing.T) {
	var d Decoder
	cases := map[string][]string{
		"\x1b[[A":        {"F1"},
		"\x1b[[B\x1b[[C": {"F2", "F3"},
		"\x1bOP":         {"F1"},
		"\x1b[A\x1b[B":   {"Up", "Down"},
		"\x1b[3~":        {"Delete"},
		"\x1b[6~":        {"PgDn"},
		"\x1b[Z":         {"BackTab"},
		"ab\r":           {"a", "b", "Enter"},
		"é\x7f":          {"é", "Backspace"},
		"\t":             {"Tab"},
	}
	for in, want := range cases {
		keys, pending := d.Feed([]byte(in))
		var got []string
		for _, k := range keys {
			got = append(got, k.String())
		}
		if strings.Join(got, ",") != strings.Join(want, ",") || pending {
			t.Errorf("%q : %v (pending %v), attendu %v", in, got, pending, want)
		}
	}
	// Échap seul : rendu après le délai ; séquence coupée en deux lectures : recollée
	if keys, pending := d.Feed([]byte{0x1b}); len(keys) != 0 || !pending {
		t.Fatal("Échap seul rendu trop tôt")
	}
	if k := d.Flush(); len(k) != 1 || k[0].Name != "Esc" {
		t.Fatalf("Échap : %v", k)
	}
	d.Feed([]byte("\x1b["))
	if keys, _ := d.Feed([]byte("[E")); len(keys) != 1 || keys[0].Name != "F5" {
		t.Fatalf("séquence en deux morceaux : %v", keys)
	}
}

func TestCharset(t *testing.T) {
	s := NewScreen(20, 1)
	s.Text(0, 0, "À Ê é — → «x» …", stNormal, 0)
	if got := strings.TrimSpace(s.String()); got != "A E é - -> «x» ..." {
		t.Errorf("translittération : %q", got)
	}
}

// ---------- serveur réel sur un faux système ----------

type harness struct {
	t     *testing.T
	a     *App
	guest testdisk.Guest
	sys   *sysconf.System
	cmds  *[]string
	store *store.Store
}

func mkDisk(t *testing.T, g testdisk.Guest, name, wwid string, size int64, hctl, majmin string) {
	d := filepath.Join(g.Sys, "block", name)
	os.MkdirAll(filepath.Join(d, "holders"), 0o755)
	os.MkdirAll(filepath.Join(g.Sys, "devices", hctl), 0o755)
	os.Symlink(filepath.Join(g.Sys, "devices", hctl), filepath.Join(d, "device"))
	os.WriteFile(filepath.Join(d, "device/wwid"), []byte(wwid+"\n"), 0o644)
	os.WriteFile(filepath.Join(d, "device/vendor"), []byte("VMware\n"), 0o644)
	os.WriteFile(filepath.Join(d, "device/model"), []byte("Virtual disk\n"), 0o644)
	os.WriteFile(filepath.Join(d, "size"), []byte(strconv.FormatInt(size/512, 10)+"\n"), 0o644)
	os.WriteFile(filepath.Join(d, "dev"), []byte(majmin+"\n"), 0o644)
	f, _ := os.Create(filepath.Join(g.Dev, name))
	f.Truncate(size)
	f.Close()
}

func newHarness(t *testing.T) *harness { return newHarnessAt(t, false) }

func newHarnessAt(t *testing.T, home bool) *harness {
	SetLang("fr") // les tests d'écran vérifient les textes sources
	t.Cleanup(func() { SetLang(DefaultLang) })
	root := t.TempDir()
	g := testdisk.Guest{Sys: filepath.Join(root, "sys"), Proc: filepath.Join(root, "proc"), Dev: filepath.Join(root, "dev")}
	os.MkdirAll(g.Proc, 0o755)
	os.MkdirAll(g.Dev, 0o755)
	os.MkdirAll(filepath.Join(g.Sys, "class/scsi_host/host0"), 0o755)
	os.WriteFile(filepath.Join(g.Sys, "class/scsi_host/host0/proc_name"), []byte("vmw_pvscsi\n"), 0o644)
	mkDisk(t, g, "sda", "naa.6000c2929ca7d8202e785152b35439b5", 512<<20, "0:0:0:0", "8:0")
	os.MkdirAll(filepath.Join(g.Sys, "block/sda/sda1"), 0o755)
	os.WriteFile(filepath.Join(g.Proc, "mounts"), []byte("/dev/sda1 /media/sda1 vfat ro 0 0\n/dev/sda2 /data ext4 rw 0 0\n"), 0o644)
	st, err := store.New(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(st.Path("first-access-token"), []byte("MotDePasse123\n"), 0o600)
	os.WriteFile(st.Path("language.json"), []byte(`{"language":"fr"}`), 0o600)
	td := &testdisk.Service{Store: st, Guest: g}
	var cmds []string
	sys := &sysconf.System{Root: filepath.Join(root, "sysroot"), Data: st.Dir, Run: func(stdin []byte, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, strings.TrimSpace(name+" "+strings.Join(args, " ")))
		if name == "cryptpw" {
			return []byte("$6$sel$empreinte\n"), nil
		}
		return nil, nil
	}}
	pw := auth.New(st)
	_ = pw.Set("MotDePasse123")
	h := web.New(web.Info{Version: "test", Started: time.Now()}, web.Deps{Auth: pw, Settings: web.NewSettings(st), TestDisk: td, Jobs: jobs.NewManager(st), SysConf: sys})
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ConnContext = web.ConsoleConn
	srv.Start()
	t.Cleanup(srv.Close)
	a := NewApp(&Client{Base: srv.URL, HTTP: srv.Client()}, 80, 25)
	hs := &harness{t: t, a: a, guest: g, sys: sys, cmds: &cmds, store: st}
	a.Chvt = func(n int) error { cmds = append(cmds, "chvt "+strconv.Itoa(n)); return nil }
	a.Start()
	hs.settle()
	if !home {
		a.leaveHome()
		hs.settle()
	}
	return hs
}

// settle exécute les mises à jour asynchrones jusqu'au calme.
func (h *harness) settle() {
	for {
		select {
		case f := <-h.a.post:
			f()
		case <-time.After(150 * time.Millisecond):
			h.a.Draw()
			return
		}
	}
}

func (h *harness) keys(ks ...string) {
	for _, k := range ks {
		switch {
		case len([]rune(k)) == 1:
			h.a.HandleKey(Key{R: []rune(k)[0]})
		default:
			h.a.HandleKey(Key{Name: k})
		}
		h.settle()
	}
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.a.HandleKey(Key{R: r})
	}
	h.settle()
}

func (h *harness) screen() string { return h.a.Scr.String() }

func (h *harness) want(subs ...string) {
	h.t.Helper()
	sc := h.screen()
	for _, s := range subs {
		if !strings.Contains(sc, s) {
			h.t.Fatalf("absent de l'écran : %q\n%s", s, sc)
		}
	}
}

func TestEveryScreenFits(t *testing.T) {
	h := newHarness(t)
	h.want("MotDePasse123", "F1 Network", "F2 Storage", "F3 Système")
	for ti, tab := range h.a.Tabs {
		h.keys("F" + strconv.Itoa(ti+1))
		for i := range tab.Items {
			h.a.Focus = false
			h.a.switchView(func() { tab.Sel = i })
			h.settle()
			h.keys("Enter")
			sc := h.screen()
			lines := strings.Split(strings.TrimRight(sc, "\n"), "\n")
			if len(lines) != 25 {
				t.Errorf("%s/%s : %d lignes", tab.Name, tab.Items[i].Label, len(lines))
			}
			for _, l := range lines {
				if len([]rune(l)) > 80 {
					t.Errorf("%s/%s : ligne de %d colonnes", tab.Name, tab.Items[i].Label, len([]rune(l)))
				}
			}
			if strings.ContainsRune(sc, '?') {
				t.Errorf("%s/%s : caractère hors police VGA (?)\n%s", tab.Name, tab.Items[i].Label, sc)
			}
			h.keys("Esc")
		}
	}
	h.keys("F3", "Home")
	h.want("Mot de passe", "MotDePasse123", "non connecté")
}

func TestAddDiskWizard(t *testing.T) {
	h := newHarness(t)
	h.keys("F2", "Down", "Enter")
	h.want("Ajouter un disque de test (sans vCenter)", "Taille", "16 Go conseillés", "Contrôleur", "SCSI 0", "Provisionnement", "Mode disque", "Dépendant")
	h.keys("Enter") // préparer
	h.want("1 disque(s) déjà présent(s)", "c'est fait")
	// l'utilisateur ajoute un disque de 8 Gio dans vSphere
	mkDisk(t, h.guest, "sdb", "naa.6000c29d111122223333444455556666", 8<<30, "0:0:1:0", "8:16")
	h.keys("Enter") // c'est fait
	h.want("sdb", "8 Gio", "SCSI 0:1 (vmw_pvscsi)", "acceptable", "disque vierge")
	h.keys("Enter")
	h.want("Disque détecté", "Nom dans la VM", "unité 1", "naa.6000c29d", "Retapez le nom (sdb)", "seront détruites")
	h.typeText("sdc") // mauvaise confirmation
	h.keys("Enter")
	h.want("confirmation refusée")
	if b := readHead(filepath.Join(h.guest.Dev, "sdb")); string(b) == "VIGIE" {
		t.Fatal("signature écrite malgré une confirmation erronée")
	}
	h.keys("Backspace")
	h.typeText("b")
	h.keys("Tab")
	h.typeText("ssd-001557")
	h.keys("Enter")
	h.want("Disque adopté")
	if b := readHead(filepath.Join(h.guest.Dev, "sdb")); string(b) != "VIGIE" {
		t.Fatal("signature absente après adoption")
	}
	h.keys("Enter", "Esc", "Up")
	h.want("Prêt pour les tests", "ajouté à la main", "ssd-001557")
	// libération : rien n'est écrit
	h.keys("End", "Enter", "Enter")
	h.want("Oublier")
	h.keys("o")
	h.want("Disque libéré", "unité 1")
}

func readHead(p string) []byte {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	b := make([]byte, 5)
	f.Read(b)
	return b
}

func TestPingNeedsTarget(t *testing.T) {
	h := newHarness(t)
	h.keys("Enter", "Enter")
	h.want("Saisir une cible")
}

func (h *harness) ran(cmd string) bool {
	for _, c := range *h.cmds {
		if c == cmd {
			return true
		}
	}
	return false
}

func TestHomeAndPower(t *testing.T) {
	h := newHarnessAt(t, true)
	h.want("Vigie test", "Pour gérer cette appliance", "Mot de passe (root et interface web) :", "MotDePasse123", "<Entrée> Console vigie", "<F12>")
	if strings.ContainsRune(h.screen(), '?') {
		t.Errorf("glyphe hors police :\n%s", h.screen())
	}
	h.keys("Enter")
	h.want("F1 Network", "F3 Système")
	h.keys("Esc")
	h.want("Pour gérer cette appliance")
	h.keys("F12")
	h.want("[r] redémarrer", "[a] arrêter")
	h.keys("r")
	h.want("Redémarrer maintenant")
	h.keys("o")
	if !h.ran("reboot") {
		t.Fatalf("reboot non demandé : %v", *h.cmds)
	}
	// retour automatique à l'accueil après inactivité
	h.keys("F1")
	h.want("F1 Network")
	h.a.lastKey = time.Now().Add(-IdleHome - time.Minute)
	h.a.Tick()
	h.settle()
	h.want("Pour gérer cette appliance")
}

func TestSystemNetwork(t *testing.T) {
	h := newHarness(t)
	h.keys("F3", "Down", "Enter")
	h.want("Actuel :", "origine :", "Mode", "Adresse IP", "Masque", "Passerelle", "DNS", "Nom d'hôte")
	f := h.a.Tabs[2].Items[1].View.(*netConfView).form.Fields
	for _, fd := range f[1:] {
		fd.Value = ""
	}
	h.keys("Right") // Statique
	h.keys("Down")
	h.typeText("10.1.2.3")
	h.keys("Down")
	h.typeText("24")
	h.keys("Down")
	h.typeText("10.9.9.9") // passerelle hors réseau : refusée avant tout envoi
	h.keys("Down")
	h.typeText("10.1.2.53")
	h.keys("Down")
	h.typeText("vigie-qa")
	h.keys("Enter")
	h.want("n'est pas dans le réseau")
	if h.ran("ifup eth0") {
		t.Fatal("réseau modifié malgré une erreur")
	}
	f[3].Value = "10.1.2.254"
	h.keys("Enter")
	h.want("statique 10.1.2.3/24, passerelle 10.1.2.254")
	h.keys("o")
	h.want("Configuration réseau appliquée")
	b, _ := os.ReadFile(filepath.Join(h.sys.Root, "etc/network/interfaces"))
	if !strings.Contains(string(b), "address 10.1.2.3") || !h.ran("ifup eth0") || !h.ran("hostname vigie-qa") {
		t.Fatalf("configuration non appliquée : %s %v", b, *h.cmds)
	}
	if n := h.sys.Saved(); n == nil || n.Gateway != "10.1.2.254" {
		t.Fatalf("non persistée : %+v", n)
	}
}

func TestSystemPassword(t *testing.T) {
	h := newHarness(t)
	h.keys("F3", "Down", "Down", "Down", "Enter")
	h.want("Mot de passe actuel : MotDePasse123", "QWERTY")
	h.typeText("Nouveau-Mdp-1")
	h.keys("Tab")
	h.typeText("Nouveau-Mdp-2")
	h.keys("Enter")
	h.want("Les deux saisies diffèrent")
	h.keys("Backspace")
	h.typeText("1")
	h.keys("Enter")
	h.want("Remplacer le mot de passe")
	h.keys("o")
	h.want("Mot de passe changé", "Nouveau-Mdp-1")
	if !auth.New(h.store).Verify("Nouveau-Mdp-1") || !h.ran("chpasswd -e") {
		t.Fatalf("mot de passe non appliqué : %v", *h.cmds)
	}
}

func TestShellRoot(t *testing.T) {
	h := newHarness(t)
	h.keys("F3", "End", "Up", "Up", "Enter", "Enter")
	if !h.ran("chvt 2") {
		t.Fatalf("pas de bascule vers tty2 : %v", *h.cmds)
	}
}
