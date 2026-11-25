#!/usr/bin/env bash
# Capture l'écran VGA de la VM QEMU de test (dist/qemu) en PNG : dist/qemu/screen.png
set -euo pipefail
cd "$(dirname "$0")/.."
D=dist/qemu
printf 'screendump %s/screen.ppm\n' "$PWD/$D" | python3 -c '
import socket,sys,time
s=socket.socket(socket.AF_UNIX); s.connect(sys.argv[1]); s.settimeout(2)
try: s.recv(4096)
except Exception: pass
s.sendall(sys.stdin.read().encode()); time.sleep(1)
try: s.recv(4096)
except Exception: pass
' "$D/monitor.sock"
sleep 1
python3 - "$D/screen.ppm" "$D/screen.png" <<'PY'
import sys, zlib, struct
data = open(sys.argv[1], 'rb').read()
# PPM P6 : en-tête "P6\n<w> <h>\n255\n" puis RGB brut
parts = data.split(b'\n', 3)
assert parts[0] == b'P6', parts[0]
w, h = map(int, parts[1].split()); raw = parts[3]
rows = b''.join(b'\x00' + raw[y*w*3:(y+1)*w*3] for y in range(h))
def chunk(t, d): return struct.pack('>I', len(d)) + t + d + struct.pack('>I', zlib.crc32(t + d) & 0xffffffff)
png = b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', w, h, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(rows, 9)) + chunk(b'IEND', b'')
open(sys.argv[2], 'wb').write(png); print(f"{sys.argv[2]} {w}x{h}")
PY
