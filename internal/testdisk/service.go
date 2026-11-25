package testdisk

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"vigie/internal/store"
	"vigie/internal/vsphere"
)

// State est l'état complet du disque de test exposé à l'API.
type State struct {
	Record    *Record    `json:"record"`               // nil : aucun disque de test
	InVSphere *bool      `json:"in_vsphere,omitempty"` // nil : non vérifié (pas de session)
	Guest     *Verified  `json:"guest,omitempty"`      // détection et contrôles dans l'invité
	Signature *Signature `json:"signature,omitempty"`
	Ready     bool       `json:"ready"` // contrôles passés et signature valide : le bench peut l'utiliser
	Error     string     `json:"error,omitempty"`
}

// Service orchestre vSphere, l'invité et la persistance.
type Service struct {
	Store *store.Store
	Guest Guest
	mu    sync.Mutex
}

// NewService crée le service sur le store et la vue système réelle.
func NewService(s *store.Store) *Service { return &Service{Store: s, Guest: DefaultGuest} }

// Record lit l'enregistrement courant (nil si aucun).
func (s *Service) Record() *Record {
	var rec Record
	if err := s.Store.Load(RecordFile, &rec); err != nil {
		return nil
	}
	return &rec
}

// Status recalcule l'état : enregistrement, présence côté vSphere, détection et contrôles invité.
func (s *Service) Status(ctx context.Context, sess *vsphere.Session) *State {
	st := &State{Record: s.Record()}
	if st.Record == nil {
		return st
	}
	if sess != nil && st.Record.Mode != ModeLocal {
		disk, err := Lookup(ctx, sess, st.Record)
		if err != nil {
			st.Error = err.Error()
		} else {
			present := disk != nil
			st.InVSphere = &present
		}
	}
	dev, err := s.Guest.FindByUUID(st.Record.BackingUUID, 0)
	if err != nil {
		st.Error = joinErr(st.Error, err.Error())
		return st
	}
	st.Guest = s.Guest.Verify(dev, st.Record)
	sig, err := ReadSignature(dev.Path)
	VerifySignature(st.Guest, sig, err)
	if err == nil {
		st.Signature = sig
	}
	st.Ready = st.Guest.OK()
	return st
}

// Create crée le disque de test de bout en bout : vSphere, rescan, détection, contrôles, signature.
func (s *Service) Create(ctx context.Context, sess *vsphere.Session, self *vsphere.Self, dsRef string, sizeGB int, progress Progress) (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if progress == nil {
		progress = func(int, string) {}
	}
	if rec := s.Record(); rec != nil {
		return nil, fmt.Errorf("un disque de test existe déjà (%s) : le supprimer d'abord", rec.FileName)
	}
	vmRef, ok := vsphere.Ref(self.VM.Ref)
	if !ok {
		return nil, errors.New("référence de VM invalide")
	}
	ds, ok := vsphere.Ref(dsRef)
	if !ok {
		return nil, errors.New("référence de datastore invalide")
	}
	progress(0, "création du disque dans vSphere")
	rec, err := Create(ctx, sess, vmRef, ds, sizeGB, progress)
	if err != nil {
		return nil, err
	}
	if err := s.Store.Save(RecordFile, rec); err != nil {
		log.Printf("testdisk: ATTENTION enregistrement non persisté : %v", err)
	}
	progress(60, "recherche du disque dans l'invité")
	dev, err := s.Guest.FindByUUID(rec.BackingUUID, 30*time.Second)
	if err != nil {
		return &State{Record: rec, Error: err.Error()}, err
	}
	progress(80, "contrôles de sécurité")
	v := s.Guest.Verify(dev, rec)
	st := &State{Record: rec, Guest: v}
	if !v.OK() {
		st.Error = "contrôles de sécurité en échec : aucune écriture effectuée"
		return st, errors.New(st.Error)
	}
	sig := Signature{UUID: dev.UUID, CreatedAt: rec.CreatedAt, SizeBytes: rec.SizeBytes}
	progress(90, "écriture de la signature (bloc 0)")
	if err := WriteSignature(v, sig); err != nil {
		st.Error = err.Error()
		return st, err
	}
	got, err := ReadSignature(dev.Path)
	VerifySignature(v, got, err)
	st.Signature, st.Ready = got, v.OK()
	progress(100, "disque de test prêt")
	return st, nil
}

// Delete supprime le disque de test : contrôle invité (jamais un disque monté), vSphere, oubli.
func (s *Service) Delete(ctx context.Context, sess *vsphere.Session, progress Progress) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if progress == nil {
		progress = func(int, string) {}
	}
	rec := s.Record()
	if rec == nil {
		return nil
	}
	if rec.Mode == ModeLocal {
		return errors.New("disque ajouté à la main (mode local) : le libérer depuis la console, puis le retirer dans vSphere")
	}
	if dev, err := s.Guest.FindByUUID(rec.BackingUUID, 0); err == nil {
		if m := s.Guest.mountedDevices(); m[dev.Name] {
			return fmt.Errorf("refus : %s est monté", dev.Path)
		}
		defer func() {
			if err := s.Guest.Forget(dev); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("testdisk: retrait de %s de l'invité : %v", dev.Name, err)
			}
		}()
	}
	progress(10, "suppression du disque dans vSphere")
	if err := Remove(ctx, sess, rec, progress); err != nil {
		return err
	}
	progress(95, "oubli de l'enregistrement")
	return s.Store.Remove(RecordFile)
}

func joinErr(a, b string) string {
	if a == "" {
		return b
	}
	return a + " ; " + b
}
