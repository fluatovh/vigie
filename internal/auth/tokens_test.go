package auth

import (
	"strings"
	"testing"

	"vigie/internal/store"
)

func TestTokens(t *testing.T) {
	s, _ := store.New(t.TempDir())
	tk := NewTokens(s)
	if _, _, err := tk.Create(""); err == nil {
		t.Fatal("nom vide accepté")
	}
	tok, secret, err := tk.Create("qa-claude")
	if err != nil || !strings.HasPrefix(secret, TokenPrefix) {
		t.Fatal(err)
	}
	if name, ok := tk.Check(secret); !ok || name != "qa-claude" {
		t.Fatal("jeton refusé")
	}
	if _, ok := tk.Check(secret + "x"); ok {
		t.Fatal("jeton altéré accepté")
	}
	if l := NewTokens(s).List(); len(l) != 1 || l[0].Hash != "" || l[0].Name != "qa-claude" {
		t.Fatalf("liste : %+v", l)
	}
	if err := tk.Delete(tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := NewTokens(s).Check(secret); ok {
		t.Fatal("jeton révoqué accepté")
	}
}
