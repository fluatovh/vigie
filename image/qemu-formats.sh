#!/usr/bin/env bash
# Phase P : valide l'ISO live (BIOS, UEFI, avec un disque vigie présent) et le qcow2 (virtio, NoCloud,
# config-drive) sous QEMU. Usage : image/qemu-formats.sh [scénario…]  (iso-bios iso-disk iso-uefi qcow2-nocloud qcow2-configdrive)
set -uo pipefail
cd "$(dirname "$0")/.."
ISO=dist/vigie.iso; QCOW=dist/vigie.qcow2; R=dist/qemu-formats
mkdir -p "$R"
KEY=$R/id_ed25519
[ -f "$KEY" ] || ssh-keygen -q -t ed25519 -N '' -f "$KEY"
o=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 -o LogLevel=ERROR)
pass=0; fail=0
ok() { echo "  OK   $*"; pass=$((pass+1)); }
ko() { echo "  ÉCHEC $*"; fail=$((fail+1)); }
check() { if eval "$2"; then ok "$1"; else ko "$1"; fi; }

boot() { # nom port_http port_ssh args…
	local n=$1 ph=$2 ps=$3; shift 3
	local d=$R/$n; rm -rf "$d"; mkdir -p "$d"
	qemu-system-x86_64 -machine q35,accel=${QEMU_ACCEL:-tcg} -cpu max -m 1024 -smp 2 -display none -vga std \
		-monitor "unix:$d/monitor.sock,server,nowait" -serial "file:$d/serial.log" -pidfile "$d/qemu.pid" -daemonize \
		-netdev "user,id=n0,hostfwd=tcp:127.0.0.1:$ph-:80,hostfwd=tcp:127.0.0.1:$ps-:22" "$@"
	for i in $(seq 1 120); do curl -fsS -m 2 "http://127.0.0.1:$ph/api/health" >/dev/null 2>&1 && return 0; sleep 3; done
	return 1
}
stop() { kill "$(cat "$R/$1/qemu.pid")" 2>/dev/null; sleep 1; }
pw() { # la bannière (mot de passe) arrive sur la console série après l'API : on l'attend
	local p=""
	for i in $(seq 1 40); do
		p=$(grep -a "Mot de passe" "$R/$1/serial.log" | tail -1 | awk '{print $NF}' | tr -d '\r')
		[ -n "$p" ] && break; sleep 2
	done
	echo "$p"
}
sh_pw() { sshpass -p "$2" ssh "${o[@]}" -p "$1" root@127.0.0.1 "$3" 2>/dev/null; }
screen() { printf 'screendump %s\n' "$PWD/$R/$1/screen.ppm" | python3 -c 'import socket,sys,time;s=socket.socket(socket.AF_UNIX);s.connect(sys.argv[1]);time.sleep(.3);s.recv(4096);s.sendall(sys.stdin.read().encode());time.sleep(1)' "$R/$1/monitor.sock"; }

seed() { # nocloud|configdrive -> iso
	local kind=$1 dir=$R/seed-$1; rm -rf "$dir"; mkdir -p "$dir"
	if [ "$kind" = nocloud ]; then
		printf 'instance-id: iid-qa\nlocal-hostname: vigie-pve\n' > "$dir/meta-data"
		printf '#cloud-config\npassword: Pve-Cloud-1\nssh_authorized_keys:\n  - %s\n' "$(cat "$KEY.pub")" > "$dir/user-data"
		xorriso -as mkisofs -quiet -o "$R/seed-nocloud.iso" -V cidata -r -J "$dir"
	else
		mkdir -p "$dir/openstack/latest"
		jq -n --arg k "$(cat "$KEY.pub")" '{hostname:"vigie-os.novalocal", name:"vigie-os", public_keys:{qa:$k}, admin_pass:"Os-Admin-Pass1"}' > "$dir/openstack/latest/meta_data.json"
		xorriso -as mkisofs -quiet -o "$R/seed-configdrive.iso" -V config-2 -r -J "$dir"
	fi
}

for s in ${@:-iso-bios iso-disk iso-uefi qcow2-nocloud qcow2-configdrive}; do
	echo "== $s"
	case $s in
	iso-bios)
		if boot iso-bios 18101 2301 -cdrom "$ISO" -device vmxnet3,netdev=n0; then
			P=$(pw iso-bios)
			check "démarrage ISO BIOS, health" true
			check "mode live (/run/vigie/live)" '[ "$(sh_pw 2301 "$P" "test -e /run/vigie/live && echo oui")" = oui ]'
			check "/data non monté" '[ -z "$(sh_pw 2301 "$P" "grep -w /data /proc/mounts")" ]'
			check "média de boot = ISO" 'sh_pw 2301 "$P" "grep -E \"/media/(sr0|cdrom)\" /proc/mounts" | grep -q iso9660'
			sleep 5; screen iso-bios
		else ko "démarrage ISO BIOS"; fi
		stop iso-bios ;;
	iso-disk)
		cp --sparse=always dist/disk.raw "$R/disk-present.raw"
		if boot iso-disk 18102 2302 -cdrom "$ISO" -boot d -device vmxnet3,netdev=n0 \
			-drive "file=$R/disk-present.raw,format=raw,if=none,id=d0" -device pvscsi,id=pv0 -device scsi-hd,drive=d0,bus=pv0.0; then
			P=$(pw iso-disk)
			check "ISO avec disque vigie présent : /data non monté" '[ -z "$(sh_pw 2302 "$P" "grep -w /data /proc/mounts")" ]'
			check "ISO avec disque : boot sur l'ISO" 'sh_pw 2302 "$P" "grep -E \"/media/(sr0|cdrom)\" /proc/mounts" | grep -q iso9660'
			sh_pw 2302 "$P" "grep /media /proc/mounts" | sed 's/^/       /'
		else ko "démarrage ISO + disque"; fi
		stop iso-disk ;;
	iso-uefi)
		cp /usr/share/OVMF/OVMF_VARS_4M.fd "$R/vars.fd" 2>/dev/null || cp /usr/share/ovmf/OVMF.fd "$R/vars.fd"
		FW=(-drive if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd -drive "if=pflash,format=raw,file=$R/vars.fd")
		[ -f /usr/share/OVMF/OVMF_CODE_4M.fd ] || FW=(-bios /usr/share/ovmf/OVMF.fd)
		if boot iso-uefi 18103 2303 "${FW[@]}" -cdrom "$ISO" -device vmxnet3,netdev=n0; then
			check "démarrage ISO UEFI, health" true
			P=$(pw iso-uefi); check "UEFI : EFI présent" '[ "$(sh_pw 2303 "$P" "test -d /sys/firmware/efi && echo oui")" = oui ]'
			sleep 5; screen iso-uefi
		else ko "démarrage ISO UEFI"; fi
		stop iso-uefi ;;
	qcow2-nocloud)
		seed nocloud; cp "$QCOW" "$R/n.qcow2"
		if boot qcow2-nocloud 18104 2304 -drive "file=$R/n.qcow2,if=none,id=d0" -device virtio-scsi-pci,id=vs -device scsi-hd,drive=d0,bus=vs.0 \
			-device virtio-net-pci,netdev=n0 -cdrom "$R/seed-nocloud.iso" \
			-device virtio-serial -chardev socket,id=qga,path=$R/qga.sock,server=on,wait=off -device virtserialport,chardev=qga,name=org.qemu.guest_agent.0; then
			check "qcow2 virtio : health" true
			check "NoCloud : nom d'hôte vigie-pve" '[ "$(curl -s http://127.0.0.1:18104/api/health | jq -r .hostname)" = vigie-pve ]'
			check "NoCloud : mot de passe (SSH)" '[ "$(sh_pw 2304 Pve-Cloud-1 "echo oui")" = oui ]'
			check "NoCloud : clé SSH" '[ "$(ssh "${o[@]}" -i "$KEY" -p 2304 root@127.0.0.1 "echo oui" 2>/dev/null)" = oui ]'
			check "plateforme kvm, open-vm-tools retiré, guest agent actif" '[ "$(ssh "${o[@]}" -i "$KEY" -p 2304 root@127.0.0.1 "cat /run/vigie/platform; rc-status default | grep -c open-vm-tools; rc-service qemu-guest-agent status | grep -c started" 2>/dev/null | tr "\n" " ")" = "kvm 0 1 " ]'
			check "outils : ethtool, smartctl, ipmitool, nmap, tcpdump" 'ssh "${o[@]}" -i "$KEY" -p 2304 root@127.0.0.1 "ethtool -i eth0 >/dev/null && smartctl --version >/dev/null && ipmitool -V >/dev/null && nmap --version >/dev/null && tcpdump --version >/dev/null 2>&1 && echo oui" 2>/dev/null | grep -q oui'
			check "disque système vu en virtio-scsi (sda)" 'ssh "${o[@]}" -i "$KEY" -p 2304 root@127.0.0.1 "grep -w /data /proc/mounts" 2>/dev/null | grep -q sda'
		else ko "démarrage qcow2 NoCloud"; fi
		stop qcow2-nocloud ;;
	qcow2-configdrive)
		seed configdrive; cp "$QCOW" "$R/c.qcow2"
		if boot qcow2-configdrive 18105 2305 -drive "file=$R/c.qcow2,if=none,id=d0" -device virtio-scsi-pci,id=vs -device scsi-hd,drive=d0,bus=vs.0 \
			-device virtio-net-pci,netdev=n0 -cdrom "$R/seed-configdrive.iso"; then
			check "config-drive : nom d'hôte vigie-os" '[ "$(curl -s http://127.0.0.1:18105/api/health | jq -r .hostname)" = vigie-os ]'
			check "config-drive : admin_pass (SSH)" '[ "$(sh_pw 2305 Os-Admin-Pass1 "echo oui")" = oui ]'
			check "config-drive : clé SSH" '[ "$(ssh "${o[@]}" -i "$KEY" -p 2305 root@127.0.0.1 "echo oui" 2>/dev/null)" = oui ]'
		else ko "démarrage qcow2 config-drive"; fi
		stop qcow2-configdrive ;;
	esac
done
echo "== bilan : $pass OK, $fail échec(s)"
[ "$fail" = 0 ]
