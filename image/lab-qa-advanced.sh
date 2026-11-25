#!/usr/bin/env bash
# QA phase 8b sur le lab : test avancé (standard + matrice 4 Kio) par l'API avec un jeton, affichage
# des chiffres titres, des paliers et du contrôle croisé. Usage : image/lab-qa-advanced.sh [ip]
set -euo pipefail
export LC_ALL=C.UTF-8
cd "$(dirname "$0")/.."
. image/vigie-api.sh
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
VIGIE_IP="${1:-$(govc vm.ip "$VIGIE_VM_NAME")}"
VIGIE_TOKEN=$(cat dist/lab-password)
VIGIE_TOKEN=$(api POST /tokens '{"name":"qa-advanced"}' | jq -r .token)
sha=$(api GET "/vsphere/thumbprint?url=$(printf '%s' "$GOVC_URL" | jq -sRr @uri)" | jq -r .sha256)
api POST /vsphere/connect "$(jq -nc --arg u "$GOVC_URL" --arg n "$GOVC_USERNAME" --arg p "$GOVC_PASSWORD" --arg t "$sha" '{url:$u,username:$n,password:$p,thumbprint:$t}')" >/dev/null
nfs=$(api GET /datastores | jq -r '[.datastores[] | select(.type=="NFS" or .type=="NFS41")][0].ref')
api GET /testdisk | jq -e '.record != null' >/dev/null 2>&1 || job_run "$(jq -nc --arg d "$nfs" '{profile:"testdisk.create", datastore:$d, size_gb:4}')" >/dev/null
t0=$(date +%s)
id=$(job_run '{"profile":"advanced"}' 2>&1 >/dev/null | grep -vE "palier [0-9]+/[0-9]+ .*: ([0-9]+ s|chauffe)|continue :|préremplissage [0-9]+ %" | tee /dev/stderr | grep -oE '^job [0-9a-f-]+ :' | awk '{print $2}' | tail -1)
f="dist/job-$id.json"
echo "== durée : $(( $(date +%s) - t0 )) s"
jq -r '.result | "VERDICT \(.verdict.level) (préréglage \(.verdict.preset))",
  "  débit séquentiel : écriture \(.headline.write_mbps|round) Mo/s, lecture \(.headline.read_mbps|round) Mo/s",
  "  IOPS utiles 4 Kio mixte 70/30 (p99 < \(.headline.ceiling_ms) ms) : \(.headline.useful_iops|round) à QD\(.headline.useful_qd)",
  "  latence 4 Kio QD1 : lecture p50 \(.headline.read_qd1_p50_ms*100|round/100) / p99 \(.headline.read_qd1_p99_ms*100|round/100) ms, écriture p50 \(.headline.write_qd1_p50_ms*100|round/100) / p99 \(.headline.write_qd1_p99_ms*100|round/100) ms",
  "  profil séquentiel : \(.standard.write.profile.text)",
  "  profil aléatoire (premier palier) : \(.steps[0].profile.text)",
  "  worker sur \(.worker_cpus) CPU ; instance disque virtuel \(.vdisk_instance)"' "$f"
echo "== paliers (IOPS appliance / vSphere, écart, p50, p99, CPU)"
jq -r '.result as $r | range(0; $r.steps|length) as $i | $r.steps[$i] as $s | $r.points[$i] as $p |
  "  \($s.step.name|.+"                    "|.[0:22]) \($s.iops|round|tostring|.+"        "|.[0:7]) / \(($p.host_iops // 0)|round|tostring|.+"        "|.[0:7]) écart \((($p.mismatch_pct // -1)*10|round/10)|tostring|.+"      "|.[0:5])% p50 \($s.latency.p50_us/1000*100|round/100) p99 \($s.latency.p99_us/1000*100|round/100) ms CPU \($p.cpu_pct|round)% \($p.flags // [] | join(","))"' "$f"
echo "== IOPS utiles par motif"
jq -r '.result.useful[] | "  \(.pattern) \(.block_kib) Kio : \(.iops|round) IO/s à QD\(.qd) (p99 \(.p99_ms*100|round/100) ms) ; brut \(.raw_max_iops|round) à QD\(.raw_max_qd) (p99 \(.raw_max_p99_ms*100|round/100) ms)"' "$f"
jq -r '.result.verdict.indicators[] | "  [\(.level)] \(.label) : \(.why)"' "$f"
jq -r '(.result.warnings // [])[] | "  avertissement : \(.)"' "$f"
