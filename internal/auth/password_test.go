package auth

import (
	"testing"

	"vigie/internal/store"
)

func TestSetVerify(t *testing.T) {
	s, _ := store.New(t.TempDir())
	p := New(s)
	if p.IsSet() || p.Verify("x") {
		t.Fatal("rien ne doit être défini au départ")
	}
	if err := p.Set("court"); err == nil {
		t.Fatal("un mot de passe court doit être refusé")
	}
	if err := p.Set("Qemu&Test\"1"); err != nil {
		t.Fatal(err)
	}
	if !p.Verify("Qemu&Test\"1") || p.Verify("Qemu&Test\"2") || p.Verify("") {
		t.Fatal("vérification incorrecte")
	}
	// rechargement depuis le disque
	if !New(s).Verify("Qemu&Test\"1") {
		t.Fatal("le hash doit être persisté")
	}
}
