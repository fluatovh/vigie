#!/bin/sh
# Construit dist/apk/x86_64/vigie-<pkgver>-r0.apk avec abuild (dans le conteneur builder, sans root).
set -eu
cd "$(dirname "$0")/.."
: "${VERSION:=dev}"
case "$VERSION" in
	v[0-9]*|[0-9]*) PKGVER=$(printf '%s' "$VERSION" | sed -E 's/^v//; s/[^0-9.].*$//') ;;
	*) PKGVER=0.0.0 ;;   # pas de tag : la version complète (hash) est dans le binaire
esac
[ -n "$PKGVER" ] || PKGVER=0.0.0
export VIGIE_PKGVER="$PKGVER"

[ -x dist/vigie ] || { echo "dist/vigie manquant (make build)"; exit 1; }
W=$(mktemp -d)
cp image/apk/APKBUILD image/apk/*.initd image/apk/*.sh dist/vigie "$W/"
cp -r image/apk/fonts "$W/"
# Clé de signature du paquet vigie (jamais versionnée) : fournie par la CI (VIGIE_APK_KEY, PEM),
# sinon générée une fois et conservée dans image/keys/ (ignoré par git). Seule l'image construite
# avec cette clé fait confiance au paquet.
mkdir -p image/keys
if [ ! -s image/keys/vigie-build.rsa ]; then
	if [ -n "${VIGIE_APK_KEY:-}" ]; then
		printf '%s\n' "$VIGIE_APK_KEY" > image/keys/vigie-build.rsa
	else
		openssl genrsa -out image/keys/vigie-build.rsa 2048 2>/dev/null
		echo "clé de signature apk générée (image/keys/vigie-build.rsa)"
	fi
	chmod 600 image/keys/vigie-build.rsa
	openssl rsa -in image/keys/vigie-build.rsa -pubout -out image/keys/vigie-build.rsa.pub 2>/dev/null
fi
mkdir -p "$HOME/.abuild"
cp image/keys/vigie-build.rsa "$HOME/.abuild/vigie-build.rsa"
cp image/keys/vigie-build.rsa.pub "$HOME/.abuild/vigie-build.rsa.pub"
cat > "$HOME/.abuild/abuild.conf" <<CONF
PACKAGER_PRIVKEY="$HOME/.abuild/vigie-build.rsa"
PACKAGER="vigie build"
CONF
cp image/keys/vigie-build.rsa.pub /etc/apk/keys/ 2>/dev/null || true

rm -rf dist/apk; mkdir -p dist/apk
export REPODEST="$PWD/dist/apk" SRCDEST="$W/src" SOURCE_DATE_EPOCH
cd "$W"
abuild -F -P "$REPODEST" -d checksum >/dev/null
abuild -F -P "$REPODEST" -d rootpkg
# abuild range le paquet dans <REPODEST>/<nom du répertoire de travail>/x86_64 ; on aplatit.
mkdir -p "$REPODEST/x86_64"
find "$REPODEST" -name '*.apk' -exec mv {} "$REPODEST/x86_64/" \;
ls -l "$REPODEST/x86_64/"
