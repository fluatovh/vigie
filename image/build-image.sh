#!/bin/sh
# Assemble dist/disk.raw (512 Mio, MBR, p1 FAT32 bootable = média Alpine diskless, p2 ext4 = /data).
# S'exécute dans le conteneur builder, SANS root ni loop device (mtools, mke2fs -d, syslinux -i).
set -eu
cd "$(dirname "$0")/.."
: "${VERSION:=dev}" "${SOURCE_DATE_EPOCH:=0}" "${LINUX_PKG_VERSION:?}" "${ALPINE_VERSION:?}" "${ALPINE_BRANCH:?}" "${ALPINE_MIRROR:?}"
: "${KERNEL_FLAVOR:=virt}"
export MTOOLS_SKIP_CHECK=1 E2FSPROGS_FAKE_TIME="$SOURCE_DATE_EPOCH"
D=dist; W=$D/work; MEDIA=$W/media
rm -rf "$W"; mkdir -p "$MEDIA/boot/syslinux" "$MEDIA/apks/x86_64"
log() { echo "[build-image] $*"; }

# ---------------------------------------------------------------- 1. noyau + modules depuis le paquet linux-$KERNEL_FLAVOR (version figée)
# Alpine ne conserve que la version courante d'un paquet dans une branche : on prend la version
# courante de la branche figée, on la met en cache, et on signale tout écart avec LINUX_PKG_VERSION.
KC=/cache/kernel
mkdir -p "$KC"
PKG=$(ls "$KC" 2>/dev/null | grep "^linux-$KERNEL_FLAVOR-[0-9].*\.apk$" | sort -V | tail -n1 || true)
if [ -z "$PKG" ]; then
	log "téléchargement de linux-$KERNEL_FLAVOR (branche $ALPINE_BRANCH)"
	apk fetch --no-cache --quiet --output "$KC" -X "$ALPINE_MIRROR/$ALPINE_BRANCH/main" "linux-$KERNEL_FLAVOR"
	PKG=$(ls "$KC" | grep "^linux-$KERNEL_FLAVOR-[0-9].*\.apk$" | sort -V | tail -n1)
	[ -n "$PKG" ] || { log "paquet linux-$KERNEL_FLAVOR introuvable"; exit 1; }
fi
PKGVER=$(echo "$PKG" | sed "s/^linux-$KERNEL_FLAVOR-//; s/\.apk$//")
if [ "$PKGVER" != "$LINUX_PKG_VERSION" ]; then
	log "ATTENTION: noyau $PKGVER différent de LINUX_PKG_VERSION=$LINUX_PKG_VERSION (mettre à jour image/versions.mk)"
fi
# Installation du paquet noyau dans le conteneur (éphémère) : mkinitfs et le modloop lisent /lib/modules.
[ "$(id -u)" = 0 ] || { log "cette étape doit tourner en root dans le conteneur (voir Makefile)"; exit 1; }
echo 'disable_trigger=yes' >> /etc/mkinitfs/mkinitfs.conf
cp image/keys/vigie-build.rsa.pub /etc/apk/keys/        # l'initramfs (apk + clés) fera confiance au paquet vigie
apk add --quiet --no-progress --no-scripts "$KC/$PKG" >/dev/null
KVER=$(ls /lib/modules | grep "$KERNEL_FLAVOR" | head -n1)
cp "/boot/vmlinuz-$KERNEL_FLAVOR" "$MEDIA/boot/"
log "noyau $KVER"

# ---------------------------------------------------------------- 1b. initramfs (mkinitfs) : base + scsi + ata + cdrom + squashfs
# L'initramfs netboot "virt" d'Alpine ne contient ni sd_mod ni ahci/scsi : il ne voit pas un disque SATA.
mkinitfs -q -P "$PWD/image/mkinitfs" -F "base scsi ata cdrom squashfs virtio vigie" -C gzip -o "$MEDIA/boot/initramfs-$KERNEL_FLAVOR" "$KVER"

# ---------------------------------------------------------------- 2. modloop élagué (image/modloop-keep + dépendances)
SRC=/lib/modules/$KVER; DST=$W/mlroot/lib/modules/$KVER
mkdir -p "$DST"
list=""
for m in $(grep -v '^#' image/modloop-keep); do
	alt=$(echo "$m" | tr '_' '-'); alt2=$(echo "$m" | tr '-' '_')
	line=$(grep -E "/($m|$alt|$alt2)\.ko(\.[a-z]+)?:" "$SRC/modules.dep" | head -n1 || true)
	[ -n "$line" ] || { log "ATTENTION: module $m absent du paquet noyau"; continue; }
	list="$list $(echo "$line" | tr -d ':')"
done
for p in $(echo "$list" | tr ' ' '\n' | sort -u); do
	mkdir -p "$DST/$(dirname "$p")"; cp "$SRC/$p" "$DST/$p"
done
cp "$SRC"/modules.builtin "$SRC"/modules.builtin.modinfo "$SRC"/modules.order "$DST/" 2>/dev/null || true
depmod -b "$W/mlroot" "$KVER" 2>/dev/null
log "modloop élagué : $(find "$DST" -name '*.ko*' | wc -l) modules"
mksquashfs "$W/mlroot/lib" "$MEDIA/boot/modloop-$KERNEL_FLAVOR" -comp xz -Xbcj x86 -b 1M \
	-noappend -no-xattrs -all-root -quiet -no-progress >/dev/null

# ---------------------------------------------------------------- 3. cache apk (world + vigie) et index signé
APKS=$MEDIA/apks/x86_64
world=$(grep -v -e '^vigie$' -e '^#' image/overlay/etc/apk/world)
log "apk fetch : $(echo $world)"
apk fetch --no-cache --recursive --output "$APKS" \
	-X "$ALPINE_MIRROR/$ALPINE_BRANCH/main" -X "$ALPINE_MIRROR/$ALPINE_BRANCH/community" $world
cp dist/apk/x86_64/vigie-*.apk "$APKS/"
(cd "$APKS" && apk index --quiet --rewrite-arch x86_64 -o APKINDEX.unsigned.tar.gz ./*.apk 2>/dev/null)
abuild-sign -q -k "$PWD/image/keys/vigie-build.rsa" "$APKS/APKINDEX.unsigned.tar.gz"
mv "$APKS/APKINDEX.unsigned.tar.gz" "$APKS/APKINDEX.tar.gz"
touch "$MEDIA/apks/.boot_repository"
echo "$ALPINE_VERSION vigie-$VERSION" > "$MEDIA/.alpine-release"

# ---------------------------------------------------------------- 4. apkovl (overlay /etc + runlevels + clé)
OVL=$W/ovl
mkdir -p "$OVL/etc/apk/keys"
cp -a image/overlay/etc/. "$OVL/etc/"
cp image/keys/vigie-build.rsa.pub "$OVL/etc/apk/keys/"
while read -r rl svcs; do
	[ -n "$rl" ] || continue
	mkdir -p "$OVL/etc/runlevels/$rl"
	for s in $svcs; do ln -sf "/etc/init.d/$s" "$OVL/etc/runlevels/$rl/$s"; done
done < image/runlevels
find "$OVL" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
tar -C "$OVL" --owner=0 --group=0 --numeric-owner --sort=name --mtime="@$SOURCE_DATE_EPOCH" \
	-czf "$MEDIA/vigie.apkovl.tar.gz" etc

# ---------------------------------------------------------------- 5. partition 1 : FAT32 bootable (syslinux)
cp image/syslinux.cfg "$MEDIA/boot/syslinux/syslinux.cfg"
find "$MEDIA" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
P1=$W/p1.img; P1_SECTORS=409600   # 200 Mio
# mtools et syslinux datent les entrées FAT avec l'heure courante : on fige l'horloge (libfaketime).
# BUILD_DATE (ISO 8601, calculé par le Makefile avec la date GNU de l'hôte) -> "@AAAA-MM-JJ hh:mm:ss"
FT_DATE="@$(echo "${BUILD_DATE:?BUILD_DATE manquant}" | tr 'T' ' ' | tr -d 'Z')"
ft() { FAKETIME="$FT_DATE" NO_FAKE_STAT=1 LD_PRELOAD=/usr/lib/faketime/libfaketime.so.1 "$@"; }
rm -f "$P1"; ft mkfs.vfat -C -F 32 -n VIGIE -i 64737031 "$P1" $((P1_SECTORS / 2)) >/dev/null
ft mcopy -i "$P1" -s -m "$MEDIA/boot" "$MEDIA/apks" ::/
ft mcopy -i "$P1" -m "$MEDIA/vigie.apkovl.tar.gz" "$MEDIA/.alpine-release" ::/
ft syslinux --install --directory /boot/syslinux "$P1"
# syslinux est statique (faketime sans effet) : on aligne les dates FAT de ldlinux.sys/.c32 sur syslinux.cfg.
fat_copy_times() {  # $1 nom 8.3 source (11 octets), $2 nom 8.3 cible
	src=$(grep -a -b -o -m1 "$1" "$P1" | cut -d: -f1); dst=$(grep -a -b -o -m1 "$2" "$P1" | cut -d: -f1)
	[ -n "$src" ] && [ -n "$dst" ] || return 0
	dd if="$P1" bs=1 skip=$((src + 13)) count=7 status=none | dd of="$P1" bs=1 seek=$((dst + 13)) conv=notrunc status=none
	dd if="$P1" bs=1 skip=$((src + 22)) count=4 status=none | dd of="$P1" bs=1 seek=$((dst + 22)) conv=notrunc status=none
}
fat_copy_times "SYSLINUXCFG" "LDLINUX SYS"
fat_copy_times "SYSLINUXCFG" "LDLINUX C32"

# ---------------------------------------------------------------- 6. disque brut : MBR + p1 + p2 ext4 (/data)
RAW=$D/disk.raw; DISK_BYTES=$((512 * 1024 * 1024))
rm -f "$RAW"; truncate -s "$DISK_BYTES" "$RAW"
printf 'label: dos\nlabel-id: 0x64737072\nstart=2048, size=%s, type=0c, bootable\ntype=83\n' "$P1_SECTORS" | sfdisk -q "$RAW"
dd if=/usr/share/syslinux/mbr.bin of="$RAW" bs=440 count=1 conv=notrunc status=none
dd if="$P1" of="$RAW" bs=512 seek=2048 conv=notrunc status=none
P2_OFF=$(( (2048 + P1_SECTORS) * 512 )); P2_KIB=$(( (DISK_BYTES - P2_OFF) / 1024 ))
mkdir -p "$W/data-empty"
mke2fs -q -F -t ext4 -L vigie-data -U 64737072-6461-7461-0000-000000000001 \
	-E "offset=$P2_OFF,lazy_itable_init=0,hash_seed=64737072-6461-7461-0000-000000000002" -d "$W/data-empty" "$RAW" "${P2_KIB}k"

# ---------------------------------------------------------------- 7. tailles (octets)
sz() { stat -c %s "$1"; }
sum() { du -cb "$@" 2>/dev/null | tail -n1 | cut -f1; }
ovt=$(sum "$APKS"/open-vm-tools*.apk "$APKS"/glib-*.apk "$APKS"/pcre2-*.apk "$APKS"/libmount-*.apk \
	"$APKS"/libblkid-*.apk "$APKS"/libintl-*.apk "$APKS"/libtirpc-*.apk "$APKS"/libgcc-*.apk "$APKS"/libffi-*.apk)
jq -n \
	--arg version "$VERSION" --arg kver "$KVER" --arg kpkg "$PKGVER" \
	--argjson kernel "$(sz "$MEDIA/boot/vmlinuz-$KERNEL_FLAVOR")" \
	--argjson initramfs "$(sz "$MEDIA/boot/initramfs-$KERNEL_FLAVOR")" \
	--argjson modloop "$(sz "$MEDIA/boot/modloop-$KERNEL_FLAVOR")" \
	--argjson apks "$(sum "$APKS"/*.apk)" --argjson apks_ovt "${ovt:-0}" \
	--argjson vigie_bin "$(sz dist/vigie)" --argjson vigie_apk "$(sz "$APKS"/vigie-*.apk)" \
	--argjson apkovl "$(sz "$MEDIA/vigie.apkovl.tar.gz")" \
	--argjson media "$(sum "$MEDIA")" --argjson disk_raw_alloc "$(du -b "$RAW" | cut -f1)" \
	'{version:$version, kernel_version:$kver, kernel_pkg:$kpkg, kernel:$kernel, initramfs:$initramfs, modloop:$modloop,
	  apks:$apks, apks_ovt:$apks_ovt, vigie_bin:$vigie_bin, vigie_apk:$vigie_apk, apkovl:$apkovl,
	  media:$media, disk_raw_alloc:$disk_raw_alloc}' > "$D/sizes.json"
log "disk.raw prêt ; média $(( $(jq .media "$D/sizes.json") / 1000000 )) Mo"
[ -n "${OWNER:-}" ] && chown -R "$OWNER" "$D"
exit 0
