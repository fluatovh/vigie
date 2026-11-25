#!/usr/bin/env bash
# Envoie des touches à la VM QEMU de test (moniteur, sendkey) : console VGA, comme un utilisateur.
# Usage : image/qemu-keys.sh f2 down ret 'texte:sdb' ...   (noms QEMU : ret, esc, tab, up, down,
# left, right, delete, f1..f12, ou « texte:… » pour taper une chaîne en minuscules/chiffres/./-)
# Monitor : image/qemu-keys.sh 'hmp:device_add …' envoie une commande brute du moniteur.
set -euo pipefail
cd "$(dirname "$0")/.."
python3 - "$PWD/dist/qemu/monitor.sock" "$@" <<'PY'
import socket, sys, time
s = socket.socket(socket.AF_UNIX); s.connect(sys.argv[1]); s.settimeout(0.3)
def drain():
    out = b""
    try:
        while True:
            d = s.recv(65536)
            if not d: break
            out += d
    except Exception: pass
    return out
drain()
names = {'.': 'dot', '-': 'minus', '/': 'slash', ' ': 'spc', ':': 'shift-semicolon', '_': 'shift-minus', ';': 'semicolon', '|': 'shift-backslash', '>': 'shift-dot', '=': 'equal', ',': 'comma'}
def send(cmd):
    s.sendall((cmd + "\n").encode()); time.sleep(0.25)
    out = drain().decode(errors="replace")
    lines = [l for l in out.replace("\r", "").split("\n") if l and not l.startswith("(qemu)") and cmd not in l]
    if lines: print("\n".join(lines))
for a in sys.argv[2:]:
    if a.startswith("hmp:"):
        send(a[4:]); continue
    if a.startswith("texte:"):
        for ch in a[6:]:
            k = names.get(ch, ch)
            if ch.isupper(): k = "shift-" + ch.lower()
            send("sendkey " + k)
        continue
    send("sendkey " + a)
    time.sleep(0.4)
PY
