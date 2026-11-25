#!/usr/bin/env bash
# CI : démarre le qcow2 sous QEMU (HTTPS redirigé sur 127.0.0.1:8443) et lance le test d'interface
# test/ui/ci.mjs dans un Chromium headless (image Docker vigie-ui-qa). Sortie : dist/ci-ui/.
set -euo pipefail
cd "$(dirname "$0")/.."
R=dist/ci-ui; rm -rf "$R"; mkdir -p "$R"
qemu-img create -q -f qcow2 -b "$PWD/dist/vigie.qcow2" -F qcow2 "$R/disk.qcow2"
qemu-system-x86_64 -machine "q35,accel=${QEMU_ACCEL:-tcg}" -cpu max -m 1024 -smp 2 -display none \
	-serial "file:$R/serial.log" -pidfile "$R/qemu.pid" -daemonize \
	-drive "file=$R/disk.qcow2,if=none,id=d0" -device virtio-scsi-pci -device scsi-hd,drive=d0 \
	-netdev "user,id=n0,hostfwd=tcp:127.0.0.1:8443-:443" -device virtio-net-pci,netdev=n0
trap 'kill "$(cat $R/qemu.pid)" 2>/dev/null || true' EXIT
for i in $(seq 1 150); do curl -ksf -m 2 https://127.0.0.1:8443/api/health >/dev/null && break; sleep 2; done
PW=""
for i in $(seq 1 60); do
	PW=$(grep -a "Mot de passe" "$R/serial.log" | tail -1 | awk '{print $NF}' | tr -d '\r' || true)
	[ -n "$PW" ] && break; sleep 2
done
[ -n "$PW" ] || { echo "mot de passe introuvable sur la console série"; tail -40 "$R/serial.log"; exit 1; }
docker build -q -t vigie-ui-qa test/ui >/dev/null
# HOME inscriptible : l'uid du runner n'existe pas dans l'image, et Chromium (crashpad) refuse de
# démarrer sans répertoire personnel.
docker run --rm --network host -u "$(id -u):$(id -g)" -v "$PWD/$R:/out" --entrypoint node -e HOME=/tmp \
	-e VIGIE_URL=https://127.0.0.1:8443/ -e VIGIE_PASSWORD="$PW" -e OUT=/out vigie-ui-qa ci.mjs
