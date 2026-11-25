#!/usr/bin/env bash
# QA phase 9 sur le lab : connexion vCenter avec mot de passe mémorisé, surveillance continue
# (intervalle minimal 15 min, mesures courtes), seuils, historique et export.
# Usage : image/lab-qa-monitor.sh setup|status|reboot|results|stop [ip]
set -euo pipefail
cd "$(dirname "$0")/.."
set -a; . ./lab.env; set +a
: "${VIGIE_VM_NAME:=vigie-dev}"
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
IP="${2:-$(govc vm.ip "$VIGIE_VM_NAME")}"
PW="$(cat dist/lab-password)"
api() { curl -sk -H "Authorization: Bearer $PW" -H 'Content-Type: application/json' "$@"; }
B="https://$IP/api"
case "${1:-status}" in
setup)
	VC="https://${GOVC_URL#https://}"; VC="${VC%/sdk}"
	FP=$(api "$B/vsphere/thumbprint?url=$VC" | jq -r .sha256)
	jq -n --arg u "$VC" --arg n "$GOVC_USERNAME" --arg p "$GOVC_PASSWORD" --arg f "$FP" '{url:$u,username:$n,password:$p,thumbprint:$f,remember:true}' |
		api -X POST "$B/vsphere/connect" -d @- | jq -c '{connected,username}'
	api -X PUT "$B/schedule" -d '{"enabled":true,"interval_min":15,"profile":"standard","baseline_s":10,"write_s":20,"read_s":20,"retention_days":30}' | jq -c .
	;;
status) api "$B/schedule" | jq -c . ; api "$B/vsphere/status" | jq -c '{connected,connected_at}' ;;
results) api "$B/results?period=24h" | jq -c '.series, (.points[] | {t,job,profile,scheduled,host,write_mbps,read_mbps,level})'
	api "$B/results?period=24h&format=csv" | head -3 ;;
reboot) sshpass -p "$PW" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR root@"$IP" reboot || true ;;
stop) api -X PUT "$B/schedule" -d '{"enabled":false,"interval_min":60,"profile":"standard","retention_days":30}' | jq -c '{enabled}' ;;
esac
