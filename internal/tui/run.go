package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// Main lance la console sur le terminal courant ; elle attend le service vigie si besoin.
func Main(socket string) error {
	t, err := OpenTerminal(os.Stdin, os.Stdout)
	if err != nil {
		return err
	}
	defer t.Close()
	// les messages du noyau (ajout à chaud d'un disque…) ne doivent pas écraser l'écran : seules les
	// erreurs graves restent affichées sur la console (équivalent de dmesg -n 1)
	_, _, _ = syscall.Syscall(syscall.SYS_SYSLOG, 8, 0, 1)
	w, h := t.Size()
	if w < 80 || h < 24 {
		fmt.Fprintf(os.Stdout, "terminal trop petit (%dx%d) : 80x24 minimum\r\n", w, h)
		time.Sleep(5 * time.Second)
	}
	a := NewApp(NewSocketClient(socket), max(80, w), max(24, h))
	a.Chvt = func(n int) error { return exec.Command("chvt", strconv.Itoa(n)).Run() }
	a.Font = func(lang string) { applyFont(os.Stdin.Fd(), lang) }
	keys := make(chan Key, 64)
	go t.Keys(keys)
	return a.Run(keys, os.Stdout)
}
