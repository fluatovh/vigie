package testdisk

import (
	"fmt"
	"os"
	"syscall"
)

// devNodeMajMin renvoie « majeur:mineur » d'un nœud de périphérique bloc. Un fichier ordinaire
// (tests) renvoie la valeur attendue par sysfs pour ne pas bloquer les tests unitaires.
func devNodeMajMin(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("stat %s impossible", path)
	}
	if fi.Mode()&os.ModeDevice == 0 {
		return "", errRegularFile
	}
	rdev := uint64(st.Rdev)
	major := (rdev >> 8) & 0xfff
	minor := (rdev & 0xff) | ((rdev >> 12) & 0xfff00)
	return fmt.Sprintf("%d:%d", major, minor), nil
}
