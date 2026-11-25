#!/bin/sh
# dist/work/media (média de l'OVA) -> dist/vigie-<ver>.iso : ISO hybride live, BIOS (isolinux) et
# UEFI (GRUB), démarrée avec vigie.live : tout en RAM, /data n'est jamais monté (décision du
# 23/11/2026 : « un liveCD c'est un liveCD, on perd tout », même si un disque est présent).
# S'exécute dans le conteneur builder (sans root).
set -eu
cd "$(dirname "$0")/.."
: "${VERSION:=dev}" "${SOURCE_DATE_EPOCH:=0}" "${KERNEL_FLAVOR:=virt}"
export MTOOLS_SKIP_CHECK=1
D=dist; W=$D/work; MEDIA=$W/media; I=$W/iso
[ -d "$MEDIA/boot" ] || { echo "[make-iso] $MEDIA absent : make image d'abord"; exit 1; }
rm -rf "$I"; mkdir -p "$I"
cp -a "$MEDIA/." "$I/"
rm -rf "$I/boot/syslinux"; mkdir -p "$I/boot/syslinux" "$I/boot/grub"
: > "$I/boot/vigie.live"   # repère cherché par GRUB (UEFI)
CMDLINE="modules=loop,squashfs,sd-mod,sr-mod quiet modloop_verify=no vigie.live console=ttyS0,115200 console=tty1 consoleblank=0"

# --- BIOS : isolinux
cp /usr/share/syslinux/isolinux.bin /usr/share/syslinux/ldlinux.c32 "$I/boot/syslinux/"
cat > "$I/boot/syslinux/isolinux.cfg" <<CFG
SERIAL 0 115200
DEFAULT live
PROMPT 0
TIMEOUT 10
LABEL live
  KERNEL /boot/vmlinuz-$KERNEL_FLAVOR
  INITRD /boot/initramfs-$KERNEL_FLAVOR
  APPEND $CMDLINE
CFG

# --- UEFI : GRUB autonome (BOOTX64.EFI) dans une image FAT
cat > "$W/grub-early.cfg" <<'CFG'
search --no-floppy --set=root --file /boot/vigie.live
set prefix=($root)/boot/grub
configfile ($root)/boot/grub/grub.cfg
CFG
cat > "$I/boot/grub/grub.cfg" <<CFG
set timeout=1
set default=0
insmod all_video
menuentry "vigie $VERSION (live)" {
	linux /boot/vmlinuz-$KERNEL_FLAVOR $CMDLINE
	initrd /boot/initramfs-$KERNEL_FLAVOR
}
CFG
grub-mkimage -O x86_64-efi -o "$W/BOOTX64.EFI" -p /boot/grub -c "$W/grub-early.cfg" \
	part_gpt part_msdos fat iso9660 normal configfile linux echo search search_fs_file test all_video efi_gop gzio
EFI=$I/boot/grub/efi.img
rm -f "$EFI"
mkfs.vfat -C -n VIGIE_EFI -i 76696765 "$EFI" 4096 >/dev/null
mmd -i "$EFI" ::/EFI ::/EFI/BOOT
mcopy -i "$EFI" -m "$W/BOOTX64.EFI" ::/EFI/BOOT/BOOTX64.EFI

# --- ISO hybride (dates figées : build reproductible)
find "$I" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +
ISO=$D/vigie-$VERSION.iso
DATE=$(date -u -d "@$SOURCE_DATE_EPOCH" +%Y%m%d%H%M%S00)
xorriso -as mkisofs -quiet -o "$ISO" -V VIGIE_LIVE -r -J -joliet-long \
	--modification-date="$DATE" \
	-isohybrid-mbr /usr/share/syslinux/isohdpfx.bin \
	-b boot/syslinux/isolinux.bin -c boot/syslinux/boot.cat -no-emul-boot -boot-load-size 4 -boot-info-table \
	-eltorito-alt-boot -e boot/grub/efi.img -no-emul-boot -isohybrid-gpt-basdat \
	"$I"
ln -sf "vigie-$VERSION.iso" "$D/vigie.iso"
jq --argjson iso "$(stat -c %s "$ISO")" '. + {iso:$iso}' "$D/sizes.json" > "$D/sizes.json.tmp" && mv "$D/sizes.json.tmp" "$D/sizes.json"
echo "[make-iso] $ISO : $(( $(stat -c %s "$ISO") / 1000000 )) Mo"
