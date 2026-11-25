#!/usr/bin/env bash
# QA phase 7 : parcours complet de l'interface web dans un Chromium headless (conteneur test/ui).
# Usage : image/lab-qa-ui.sh [ip]   FLOW=ui pour ne pas lancer de test, BASE_S/DUR_S pour les durées.
set -euo pipefail
cd "$(dirname "$0")/.."
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
IP="${1:-$(govc vm.ip "$VIGIE_VM_NAME")}"
docker build -q -t vigie-ui-qa test/ui >/dev/null
rm -rf dist/ui-qa; mkdir -p dist/ui-qa
docker run --rm --network host -u "$(id -u):$(id -g)" -v "$PWD/dist/ui-qa:/out" \
	-e VIGIE_URL="https://$IP/" -e VIGIE_PASSWORD="$(cat dist/lab-password)" \
	-e VC_URL="$GOVC_URL" -e VC_USER="$GOVC_USERNAME" -e VC_PASS="$GOVC_PASSWORD" \
	-e FLOW="${FLOW:-full}" -e NET_TARGET="${NET_TARGET:-}" -e BASE_S="${BASE_S:-60}" -e DUR_S="${DUR_S:-80}" -e OUT=/out vigie-ui-qa
