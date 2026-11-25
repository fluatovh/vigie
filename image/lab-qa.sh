#!/usr/bin/env bash
# QA de l'API de la phase 2 contre la VM de dev déployée sur le lab (govc + lab.env + dist/lab-password).
# Usage : image/lab-qa.sh [ip]
set -euo pipefail
export LC_ALL=C.UTF-8
cd "$(dirname "$0")/.."
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
IP="${1:-$(govc vm.ip "$VIGIE_VM_NAME")}"
PW=$(cat dist/lab-password)
API="http://$IP/api"
H=(-H "Authorization: Bearer $PW" -H "Content-Type: application/json")
call() { # méthode chemin [corps]
	local m=$1 p=$2 b=${3:-}
	local out code
	out=$(curl -sS -m 120 -X "$m" "${H[@]}" ${b:+-d "$b"} -w '\n%{http_code}' "$API$p")
	code=${out##*$'\n'}; out=${out%$'\n'*}
	printf '%s %s -> HTTP %s\n' "$m" "$p" "$code" >&2
	[ "$code" -lt 400 ] || { echo "$out" >&2; return 1; }
	printf '%s' "$out"
}
echo "== sans jeton : attendu 401"; curl -s -o /dev/null -w '%{http_code}\n' "$API/vsphere/status"
echo "== empreinte du vCenter"
tp=$(call GET "/vsphere/thumbprint?url=$(printf '%s' "$GOVC_URL" | jq -sRr @uri)")
echo "$tp" | jq -c '{host, sha256, subject, self_signed, trusted}'
sha=$(echo "$tp" | jq -r .sha256)
echo "== connexion"
call POST /vsphere/connect "$(jq -nc --arg u "$GOVC_URL" --arg n "$GOVC_USERNAME" --arg p "$GOVC_PASSWORD" --arg t "$sha" '{url:$u,username:$n,password:$p,thumbprint:$t}')" | jq -c '{connected, username, server: .server.full_name}'
echo "== mauvaise empreinte : attendu 502"
call POST /vsphere/connect "$(jq -nc --arg u "$GOVC_URL" --arg n "$GOVC_USERNAME" --arg p "$GOVC_PASSWORD" '{url:$u,username:$n,password:$p,thumbprint:"00:11:22"}')" >/dev/null 2>&1 && echo "ERREUR: accepté" || echo "refusé (OK)"
echo "== self"
self=$(call GET /self); echo "$self" | jq -c '{method, vm: .vm.name, host: .host.name, host_version: .host.version, cluster: .cluster.name}'
echo "== datastores de l'hôte courant"
ds=$(call GET /datastores); echo "$ds" | jq -r '.datastores[] | [.type, .name, (.nfs_server // "-"), (.nfs_export // "-"), (.free_bytes/1e9|floor|tostring+" Go libres"), (.host_count|tostring+" hôtes"), (if .mounted then "monté" else "non monté" end)] | @tsv' | column -t | head -12
nfs=$(echo "$ds" | jq -r '[.datastores[] | select(.type=="NFS" or .type=="NFS41")][0].ref')
echo "== hôtes montant $nfs"
call GET "/hosts?datastore=$nfs" | jq -r '.hosts[] | [.name, .version, .connection_state, (if .vmotion_enabled then "vmotion" else "-" end)] | @tsv' | column -t | head -12
echo "== chemin hôte courant -> serveur NFS"
call GET "/path?datastore=$nfs" | jq -c '{nfs_server, nfs_ip, via, vmk: .vmk.device, vmk_ip: .vmk.ip, mtu: .vmk.mtu, portgroup, switch, switch_type, uplinks: [.uplinks[] | "\(.device):\(.role):\(.speed_mb)Mb"], notes}'
echo "== privilèges"
call GET "/privileges?datastore=$nfs" | jq -c '{user, missing}'
echo "== santé (vsphere_connected)"
curl -s "$API/health" | jq -c '{version, vsphere_connected}'
echo "== déconnexion"
call POST /vsphere/disconnect | jq -c '{connected}'
