package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"vigie/internal/store"
)

// TokensFile est le document du store qui contient les jetons d'API (empreintes SHA-256 seulement).
const TokensFile = "api-tokens.json"

// TokenPrefix identifie un jeton d'API dans l'en-tête Authorization.
const TokenPrefix = "vg_"

// Token décrit un jeton d'API (jamais le secret lui-même).
type Token struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Hash     string    `json:"hash"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used,omitempty"`
}

// Tokens gère les jetons d'API, destinés aux scripts de QA et à l'automatisation (§10, demande du 16/11).
type Tokens struct {
	store *store.Store
	mu    sync.Mutex
	list  []Token
}

// NewTokens charge les jetons existants.
func NewTokens(s *store.Store) *Tokens {
	t := &Tokens{store: s}
	_ = s.Load(TokensFile, &t.list)
	return t
}

func digest(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// Create génère un jeton et renvoie son secret (affiché une seule fois).
func (t *Tokens) Create(name string) (Token, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return Token{}, "", errors.New("nom de jeton requis (64 caractères max)")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, "", err
	}
	secret := TokenPrefix + hex.EncodeToString(raw)
	id := hex.EncodeToString(raw[:4])
	tok := Token{ID: id, Name: name, Hash: digest(secret), Created: time.Now().UTC()}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.list = append(t.list, tok)
	if err := t.store.Save(TokensFile, t.list); err != nil {
		t.list = t.list[:len(t.list)-1]
		return Token{}, "", err
	}
	return tok, secret, nil
}

// List renvoie les jetons (sans empreinte).
func (t *Tokens) List() []Token {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Token, len(t.list)) // jamais null en JSON
	for i, tok := range t.list {
		tok.Hash = ""
		out[i] = tok
	}
	return out
}

// Delete révoque un jeton.
func (t *Tokens) Delete(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, tok := range t.list {
		if tok.ID == id {
			t.list = append(t.list[:i], t.list[i+1:]...)
			return t.store.Save(TokensFile, t.list)
		}
	}
	return errors.New("jeton inconnu")
}

// Check vérifie un secret ; renvoie le nom du jeton.
func (t *Tokens) Check(secret string) (string, bool) {
	if !strings.HasPrefix(secret, TokenPrefix) {
		return "", false
	}
	d := digest(secret)
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, tok := range t.list {
		if subtle.ConstantTimeCompare([]byte(tok.Hash), []byte(d)) == 1 {
			if time.Since(tok.LastUsed) > time.Minute {
				t.list[i].LastUsed = time.Now().UTC()
				_ = t.store.Save(TokensFile, t.list)
			}
			return tok.Name, true
		}
	}
	return "", false
}
