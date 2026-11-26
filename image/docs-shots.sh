#!/usr/bin/env bash
# Captures d'écran de la documentation (docs/img/) sur l'appliance de lab connectée au vCenter.
# Les identifiants du lab sont masqués : règles de remplacement dans docs-redact.json (hors git, à
# côté de lab.env), au format [["regex", "remplacement"], …].
# Usage : image/docs-shots.sh [ip]   (BASE_S, DUR_S, TRACE_TARGET facultatifs)
set -euo pipefail
cd "$(dirname "$0")/.."
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
IP="${1:-$(govc vm.ip "$VIGIE_VM_NAME")}"
[ -f docs-redact.json ] || { echo "docs-redact.json manquant : rien ne serait masqué"; exit 1; }
docker build -q -t vigie-ui-qa test/ui >/dev/null
rm -rf dist/docs-shots; mkdir -p dist/docs-shots
docker run --rm --network host -u "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD/dist/docs-shots:/out" --entrypoint node \
	-e VIGIE_URL="https://$IP/" -e VIGIE_PASSWORD="$(cat dist/lab-password)" \
	-e VC_URL="$GOVC_URL" -e VC_USER="$GOVC_USERNAME" -e VC_PASS="$GOVC_PASSWORD" \
	-e REDACT="$(cat docs-redact.json)" -e BASE_S="${BASE_S:-30}" -e DUR_S="${DUR_S:-60}" \
	-e TRACE_TARGET="${TRACE_TARGET:-}" -e OUT=/out vigie-ui-qa docs.mjs
