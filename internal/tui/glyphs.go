package tui

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// Glyphes réellement affichables : lus dans la table unicode de la police chargée (GIO_UNIMAP),
// sinon la page de codes 437 de la police VGA d'origine (tests, SSH).
var (
	glyphMu sync.RWMutex
	glyphs  map[rune]bool
)

func supported(r rune) bool {
	if r < 0x80 {
		return true
	}
	glyphMu.RLock()
	defer glyphMu.RUnlock()
	if glyphs != nil {
		return glyphs[r]
	}
	return cp437[r]
}

const gioUnimap = 0x4B66

type unipair struct{ Unicode, FontPos uint16 }

type unimapdesc struct {
	Count   uint16
	_       [6]byte
	Entries *unipair
}

// readUnimap lit la table unicode → glyphe de la console (fd : le terminal).
func readUnimap(fd uintptr) map[rune]bool {
	buf := make([]unipair, 8192)
	d := unimapdesc{Count: uint16(len(buf)), Entries: &buf[0]}
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, gioUnimap, uintptr(unsafe.Pointer(&d))); e != 0 {
		return nil
	}
	m := make(map[rune]bool, d.Count)
	for _, p := range buf[:d.Count] {
		m[rune(p.Unicode)] = true
	}
	return m
}

// Polices : Lat2-Terminus16 (ISO-8859-2 + graphiques, 256 glyphes) pour le polonais ; police VGA
// 8x16 (CP437, 256 glyphes, couleurs vives conservées) pour les autres langues.
const fontDir = "/usr/share/vigie/fonts/"

var fontLoaded string

// applyFont charge la police adaptée à la langue sur le terminal fd, puis relit ses glyphes.
func applyFont(fd uintptr, lang string) {
	want := "default8x16"
	if lang == "pl" {
		want = "lat2-terminus16"
	}
	if want != fontLoaded && !(want == "default8x16" && fontLoaded == "") {
		if exec.Command("setfont", fontDir+want+".psfu").Run() == nil {
			fontLoaded = want
		}
	}
	if m := readUnimap(fd); len(m) > 0 {
		glyphMu.Lock()
		glyphs = m
		glyphMu.Unlock()
	}
}
