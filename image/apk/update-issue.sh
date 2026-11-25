#!/bin/sh
# Écrit /etc/issue (affiché sur la console VMware avant le login) et /etc/motd (SSH).
# Appelé au boot (vigie-issue), par le hook udhcpc, et par vigie-init.
ver=$(/usr/bin/vigie version 2>/dev/null | awk '{print $2}')
host=$(hostname 2>/dev/null)
ip=$(ip -4 addr show 2>/dev/null | awk '/inet / && !/127\./ {sub("/.*","",$2); print $2; exit}')
if grep -qs 'inet dhcp' /etc/network/interfaces; then mode=DHCP; else mode=statique; fi
token=$(cat /data/vigie/first-access-token 2>/dev/null)
fp=$(cat /data/vigie/tls.fingerprint 2>/dev/null)

tmp=$(mktemp)
{
	echo
	echo "  vigie ${ver:-?}  -  $host"
	echo "  ------------------------------------------------"
	if [ -n "$ip" ]; then
		echo "  IP    : $ip ($mode)"
		echo "  Web   : https://$ip/"
		echo "  SSH   : ssh root@$ip"
	else
		echo "  IP    : en attente ($mode)..."
	fi
	if [ -n "$fp" ]; then
		echo "  Certificat (SHA-256) : $(echo "$fp" | cut -c1-47)"
		echo "                         $(echo "$fp" | cut -c49-95)"
	fi
	if [ -n "$token" ]; then
		echo "  Mot de passe (root et interface web) : $token"
	else
		echo "  ATTENTION : aucun mot de passe défini, l'interface refuse toute connexion"
	fi
	echo
} > "$tmp"
cp "$tmp" /etc/motd
mv "$tmp" /etc/issue
chmod 644 /etc/issue /etc/motd
