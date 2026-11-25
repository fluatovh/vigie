package store

import (
	"errors"
	"os"
	"testing"
)

func TestSaveLoad(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	type doc struct{ A int }
	if err := s.Save("x.json", doc{A: 42}); err != nil {
		t.Fatal(err)
	}
	var d doc
	if err := s.Load("x.json", &d); err != nil || d.A != 42 {
		t.Fatalf("load: %v %+v", err, d)
	}
	if !s.Exists("x.json") || s.Exists("nope") {
		t.Fatal("exists")
	}
	if err := s.Load("nope", &d); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
	if err := s.Remove("x.json"); err != nil || s.Exists("x.json") {
		t.Fatal("remove")
	}
}
