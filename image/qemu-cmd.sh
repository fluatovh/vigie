#!/usr/bin/env bash
# Envoie une ligne de commande au shell série de la VM QEMU de test et affiche la réponse.
# Usage : image/qemu-cmd.sh 'ls /media' [délai_s]
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - "$PWD/dist/qemu/serial.sock" "$1" "${2:-3}" <<'PY'
import socket, sys, time
sock, cmd, wait = sys.argv[1], sys.argv[2], float(sys.argv[3])
s = socket.socket(socket.AF_UNIX); s.connect(sock); s.settimeout(0.5)
try:
    while s.recv(65536): pass
except Exception: pass
s.sendall((cmd + "\n").encode())
end = time.time() + wait; out = b""
while time.time() < end:
    try: out += s.recv(65536)
    except Exception: pass
print(out.decode(errors="replace").replace("\r", ""))
PY
