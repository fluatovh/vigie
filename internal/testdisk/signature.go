package testdisk

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"log"
	"os"
	"strings"
	"syscall"
	"time"
)

// Le bloc 0 (4 Kio) du disque de test porte une signature ; la zone de bench commence à 1 Mio.
const (
	SignatureSize = 4096
	sigMagic      = "VIGIE\x00\x00\x00"
	sigVersion    = 1
)

// Signature est le contenu du bloc 0.
type Signature struct {
	UUID           string    `json:"uuid"`
	CreatedAt      time.Time `json:"created_at"`
	SizeBytes      int64     `json:"size_bytes"`
	PrefilledBytes int64     `json:"prefilled_bytes"`
	PrefilledAt    time.Time `json:"prefilled_at"`
}

// ErrNoSignature indique un bloc 0 sans signature vigie.
var ErrNoSignature = errors.New("pas de signature vigie sur le bloc 0")

// Encode sérialise la signature dans un bloc de 4 Kio.
func (s Signature) Encode() []byte {
	buf := make([]byte, SignatureSize)
	copy(buf[0:8], sigMagic)
	binary.LittleEndian.PutUint32(buf[8:12], sigVersion)
	copy(buf[12:48], strings.ToLower(s.UUID))
	binary.LittleEndian.PutUint64(buf[48:56], uint64(s.CreatedAt.Unix()))
	binary.LittleEndian.PutUint64(buf[56:64], uint64(s.SizeBytes))
	binary.LittleEndian.PutUint64(buf[64:72], uint64(s.PrefilledBytes))
	binary.LittleEndian.PutUint64(buf[72:80], uint64(s.PrefilledAt.Unix()))
	binary.LittleEndian.PutUint32(buf[SignatureSize-4:], crc32.ChecksumIEEE(buf[:SignatureSize-4]))
	return buf
}

// Decode lit une signature depuis un bloc ; ErrNoSignature si le magic ou le CRC ne correspondent pas.
func Decode(buf []byte) (*Signature, error) {
	if len(buf) < SignatureSize || string(buf[0:8]) != sigMagic {
		return nil, ErrNoSignature
	}
	if crc32.ChecksumIEEE(buf[:SignatureSize-4]) != binary.LittleEndian.Uint32(buf[SignatureSize-4:]) {
		return nil, fmt.Errorf("%w (CRC invalide)", ErrNoSignature)
	}
	if v := binary.LittleEndian.Uint32(buf[8:12]); v != sigVersion {
		return nil, fmt.Errorf("signature vigie de version %d inconnue", v)
	}
	s := &Signature{
		UUID:           string(bytes.TrimRight(buf[12:48], "\x00")),
		CreatedAt:      time.Unix(int64(binary.LittleEndian.Uint64(buf[48:56])), 0).UTC(),
		SizeBytes:      int64(binary.LittleEndian.Uint64(buf[56:64])),
		PrefilledBytes: int64(binary.LittleEndian.Uint64(buf[64:72])),
	}
	if t := int64(binary.LittleEndian.Uint64(buf[72:80])); t > 0 {
		s.PrefilledAt = time.Unix(t, 0).UTC()
	}
	return s, nil
}

// ReadSignature lit le bloc 0 du périphérique (lecture seule, O_DIRECT).
func ReadSignature(devPath string) (*Signature, error) {
	f, err := os.OpenFile(devPath, os.O_RDONLY|syscall.O_DIRECT, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := alignedBuffer(SignatureSize)
	n, err := f.ReadAt(buf, 0)
	if err != nil && n < SignatureSize {
		return nil, fmt.Errorf("lecture du bloc 0 : %w", err)
	}
	return Decode(buf)
}

// WriteSignature est LA fonction qui écrit sur le disque de test en phase 3. Elle exige un
// Verified (produit par Verify sur ce même périphérique) : impossible de l'appeler sur un chemin
// arbitraire. Elle est journalisée.
func WriteSignature(v *Verified, s Signature) error {
	if v == nil || v.Device == nil || !v.OK() {
		return errors.New("refus d'écriture : périphérique non vérifié")
	}
	if !strings.EqualFold(s.UUID, v.Device.UUID) {
		return errors.New("refus d'écriture : l'UUID de la signature ne correspond pas au périphérique vérifié")
	}
	log.Printf("testdisk: ÉCRITURE de la signature sur %s (wwid %s, %d octets)", v.Device.Path, v.Device.WWID, SignatureSize)
	f, err := os.OpenFile(v.Device.Path, os.O_WRONLY|syscall.O_DIRECT|syscall.O_SYNC, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := alignedBuffer(SignatureSize)
	copy(buf, s.Encode())
	if _, err := f.WriteAt(buf, 0); err != nil {
		return fmt.Errorf("écriture du bloc 0 : %w", err)
	}
	return f.Sync()
}

// alignedBuffer renvoie un tampon aligné sur 4 Kio (exigence d'O_DIRECT).
func alignedBuffer(size int) []byte {
	raw := make([]byte, size+4096)
	off := 0
	if rem := int(uintptrOf(raw) % 4096); rem != 0 {
		off = 4096 - rem
	}
	return raw[off : off+size]
}
