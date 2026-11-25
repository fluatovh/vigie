package bench

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func TestPoolIncompressibleAndUnique(t *testing.T) {
	p := NewPool(42, 1<<20)
	a := make([]byte, 64<<10)
	b := make([]byte, 64<<10)
	p.Fill(a, 0, 0)
	p.Fill(b, 1<<20, 0) // même position dans le réservoir (1 Mio modulo 1 Mio = 0), secteurs différents
	if bytes.Equal(a, b) {
		t.Fatal("deux blocs à des offsets différents ne doivent pas être identiques")
	}
	if bytes.Equal(a[8:4096], b[8:4096]) == false {
		t.Fatal("seul le premier mot de chaque secteur doit différer")
	}
	var zb bytes.Buffer
	w := gzip.NewWriter(&zb)
	w.Write(a)
	w.Close()
	if zb.Len() < len(a)*95/100 {
		t.Fatalf("données compressibles : %d -> %d", len(a), zb.Len())
	}
	// reproductible
	c := make([]byte, 64<<10)
	NewPool(42, 1<<20).Fill(c, 0, 0)
	if !bytes.Equal(a, c) {
		t.Fatal("la même graine doit donner les mêmes données")
	}
	// un autre salt (autre passe) : contenu différent sur presque tout le bloc
	d := make([]byte, 64<<10)
	p.Fill(d, 0, 1)
	same := 0
	for i := range a {
		if a[i] == d[i] {
			same++
		}
	}
	if same > len(a)/100 {
		t.Fatalf("une autre passe doit réécrire des données différentes (%d octets identiques)", same)
	}
}
