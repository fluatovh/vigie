#!/usr/bin/env bash
# QA phase 8 sur le lab : prévalidation des hôtes, déplacement simple, tournée de comparaison sur
# deux hôtes × 2 cycles avec override DRS, contrôle de la restauration du DRS et du retour à l'hôte
# de départ. Usage : image/lab-qa-tour.sh [hôte A] [hôte B]   (noms, défaut 172.17.136.50 et .51)
set -euo pipefail
export LC_ALL=C.UTF-8
cd "$(dirname "$0")/.."
. image/vigie-api.sh
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
A="${1:-172.17.136.50}"; B="${2:-172.17.136.51}"
VIGIE_IP=$(govc vm.ip "$VIGIE_VM_NAME"); VIGIE_IP=$VIGIE_IP
VIGIE_TOKEN=$(cat dist/lab-password); VIGIE_TOKEN=$(api POST /tokens '{"name":"qa-tour"}' | jq -r .token)
sha=$(api GET "/vsphere/thumbprint?url=$(printf '%s' "$GOVC_URL" | jq -sRr @uri)" | jq -r .sha256)
api POST /vsphere/connect "$(jq -nc --arg u "$GOVC_URL" --arg n "$GOVC_USERNAME" --arg p "$GOVC_PASSWORD" --arg t "$sha" '{url:$u,username:$n,password:$p,thumbprint:$t}')" >/dev/null
nfs=$(api GET /datastores | jq -r '[.datastores[] | select(.type=="NFS" or .type=="NFS41")][0].ref')
api GET /testdisk | jq -e '.record != null' >/dev/null 2>&1 || job_run "$(jq -nc --arg d "$nfs" '{profile:"testdisk.create", datastore:$d, size_gb:4}')" >/dev/null
chk=$(api GET "/migrate/check?datastore=$nfs")
echo "$chk" | jq -r '"hôte courant : \(.current.name)", "DRS : \(.drs.enabled) \(.drs.default_behavior), override \(.drs.override), needs_manual \(.drs.needs_manual)", (.checks[] | "  \(.host) : \(if .current then "courant" elif .ok then "possible" else "impossible \(.errors)" end)")'
ra=$(echo "$chk" | jq -r --arg h "$A" '.checks[] | select(.host==$h) | .host_ref'); rb=$(echo "$chk" | jq -r --arg h "$B" '.checks[] | select(.host==$h) | .host_ref')
echo "== DRS de l'appliance avant (govc)"; govc cluster.override.info -cluster "/$GOVC_DATACENTER/host/Cluster1" 2>/dev/null | grep -A3 "$VIGIE_VM_NAME" || echo "  aucun override"
echo "== tournée $A / $B, 2 cycles, baseline 40 s, écriture 60 s, lecture 60 s"
t0=$(date +%s)
id=$(job_run "$(jq -nc --arg a "$ra" --arg b "$rb" '{profile:"tour", hosts:[$a,$b], cycles:2, baseline_s:40, write_s:60, read_s:60}')" 2>&1 >/dev/null | grep -E "vMotion [0-9]+ %|terminé —|^job .* :|: (OK|ATTENTION|ALERTE) —|DRS" | grep -vE "vMotion [1-9][0-9]? %" | tee /dev/stderr | grep -oE '^job [0-9a-f-]+ :' | awk '{print $2}' | tail -1)
f="dist/job-$id.json"; echo "== durée : $(( $(date +%s) - t0 )) s"
jq -r '.result | "VERDICT \(.comparison.level) — \(.comparison.sentence)",
 "  profil de chaque passage : \(.legs[0].report.write.profile.text) ; puis \(.legs[0].report.read.profile.text) ; ligne de base \(.legs[0].report.options.baseline_s) s",
 (.comparison.hosts[] | "  \(.host) : \(.runs) passages, écriture \(.write_mbps_median|round) Mo/s, lecture \(.read_mbps_median|round) Mo/s, ratio \(.tx_ratio_median*1000|round/1000), p99 écriture \(.write_p99_ms_median*100|round/100) ms, ×\(.write_vs_best*100|round/100) du meilleur, \(.level)"),
 (.legs[] | "  cycle \(.cycle) \(.host) : vMotion \(if .moved then "\(.vmotion_s|round) s" else "—" end), écriture \(.report.write.mbps|round), lecture \(.report.read.mbps|round), ratio \(.report.tx_ratio.value*1000|round/1000) (\(.report.tx_ratio.samples) éch.), \(.report.verdict.level)"),
 "  départ : \(.home_host) ; override DRS : \(if .drs then "posé puis restauré" else "non" end)", (.warnings // [] | .[] | "  avertissement : \(.)")' "$f"
echo "== après la tournée"
echo "  hôte de l'appliance : $(govc ls -L $(govc vm.info -json "$VIGIE_VM_NAME" | jq -r '.virtualMachines[0].runtime.host | "\(.type):\(.value)"') | xargs basename)"
govc cluster.override.info -cluster "/$GOVC_DATACENTER/host/Cluster1" 2>/dev/null | grep -A3 "$VIGIE_VM_NAME" || echo "  aucun override (DRS restauré)"
