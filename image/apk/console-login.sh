#!/bin/sh
# Connexion root sans mot de passe sur les consoles locales (tty2, shell de la TUI) : l'accès à la
# console de la VM vaut accès physique, comme la DCUI d'un ESXi. SSH demande toujours le mot de passe.
exec /bin/login -f root
