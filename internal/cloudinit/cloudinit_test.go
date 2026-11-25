package cloudinit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const key1 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample qa@vigie"

func write(t *testing.T, root, rel, body string) {
	p := filepath.Join(root, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigDrive(t *testing.T) {
	d := t.TempDir()
	write(t, d, "openstack/latest/meta_data.json", `{"hostname":"vigie-os1.novalocal","name":"x","public_keys":{"k":"`+key1+`","bad":"rm -rf /"},"admin_pass":"Adm1n-Pass"}`)
	write(t, d, "openstack/latest/user_data", "#cloud-config\npassword: Cl0ud-Pass\nssh_authorized_keys:\n  - "+key1+"\n  - ssh-rsa AAAAB3Nza second\nruncmd:\n  - reboot\n")
	c, err := FromDir(d)
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "configdrive" || c.Hostname != "vigie-os1" || c.Password != "Cl0ud-Pass" || len(c.SSHKeys) != 2 {
		t.Fatalf("%+v", c)
	}
}

func TestNoCloud(t *testing.T) {
	d := t.TempDir()
	write(t, d, "meta-data", "instance-id: iid-1\nlocal-hostname: pve-vigie\n")
	write(t, d, "user-data", "#cloud-config\nhostname: \"vigie-pve\"\nvigie_password: 'Pve-Pass-1'\n")
	c, err := FromDir(d)
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "nocloud" || c.Hostname != "vigie-pve" || c.Password != "Pve-Pass-1" {
		t.Fatalf("%+v", c)
	}
	// user-data qui n'est pas un #cloud-config (script) : ignoré
	c2 := &Config{}
	ParseUserData([]byte("#!/bin/sh\nhostname: pirate\n"), c2)
	if c2.Hostname != "" {
		t.Error("script pris pour un cloud-config")
	}
	if _, err := FromDir(t.TempDir()); err == nil {
		t.Error("répertoire vide accepté")
	}
	if shortHost("bad name") != "" || shortHost("a;b") != "" {
		t.Error("nom d'hôte invalide accepté")
	}
}

func TestApplyRespectsConsole(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc"), 0o755)
	var setPw, setHost string
	c := &Config{Source: "nocloud", Hostname: "cloudname", Password: "Cloud-Pass-1", SSHKeys: []string{key1}}
	tgt := Target{Root: root, SetPassword: func(p string) error { setPw = p; return nil }, SetHostname: func(h string) error { setHost = h; return nil }}
	c.Apply(tgt)
	if setPw != "Cloud-Pass-1" || setHost != "cloudname" {
		t.Fatalf("non appliqué : %q %q", setPw, setHost)
	}
	b, _ := os.ReadFile(filepath.Join(root, "root/.ssh/authorized_keys"))
	if strings.Count(string(b), key1) != 1 {
		t.Fatal("clé absente")
	}
	c.Apply(tgt) // rejoué au boot suivant : pas de doublon
	b, _ = os.ReadFile(filepath.Join(root, "root/.ssh/authorized_keys"))
	if strings.Count(string(b), key1) != 1 {
		t.Fatal("clé dupliquée")
	}
	setPw, setHost = "", ""
	tgt.ConsoleHost, tgt.ConsolePass = true, true
	os.WriteFile(filepath.Join(root, "etc/hostname"), []byte("console\n"), 0o644)
	c.Apply(tgt)
	if setPw != "" || setHost != "" {
		t.Fatal("la console n'a pas priorité")
	}
	if h, _ := os.ReadFile(filepath.Join(root, "etc/hostname")); string(h) != "console\n" {
		t.Fatal("nom d'hôte de la console écrasé")
	}
}
