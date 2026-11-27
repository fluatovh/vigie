#!/bin/sh
# dist/disk.raw -> VMDK streamOptimized -> OVF (template) + manifeste -> dist/vigie-<ver>.ova
set -eu
cd "$(dirname "$0")/.."
: "${VERSION:=dev}" "${SOURCE_DATE_EPOCH:=0}"
D=dist; O=$D/ova
rm -rf "$O"; mkdir -p "$O"
qemu-img convert -O vmdk -o subformat=streamOptimized,adapter_type=lsilogic "$D/disk.raw" "$O/vigie-disk1.vmdk"
# qemu-img écrit "version=1" ; vSphere attend "version=3" pour un streamOptimized compressé (comme ovftool).
off=$(head -c 65536 "$O/vigie-disk1.vmdk" | grep -a -b -o 'version=1' | head -n1 | cut -d: -f1)
[ -n "$off" ] && printf 'version=3' | dd of="$O/vigie-disk1.vmdk" bs=1 seek="$off" conv=notrunc status=none
# qemu-img tire un CID aléatoire à chaque conversion : on le fige pour un build reproductible.
off=$(head -c 65536 "$O/vigie-disk1.vmdk" | grep -a -b -o -E 'CID=[0-9a-f]{8}' | grep -v parent | head -n1 | cut -d: -f1)
[ -n "$off" ] && printf 'CID=64737072' | dd of="$O/vigie-disk1.vmdk" bs=1 seek="$off" conv=notrunc status=none
VMDK_SIZE=$(stat -c %s "$O/vigie-disk1.vmdk")
POPULATED=$(du -b "$D/disk.raw" | cut -f1)
sed -e "s|@VERSION@|$VERSION|g" -e "s|@VMDK_SIZE@|$VMDK_SIZE|g" -e "s|@POPULATED@|$POPULATED|g" \
	image/vigie.ovf.tmpl > "$O/vigie.ovf"
# Le client vSphere refuse l'OVA si le type d'OS invité n'admet pas nos contrôleurs : avec
# « otherLinux64Guest », seuls LSI Logic, IDE et E1000 sont permis (PVSCSI, SATA et vmxnet3 refusés).
grep -Eq 'vmw:osType="other[456]xLinux64Guest"' "$O/vigie.ovf" ||
	{ echo "[make-ova] type d'OS invité incompatible avec PVSCSI/SATA/vmxnet3 (other4xLinux64Guest attendu)" >&2; exit 1; }
(cd "$O" && for f in vigie.ovf vigie-disk1.vmdk; do
	echo "SHA256($f)= $(sha256sum "$f" | cut -d' ' -f1)"
done > vigie.mf)
OVA=$D/vigie-$VERSION.ova
tar --format=ustar --owner=0 --group=0 --numeric-owner --mtime="@$SOURCE_DATE_EPOCH" \
	-C "$O" -cf "$OVA" vigie.ovf vigie.mf vigie-disk1.vmdk
ln -sf "vigie-$VERSION.ova" "$D/vigie.ova"
jq --argjson ova "$(stat -c %s "$OVA")" --argjson vmdk "$VMDK_SIZE" '. + {vmdk:$vmdk, ova:$ova}' \
	"$D/sizes.json" > "$D/sizes.json.tmp" && mv "$D/sizes.json.tmp" "$D/sizes.json"
echo "[make-ova] $OVA : $(( $(stat -c %s "$OVA") / 1000000 )) Mo"
