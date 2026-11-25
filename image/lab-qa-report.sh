#!/usr/bin/env bash
# QA phase 8c sur le lab : personnalisation par l'API (logo SVG valide, puis SVG piégé refusé), test
# avancé puis test standard, téléchargement des rapports (simple et avant/après) par l'API, puis
# contrôle hors ligne et impression A4 (image/qa-report.sh). Usage : image/lab-qa-report.sh [ip]
set -euo pipefail
export LC_ALL=C.UTF-8
cd "$(dirname "$0")/.."
. image/vigie-api.sh
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
VIGIE_IP="${1:-$(govc vm.ip "$VIGIE_VM_NAME")}"
VIGIE_TOKEN=$(cat dist/lab-password)
VIGIE_TOKEN=$(api POST /tokens '{"name":"qa-report"}' | jq -r .token)
D=dist/report-qa; rm -rf "$D"; mkdir -p "$D"

echo "== personnalisation"
logo="data:image/svg+xml;base64,$(base64 -w0 web/favicon.svg)"
api PUT /branding "$(jq -nc --arg l "$logo" '{client:"Lab OVH (QA)",site:"PCC DC999",author:"vigie QA",comment:"Validation de la phase 8c sur le lab.",logo:$l}')" | jq -c '{client,site,author,logo:(.logo|length)}'
evil="data:image/svg+xml;base64,$(printf '<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>' | base64 -w0)"
api PUT /branding "$(jq -nc --arg l "$evil" '{client:"x",logo:$l}')" >/dev/null 2>"$D/evil.txt" && { echo "ÉCHEC : SVG piégé accepté"; exit 1; } || echo "SVG piégé refusé : $(cat "$D/evil.txt")"

echo "== vSphere et disque de test"
sha=$(api GET "/vsphere/thumbprint?url=$(printf '%s' "$GOVC_URL" | jq -sRr @uri)" | jq -r .sha256)
api POST /vsphere/connect "$(jq -nc --arg u "$GOVC_URL" --arg n "$GOVC_USERNAME" --arg p "$GOVC_PASSWORD" --arg t "$sha" '{url:$u,username:$n,password:$p,thumbprint:$t}')" >/dev/null
ds=$(api GET /datastores | jq -r '[.datastores[] | select(.name=="ssd-001557")][0].ref // ([.datastores[] | select(.type=="NFS" or .type=="NFS41")][0].ref)')
api GET /testdisk | jq -e '.record != null' >/dev/null 2>&1 || job_run "$(jq -nc --arg d "$ds" '{profile:"testdisk.create", datastore:$d, size_gb:4}')" >/dev/null

quiet() { grep -vE "palier [0-9]+/[0-9]+ .*: ([0-9]+ s|chauffe)|continue :|préremplissage [0-9]+ %" >&2; }
echo "== test avancé"
adv=$(job_run '{"profile":"advanced"}' 2> >(quiet))
echo "== test standard"
std=$(job_run '{"profile":"standard"}' 2> >(quiet))
for id in "$adv" "$std"; do jq -r '"job \(.id) \(.profile) : \(.state) — \(.summary.level // "?") \(.summary.text // "")"' "dist/job-$id.json"; done

echo "== rapports par l'API"
get() { curl -sS -k -H "Authorization: Bearer $VIGIE_TOKEN" -D "$D/$2.headers" -o "$D/$2.html" -w "%{http_code} %{content_type} %{size_download} o" "https://$VIGIE_IP/api/$1"; echo " -> $2.html"; }
get "reports/$adv?download=1" lab-advanced
get "reports/$std" lab-standard
get "reports/compare?a=$adv&b=$std" lab-avant-apres
grep -i "content-disposition\|content-security" "$D/lab-advanced.headers" | sed 's/^/  /'
code=$(curl -sS -k -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $VIGIE_TOKEN" "https://$VIGIE_IP/api/reports/$(api GET '/jobs?profile=testdisk.create&limit=1' | jq -r '.jobs[0].id')")
echo "rapport d'un job testdisk.create : HTTP $code (422 attendu)"
echo "== historique (résumés)"
api GET '/jobs?limit=5' | jq -r '.jobs[] | "  \(.profile)\t\(.summary.host // "")\t\(.summary.level // "")\t\(.summary.text // "")"'
rm -f "$D"/*.headers "$D/evil.txt"
echo "== contrôle hors ligne et impression"
image/qa-report.sh "$D" | grep -v '\.p[nd][gf]$'
