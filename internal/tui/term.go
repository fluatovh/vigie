package tui

import (
	"errors"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// Key est une touche décodée.
type Key struct {
	Name string // "" pour un caractère ; sinon Up, Down, Left, Right, Enter, Esc, Tab, BackTab,
	//            Backspace, Delete, Home, End, PgUp, PgDn, F1…F12, Ctrl-L, Ctrl-C
	R rune
}

func (k Key) String() string {
	if k.Name != "" {
		return k.Name
	}
	return string(k.R)
}

// Séquences de la console Linux (F1-F5 : ESC [[A…E) et de xterm (SSH, tests).
var seqs = map[string]string{
	"[A": "Up", "[B": "Down", "[C": "Right", "[D": "Left", "OA": "Up", "OB": "Down", "OC": "Right", "OD": "Left",
	"[H": "Home", "[F": "End", "OH": "Home", "OF": "End", "[1~": "Home", "[4~": "End", "[7~": "Home", "[8~": "End",
	"[2~": "Insert", "[3~": "Delete", "[5~": "PgUp", "[6~": "PgDn", "[Z": "BackTab",
	"[[A": "F1", "[[B": "F2", "[[C": "F3", "[[D": "F4", "[[E": "F5",
	"OP": "F1", "OQ": "F2", "OR": "F3", "OS": "F4", "[11~": "F1", "[12~": "F2", "[13~": "F3", "[14~": "F4", "[15~": "F5",
	"[17~": "F6", "[18~": "F7", "[19~": "F8", "[20~": "F9", "[21~": "F10", "[23~": "F11", "[24~": "F12",
}

// Decoder transforme les octets reçus en touches. Un ESC seul est rendu après un court délai.
type Decoder struct {
	buf []byte
}

// Feed ajoute des octets et renvoie les touches complètes ; pending indique un ESC en attente.
func (d *Decoder) Feed(b []byte) (keys []Key, pending bool) {
	d.buf = append(d.buf, b...)
	for len(d.buf) > 0 {
		c := d.buf[0]
		if c == 0x1b {
			if len(d.buf) == 1 {
				return keys, true
			}
			// séquence : ESC [ ... (lettre ou ~) / ESC O lettre / ESC [[ lettre
			rest := d.buf[1:]
			if rest[0] != '[' && rest[0] != 'O' {
				keys = append(keys, Key{Name: "Esc"}) // Alt+touche : ESC puis la touche
				d.buf = d.buf[1:]
				continue
			}
			end := -1
			for i := 1; i < len(rest) && i < 8; i++ {
				ch := rest[i]
				if rest[0] == '[' && i == 1 && ch == '[' {
					continue
				}
				if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '~' {
					end = i
					break
				}
			}
			if end < 0 {
				if len(rest) < 8 {
					return keys, true // séquence incomplète
				}
				d.buf = d.buf[1:]
				keys = append(keys, Key{Name: "Esc"})
				continue
			}
			seq := string(rest[:end+1])
			d.buf = d.buf[end+2:]
			if n, ok := seqs[seq]; ok {
				keys = append(keys, Key{Name: n})
			}
			continue
		}
		switch {
		case c == '\r' || c == '\n':
			keys = append(keys, Key{Name: "Enter"})
		case c == '\t':
			keys = append(keys, Key{Name: "Tab"})
		case c == 0x7f || c == 0x08:
			keys = append(keys, Key{Name: "Backspace"})
		case c == 0x0c:
			keys = append(keys, Key{Name: "Ctrl-L"})
		case c == 0x03:
			keys = append(keys, Key{Name: "Ctrl-C"})
		case c < 0x20:
			// autre contrôle : ignoré
		default:
			r, n := decodeRune(d.buf)
			if n == 0 {
				return keys, false // UTF-8 incomplet
			}
			d.buf = d.buf[n:]
			keys = append(keys, Key{R: r})
			continue
		}
		d.buf = d.buf[1:]
	}
	return keys, false
}

// Flush rend un ESC en attente (après le délai).
func (d *Decoder) Flush() []Key {
	if len(d.buf) == 1 && d.buf[0] == 0x1b {
		d.buf = nil
		return []Key{{Name: "Esc"}}
	}
	return nil
}

func decodeRune(b []byte) (rune, int) {
	c := b[0]
	n := 1
	switch {
	case c < 0x80:
		return rune(c), 1
	case c&0xE0 == 0xC0:
		n = 2
	case c&0xF0 == 0xE0:
		n = 3
	case c&0xF8 == 0xF0:
		n = 4
	default:
		return '?', 1
	}
	if len(b) < n {
		return 0, 0
	}
	r := []rune(string(b[:n]))
	return r[0], n
}

// Terminal est le terminal réel (tty1 ou SSH) en mode brut.
type Terminal struct {
	in, out *os.File
	saved   syscall.Termios
	raw     bool
}

// OpenTerminal passe stdin en mode brut.
func OpenTerminal(in, out *os.File) (*Terminal, error) {
	t := &Terminal{in: in, out: out}
	if err := ioctl(in.Fd(), syscall.TCGETS, unsafe.Pointer(&t.saved)); err != nil {
		return nil, errors.New("pas un terminal : " + err.Error())
	}
	raw := t.saved
	raw.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN], raw.Cc[syscall.VTIME] = 1, 0
	if err := ioctl(in.Fd(), syscall.TCSETS, unsafe.Pointer(&raw)); err != nil {
		return nil, err
	}
	t.raw = true
	return t, nil
}

// Size renvoie la taille du terminal (80 × 25 par défaut).
func (t *Terminal) Size() (int, int) {
	var ws struct{ Row, Col, X, Y uint16 }
	if err := ioctl(t.out.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&ws)); err != nil || ws.Col == 0 || ws.Row == 0 {
		return 80, 25
	}
	return int(ws.Col), int(ws.Row)
}

// Close restaure le terminal.
func (t *Terminal) Close() {
	if t.raw {
		_ = ioctl(t.in.Fd(), syscall.TCSETS, unsafe.Pointer(&t.saved))
		t.raw = false
	}
	_, _ = t.out.WriteString("\x1b[0m\x1b[2J\x1b[H\x1b[?25h")
}

// Keys lit le clavier et envoie les touches sur ch (goroutine).
func (t *Terminal) Keys(ch chan<- Key) {
	var d Decoder
	buf := make([]byte, 256)
	bytesCh := make(chan []byte)
	go func() {
		for {
			n, err := t.in.Read(buf)
			if err != nil {
				close(bytesCh)
				return
			}
			b := append([]byte(nil), buf[:n]...)
			bytesCh <- b
		}
	}()
	var timer <-chan time.Time
	for {
		select {
		case b, ok := <-bytesCh:
			if !ok {
				close(ch)
				return
			}
			keys, pending := d.Feed(b)
			for _, k := range keys {
				ch <- k
			}
			if pending {
				timer = time.After(60 * time.Millisecond)
			}
		case <-timer:
			for _, k := range d.Flush() {
				ch <- k
			}
			timer = nil
		}
	}
}

func ioctl(fd uintptr, req uint, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(req), uintptr(arg)); e != 0 {
		return e
	}
	return nil
}
