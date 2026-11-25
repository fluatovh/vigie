package netdiag

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
)

// Iface est une interface IPv4 de l'appliance.
type Iface struct {
	Name string `json:"name"`
	MAC  string `json:"mac"`
	CIDR string `json:"cidr"`
	MTU  int    `json:"mtu"`
	Up   bool   `json:"up"`
}

// Local décrit le réseau de l'appliance.
type Local struct {
	Ifaces  []Iface  `json:"interfaces"`
	Gateway string   `json:"gateway"`
	DNS     []string `json:"dns"`
}

// ProcRoot est remplaçable dans les tests.
var ProcRoot = "/proc"

// LocalInfo lit les interfaces, la passerelle par défaut et les DNS.
func LocalInfo() Local {
	var l Local
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				l.Ifaces = append(l.Ifaces, Iface{Name: i.Name, MAC: i.HardwareAddr.String(), CIDR: ipn.String(), MTU: i.MTU, Up: i.Flags&net.FlagUp != 0})
			}
		}
	}
	l.Gateway = defaultGateway()
	if f, err := os.Open("/etc/resolv.conf"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fs := strings.Fields(sc.Text())
			if len(fs) >= 2 && fs[0] == "nameserver" {
				l.DNS = append(l.DNS, fs[1])
			}
		}
		f.Close()
	}
	return l
}

func defaultGateway() string {
	f, err := os.Open(ProcRoot + "/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) >= 3 && fs[1] == "00000000" {
			b, err := hex.DecodeString(fs[2])
			if err == nil && len(b) == 4 {
				return net.IPv4(b[3], b[2], b[1], b[0]).String()
			}
		}
	}
	return ""
}

// OnLink indique si l'adresse est dans le sous-réseau d'une interface de l'appliance.
func OnLink(ip net.IP, l Local) bool {
	for _, i := range l.Ifaces {
		if _, n, err := net.ParseCIDR(i.CIDR); err == nil && n.Contains(ip) {
			return true
		}
	}
	return false
}

// ARPEntry est une entrée de la table ARP.
type ARPEntry struct {
	IP     string `json:"ip"`
	MAC    string `json:"mac"`
	Device string `json:"device"`
	State  string `json:"state"` // complète, incomplète, permanente (+ état NUD si connu)
}

// ARP lit /proc/net/arp et complète avec l'état NUD (`ip neigh`).
func ARP() ([]ARPEntry, error) {
	f, err := os.Open(ProcRoot + "/net/arp")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := []ARPEntry{}
	sc := bufio.NewScanner(f)
	sc.Scan() // en-tête
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) < 6 {
			continue
		}
		state := "complète"
		switch fs[2] {
		case "0x0":
			state = "incomplète"
		case "0x6":
			state = "permanente"
		}
		out = append(out, ARPEntry{IP: fs[0], MAC: fs[3], Device: fs[5], State: state})
	}
	if nud, err := neighStates(); err == nil {
		for i := range out {
			if s, ok := nud[out[i].IP]; ok {
				out[i].State += " (" + s + ")"
			}
		}
	}
	return out, nil
}

func neighStates() (map[string]string, error) {
	b, err := exec.Command("ip", "-4", "neigh", "show").Output()
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		fs := strings.Fields(l)
		if len(fs) >= 2 {
			last := fs[len(fs)-1]
			if last == strings.ToUpper(last) && net.ParseIP(fs[0]) != nil {
				m[fs[0]] = last
			}
		}
	}
	return m, nil
}

// DeleteARP supprime une entrée (ip valide et présente dans la table), ou toute la table si ip == "".
// La commande est lancée sans shell avec des arguments validés.
func DeleteARP(ip string) error {
	entries, err := ARP()
	if err != nil {
		return err
	}
	if ip == "" {
		for _, e := range entries {
			if err := flush(e); err != nil {
				return err
			}
		}
		return nil
	}
	if p := net.ParseIP(ip); p == nil || p.To4() == nil {
		return errors.New("adresse IPv4 attendue")
	}
	for _, e := range entries {
		if e.IP == ip {
			return flush(e)
		}
	}
	return fmt.Errorf("%s absente de la table ARP", ip)
}

func flush(e ARPEntry) error {
	if strings.ContainsAny(e.Device, " /;") {
		return errors.New("interface invalide")
	}
	out, err := exec.Command("ip", "neigh", "flush", "to", e.IP, "dev", e.Device).CombinedOutput()
	if err != nil {
		return fmt.Errorf("suppression de %s : %v %s", e.IP, err, strings.TrimSpace(string(out)))
	}
	return nil
}
