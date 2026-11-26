package testdisk

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Mode local (phase T) : sans vCenter, l'utilisateur ajoute lui-même un disque à la VM dans le
// client vSphere, puis l'appliance l'adopte. Déroulé :
//
//  1. LocalPrepare : instantané des disques présents (avant l'ajout) ;
//  2. l'utilisateur ajoute le disque et confirme ;
//  3. LocalScan : rescan, liste des disques apparus depuis l'instantané, avec tous les contrôles ;
//  4. LocalAdopt : l'utilisateur retape le nom du disque ; si tous les contrôles passent (ceux du
//     §6.3 via Verify, plus : apparu après l'instantané, vierge, taille suffisante), la signature est
//     écrite par WriteSignature, seule écriture possible, puis l'enregistrement est persisté.
//
// Le disque n'est jamais retiré ni effacé par l'appliance : LocalRelease oublie l'enregistrement et
// l'utilisateur retire le disque dans vSphere.

// ModeLocal marque un disque de test adopté sans vCenter.
const ModeLocal = "local"

// LocalExpectFile est l'instantané des disques pris avant l'ajout manuel.
const LocalExpectFile = "localdisk-expect.json"

// Taille minimale et taille conseillée d'un disque local.
const (
	LocalMinBytes   = 2 << 30
	LocalAdviseGiB  = 16
	localProbeBytes = 1 << 20 // premier et dernier Mio lus pour vérifier que le disque est vierge
)

// Expect est l'instantané pris par LocalPrepare.
type Expect struct {
	At    time.Time `json:"at"`
	Known []string  `json:"known"` // clés des disques déjà présents
	Disks []Device  `json:"disks"`
}

// Candidate est un disque apparu depuis l'instantané, avec le résultat des contrôles.
type Candidate struct {
	Device     Device  `json:"device"`
	Controller string  `json:"controller"` // pilote du contrôleur (vmw_pvscsi…)
	Bus        int     `json:"bus"`        // numéro du contrôleur SCSI dans l'invité (host)
	Unit       int     `json:"unit"`       // numéro d'unité (cible SCSI)
	Checks     []Check `json:"checks"`
	OK         bool    `json:"ok"`
}

func diskKey(d Device) string {
	if d.WWID != "" {
		return "wwid:" + d.WWID
	}
	return "dev:" + d.Name + ":" + d.MajMin
}

// LocalPrepare prend l'instantané des disques avant l'ajout manuel.
func (s *Service) LocalPrepare() (*Expect, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec := s.Record(); rec != nil {
		return nil, fmt.Errorf("un disque de test est déjà en place (%s) : le libérer d'abord", rec.FileName)
	}
	_ = s.Guest.Rescan()
	disks, err := s.Guest.Disks()
	if err != nil {
		return nil, err
	}
	e := &Expect{At: time.Now().UTC(), Disks: disks}
	for _, d := range disks {
		e.Known = append(e.Known, diskKey(d))
	}
	if err := s.Store.Save(LocalExpectFile, e); err != nil {
		return nil, err
	}
	log.Printf("testdisk: mode local, instantané de %d disque(s) avant ajout manuel", len(disks))
	return e, nil
}

// LocalExpect renvoie l'instantané en cours (nil si aucun).
func (s *Service) LocalExpect() *Expect {
	var e Expect
	if err := s.Store.Load(LocalExpectFile, &e); err != nil {
		return nil
	}
	return &e
}

// LocalCancel abandonne l'instantané.
func (s *Service) LocalCancel() error { return s.Store.Remove(LocalExpectFile) }

// LocalScan relance la détection et contrôle chaque disque apparu depuis l'instantané (lecture seule).
func (s *Service) LocalScan(wait time.Duration) ([]Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.localScan(wait)
}

func (s *Service) localScan(wait time.Duration) ([]Candidate, error) {
	e := s.LocalExpect()
	if e == nil {
		return nil, errors.New("aucune demande d'ajout en cours : commencer par « préparer »")
	}
	known := map[string]bool{}
	for _, k := range e.Known {
		known[k] = true
	}
	deadline := time.Now().Add(wait)
	for {
		_ = s.Guest.Rescan()
		disks, err := s.Guest.Disks()
		if err != nil {
			return nil, err
		}
		var out []Candidate
		for _, d := range disks {
			if known[diskKey(d)] {
				continue
			}
			out = append(out, s.localCheck(d, e))
		}
		if len(out) > 0 || time.Now().After(deadline) {
			return out, nil
		}
		time.Sleep(time.Second)
	}
}

// localRecord construit l'enregistrement d'un disque local (sans le persister).
func localRecord(d Device, label string) *Record {
	unit := -1
	if p := strings.Split(d.HCTL, ":"); len(p) == 4 {
		unit, _ = strconv.Atoi(p[2])
	}
	name := fmt.Sprintf("disque local %s (SCSI %s)", d.Name, busUnit(d.HCTL))
	return &Record{DatastoreName: label, SizeBytes: d.SizeBytes, FileName: name, BackingUUID: d.UUID,
		UnitNumber: int32(unit), Provisioning: "inconnu (non visible depuis la VM)", CreatedAt: time.Now().UTC(), Mode: ModeLocal}
}

func busUnit(hctl string) string {
	p := strings.Split(hctl, ":")
	if len(p) != 4 {
		return "?"
	}
	return p[0] + ":" + p[2]
}

// localCheck applique Verify (§6.3) et les contrôles propres au mode local.
func (s *Service) localCheck(d Device, e *Expect) Candidate {
	c := Candidate{Device: d, Bus: -1, Unit: -1}
	if p := strings.Split(d.HCTL, ":"); len(p) == 4 {
		c.Bus, _ = strconv.Atoi(p[0])
		c.Unit, _ = strconv.Atoi(p[2])
		c.Controller = readTrim(filepath.Join(s.Guest.Sys, "class/scsi_host", "host"+p[0], "proc_name"))
	}
	rec := localRecord(d, "")
	v := s.Guest.Verify(&d, rec)
	var local []Check // contrôles propres au mode local, présentés en premier
	add := func(name string, ok bool, detail string) { local = append(local, Check{name, ok, detail}) }
	blank, detail := s.blank(d)
	add("disque vierge", blank, detail)
	add("apparu après la demande", true, fmt.Sprintf("absent de l'instantané du %s UTC", e.At.Format("02/01 15:04:05")))
	add("taille suffisante", d.SizeBytes >= LocalMinBytes, fmt.Sprintf("%d Gio (minimum %d Gio, conseillé %d Gio)", d.SizeBytes>>30, LocalMinBytes>>30, LocalAdviseGiB))
	add("identifiant unique (disk.EnableUUID)", d.UUID != "", d.WWID)
	v.Checks = append(local, v.Checks...)
	c.Checks = v.Checks
	c.OK = v.OK()
	return c
}

// blank lit le premier et le dernier Mio (lecture seule, O_DIRECT) : le disque doit être à zéro, ou
// porter déjà la signature vigie de ce même disque (réadoption).
func (s *Service) blank(d Device) (bool, string) {
	f, err := os.OpenFile(d.Path, os.O_RDONLY|syscall.O_DIRECT, 0)
	if errors.Is(err, syscall.EINVAL) && s.Guest.Dev != DefaultGuest.Dev {
		f, err = os.OpenFile(d.Path, os.O_RDONLY, 0) // fichier ordinaire (tests)
	}
	if err != nil {
		return false, fmt.Sprintf("lecture impossible : %v", err)
	}
	defer f.Close()
	if d.SizeBytes < 2*localProbeBytes {
		return false, "disque trop petit pour être contrôlé"
	}
	head := alignedBuffer(localProbeBytes)
	if _, err := f.ReadAt(head, 0); err != nil {
		return false, fmt.Sprintf("lecture du début : %v", err)
	}
	tail := alignedBuffer(localProbeBytes)
	if _, err := f.ReadAt(tail, d.SizeBytes-localProbeBytes); err != nil {
		return false, fmt.Sprintf("lecture de la fin : %v", err)
	}
	if sig, err := Decode(head[:SignatureSize]); err == nil {
		if strings.EqualFold(sig.UUID, d.UUID) && d.UUID != "" {
			return true, "signature vigie de ce disque déjà présente (réadoption)"
		}
		return false, fmt.Sprintf("signature vigie d'un autre disque (%s)", sig.UUID)
	}
	if allZero(head[SignatureSize:]) && allZero(head[:SignatureSize]) && allZero(tail) {
		return true, "premier et dernier Mio à zéro"
	}
	return false, fmt.Sprintf("données présentes : %s ; l'appliance n'écrit que sur un disque neuf", whatIs(head, tail))
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// whatIs reconnaît les signatures courantes (pour expliquer le refus).
func whatIs(head, tail []byte) string {
	var found []string
	if bytes.Equal(head[512:520], []byte("EFI PART")) {
		found = append(found, "table de partitions GPT")
	} else if head[510] == 0x55 && head[511] == 0xAA {
		found = append(found, "table de partitions MBR")
	}
	if binary.LittleEndian.Uint16(head[1080:1082]) == 0xEF53 {
		found = append(found, "système de fichiers ext2/3/4")
	}
	if bytes.Equal(head[0:4], []byte("XFSB")) {
		found = append(found, "système de fichiers XFS")
	}
	if bytes.Equal(head[512:520], []byte("LABELONE")) {
		found = append(found, "volume physique LVM")
	}
	if bytes.Equal(head[3:7], []byte("NTFS")) {
		found = append(found, "système de fichiers NTFS")
	}
	if len(found) == 0 {
		found = append(found, "contenu non reconnu")
	}
	return strings.Join(found, ", ")
}

// LocalAdopt adopte le disque name si confirm le reprend exactement et si tous les contrôles passent.
// C'est le seul chemin d'écriture du mode local, et il passe par WriteSignature.
func (s *Service) LocalAdopt(name, confirm, label string) (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec := s.Record(); rec != nil {
		return nil, fmt.Errorf("un disque de test est déjà en place (%s)", rec.FileName)
	}
	if name == "" || confirm != name {
		return nil, fmt.Errorf("confirmation refusée : retaper exactement le nom du disque (%q)", name)
	}
	if len(label) > 80 {
		return nil, errors.New("libellé : 80 caractères au maximum")
	}
	cands, err := s.localScan(0)
	if err != nil {
		return nil, err
	}
	var c *Candidate
	for i := range cands {
		if cands[i].Device.Name == name {
			c = &cands[i]
		}
	}
	if c == nil {
		return nil, fmt.Errorf("%s n'est pas un disque apparu depuis la demande d'ajout", name)
	}
	// les contrôles sont refaits ici, juste avant l'écriture
	e := s.LocalExpect()
	cc := s.localCheck(c.Device, e)
	rec := localRecord(c.Device, strings.TrimSpace(label))
	v := s.Guest.Verify(&c.Device, rec)
	v.Checks = cc.Checks
	st := &State{Record: rec, Guest: v}
	if !v.OK() {
		st.Error = "contrôles en échec : aucune écriture effectuée"
		return st, errors.New(st.Error)
	}
	log.Printf("testdisk: mode local, adoption de %s (%s, %d octets, SCSI %s) confirmée", c.Device.Path, c.Device.WWID, c.Device.SizeBytes, busUnit(c.Device.HCTL))
	if sig, err := ReadSignature(c.Device.Path); err != nil || !strings.EqualFold(sig.UUID, c.Device.UUID) {
		if err := WriteSignature(v, Signature{UUID: c.Device.UUID, CreatedAt: rec.CreatedAt, SizeBytes: rec.SizeBytes}); err != nil {
			st.Error = err.Error()
			return st, err
		}
	}
	got, err := ReadSignature(c.Device.Path)
	VerifySignature(v, got, err)
	st.Signature, st.Ready = got, v.OK()
	if !st.Ready {
		st.Error = "signature illisible après écriture"
		return st, errors.New(st.Error)
	}
	if err := s.Store.Save(RecordFile, rec); err != nil {
		return st, err
	}
	_ = s.Store.Remove(LocalExpectFile)
	return st, nil
}

// LocalRelease oublie un disque local (aucune écriture) ; l'utilisateur le retire dans vSphere.
func (s *Service) LocalRelease() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := s.Record()
	if rec == nil {
		return nil
	}
	if rec.Mode != ModeLocal {
		return errors.New("disque créé par l'appliance via vCenter : le supprimer depuis l'onglet Datastore performance")
	}
	log.Printf("testdisk: mode local, %s libéré (non effacé, à retirer dans vSphere)", rec.FileName)
	return s.Store.Remove(RecordFile)
}
