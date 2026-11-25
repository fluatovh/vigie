#!/usr/bin/env bash
# QA phase 4 sur le lab : disque de test sur NFS, préremplissage, écriture/lecture séquentielles,
# lecture aléatoire 4k, puis suppression. Usage : image/lab-qa-bench.sh [ip] [size_gb] [duration_s]
# SYNC=false pour une comparaison sans O_SYNC (par défaut : O_SYNC actif, §6.2).
set -euo pipefail
export LC_ALL=C.UTF-8
cd "$(dirname "$0")/.."
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
IP="${1:-$(govc vm.ip "$VIGIE_VM_NAME")}"; SIZE="${2:-4}"; DUR="${3:-20}"
PW=$(cat dist/lab-password)
API="http://$IP/api"
H=(-H "Authorization: Bearer $PW" -H "Content-Type: application/json")
call() { local m=$1 p=$2 b=${3:-}; local out code
	out=$(curl -sS -m 1800 -X "$m" "${H[@]}" ${b:+-d "$b"} -w '\n%{http_code}' "$API$p"); code=${out##*$'\n'}; out=${out%$'\n'*}
	printf '%s %s -> HTTP %s\n' "$m" "$p" "$code" >&2; [ "$code" -lt 400 ] || { echo "$out" >&2; return 1; }; printf '%s' "$out"; }
sha=$(call GET "/vsphere/thumbprint?url=$(printf '%s' "$GOVC_URL" | jq -sRr @uri)" | jq -r .sha256)
call POST /vsphere/connect "$(jq -nc --arg u "$GOVC_URL" --arg n "$GOVC_USERNAME" --arg p "$GOVC_PASSWORD" --arg t "$sha" '{url:$u,username:$n,password:$p,thumbprint:$t}')" >/dev/null
nfs=$(call GET /datastores | jq -r '[.datastores[] | select(.type=="NFS" or .type=="NFS41")][0].ref')
echo "== disque de test $SIZE Go sur $nfs"
call GET /testdisk | jq -e '.record != null' >/dev/null 2>&1 || call POST /testdisk "$(jq -nc --arg d "$nfs" --argjson s "$SIZE" '{datastore:$d,size_gb:$s}')" | jq -c '{ready, device: .guest.device.path}'
echo "== bench (prefill + seqwrite/seqread ${DUR}s 1 Mio QD4 + randread 4k QD16 10s)"
t0=$(date +%s)
SYNC="${SYNC:-true}"
rep=$(call POST /bench/run "$(jq -nc --argjson d "$DUR" --argjson sy "$SYNC" '{prefill:true, sync:$sy, steps:[
  {kind:"seqwrite", duration_s:$d, block_kib:1024, qd:4, name:"écriture séquentielle"},
  {kind:"seqread",  duration_s:$d, block_kib:1024, qd:4, name:"lecture séquentielle"},
  {kind:"randread", duration_s:10, warmup_s:3, block_kib:4, qd:16, name:"lecture aléatoire 4k QD16"},
  {kind:"randwrite", duration_s:10, warmup_s:3, block_kib:4, qd:1, name:"écriture aléatoire 4k QD1"}]}')")
echo "durée totale : $(( $(date +%s) - t0 )) s ; CPU du worker sur : $(echo "$rep" | jq -c .worker_cpus)"
echo "$rep" | jq -r 'if .prefill == null then "prefill : déjà fait (signature), non refait" else .prefill | "prefill : \(.bytes/1e9|.*100|round/100) Go en \(.measured_s|round) s = \(.mbps|round) Mo/s, p99 \(.latency.p99_us/1000|.*100|round/100) ms\n    profil : \(.profile.text)" end'
echo "$rep" | jq -r '.results[] | "\(.step.name): \(.mbps|round) Mo/s, \(.iops|round) IOPS, p50 \(.latency.p50_us/1000|.*100|round/100) ms, p99 \(.latency.p99_us/1000|.*100|round/100) ms, max \(.latency.max_us/1000|round) ms, CPU worker \(.cpu_pct|round)%, erreurs \(.errors), \(.seconds|length) s\n    profil : \(.profile.text)"'
echo "== série à la seconde (écriture séquentielle, Mo/s) :"; echo "$rep" | jq -c '[.results[0].seconds[] | (.bytes/1e6|round)]'
echo "== second run sans prefill (signature) :"; call POST /bench/run '{"steps":[{"kind":"seqread","duration_s":5,"block_kib":1024,"qd":4}]}' | jq -c '{prefill_refait: (.prefill != null), mbps: (.results[0].mbps|round)}'
echo "== dernier rapport :"; call GET /bench/last | jq -c '{finished, steps: (.results|length)}'
[ "${KEEP_DISK:-0}" = 1 ] || { echo "== suppression du disque de test"; call DELETE /testdisk | jq -c '{record: (.record != null)}'; }
call POST /vsphere/disconnect >/dev/null
