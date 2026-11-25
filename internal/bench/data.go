package bench

import "encoding/binary"

// PoolSize est la taille du réservoir de données pseudo-aléatoires (§2 du plan : 64 Mio).
const PoolSize = 64 << 20

// Sector est la granularité à laquelle chaque bloc est rendu unique (numéro de secteur XORé).
const Sector = 4096

// xorshift64* : générateur rapide et reproductible (la graine est journalisée pour rejouer un run).
type rng struct{ s uint64 }

func newRNG(seed uint64) *rng {
	if seed == 0 {
		seed = 0x9E3779B97F4A7C15
	}
	return &rng{s: seed}
}

func (r *rng) next() uint64 {
	r.s ^= r.s >> 12
	r.s ^= r.s << 25
	r.s ^= r.s >> 27
	return r.s * 2685821657736338717
}

// Pool est un réservoir d'octets incompressibles.
type Pool struct {
	Seed uint64
	buf  []byte
}

// NewPool génère un réservoir de size octets à partir de la graine.
func NewPool(seed uint64, size int) *Pool {
	r := newRNG(seed)
	p := &Pool{Seed: seed, buf: make([]byte, size)}
	for i := 0; i+8 <= size; i += 8 {
		binary.LittleEndian.PutUint64(p.buf[i:], r.next())
	}
	return p
}

// Fill remplit dst avec les données du réservoir pour le bloc commençant à l'offset off (octets)
// du disque. salt choisit la position de lecture dans le réservoir : deux salts différents donnent
// un contenu entièrement différent pour le même offset (chaque passe d'écriture réécrit donc des
// données nouvelles, jamais celles du préremplissage). Le numéro de secteur absolu est ensuite XORé
// dans le premier mot de chaque secteur de 4 Kio : deux secteurs ne sont jamais identiques
// (anti-dédup) et le contenu reste incompressible.
func (p *Pool) Fill(dst []byte, off int64, salt uint64) {
	n := len(dst)
	size := uint64(len(p.buf))
	start := int((uint64(off) + salt*0x9E3779B97F4A7C15) % size &^ 7)
	for done := 0; done < n; {
		c := copy(dst[done:], p.buf[start:])
		done += c
		start = 0
	}
	for s := 0; s+8 <= n; s += Sector {
		sector := uint64((off + int64(s)) / Sector)
		w := binary.LittleEndian.Uint64(dst[s:]) ^ (sector * 0x9E3779B97F4A7C15)
		binary.LittleEndian.PutUint64(dst[s:], w)
	}
}
