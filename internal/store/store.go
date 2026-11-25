// Package store persiste de petits documents JSON sous /data/vigie.
//
// Toutes les écritures passent par une goroutine avec délai (§4.2 du plan) : si le datastore qui
// porte /data est malade, l'appelant reçoit ErrTimeout mais n'est jamais bloqué.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrTimeout signale qu'une écriture n'a pas abouti dans le délai ; elle continue en arrière-plan.
var ErrTimeout = errors.New("store: écriture trop lente (datastore /data dégradé ?)")

// Store est un répertoire de documents JSON.
type Store struct {
	Dir     string
	Timeout time.Duration
}

// New ouvre (et crée si besoin) le répertoire.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return &Store{Dir: dir, Timeout: 5 * time.Second}, nil
}

// Path renvoie le chemin complet d'un document.
func (s *Store) Path(name string) string { return filepath.Join(s.Dir, name) }

// Save écrit v en JSON dans name, de façon atomique (fichier temporaire puis rename), avec délai.
func (s *Store) Save(name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.WriteBytes(name, data, 0o600)
}

// WriteBytes écrit data dans name avec le mode donné, atomiquement et avec délai.
func (s *Store) WriteBytes(name string, data []byte, mode os.FileMode) error {
	done := make(chan error, 1)
	go func() {
		tmp := s.Path(name) + ".tmp"
		err := os.WriteFile(tmp, data, mode)
		if err == nil {
			err = os.Rename(tmp, s.Path(name))
		}
		if err != nil {
			os.Remove(tmp)
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(s.Timeout):
		return ErrTimeout
	}
}

// Load lit name dans v. Renvoie os.ErrNotExist si le document n'existe pas.
func (s *Store) Load(name string, v any) error {
	data, err := os.ReadFile(s.Path(name))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// Exists indique si le document existe.
func (s *Store) Exists(name string) bool {
	_, err := os.Stat(s.Path(name))
	return err == nil
}

// Remove supprime le document (pas d'erreur s'il n'existe pas).
func (s *Store) Remove(name string) error {
	err := os.Remove(s.Path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
