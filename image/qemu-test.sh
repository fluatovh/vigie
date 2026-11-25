#!/usr/bin/env bash
# Test de boot local de dist/disk.raw sous QEMU (PVSCSI + AHCI pour le CD + vmxnet3, sans KVM) : vérifie /api/health.
# Usage : image/qemu-test.sh [--static] [--keep] [--direct]
#   --testdisk : ajoute un second disque PVSCSI de 1 Gio avec le wwn 6000c29c0000d15c (NAA 8 octets,
#              QEMU ne sait pas en émuler de 16) -> `vigie testdisk -uuid 6000c29c0000d15c -size 1073741824 -sign`
#   --direct : charge noyau+initramfs directement (sans syslinux) avec la console série en primaire,
#              ce qui rend le shell de secours de l'initramfs pilotable via image/qemu-cmd.sh
#   --static : fournit un ovf-env.xml (ISO) avec IP statique 10.0.2.15 pour tester le chemin OVF
#   --keep   : laisse la VM tourner (ports 127.0.0.1:18080 -> 80, 127.0.0.1:2222 -> 22)
set -euo pipefail
cd "$(dirname "$0")/.."
STATIC=0; KEEP=0; DIRECT=0; TESTDISK=0
for a in "$@"; do case $a in --static) STATIC=1;; --keep) KEEP=1;; --direct) DIRECT=1;; --testdisk) TESTDISK=1;; esac; done
D=dist/qemu; mkdir -p "$D"
IMG=$D/disk.raw; LOG=$D/serial.log; PID=$D/qemu.pid
cp --sparse=always dist/disk.raw "$IMG"
[ -f "$PID" ] && kill "$(cat "$PID")" 2>/dev/null || true
rm -f "$LOG"

cdrom=()
if [ "$STATIC" = 1 ]; then
	mkdir -p "$D/ovf"
	cat > "$D/ovf/ovf-env.xml" <<'XML'
<?xml version="1.0" encoding="UTF-8"?>
<Environment xmlns="http://schemas.dmtf.org/ovf/environment/1" xmlns:oe="http://schemas.dmtf.org/ovf/environment/1" oe:id="">
   <PropertySection>
      <Property oe:key="hostname" oe:value="vigie-qemu"/>
      <Property oe:key="ip" oe:value="10.0.2.15"/>
      <Property oe:key="netmask" oe:value="255.255.255.0"/>
      <Property oe:key="gateway" oe:value="10.0.2.2"/>
      <Property oe:key="dns" oe:value="10.0.2.3"/>
      <Property oe:key="ntp" oe:value=""/>
      <Property oe:key="ui_password" oe:value="Qemu&amp;Test&quot;1"/>
   </PropertySection>
</Environment>
XML
	xorriso -as mkisofs -quiet -o "$D/ovf-env.iso" -V OVF-ENV -r "$D/ovf" 2>/dev/null
	cdrom=(-cdrom "$D/ovf-env.iso")
fi

extra=()
if [ "$TESTDISK" = 1 ]; then
	rm -f "$D/testdisk.raw"; truncate -s 1G "$D/testdisk.raw"
	extra=(-drive "file=$D/testdisk.raw,format=raw,if=none,id=d1" -device "scsi-hd,drive=d1,bus=pv0.0,scsi-id=1,wwn=0x6000c29c0000d15c,vendor=VMware,product=Virtual disk")
fi
direct=()
if [ "$DIRECT" = 1 ]; then
	direct=(-kernel dist/work/media/boot/vmlinuz-virt -initrd dist/work/media/boot/initramfs-virt
	        -append "modules=loop,squashfs,sd-mod modloop_verify=no console=tty1 console=ttyS0,115200")
fi
rm -f "$D/serial.sock" "$D/monitor.sock"
qemu-system-x86_64 -machine q35,accel=${QEMU_ACCEL:-tcg} -cpu max -m 1024 -smp 2 -display none -vga std -monitor "unix:$D/monitor.sock,server,nowait" \
	-chardev "socket,id=ser0,path=$D/serial.sock,server=on,wait=off,logfile=$LOG" -serial chardev:ser0 \
	-drive "file=$IMG,format=raw,if=none,id=d0" -device pvscsi,id=pv0 -device scsi-hd,drive=d0,bus=pv0.0,scsi-id=0 "${extra[@]}" "${cdrom[@]}" "${direct[@]}" \
	-netdev user,id=n0,hostfwd=tcp:127.0.0.1:18080-:80,hostfwd=tcp:127.0.0.1:2222-:22 -device vmxnet3,netdev=n0 \
	-pidfile "$PID" -daemonize
echo "QEMU démarré (pid $(cat "$PID")), journal série : $LOG"
ok=0
for i in $(seq 1 150); do
	if out=$(curl -fsS -m 2 http://127.0.0.1:18080/api/health 2>/dev/null); then echo "$out"; ok=1; break; fi
	sleep 2
done
echo "--- fin du journal série ---"; tail -n 40 "$LOG" | tr -d '\r' | grep -v '^$' || true
if [ "$KEEP" = 0 ]; then kill "$(cat "$PID")" 2>/dev/null || true; rm -f "$PID"; fi
[ "$ok" = 1 ] && echo "QEMU: health OK" || { echo "QEMU: health KO (timeout 300 s)"; exit 1; }
