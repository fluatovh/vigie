#!/usr/bin/env bash
# Parcours de l'interface web du lab dans une langue (défaut : de) ; relève les textes non traduits.
# Usage : LANG_UI=de image/lab-qa-i18n.sh [ip]
set -euo pipefail
cd "$(dirname "$0")/.."
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
IP="${1:-$(govc vm.ip "$VIGIE_VM_NAME")}"
docker build -q -t vigie-ui-qa test/ui >/dev/null
mkdir -p dist/i18n-qa
docker run --rm --network host -u "$(id -u):$(id -g)" -v "$PWD/dist/i18n-qa:/out" --entrypoint node \
	-e VIGIE_URL="https://$IP/" -e VIGIE_PASSWORD="$(cat dist/lab-password)" -e LANG_UI="${LANG_UI:-de}" \
	-e VC_URL="$GOVC_URL" -e VC_USER="$GOVC_USERNAME" -e VC_PASS="$GOVC_PASSWORD" -e OUT=/out vigie-ui-qa i18n.mjs
