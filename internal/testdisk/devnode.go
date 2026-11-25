package testdisk

import "errors"

// errRegularFile : le chemin n'est pas un périphérique bloc (cas des tests sur fichiers).
var errRegularFile = errors.New("pas un périphérique bloc")
