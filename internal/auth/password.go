// Package auth gère le mot de passe de l'interface web et de l'API (bcrypt, stocké dans /data).
package auth

import (
	"crypto/subtle"
	"errors"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"

	"vigie/internal/store"
)

// HashFile est le document du store contenant le hash bcrypt.
const HashFile = "ui.hash"

// Passwords vérifie et modifie le mot de passe de l'appliance.
type Passwords struct {
	store *store.Store
	mu    sync.RWMutex
	hash  []byte
}

// New charge le hash existant (absent = aucun mot de passe, toute vérification échoue).
func New(s *store.Store) *Passwords {
	p := &Passwords{store: s}
	if data, err := os.ReadFile(s.Path(HashFile)); err == nil {
		p.hash = []byte(strings.TrimSpace(string(data)))
	}
	return p
}

// IsSet indique si un mot de passe est défini.
func (p *Passwords) IsSet() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.hash) > 0
}

// Set remplace le mot de passe (bcrypt coût 10, environ 60 ms sur 1 vCPU).
func (p *Passwords) Set(password string) error {
	if len(password) < 8 {
		return errors.New("mot de passe trop court (8 caractères minimum)")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), 10)
	if err != nil {
		return err
	}
	if err := p.store.WriteBytes(HashFile, append(h, '\n'), 0o600); err != nil {
		return err
	}
	p.mu.Lock()
	p.hash = h
	p.mu.Unlock()
	return nil
}

// Verify compare un mot de passe au hash. Temps constant côté comparaison, faux si rien n'est défini.
func (p *Passwords) Verify(password string) bool {
	p.mu.RLock()
	h := p.hash
	p.mu.RUnlock()
	if len(h) == 0 || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword(h, []byte(password)) == nil
}

// VerifyToken compare un jeton d'API (chaîne opaque) de façon constante. Réservé aux tests et
// aux jetons générés ; le mot de passe passe par Verify.
func VerifyToken(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
