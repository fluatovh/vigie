package testdisk

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Guest lit l'état des disques depuis sysfs/procfs. Les racines sont paramétrables pour les tests.
type Guest struct {
	Sys  string // /sys
	Proc string // /proc
	Dev  string // /dev
}

// DefaultGuest est la vue réelle du système.
var DefaultGuest = Guest{Sys: "/sys", Proc: "/proc", Dev: "/dev"}

// Device est un disque SCSI vu par l'invité.
type Device struct {
	Name      string `json:"name"` // sdb
	Path      string `json:"path"` // /dev/sdb
	WWID      string `json:"wwid"` // naa.6000c29...
	UUID      string `json:"uuid"` // UUID VMDK reconstitué depuis le wwid
	SizeBytes int64  `json:"size_bytes"`
	MajMin    string `json:"majmin"`
	Vendor    string `json:"vendor"`
	Model     string `json:"model"`
	HCTL      string `json:"hctl"` // host:channel:target:lun
}

// Check est le résultat d'un contrôle de sécurité.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Verified est le résultat de Verify : il ne peut être construit que par Verify.
type Verified struct {
	Device *Device `json:"device"`
	Checks []Check `json:"checks"`
}

// OK indique que tous les contrôles sont passés.
func (v *Verified) OK() bool {
	if v == nil || v.Device == nil || len(v.Checks) == 0 {
		return false
	}
	for _, c := range v.Checks {
		if !c.OK {
			return false
		}
	}
	return true
}

// Rescan demande à tous les contrôleurs SCSI de rechercher de nouveaux périphériques.
func (g Guest) Rescan() error {
	hosts, _ := filepath.Glob(filepath.Join(g.Sys, "class/scsi_host/host*/scan"))
	if len(hosts) == 0 {
		return errors.New("aucun contrôleur SCSI dans sysfs")
	}
	var last error
	for _, h := range hosts {
		if err := os.WriteFile(h, []byte("- - -\n"), 0o200); err != nil {
			last = err
		}
	}
	return last
}

// Forget retire un disque de l'invité (après suppression côté vSphere).
func (g Guest) Forget(dev *Device) error {
	return os.WriteFile(filepath.Join(g.Sys, "block", dev.Name, "device/delete"), []byte("1\n"), 0o200)
}

// WWIDFromUUID convertit un UUID VMDK (6000C292-9ca7-...) en wwid SCSI (naa.6000c2929ca7...).
func WWIDFromUUID(uuid string) string {
	return "naa." + strings.ToLower(strings.ReplaceAll(uuid, "-", ""))
}

// UUIDFromWWID fait l'inverse. Un NAA de 16 octets (VMware) redevient un UUID ; un NAA de 8 octets
// (QEMU, QA) est renvoyé tel quel en hexadécimal. Hors VMware (Proxmox, OpenStack : identifiants
// t10.… ou eui.…), l'UUID est dérivé du wwid (SHA-256), stable pour un même disque. Vide sinon.
func UUIDFromWWID(wwid string) string {
	w := strings.TrimSpace(wwid)
	h := strings.TrimPrefix(strings.ToLower(w), "naa.")
	if strings.HasPrefix(strings.ToLower(w), "naa.") {
		switch len(h) {
		case 32:
			return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
		case 16:
			return h
		}
		return ""
	}
	if len(w) < 8 {
		return ""
	}
	sum := sha256.Sum256([]byte(w))
	x := hex.EncodeToString(sum[:16])
	return "d" + x[1:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32]
}

// Disks liste les disques SCSI (sd*) de l'invité.
func (g Guest) Disks() ([]Device, error) {
	entries, err := filepath.Glob(filepath.Join(g.Sys, "block/sd*"))
	if err != nil {
		return nil, err
	}
	var out []Device
	for _, e := range entries {
		name := filepath.Base(e)
		d := Device{Name: name, Path: filepath.Join(g.Dev, name)}
		d.WWID = readTrim(filepath.Join(e, "device/wwid"))
		d.UUID = UUIDFromWWID(d.WWID)
		d.Vendor = readTrim(filepath.Join(e, "device/vendor"))
		d.Model = readTrim(filepath.Join(e, "device/model"))
		d.MajMin = readTrim(filepath.Join(e, "dev"))
		if sectors, err := strconv.ParseInt(readTrim(filepath.Join(e, "size")), 10, 64); err == nil {
			d.SizeBytes = sectors * 512
		}
		if link, err := os.Readlink(filepath.Join(e, "device")); err == nil {
			d.HCTL = filepath.Base(link)
		}
		out = append(out, d)
	}
	return out, nil
}

// FindByUUID cherche le disque dont le wwid correspond à l'UUID VMDK, en relançant un rescan
// jusqu'au délai donné (le disque ajouté à chaud met quelques secondes à apparaître).
func (g Guest) FindByUUID(uuid string, wait time.Duration) (*Device, error) {
	want := WWIDFromUUID(uuid)
	deadline := time.Now().Add(wait)
	match := func(d Device) bool { return d.WWID == want || (d.UUID != "" && strings.EqualFold(d.UUID, uuid)) }
	for {
		disks, err := g.Disks()
		if err != nil {
			return nil, err
		}
		for i := range disks {
			if match(disks[i]) {
				return &disks[i], nil
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("aucun disque avec le wwid %s (disk.EnableUUID absent ? disque non attaché ?)", want)
		}
		_ = g.Rescan()
		time.Sleep(time.Second)
	}
}

// Verify applique les contrôles du §6.3 à un périphérique candidat pour le disque de test décrit
// par rec. Aucun contrôle ne peut être sauté ; le résultat liste chacun d'eux.
func (g Guest) Verify(dev *Device, rec *Record) *Verified {
	v := &Verified{Device: dev}
	add := func(name string, ok bool, detail string) { v.Checks = append(v.Checks, Check{name, ok, detail}) }

	// 1. ajouté par l'appliance : un enregistrement existe et désigne ce VMDK
	if rec == nil || rec.BackingUUID == "" {
		add("ajouté par l'appliance", false, "aucun enregistrement de création")
	} else if rec.Mode == ModeLocal {
		add("adopté par l'appliance (mode local)", true, fmt.Sprintf("ajouté à la main dans vSphere, %s", rec.FileName))
	} else {
		add("ajouté par l'appliance", true, fmt.Sprintf("VMDK %s (clé %d, unité %d)", rec.FileName, rec.DeviceKey, rec.UnitNumber))
	}
	// 2. numéro de série = UUID du VMDK
	if rec != nil && dev.WWID != "" && UUIDFromWWID(dev.WWID) != "" && strings.EqualFold(UUIDFromWWID(dev.WWID), rec.BackingUUID) {
		add("numéro de série = UUID du VMDK", true, dev.WWID)
	} else {
		add("numéro de série = UUID du VMDK", false, fmt.Sprintf("wwid %q, attendu %q", dev.WWID, WWIDFromUUID(stringOr(rec))))
	}
	// 2b. unité SCSI annoncée par vSphere (hctl host:canal:cible:lun → cible = unité)
	if rec != nil && dev.HCTL != "" {
		parts := strings.Split(dev.HCTL, ":")
		if len(parts) == 4 && parts[2] == strconv.Itoa(int(rec.UnitNumber)) {
			add("unité SCSI attendue", true, dev.HCTL)
		} else {
			add("unité SCSI attendue", false, fmt.Sprintf("hctl %s, unité attendue %d", dev.HCTL, rec.UnitNumber))
		}
	}
	// 3. ni monté, ni partitionné, ni disque de boot, ni utilisé (holders)
	mounted := g.mountedDevices()
	if mounted[dev.Name] {
		add("non monté", false, "une partition ou le disque est monté")
	} else {
		add("non monté", true, "")
	}
	parts, _ := filepath.Glob(filepath.Join(g.Sys, "block", dev.Name, dev.Name+"*"))
	add("sans partition", len(parts) == 0, fmt.Sprintf("%d partition(s)", len(parts)))
	holders, _ := os.ReadDir(filepath.Join(g.Sys, "block", dev.Name, "holders"))
	add("sans utilisateur (LVM, md…)", len(holders) == 0, fmt.Sprintf("%d holder(s)", len(holders)))
	boot := g.bootDevices()
	if boot[dev.Name] {
		add("différent du disque système", false, "ce disque porte le média de boot ou /data")
	} else {
		add("différent du disque système", true, fmt.Sprintf("système : %s", strings.Join(keys(boot), ",")))
	}
	// 3b. le nœud /dev/sdX désigne bien le disque de sysfs (majeur:mineur identiques)
	if mm, err := devNodeMajMin(dev.Path); errors.Is(err, errRegularFile) && g.Dev != DefaultGuest.Dev {
		add("nœud /dev cohérent", true, "fichier ordinaire (tests)")
	} else if err != nil {
		add("nœud /dev cohérent", false, err.Error())
	} else if mm != dev.MajMin {
		add("nœud /dev cohérent", false, fmt.Sprintf("%s est %s, sysfs annonce %s", dev.Path, mm, dev.MajMin))
	} else {
		add("nœud /dev cohérent", true, mm)
	}
	// 4. taille
	if rec != nil && dev.SizeBytes == rec.SizeBytes {
		add("taille attendue", true, fmt.Sprintf("%d octets", dev.SizeBytes))
	} else {
		add("taille attendue", false, fmt.Sprintf("%d octets, attendu %d", dev.SizeBytes, sizeOr(rec)))
	}
	// vendeur/modèle : VMware "Virtual disk" (informatif mais bloquant : on ne touche qu'à des VMDK)
	if strings.HasPrefix(strings.ToUpper(dev.Vendor), "VMWARE") || dev.Vendor == "" && dev.Model == "" {
		add("disque virtuel VMware", true, strings.TrimSpace(dev.Vendor+" "+dev.Model))
	} else {
		add("disque virtuel VMware", strings.Contains(strings.ToLower(dev.Model), "virtual") || strings.EqualFold(dev.Vendor, "QEMU"), strings.TrimSpace(dev.Vendor+" "+dev.Model))
	}
	return v
}

// VerifySignature complète Verify pour un réemploi : le bloc 0 doit porter la signature avec le même UUID.
func VerifySignature(v *Verified, sig *Signature, err error) {
	switch {
	case err != nil:
		v.Checks = append(v.Checks, Check{"signature vigie", false, err.Error()})
	case !strings.EqualFold(sig.UUID, v.Device.UUID):
		v.Checks = append(v.Checks, Check{"signature vigie", false, fmt.Sprintf("UUID signé %s ≠ %s", sig.UUID, v.Device.UUID)})
	default:
		v.Checks = append(v.Checks, Check{"signature vigie", true, fmt.Sprintf("créée le %s, préremplie %d octets", sig.CreatedAt.Format(time.RFC3339), sig.PrefilledBytes)})
	}
}

// mountedDevices renvoie les disques de base (sdX) dont une partition ou le disque entier est monté.
func (g Guest) mountedDevices() map[string]bool {
	out := map[string]bool{}
	f, err := os.Open(filepath.Join(g.Proc, "mounts"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "/dev/") {
			continue
		}
		out[baseDisk(strings.TrimPrefix(fields[0], "/dev/"))] = true
	}
	return out
}

// bootDevices : les disques qui portent le média de boot (/media/*) ou /data.
func (g Guest) bootDevices() map[string]bool {
	out := map[string]bool{}
	f, err := os.Open(filepath.Join(g.Proc, "mounts"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || !strings.HasPrefix(fields[0], "/dev/sd") {
			continue
		}
		if strings.HasPrefix(fields[1], "/media/") || fields[1] == "/data" || fields[1] == "/" {
			out[baseDisk(strings.TrimPrefix(fields[0], "/dev/"))] = true
		}
	}
	return out
}

func baseDisk(name string) string {
	return strings.TrimRight(name, "0123456789")
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func stringOr(r *Record) string {
	if r == nil {
		return ""
	}
	return r.BackingUUID
}

func sizeOr(r *Record) int64 {
	if r == nil {
		return 0
	}
	return r.SizeBytes
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
