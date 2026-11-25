#!/usr/bin/env bash
# QA phase 5 sur le lab : test standard complet (ligne de base, écriture, lecture, collecte, verdict).
# Usage : image/lab-qa-standard.sh [ip] [size_gb]   (BASELINE/WRITE/READ en secondes, défauts 60/80/80)
set -euo pipefail
export LC_ALL=C.UTF-8
cd "$(dirname "$0")/.."
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
IP="${1:-$(govc vm.ip "$VIGIE_VM_NAME")}"; SIZE="${2:-4}"
PW=$(cat dist/lab-password)
API="http://$IP/api"
H=(-H "Authorization: Bearer $PW" -H "Content-Type: application/json")
call() { local m=$1 p=$2 b=${3:-}; local out code
	out=$(curl -sS -m 3600 -X "$m" "${H[@]}" ${b:+-d "$b"} -w '\n%{http_code}' "$API$p"); code=${out##*$'\n'}; out=${out%$'\n'*}
	printf '%s %s -> HTTP %s\n' "$m" "$p" "$code" >&2; [ "$code" -lt 400 ] || { echo "$out" >&2; return 1; }; printf '%s' "$out"; }
sha=$(call GET "/vsphere/thumbprint?url=$(printf '%s' "$GOVC_URL" | jq -sRr @uri)" | jq -r .sha256)
call POST /vsphere/connect "$(jq -nc --arg u "$GOVC_URL" --arg n "$GOVC_USERNAME" --arg p "$GOVC_PASSWORD" --arg t "$sha" '{url:$u,username:$n,password:$p,thumbprint:$t}')" >/dev/null
nfs=$(call GET /datastores | jq -r '[.datastores[] | select(.type=="NFS" or .type=="NFS41")][0].ref')
call GET /testdisk | jq -e '.record != null' >/dev/null 2>&1 || call POST /testdisk "$(jq -nc --arg d "$nfs" --argjson s "$SIZE" '{datastore:$d,size_gb:$s}')" | jq -c '{ready}'
t0=$(date +%s)
rep=$(call POST /standard/run "$(jq -nc --argjson b "${BASELINE:-60}" --argjson w "${WRITE:-80}" --argjson r "${READ:-80}" '{baseline_s:$b, write_s:$w, read_s:$r}')")
echo "$rep" > dist/standard-last.json
echo "== durée totale : $(( $(date +%s) - t0 )) s"
echo "$rep" | jq -r '"hôte \(.host.name) (ESXi \(.host.version)), datastore \(.datastore.name) (\(.datastore.type) \(.datastore.nfs_server):\(.datastore.nfs_export))",
 "chemin : \(.path.nfs_ip) via \(.path.vmk.device) \(.path.vmk.ip) MTU \(.path.vmk.mtu) -> \(.path.switch_type) \(.path.switch) -> \([.path.uplinks[] | "\(.device)(\(.role),\(.speed_mb) Mb/s)"] | join(", "))",
 "horloge : hôte \(.clock.host_offset_ns/1e6|round) ms, vCenter \(.clock.server_offset_ns/1e6|round) ms par rapport à l appliance",
 "compteurs : \(.counters.tx), \(.counters.rx), \(.counters.dsWriteLat)"'
echo "$rep" | jq -r '(.prefill // empty | "préremplissage : \(.mbps|round) Mo/s\n    profil : \(.profile.text)"),
 (.write | "écriture : \(.mbps|round) Mo/s, p50 \(.latency.p50_us/1000|.*100|round/100) ms, p99 \(.latency.p99_us/1000|.*100|round/100) ms, CPU worker \(.cpu_pct|round) %\n    profil : \(.profile.text)"),
 (.read  | "lecture : \(.mbps|round) Mo/s, p50 \(.latency.p50_us/1000|.*100|round/100) ms, p99 \(.latency.p99_us/1000|.*100|round/100) ms\n    profil : \(.profile.text)")'
echo "$rep" | jq -r '"fenêtres (heure hôte) : base \(.windows.baseline.start[11:19])-\(.windows.baseline.end[11:19]), écriture \(.windows.write.start[11:19])-\(.windows.write.end[11:19]), lecture \(.windows.read.start[11:19])-\(.windows.read.end[11:19])",
 "ratio TX/écrit : \(.tx_ratio.value*1000|round/1000) (\(.tx_ratio.samples) échantillons, \(.tx_ratio.covered_s) s, réseau \(.tx_ratio.net_bytes/1e6|round) Mo, fond \(.tx_ratio.background_bytes/1e6|round) Mo, utile \(.tx_ratio.useful_bytes/1e6|round) Mo, fiable \(.tx_ratio.reliable)) \(.tx_ratio.notes // [] | join(" ; "))",
 "ratio RX/lu : \(.rx_ratio.value*1000|round/1000) (\(.rx_ratio.samples) échantillons, fiable \(.rx_ratio.reliable))",
 "latence datastore vue par l hôte : écriture \(.datastore_latency_ms.write // "?") ms, lecture \(.datastore_latency_ms.read // "?") ms ; IOPS hôte écriture \(.datastore_iops.write // "?" ), lecture \(.datastore_iops.read // "?")",
 "vmkernel.log : \(.vmkernel_log.new_lines // "?") nouvelles lignes, \(.vmkernel_log.nfs_warning_lines // "?") avertissements NFS ; événements : \(.events | length)"'
echo "$rep" | jq -r '.per_vmnic[] | "  \(.device) \(.role) \(.speed_mb) Mb/s : TX écriture \(.tx_write_mbps|round) Mo/s, RX lecture \(.rx_read_mbps|round) Mo/s, TX base \(.tx_baseline_mbps*10|round/10) Mo/s, erreurs RX \(.errors_rx) pertes RX \(.dropped_rx) erreurs TX \(.errors_tx)"'
echo "$rep" | jq -r '"VERDICT : \(.verdict.level) (préréglage \(.verdict.preset))", (.verdict.indicators[] | "  [\(.level)] \(.label) : \(.why)"), (.warnings // [] | .[] | "  avertissement : \(.)")'
[ "${KEEP_DISK:-0}" = 1 ] || call DELETE /testdisk | jq -c '{record: (.record != null)}'
call POST /vsphere/disconnect >/dev/null
