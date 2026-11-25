#!/bin/sh
# dist/disk.raw -> dist/vigie-<ver>.qcow2 (Proxmox, OpenStack, KVM) : le même disque que l'OVA,
# compressé. Pilotes virtio (disque, SCSI, réseau) dans l'initramfs et le modloop.
set -eu
cd "$(dirname "$0")/.."
: "${VERSION:=dev}"
D=dist
Q=$D/vigie-$VERSION.qcow2
qemu-img convert -f raw -O qcow2 -c -o compat=1.1 "$D/disk.raw" "$Q"
ln -sf "vigie-$VERSION.qcow2" "$D/vigie.qcow2"
jq --argjson qcow2 "$(stat -c %s "$Q")" '. + {qcow2:$qcow2}' "$D/sizes.json" > "$D/sizes.json.tmp" && mv "$D/sizes.json.tmp" "$D/sizes.json"
echo "[make-qcow2] $Q : $(( $(stat -c %s "$Q") / 1000000 )) Mo"
