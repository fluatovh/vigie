package sysconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMask(t *testing.T) {
	ok := map[string]int{"24": 24, "/16": 16, "255.255.255.0": 24, "255.255.240.0": 20, "255.255.255.255": 32}
	for in, want := range ok {
		if got, err := ParseMask(in); err != nil || got != want {
			t.Errorf("%q : %d %v", in, got, err)
		}
	}
	for _, in := range []string{"0", "33", "255.0.255.0", "abc", "0.0.0.0"} {
		if _, err := ParseMask(in); err == nil {
			t.Errorf("%q accepté", in)
		}
	}
	if Mask(24) != "255.255.255.0" || Mask(20) != "255.255.240.0" {
		t.Error("Mask")
	}
}

func TestValidate(t *testing.T) {
	good := Network{Mode: "static", Address: "192.168.0.50", Prefix: 24, Gateway: "192.168.0.254", DNS: []string{"1.1.1.1", " ", "9.9.9.9"}, Hostname: "vigie-01"}
	if err := good.Validate(); err != nil || len(good.DNS) != 2 {
		t.Fatalf("%v %v", err, good.DNS)
	}
	bad := []Network{
		{Mode: "static", Address: "192.168.0.0", Prefix: 24},
		{Mode: "static", Address: "192.168.0.255", Prefix: 24},
		{Mode: "static", Address: "192.168.0.5", Prefix: 24, Gateway: "10.0.0.1"},
		{Mode: "static", Address: "192.168.0.5", Prefix: 24, Gateway: "192.168.0.5"},
		{Mode: "static", Address: "::1", Prefix: 24},
		{Mode: "static", Address: "192.168.0.5", Prefix: 24, DNS: []string{"x"}},
		{Mode: "dhcp", Hostname: "bad_name"},
		{Mode: "dhcp", Hostname: "a;reboot"},
		{Mode: "autre"},
	}
	for _, n := range bad {
		if err := n.Validate(); err == nil {
			t.Errorf("accepté : %+v", n)
		}
	}
}

type fake struct {
	cmds []string
	in   []string
}

func (f *fake) run(stdin []byte, name string, args ...string) ([]byte, error) {
	f.cmds = append(f.cmds, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	f.in = append(f.in, string(stdin))
	if name == "cryptpw" {
		return []byte("$6$sel$empreinte\n"), nil
	}
	return nil, nil
}

func newSys(t *testing.T) (*System, *fake) {
	root := t.TempDir()
	f := &fake{}
	s := &System{Root: root, Data: filepath.Join(root, "data"), Run: f.run}
	os.MkdirAll(s.Data, 0o700)
	return s, f
}

func read(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyNetworkAndBoot(t *testing.T) {
	s, f := newSys(t)
	if ok, _ := s.Boot(); ok {
		t.Fatal("boot sans configuration de console")
	}
	err := s.ApplyNetwork(Network{Mode: "static", Address: "10.1.2.3", Prefix: 24, Gateway: "10.1.2.254", DNS: []string{"10.1.2.53"}, Hostname: "vigie-a"})
	if err != nil {
		t.Fatal(err)
	}
	ifs := read(t, filepath.Join(s.Root, "etc/network/interfaces"))
	for _, w := range []string{"iface eth0 inet static", "address 10.1.2.3", "netmask 255.255.255.0", "gateway 10.1.2.254"} {
		if !strings.Contains(ifs, w) {
			t.Errorf("interfaces sans %q :\n%s", w, ifs)
		}
	}
	if read(t, filepath.Join(s.Root, "etc/resolv.conf")) != "nameserver 10.1.2.53\n" || read(t, filepath.Join(s.Root, "etc/hostname")) != "vigie-a\n" {
		t.Error("resolv.conf ou hostname")
	}
	if !strings.Contains(strings.Join(f.cmds, "|"), "ifup eth0") {
		t.Errorf("réseau non redémarré : %v", f.cmds)
	}
	// au boot suivant (/etc en RAM, donc vide), la configuration de la console est rejouée
	os.RemoveAll(filepath.Join(s.Root, "etc"))
	if ok, err := s.Boot(); !ok || err != nil {
		t.Fatalf("boot : %v %v", ok, err)
	}
	if !strings.Contains(read(t, filepath.Join(s.Root, "etc/network/interfaces")), "address 10.1.2.3") {
		t.Error("configuration non rejouée au boot")
	}
	// retour en DHCP
	if err := s.ApplyNetwork(Network{Mode: "dhcp", Hostname: "vigie-a"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, filepath.Join(s.Root, "etc/network/interfaces")), "inet dhcp") || s.Saved().Mode != "dhcp" {
		t.Error("DHCP")
	}
	// une configuration invalide n'est ni appliquée ni persistée
	if err := s.ApplyNetwork(Network{Mode: "static", Address: "10.1.2.0", Prefix: 24}); err == nil || s.Saved().Mode != "dhcp" {
		t.Error("configuration invalide appliquée")
	}
}

func TestSetPassword(t *testing.T) {
	s, f := newSys(t)
	var ui string
	set := func(p string) error { ui = p; return nil }
	for _, p := range []string{"court", " espace-debut", "accentué-é", strings.Repeat("x", 65)} {
		if err := s.SetPassword(p, set); err == nil {
			t.Errorf("mot de passe %q accepté", p)
		}
	}
	if err := s.SetPassword("N0uveau-Mdp!", set); err != nil {
		t.Fatal(err)
	}
	if ui != "N0uveau-Mdp!" {
		t.Error("interface web non mise à jour")
	}
	if read(t, filepath.Join(s.Data, "first-access-token")) != "N0uveau-Mdp!\n" || read(t, filepath.Join(s.Data, "root.hash")) != "$6$sel$empreinte\n" {
		t.Error("fichiers persistés")
	}
	if _, err := os.Stat(filepath.Join(s.Data, PasswordFlag)); err != nil {
		t.Error("drapeau password.local absent")
	}
	joined := strings.Join(f.cmds, "|")
	if strings.Contains(joined, "N0uveau") {
		t.Error("mot de passe en clair dans une ligne de commande")
	}
	if !strings.Contains(joined, "chpasswd -e") || !strings.Contains(strings.Join(f.in, "|"), "root:$6$sel$empreinte") {
		t.Errorf("chpasswd : %v", f.cmds)
	}
}

func TestPower(t *testing.T) {
	s, f := newSys(t)
	if s.Power("rm -rf /") == nil {
		t.Error("action arbitraire acceptée")
	}
	if err := s.Power("reboot"); err != nil || f.cmds[len(f.cmds)-1] != "reboot" {
		t.Error("reboot")
	}
}
