// Package sysconf modifie la configuration de l'appliance elle-même (phase D, depuis la console) :
// réseau de gestion (DHCP ou statique, DNS, nom d'hôte), mot de passe root (qui est aussi celui de
// l'interface web) et alimentation. Rien ici ne touche à l'infrastructure vSphere.
//
// Chaque opération est une fonction unique, validée, journalisée. La configuration choisie en console
// est persistée dans /data et prime au boot sur les propriétés OVF (init.sh appelle `vigie sysconf boot`).
package sysconf

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Fichiers persistés dans le répertoire de données (/data/vigie).
const (
	NetworkFile  = "network.conf"   // clé=valeur, réécrit par la console
	PasswordFlag = "password.local" // présent : le mot de passe a été changé en console (l'OVF ne l'écrase plus)
)

// Network est la configuration du réseau de gestion (eth0).
type Network struct {
	Mode     string   `json:"mode"`     // dhcp ou static
	Address  string   `json:"address"`  // 192.168.0.50
	Prefix   int      `json:"prefix"`   // 24
	Gateway  string   `json:"gateway"`  // facultative
	DNS      []string `json:"dns"`      // statique : serveurs DNS
	Hostname string   `json:"hostname"` // nom d'hôte (les deux modes)
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// ParseMask accepte « 24 », « /24 » ou « 255.255.255.0 ».
func ParseMask(s string) (int, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "/")
	if n, err := strconv.Atoi(s); err == nil {
		if n < 1 || n > 32 {
			return 0, errors.New("préfixe de 1 à 32")
		}
		return n, nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() {
		return 0, fmt.Errorf("masque invalide %q (ex. 255.255.255.0 ou 24)", s)
	}
	b := a.As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	n := 0
	for v&0x80000000 != 0 {
		n++
		v <<= 1
	}
	if v != 0 || n == 0 {
		return 0, fmt.Errorf("masque non contigu %q", s)
	}
	return n, nil
}

// Mask renvoie le masque pointé d'un préfixe.
func Mask(prefix int) string {
	v := ^uint32(0) << (32 - prefix)
	return fmt.Sprintf("%d.%d.%d.%d", v>>24, v>>16&255, v>>8&255, v&255)
}

// Validate contrôle et normalise la configuration.
func (n *Network) Validate() error {
	n.Hostname = strings.TrimSpace(n.Hostname)
	if n.Hostname != "" && !hostRe.MatchString(n.Hostname) {
		return fmt.Errorf("nom d'hôte invalide %q (lettres, chiffres, tirets, 63 caractères au plus)", n.Hostname)
	}
	switch n.Mode {
	case "dhcp":
		n.Address, n.Prefix, n.Gateway, n.DNS = "", 0, "", nil
		return nil
	case "static":
	default:
		return errors.New("mode : dhcp ou static")
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(n.Address))
	if err != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return fmt.Errorf("adresse IPv4 invalide %q", n.Address)
	}
	if n.Prefix < 1 || n.Prefix > 32 {
		return errors.New("préfixe de 1 à 32")
	}
	pfx := netip.PrefixFrom(ip, n.Prefix).Masked()
	if n.Prefix < 31 && (ip == pfx.Addr() || ip == broadcast(pfx)) {
		return fmt.Errorf("%s est l'adresse de réseau ou de diffusion de %s", ip, pfx)
	}
	n.Address = ip.String()
	if g := strings.TrimSpace(n.Gateway); g != "" {
		gw, err := netip.ParseAddr(g)
		if err != nil || !gw.Is4() {
			return fmt.Errorf("passerelle invalide %q", g)
		}
		if !pfx.Contains(gw) {
			return fmt.Errorf("la passerelle %s n'est pas dans le réseau %s", gw, pfx)
		}
		if gw == ip {
			return errors.New("la passerelle ne peut pas être l'adresse de l'appliance")
		}
		n.Gateway = gw.String()
	} else {
		n.Gateway = ""
	}
	var dns []string
	for _, d := range n.DNS {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		a, err := netip.ParseAddr(d)
		if err != nil {
			return fmt.Errorf("serveur DNS invalide %q", d)
		}
		dns = append(dns, a.String())
	}
	if len(dns) > 3 {
		return errors.New("trois serveurs DNS au plus")
	}
	n.DNS = dns
	return nil
}

func broadcast(p netip.Prefix) netip.Addr {
	b := p.Addr().As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v |= ^(^uint32(0) << (32 - p.Bits()))
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// Encode sérialise la configuration (clé=valeur, une par ligne).
func (n Network) Encode() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "mode=%s\nhostname=%s\n", n.Mode, n.Hostname)
	if n.Mode == "static" {
		fmt.Fprintf(&b, "address=%s\nprefix=%d\ngateway=%s\ndns=%s\n", n.Address, n.Prefix, n.Gateway, strings.Join(n.DNS, ","))
	}
	return b.Bytes()
}

// Decode relit la configuration et la revalide.
func Decode(b []byte) (*Network, error) {
	n := &Network{}
	for _, l := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		switch k {
		case "mode":
			n.Mode = v
		case "hostname":
			n.Hostname = v
		case "address":
			n.Address = v
		case "prefix":
			n.Prefix, _ = strconv.Atoi(v)
		case "gateway":
			n.Gateway = v
		case "dns":
			if v != "" {
				n.DNS = strings.Split(v, ",")
			}
		}
	}
	if err := n.Validate(); err != nil {
		return nil, fmt.Errorf("%s invalide : %w", NetworkFile, err)
	}
	return n, nil
}

// System regroupe les chemins et l'exécution des commandes (remplaçables dans les tests).
type System struct {
	Root string                                                          // racine du système de fichiers ("" ou "/")
	Data string                                                          // /data/vigie
	Run  func(stdin []byte, name string, args ...string) ([]byte, error) // exécution d'une commande
}

// Default est le système réel.
func Default(data string) *System {
	return &System{Root: "/", Data: data, Run: func(stdin []byte, name string, args ...string) ([]byte, error) {
		cmd := exec.Command(name, args...)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		return cmd.CombinedOutput()
	}}
}

func (s *System) path(p string) string { return filepath.Join(s.Root, p) }

// Saved renvoie la configuration réseau choisie en console (nil : OVF ou DHCP par défaut).
func (s *System) Saved() *Network {
	b, err := os.ReadFile(filepath.Join(s.Data, NetworkFile))
	if err != nil {
		return nil
	}
	n, err := Decode(b)
	if err != nil {
		log.Printf("sysconf: %v", err)
		return nil
	}
	return n
}

// render écrit /etc/network/interfaces, /etc/resolv.conf (statique) et /etc/hostname.
func (s *System) render(n *Network) error {
	var b bytes.Buffer
	b.WriteString("# écrit par vigie (configuration de la console)\nauto lo\niface lo inet loopback\n\nauto eth0\n")
	if n.Mode == "dhcp" {
		b.WriteString("iface eth0 inet dhcp\n")
	} else {
		fmt.Fprintf(&b, "iface eth0 inet static\n    address %s\n    netmask %s\n", n.Address, Mask(n.Prefix))
		if n.Gateway != "" {
			fmt.Fprintf(&b, "    gateway %s\n", n.Gateway)
		}
	}
	if err := writeFile(s.path("etc/network/interfaces"), b.Bytes(), 0o644); err != nil {
		return err
	}
	if n.Mode == "static" && len(n.DNS) > 0 {
		var r bytes.Buffer
		for _, d := range n.DNS {
			fmt.Fprintf(&r, "nameserver %s\n", d)
		}
		if err := writeFile(s.path("etc/resolv.conf"), r.Bytes(), 0o644); err != nil {
			return err
		}
	}
	if n.Hostname != "" {
		if err := writeFile(s.path("etc/hostname"), []byte(n.Hostname+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(p string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".vigie-tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Boot applique au démarrage la configuration choisie en console, s'il y en a une (appelé par
// init.sh après la lecture des propriétés OVF, avant le réseau). Renvoie true si appliquée.
func (s *System) Boot() (bool, error) {
	n := s.Saved()
	if n == nil {
		return false, nil
	}
	log.Printf("sysconf: configuration réseau de la console appliquée au boot (%s)", describe(n))
	return true, s.render(n)
}

func describe(n *Network) string {
	if n.Mode == "dhcp" {
		return "DHCP, nom " + n.Hostname
	}
	return fmt.Sprintf("statique %s/%d, passerelle %s, DNS %s, nom %s", n.Address, n.Prefix, n.Gateway, strings.Join(n.DNS, " "), n.Hostname)
}

// ApplyNetwork est LA fonction qui change le réseau de l'appliance : validation, persistance,
// fichiers système, redémarrage du réseau. Journalisée.
func (s *System) ApplyNetwork(n Network) error {
	if err := n.Validate(); err != nil {
		return err
	}
	log.Printf("sysconf: CHANGEMENT du réseau de gestion demandé en console : %s", describe(&n))
	if err := writeFile(filepath.Join(s.Data, NetworkFile), n.Encode(), 0o600); err != nil {
		return err
	}
	// eth0 est descendue avec l'ANCIENNE configuration (ifdown arrête udhcpc s'il était en DHCP),
	// puis la nouvelle est écrite et remontée. Pas de « networking restart » : OpenRC arrêterait
	// aussi les services qui dépendent du réseau, dont ce serveur et dropbear.
	if out, err := s.Run(nil, "ifdown", "eth0"); err != nil {
		log.Printf("sysconf: ifdown eth0 : %v %s", err, strings.TrimSpace(string(out)))
	}
	_, _ = s.Run(nil, "ip", "addr", "flush", "dev", "eth0")
	if err := s.render(&n); err != nil {
		return err
	}
	if n.Hostname != "" {
		if out, err := s.Run(nil, "hostname", n.Hostname); err != nil {
			log.Printf("sysconf: hostname : %v %s", err, out)
		}
	}
	if out, err := s.Run(nil, "ifup", "eth0"); err != nil {
		return fmt.Errorf("ifup eth0 : %v %s", err, strings.TrimSpace(string(out)))
	}
	_, _ = s.Run(nil, "/usr/lib/vigie/update-issue")
	return nil
}

// SetPassword est LA fonction qui change le mot de passe root (et donc celui de l'interface web,
// via setUI) : /etc/shadow, hash persisté, mot de passe affiché sur la console. Journalisée.
func (s *System) SetPassword(pw string, setUI func(string) error) error {
	return s.setPassword(pw, setUI, true)
}

// SetPasswordCloud applique un mot de passe venu des métadonnées cloud : comme SetPassword, sans le
// marquer « choisi en console » (la console garde la priorité).
func (s *System) SetPasswordCloud(pw string, setUI func(string) error) error {
	return s.setPassword(pw, setUI, false)
}

func (s *System) setPassword(pw string, setUI func(string) error, console bool) error {
	if err := CheckPassword(pw); err != nil {
		return err
	}
	out, err := s.Run([]byte(pw+"\n"), "cryptpw", "-m", "sha512")
	if err != nil {
		return fmt.Errorf("cryptpw : %v %s", err, out)
	}
	hash := strings.TrimSpace(string(out))
	if !strings.HasPrefix(hash, "$6$") {
		return errors.New("cryptpw : empreinte inattendue")
	}
	if err := setUI(pw); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(s.Data, "root.hash"), []byte(hash+"\n"), 0o600); err != nil {
		return err
	}
	if out, err := s.Run([]byte("root:"+hash+"\n"), "chpasswd", "-e"); err != nil {
		return fmt.Errorf("chpasswd : %v %s", err, out)
	}
	if err := writeFile(filepath.Join(s.Data, "first-access-token"), []byte(pw+"\n"), 0o600); err != nil {
		return err
	}
	if console {
		if err := writeFile(filepath.Join(s.Data, PasswordFlag), []byte("changé depuis la console\n"), 0o600); err != nil {
			return err
		}
	}
	_, _ = s.Run(nil, "/usr/lib/vigie/update-issue")
	origin := "la console"
	if !console {
		origin = "les métadonnées cloud"
	}
	log.Printf("sysconf: CHANGEMENT du mot de passe root (et de l'interface web) depuis %s", origin)
	return nil
}

// CheckPassword : 8 à 64 caractères imprimables, sans espace aux extrémités.
func CheckPassword(pw string) error {
	if len([]rune(pw)) < 8 || len([]rune(pw)) > 64 {
		return errors.New("mot de passe : 8 à 64 caractères")
	}
	if strings.TrimSpace(pw) != pw {
		return errors.New("mot de passe : pas d'espace au début ni à la fin")
	}
	for _, r := range pw {
		if r < 0x20 || r == 0x7f || r > 0x7e {
			return errors.New("mot de passe : caractères ASCII imprimables uniquement (saisie possible sur toute console)")
		}
	}
	return nil
}

// Power arrête ou redémarre l'appliance (action reboot ou poweroff). Journalisée.
func (s *System) Power(action string) error {
	switch action {
	case "reboot", "poweroff":
	default:
		return errors.New("action : reboot ou poweroff")
	}
	log.Printf("sysconf: %s demandé depuis la console", action)
	out, err := s.Run(nil, action)
	if err != nil {
		return fmt.Errorf("%s : %v %s", action, err, out)
	}
	return nil
}
