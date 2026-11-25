#!/usr/bin/env bash
# QA phase 6 sur le lab : jeton d'API, jobs (création disque, standard, bench annulé, 3 répétitions,
# suppression), verrou global, flux SSE et rejeu. Usage : image/lab-qa-jobs.sh [ip]
set -euo pipefail
export LC_ALL=C.UTF-8
cd "$(dirname "$0")/.."
. image/vigie-api.sh
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
VIGIE_IP="${1:-$(govc vm.ip "$VIGIE_VM_NAME")}"

echo "== 1. jeton d'API (créé avec le mot de passe, utilisé pour tout le reste)"
VIGIE_TOKEN=$(cat dist/lab-password)
tok=$(api POST /tokens '{"name":"qa-claude"}')
VIGIE_TOKEN=$(echo "$tok" | jq -r .token); echo "$VIGIE_TOKEN" > dist/lab-token; chmod 600 dist/lab-token
echo "jeton $(echo "$tok" | jq -r .id) créé ; création de jeton par un jeton : $(api POST /tokens '{"name":"x"}' 2>&1 >/dev/null | grep -o 'HTTP [0-9]*')"

echo "== 2. connexion vSphere"
sha=$(api GET "/vsphere/thumbprint?url=$(printf '%s' "$GOVC_URL" | jq -sRr @uri)" | jq -r .sha256)
api POST /vsphere/connect "$(jq -nc --arg u "$GOVC_URL" --arg n "$GOVC_USERNAME" --arg p "$GOVC_PASSWORD" --arg t "$sha" '{url:$u,username:$n,password:$p,thumbprint:$t}')" | jq -c '{connected, server: .server.full_name}'
nfs=$(api GET /datastores | jq -r '[.datastores[] | select(.type=="NFS" or .type=="NFS41")][0].ref')

echo "== 3. job testdisk.create (4 Go sur $nfs)"
id=$(job_run "$(jq -nc --arg d "$nfs" '{profile:"testdisk.create", datastore:$d, size_gb:4}')")
jq -c '.result | {ready, file: .record.file_name, prov: .record.provisioning}' "dist/job-$id.json"

echo "== 4. job standard (60/80/80 s) + tentative d'un second job pendant l'exécution"
j=$(api POST /jobs '{"profile":"standard"}'); sid=$(echo "$j" | jq -r .id)
sleep 5
echo "second job pendant le standard : $(api POST /jobs '{"profile":"bench","steps":[{"kind":"seqread","duration_s":5}]}' 2>&1 >/dev/null | grep -o 'HTTP [0-9]*.*' | cut -c1-90)"
echo "health : $(curl -s "http://$VIGIE_IP/api/health" | jq -c '{job_running}')"
job_follow "$sid"; api GET "/jobs/$sid" > "dist/job-$sid.json"
jq -r '.result.summary | "VERDICT \(.level) — écriture \(.write_mbps_median|round) Mo/s (p99 \(.write_p99_ms_median*100|round/100) ms), lecture \(.read_mbps_median|round) Mo/s, ratio TX/écrit \(.tx_ratio_median*1000|round/1000)\n  profil écriture : \(.write_profile)\n  profil lecture  : \(.read_profile)"' "dist/job-$sid.json"
echo "durée : $(jq -r '((.ended|.[0:19]+"Z"|fromdate) - (.started|.[0:19]+"Z"|fromdate))' "dist/job-$sid.json") s, lancé par $(jq -r .by "dist/job-$sid.json")"

echo "== 5. rejeu SSE du job terminé (après la fin)"
curl -sS -m 30 -H "Authorization: Bearer $VIGIE_TOKEN" "http://$VIGIE_IP/api/jobs/$sid/events" | grep -c '^event:' | xargs echo "événements rejoués :"

echo "== 6. job bench annulé après 8 s"
j=$(api POST /jobs '{"profile":"bench","steps":[{"kind":"seqwrite","duration_s":60,"block_kib":1024,"qd":4,"name":"écriture longue"}]}'); bid=$(echo "$j" | jq -r .id)
sleep 8; api POST "/jobs/$bid/cancel" >/dev/null; job_follow "$bid" | tail -3
api GET "/jobs/$bid" | jq -c '{state, error}'
echo "disque après annulation : $(api GET /testdisk | jq -c '{ready, in_vsphere}')"

echo "== 7. job standard × 3 répétitions (40/40/40 s)"
id=$(job_run '{"profile":"standard","repetitions":3,"baseline_s":40,"write_s":40,"read_s":40}')
jq -r '.result.runs[] | "  \(.verdict.level) écriture \(.write.mbps|round) Mo/s ratio \(.tx_ratio.value*1000|round/1000) (\(.tx_ratio.samples) éch.)"' "dist/job-$id.json"
jq -r '.result.summary | "médianes : écriture \(.write_mbps_median|round) Mo/s, lecture \(.read_mbps_median|round) Mo/s, ratio \(.tx_ratio_median*1000|round/1000), verdict \(.level)\n  profil écriture : \(.write_profile)"' "dist/job-$id.json"

echo "== 8. historique"
api GET "/jobs?limit=10" | jq -r '.jobs[] | "  \(.id) \(.profile) \(.state) par \(.by)"'

echo "== 9. job testdisk.delete"
id=$(job_run '{"profile":"testdisk.delete"}'); jq -c '.result | {record: (.record != null)}' "dist/job-$id.json"
api POST /vsphere/disconnect >/dev/null
