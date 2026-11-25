// Package cloudinit lit les métadonnées d'instance des clouds et hyperviseurs KVM (phase P) :
// config-drive OpenStack (étiquette config-2), NoCloud de Proxmox (étiquette cidata) et service de
// métadonnées OpenStack (169.254.169.254). Il en retient le nom d'hôte, le mot de passe et les clés
// SSH publiques. Ce qui a été choisi dans la console de l'appliance prime toujours.
package cloudinit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Config est ce que l'appliance retient des métadonnées.
type Config struct {
	Source   string   `json:"source"` // configdrive, nocloud, metadata
	Hostname string   `json:"hostname,omitempty"`
	Password string   `json:"-"`
	SSHKeys  []string `json:"ssh_keys,omitempty"`
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// shortHost : « vm1.novalocal » → « vm1 » ; vide si invalide.
func shortHost(h string) string {
	h = strings.TrimSpace(strings.SplitN(strings.TrimSpace(h), ".", 2)[0])
	if !hostRe.MatchString(h) {
		return ""
	}
	return h
}

func validKey(k string) bool {
	k = strings.TrimSpace(k)
	for _, p := range []string{"ssh-rsa ", "ssh-ed25519 ", "ecdsa-sha2-", "sk-ssh-ed25519", "sk-ecdsa-"} {
		if strings.HasPrefix(k, p) {
			return !strings.ContainsAny(k, "\n\r")
		}
	}
	return false
}

func (c *Config) addKey(k string) {
	k = strings.TrimSpace(k)
	if !validKey(k) {
		return
	}
	for _, x := range c.SSHKeys {
		if x == k {
			return
		}
	}
	c.SSHKeys = append(c.SSHKeys, k)
}

// ParseOpenStackMeta lit meta_data.json (config-drive ou service de métadonnées).
func ParseOpenStackMeta(b []byte, c *Config) error {
	var m struct {
		Hostname   string            `json:"hostname"`
		Name       string            `json:"name"`
		PublicKeys map[string]string `json:"public_keys"`
		AdminPass  string            `json:"admin_pass"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("meta_data.json : %w", err)
	}
	if h := shortHost(m.Hostname); h != "" {
		c.Hostname = h
	} else if h := shortHost(m.Name); h != "" {
		c.Hostname = h
	}
	for _, k := range m.PublicKeys {
		c.addKey(k)
	}
	if m.AdminPass != "" {
		c.Password = m.AdminPass
	}
	return nil
}

// ParseNoCloudMeta lit meta-data (YAML simple : local-hostname, hostname).
func ParseNoCloudMeta(b []byte, c *Config) {
	for _, l := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		v = unquote(v)
		switch strings.TrimSpace(k) {
		case "local-hostname", "hostname":
			if h := shortHost(v); h != "" {
				c.Hostname = h
			}
		}
	}
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		v = v[1 : len(v)-1]
	}
	return v
}

// ParseUserData lit un #cloud-config minimal : hostname, password (ou vigie_password),
// ssh_authorized_keys (liste). Les autres clés sont ignorées ; pas de script exécuté.
func ParseUserData(b []byte, c *Config) {
	s := string(b)
	if !strings.HasPrefix(strings.TrimSpace(s), "#cloud-config") {
		return
	}
	inKeys := false
	for _, l := range strings.Split(s, "\n") {
		t := strings.TrimRight(l, "\r")
		if inKeys {
			if it := strings.TrimSpace(t); strings.HasPrefix(it, "- ") {
				c.addKey(unquote(it[2:]))
				continue
			}
			inKeys = false
		}
		if strings.HasPrefix(t, " ") || strings.HasPrefix(t, "\t") || strings.HasPrefix(t, "#") {
			continue
		}
		k, v, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "hostname", "local-hostname":
			if h := shortHost(unquote(v)); h != "" {
				c.Hostname = h
			}
		case "password", "vigie_password":
			if p := unquote(v); p != "" {
				c.Password = p
			}
		case "ssh_authorized_keys":
			inKeys = true
		}
	}
}

func readIf(p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return b
}

// FromDir lit une source montée : config-drive (openstack/latest/…) ou NoCloud (meta-data, user-data).
func FromDir(dir string) (*Config, error) {
	if b := readIf(filepath.Join(dir, "openstack/latest/meta_data.json")); b != nil {
		c := &Config{Source: "configdrive"}
		if err := ParseOpenStackMeta(b, c); err != nil {
			return nil, err
		}
		ParseUserData(readIf(filepath.Join(dir, "openstack/latest/user_data")), c)
		return c, nil
	}
	md, ud := readIf(filepath.Join(dir, "meta-data")), readIf(filepath.Join(dir, "user-data"))
	if md != nil || ud != nil {
		c := &Config{Source: "nocloud"}
		ParseNoCloudMeta(md, c)
		ParseUserData(ud, c)
		return c, nil
	}
	return nil, errors.New("ni config-drive ni NoCloud dans " + dir)
}

// FromMetadata interroge le service de métadonnées OpenStack (base : http://169.254.169.254).
func FromMetadata(ctx context.Context, base string) (*Config, error) {
	get := func(p string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+p, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s : HTTP %d", p, resp.StatusCode)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	}
	b, err := get("/openstack/latest/meta_data.json")
	if err != nil {
		return nil, err
	}
	c := &Config{Source: "metadata"}
	if err := ParseOpenStackMeta(b, c); err != nil {
		return nil, err
	}
	if ud, err := get("/openstack/latest/user_data"); err == nil {
		ParseUserData(ud, c)
	}
	return c, nil
}

// Target : ce que l'application peut modifier (remplaçable dans les tests).
type Target struct {
	Root        string                  // racine (« / »)
	ConsoleHost bool                    // un nom d'hôte a été choisi en console : ne pas le changer
	ConsolePass bool                    // un mot de passe a été choisi en console : ne pas le changer
	SetPassword func(pw string) error   // root + interface web + console
	SetHostname func(name string) error // hostname courant
}

// Apply applique la configuration (nom d'hôte, clés SSH de root, mot de passe), en respectant la
// console. Journalisé ; renvoie ce qui a été appliqué.
func (c *Config) Apply(t Target) []string {
	var done []string
	if c.Hostname != "" && !t.ConsoleHost {
		if err := os.WriteFile(filepath.Join(t.Root, "etc/hostname"), []byte(c.Hostname+"\n"), 0o644); err == nil {
			if t.SetHostname != nil {
				_ = t.SetHostname(c.Hostname)
			}
			done = append(done, "nom d'hôte "+c.Hostname)
		}
	}
	if len(c.SSHKeys) > 0 {
		dir := filepath.Join(t.Root, "root/.ssh")
		_ = os.MkdirAll(dir, 0o700)
		p := filepath.Join(dir, "authorized_keys")
		have := map[string]bool{}
		for _, l := range strings.Split(string(readIf(p)), "\n") {
			have[strings.TrimSpace(l)] = true
		}
		f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			n := 0
			for _, k := range c.SSHKeys {
				if !have[k] {
					fmt.Fprintln(f, k)
					n++
				}
			}
			f.Close()
			done = append(done, fmt.Sprintf("%d clé(s) SSH pour root", len(c.SSHKeys)))
		}
	}
	if c.Password != "" && !t.ConsolePass && t.SetPassword != nil {
		if err := t.SetPassword(c.Password); err != nil {
			log.Printf("cloudinit: mot de passe ignoré : %v", err)
		} else {
			done = append(done, "mot de passe")
		}
	}
	log.Printf("cloudinit: source %s : %s", c.Source, strings.Join(done, ", "))
	return done
}
