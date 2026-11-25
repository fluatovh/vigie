#!/bin/sh
# Configuration initiale de l'appliance, exécutée à chaque boot avant le réseau (service vigie-init).
# 1. Persistance sur /data (clés d'hôte dropbear, mot de passe root).
# 2. Lecture des propriétés OVF (ovf-env.xml sur le CD virtuel, transport ISO).
# 3. Application : hostname, réseau (DHCP par défaut), DNS, NTP, mot de passe.
set -u
DATA=/data
OVF=/run/vigie/ovfenv
mkdir -p /run/vigie

log() { echo "vigie-init: $*"; logger -t vigie-init "$*" 2>/dev/null || true; }

# --- 1. persistance ---------------------------------------------------------
if mountpoint -q "$DATA"; then
	mkdir -p "$DATA/dropbear" "$DATA/vigie"
	chmod 700 "$DATA/dropbear" "$DATA/vigie"
	if [ ! -L /etc/dropbear ]; then
		rm -rf /etc/dropbear
		ln -s "$DATA/dropbear" /etc/dropbear
	fi
else
	log "ATTENTION: $DATA n'est pas monté, rien ne sera persisté"
	mkdir -p "$DATA/vigie"
fi

# --- 2. propriétés OVF ------------------------------------------------------
: > "$OVF"
modprobe -q sr_mod 2>/dev/null; modprobe -q cdrom 2>/dev/null; modprobe -q iso9660 2>/dev/null
for dev in /dev/sr0 /dev/sr1 /dev/cdrom; do
	[ -b "$dev" ] || continue
	mkdir -p /mnt/ovf
	if mount -t iso9660 -o ro "$dev" /mnt/ovf 2>/dev/null; then
		if [ -f /mnt/ovf/ovf-env.xml ]; then
			# <Property oe:key="ip" oe:value="10.0.0.5"/>  ->  ip=10.0.0.5 (entités XML décodées)
			sed -n 's/.*oe:key="\([A-Za-z0-9_.]*\)"[^>]*oe:value="\([^"]*\)".*/\1=\2/p' /mnt/ovf/ovf-env.xml \
				| sed 's/&lt;/</g; s/&gt;/>/g; s/&quot;/"/g; s/&apos;/'"'"'/g; s/&amp;/\&/g' > "$OVF"
			log "propriétés OVF lues depuis $dev ($(grep -c . "$OVF") clés)"
		fi
		umount /mnt/ovf
		[ -s "$OVF" ] && break
	fi
done
chmod 600 "$OVF"

prop() { sed -n "s/^$1=//p" "$OVF" | head -n1; }
P_HOSTNAME=$(prop hostname); P_IP=$(prop ip); P_NETMASK=$(prop netmask)
P_GATEWAY=$(prop gateway); P_DNS=$(prop dns); P_NTP=$(prop ntp); P_PASSWORD=$(prop ui_password)

# --- 3a. hostname -----------------------------------------------------------
if [ -n "$P_HOSTNAME" ]; then
	echo "$P_HOSTNAME" > /etc/hostname
fi
hostname -F /etc/hostname 2>/dev/null || true

# --- 3b. réseau : statique si ip renseignée, sinon DHCP ----------------------
if [ -n "$P_IP" ]; then
	{
		echo "auto lo"
		echo "iface lo inet loopback"
		echo
		echo "auto eth0"
		echo "iface eth0 inet static"
		echo "    address $P_IP"
		echo "    netmask ${P_NETMASK:-255.255.255.0}"
		[ -n "$P_GATEWAY" ] && echo "    gateway $P_GATEWAY"
	} > /etc/network/interfaces
	if [ -n "$P_DNS" ]; then
		: > /etc/resolv.conf
		for d in $(echo "$P_DNS" | tr ',' ' '); do echo "nameserver $d" >> /etc/resolv.conf; done
	fi
	log "réseau statique $P_IP/${P_NETMASK:-255.255.255.0} gw ${P_GATEWAY:-aucune}"
else
	log "réseau en DHCP"
fi

# --- 3b'. configuration réseau choisie dans la console (phase D) : elle prime sur l'OVF ----
if [ "$(/usr/bin/vigie sysconf boot -data "$DATA/vigie" 2>/dev/null)" = "console" ]; then
	hostname -F /etc/hostname 2>/dev/null || true
	log "réseau : configuration choisie dans la console (prime sur les propriétés OVF)"
fi

# --- 3b''. plateforme (VMware, KVM) et métadonnées d'instance (config-drive, NoCloud) ------------
PLATFORM=other
case "$(cat /sys/class/dmi/id/sys_vendor 2>/dev/null) $(cat /sys/class/dmi/id/product_name 2>/dev/null)" in
	VMware*) PLATFORM=vmware ;;
	*QEMU*|*KVM*|*OpenStack*|*"Red Hat"*|*Proxmox*) PLATFORM=kvm ;;
esac
echo "$PLATFORM" > /run/vigie/platform
log "plateforme : $PLATFORM"
# outils d'invité : open-vm-tools sur VMware, qemu-guest-agent sur KVM (canal virtio présent)
[ "$PLATFORM" = vmware ] || rc-update -q del open-vm-tools default 2>/dev/null
[ -e /dev/virtio-ports/org.qemu.guest_agent.0 ] || rc-update -q del qemu-guest-agent default 2>/dev/null
CLOUD_DEV=""
for lbl in config-2 CONFIG-2 cidata CIDATA; do
	CLOUD_DEV=$(findfs "LABEL=$lbl" 2>/dev/null) && break
done
if [ -n "$CLOUD_DEV" ]; then
	mkdir -p /mnt/cloud
	if mount -o ro "$CLOUD_DEV" /mnt/cloud 2>/dev/null; then
		/usr/bin/vigie cloud apply -data "$DATA/vigie" -dir /mnt/cloud | while read -r l; do log "$l"; done
		umount /mnt/cloud
		: > /run/vigie/cloud-local
	fi
fi
hostname -F /etc/hostname 2>/dev/null || true

# --- 3c. NTP (busybox ntpd) --------------------------------------------------
if [ -n "$P_NTP" ]; then
	opts="-N"
	for s in $(echo "$P_NTP" | tr ',' ' '); do opts="$opts -p $s"; done
	echo "NTPD_OPTS=\"$opts\"" > /etc/conf.d/ntpd
fi

# --- 3d. mot de passe root (= mot de passe initial de l'UI) -----------------
# /etc est en RAM (diskless) : le hash est rejoué depuis /data à chaque boot.
HASHFILE="$DATA/vigie/root.hash"
# Le même secret sert à root (hash crypt pour /etc/shadow) et à l'interface web / API
# (hash bcrypt écrit par `vigie passwd` dans $DATA/vigie/ui.hash).
set_password() {
	hash=$(cryptpw -m sha512 "$1")
	printf '%s\n' "$hash" > "$HASHFILE"; chmod 600 "$HASHFILE"
	printf '%s' "$1" | /usr/bin/vigie passwd -data "$DATA/vigie" >/dev/null 2>&1 || log "échec de vigie passwd"
}
# Le mot de passe en clair est conservé dans $DATA/vigie/first-access-token (mode 600) pour être
# affiché sur la console VMware, qu'il soit généré ou fourni au déploiement (choix utilisateur du
# 17/11/2026 : la console doit toujours montrer le mot de passe).
# Un mot de passe changé dans la console (drapeau password.local) n'est plus écrasé par l'OVF.
if [ -n "$P_PASSWORD" ] && [ -e "$DATA/vigie/password.local" ]; then
	log "propriété OVF ui_password ignorée : mot de passe changé depuis la console"
elif [ -n "$P_PASSWORD" ]; then
	set_password "$P_PASSWORD"
	printf '%s\n' "$P_PASSWORD" > "$DATA/vigie/first-access-token"; chmod 600 "$DATA/vigie/first-access-token"
	log "mot de passe défini depuis les propriétés OVF"
elif [ ! -s "$HASHFILE" ] || [ ! -s "$DATA/vigie/ui.hash" ]; then
	token=$(head -c 9 /dev/urandom | base64 | tr '+/' 'xz')
	set_password "$token"
	printf '%s\n' "$token" > "$DATA/vigie/first-access-token"; chmod 600 "$DATA/vigie/first-access-token"
	log "aucun mot de passe fourni : jeton de premier accès généré (voir console)"
fi
if [ -s "$HASHFILE" ]; then
	printf 'root:%s\n' "$(cat "$HASHFILE")" | chpasswd -e 2>/dev/null || log "échec chpasswd"
fi

# --- 4. certificat HTTPS (auto-signé, généré une fois, empreinte affichée sur la console) ---
/usr/bin/vigie tls-init -data "$DATA/vigie" >/dev/null 2>&1 || log "échec de la génération du certificat TLS"

/usr/lib/vigie/update-issue
exit 0
