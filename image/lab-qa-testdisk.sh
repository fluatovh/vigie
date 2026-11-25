#!/usr/bin/env bash
# QA phase 3 sur le lab : création du disque de test sur un datastore NFS, contrôles, signature, suppression.
# Usage : image/lab-qa-testdisk.sh [ip] [size_gb]
set -euo pipefail
export LC_ALL=C.UTF-8
cd "$(dirname "$0")/.."
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
IP="${1:-$(govc vm.ip "$VIGIE_VM_NAME")}"; SIZE="${2:-1}"
PW=$(cat dist/lab-password)
API="http://$IP/api"
H=(-H "Authorization: Bearer $PW" -H "Content-Type: application/json")
call() { local m=$1 p=$2 b=${3:-}; local out code
	out=$(curl -sS -m 600 -X "$m" "${H[@]}" ${b:+-d "$b"} -w '\n%{http_code}' "$API$p"); code=${out##*$'\n'}; out=${out%$'\n'*}
	printf '%s %s -> HTTP %s\n' "$m" "$p" "$code" >&2; [ "$code" -lt 400 ] || { echo "$out" >&2; return 1; }; printf '%s' "$out"; }
sha=$(call GET "/vsphere/thumbprint?url=$(printf '%s' "$GOVC_URL" | jq -sRr @uri)" | jq -r .sha256)
call POST /vsphere/connect "$(jq -nc --arg u "$GOVC_URL" --arg n "$GOVC_USERNAME" --arg p "$GOVC_PASSWORD" --arg t "$sha" '{url:$u,username:$n,password:$p,thumbprint:$t}')" >/dev/null
nfs=$(call GET /datastores | jq -r '[.datastores[] | select(.type=="NFS" or .type=="NFS41")][0] | "\(.ref) \(.name)"')
echo "== datastore NFS cible : $nfs"
echo "== état initial"; call GET /testdisk | jq -c '{record: (.record != null), ready}'
echo "== création ($SIZE Go)"
st=$(call POST /testdisk "$(jq -nc --arg d "${nfs%% *}" --argjson s "$SIZE" '{datastore:$d,size_gb:$s}')")
echo "$st" | jq -c '{file: .record.file_name, uuid: .record.backing_uuid, prov: .record.provisioning, unit: .record.unit_number, device: .guest.device.path, wwid: .guest.device.wwid, ready, error}'
echo "$st" | jq -r '.guest.checks[] | "  [\(if .ok then "OK" else "KO" end)] \(.name) \(.detail)"'
case "$(echo "$st" | jq -r .record.file_name)" in "[${nfs#* }] vigie-$VIGIE_VM_NAME/testdisk.vmdk") echo "VMDK dans le dossier dédié du datastore NFS (OK)";; *) echo "ERREUR: chemin inattendu";; esac
govc datastore.ls -ds "${nfs#* }" "vigie-$VIGIE_VM_NAME"
echo "== vSphere voit le disque ?"; govc device.ls -vm "$VIGIE_VM_NAME" | grep -i disk
echo "== état (réemploi : signature relue)"; call GET /testdisk | jq -c '{in_vsphere, ready, signature: .signature.uuid}'
echo "== doublon refusé ?"; call POST /testdisk "$(jq -nc --arg d "${nfs%% *}" '{datastore:$d,size_gb:1}')" >/dev/null 2>&1 && echo "ERREUR: accepté" || echo "refusé (OK)"
echo "== suppression"; call DELETE /testdisk | jq -c '{record: (.record != null)}'
govc device.ls -vm "$VIGIE_VM_NAME" | grep -i disk
echo "== dossier supprimé du datastore ?"; govc datastore.ls -ds "${nfs#* }" "vigie-$VIGIE_VM_NAME" 2>/dev/null && echo "ERREUR: dossier encore présent" || echo "(dossier absent : OK)"
call POST /vsphere/disconnect >/dev/null
