#!/usr/bin/env bash
# Déploie dist/vigie.ova sur le lab avec govc, d'après lab.env (racine du projet, hors git).
# Usage : image/deploy.sh [--name NOM] [--destroy]
set -euo pipefail
cd "$(dirname "$0")/.."
[ -f lab.env ] || { echo "lab.env manquant à la racine du projet"; exit 1; }
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
if [ "${1:-}" = "--name" ]; then VIGIE_VM_NAME=$2; VIGIE_HOSTNAME=$2; shift 2; fi
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER GOVC_DATASTORE GOVC_NETWORK
[ -n "${GOVC_HOST:-}" ] && export GOVC_HOST
[ -n "${GOVC_RESOURCE_POOL:-}" ] && export GOVC_RESOURCE_POOL
[ -n "${GOVC_FOLDER:-}" ] && export GOVC_FOLDER

exists() { govc vm.info -json "$VIGIE_VM_NAME" | jq -e '.virtualMachines != null and (.virtualMachines|length) > 0' >/dev/null; }

if [[ "${1:-}" == "--destroy" ]]; then
	exists && { govc vm.power -off -force "$VIGIE_VM_NAME" >/dev/null 2>&1 || true; govc vm.destroy "$VIGIE_VM_NAME"; echo "VM $VIGIE_VM_NAME détruite"; }
	exit 0
fi
OVA=dist/vigie.ova
[ -e "$OVA" ] || { echo "$OVA manquant (make ova)"; exit 1; }
# Mot de passe de la VM de dev : celui de lab.env, sinon généré et conservé dans dist/lab-password
# (hors git) pour les scripts de QA.
if [ -z "${VIGIE_UI_PASSWORD:-}" ]; then
	# stable d'un déploiement à l'autre : on réutilise dist/lab-password s'il existe
	VIGIE_UI_PASSWORD=$(cat dist/lab-password 2>/dev/null || true)
	[ -n "$VIGIE_UI_PASSWORD" ] || VIGIE_UI_PASSWORD=$(head -c 12 /dev/urandom | base64 | tr -d '/+=' | head -c 16)
fi
umask 077; printf '%s\n' "$VIGIE_UI_PASSWORD" > dist/lab-password; umask 022

if exists; then
	echo "La VM $VIGIE_VM_NAME existe déjà : remplacement."
	govc vm.power -off -force "$VIGIE_VM_NAME" >/dev/null 2>&1 || true
	govc vm.destroy "$VIGIE_VM_NAME"
fi

spec=$(mktemp)
govc import.spec "$OVA" | jq \
	--arg name "$VIGIE_VM_NAME" --arg net "${GOVC_NETWORK:-VM Network}" \
	--arg hostname "${VIGIE_HOSTNAME:-vigie-dev}" --arg ip "${VIGIE_IP:-}" --arg netmask "${VIGIE_NETMASK:-255.255.255.0}" \
	--arg gateway "${VIGIE_GATEWAY:-}" --arg dns "${VIGIE_DNS:-}" --arg ntp "${VIGIE_NTP:-}" --arg pw "${VIGIE_UI_PASSWORD:-}" '
	.Name = $name | .DiskProvisioning = "thin" | .IPAllocationPolicy = "dhcpPolicy" | .PowerOn = false | .WaitForIP = false
	| .NetworkMapping |= map(.Network = $net)
	| .PropertyMapping |= map(.Value = ({hostname:$hostname, ip:$ip, netmask:$netmask, gateway:$gateway, dns:$dns, ntp:$ntp, ui_password:$pw}[.Key] // .Value))
' > "$spec"
echo "Import de $(readlink -f "$OVA") -> $VIGIE_VM_NAME"
govc import.ova -options "$spec" "$OVA" >/dev/null
rm -f "$spec"
govc vm.power -on "$VIGIE_VM_NAME" >/dev/null
echo "VM démarrée, attente de l'IP (open-vm-tools, max 5 min)…"
ip=$(govc vm.ip -wait 5m "$VIGIE_VM_NAME")
echo "IP : $ip"
for i in $(seq 1 30); do
	if curl -fsS -m 3 "http://$ip/api/health" 2>/dev/null; then echo; echo "health OK  ->  https://$ip/   ssh root@$ip   mot de passe : $VIGIE_UI_PASSWORD"; exit 0; fi
	sleep 2
done
echo "health KO après 60 s (voir la console vSphere)"; exit 1
