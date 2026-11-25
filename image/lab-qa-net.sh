#!/usr/bin/env bash
# QA phase N sur le lab : deux appliances (vigie-dev, vigie-dev2), ping, traceroute, surveillance,
# ARP, pairs et iperf3 dans les deux sens, TCP et UDP. Usage : image/lab-qa-net.sh
set -euo pipefail
export LC_ALL=C.UTF-8
cd "$(dirname "$0")/.."
. image/vigie-api.sh
set -a; . ./lab.env; set +a
export GOVC_URL GOVC_USERNAME GOVC_PASSWORD GOVC_INSECURE GOVC_DATACENTER
IP1=$(govc vm.ip vigie-dev); IP2=$(govc vm.ip vigie-dev2)
PW=$(cat dist/lab-password)
echo "appliances : vigie-dev $IP1, vigie-dev2 $IP2"
on() { VIGIE_IP=$1; VIGIE_TOKEN=$PW; }

on "$IP1"
L=$(api GET /net/local); echo "$L" | jq -c '{ifaces: [.interfaces[] | "\(.name) \(.cidr) MTU \(.mtu)"], gateway, dns}'
GW=$(echo "$L" | jq -r .gateway)
echo "== ping passerelle $GW (20 × 200 ms)"
api POST /net/ping "{\"target\":\"$GW\",\"count\":20}" | jq -c '{ip, on_link, loss: .stats.loss_pct, med: .stats.median_ms, p90: .stats.p90_ms, jitter: .stats.jitter_ms}'
echo "== ping du pair $IP2 (20 × 200 ms)"
api POST /net/ping "{\"target\":\"$IP2\",\"count\":20}" | jq -c '{ip, on_link, loss: .stats.loss_pct, min: .stats.min_ms, med: .stats.median_ms, max: .stats.max_ms}'
echo "== ping hors sous-réseau (172.16.0.10) + traceroute"
api POST /net/ping '{"target":"172.16.0.10","count":10}' | jq -c '{ip, on_link, loss: .stats.loss_pct, med: .stats.median_ms}'
api POST /net/traceroute '{"target":"172.16.0.10"}' | jq -r '"atteinte : \(.reached)", (.hops[] | "  \(.ttl) \(.addr // "*") \(.name // "") \(.rtts_ms | map(if . < 0 then "*" else (.*100|round/100|tostring) end) | join(" ")) ms")'
echo "== injection refusée"; { api POST /net/ping '{"target":"1.2.3.4;id"}' 2>&1 >/dev/null || true; } | tr -d '\n' | grep -o 'HTTP [0-9]*.*' | cut -c1-90 || true

echo "== surveillance : passerelle et pair"
api POST /net/targets "{\"host\":\"$GW\",\"label\":\"passerelle\"}" | jq -c '{id, host}'
api POST /net/targets "{\"host\":\"$IP2\",\"label\":\"vigie-dev2\"}" | jq -c '{id, host}'

echo "== ARP : table, suppression de $IP2, réapparition après ping"
api GET /net/arp | jq -r '.entries[] | "  \(.ip) \(.mac) \(.device) \(.state)"'
api DELETE "/net/arp/$IP2" | jq -r --arg ip "$IP2" '"après suppression : \([.entries[] | select(.ip == $ip)] | length) entrée(s) pour \($ip) (\([.entries[] | select(.ip == $ip) | .state] | join(",")))"'
api POST /net/ping "{\"target\":\"$IP2\",\"count\":2}" >/dev/null
api GET /net/arp | jq -r --arg ip "$IP2" '"après ping : \([.entries[] | select(.ip == $ip) | .state] | join(","))"'

echo "== pair : jeton créé sur vigie-dev2, ajouté sur vigie-dev avec son empreinte"
on "$IP2"; TOK2=$(api POST /tokens '{"name":"peer-vigie-dev"}' | jq -r .token)
on "$IP1"
FP=$(api GET "/net/peers/fingerprint?url=https://$IP2" | jq -r .sha256)
PEER=$(api POST /net/peers "$(jq -nc --arg u "https://$IP2" --arg t "$TOK2" --arg f "$FP" '{url:$u,token:$t,fingerprint:$f}')" | jq -r .id)
api GET /net/peers | jq -c '[.peers[] | {name, url, token}]'
echo "mauvaise empreinte : $({ api POST /net/peers "$(jq -nc --arg u "https://$IP2" --arg t "$TOK2" '{url:$u,token:$t,fingerprint:"00:11"}')" 2>&1 >/dev/null || true; } | grep -o 'HTTP [0-9]*' || true)"

VIGIE_TOKEN=$PW
for spec in '{"protocol":"tcp","duration_s":10,"streams":1}' '{"protocol":"tcp","duration_s":10,"streams":4}' '{"protocol":"tcp","duration_s":10,"streams":1,"reverse":true}' '{"protocol":"udp","duration_s":10,"bitrate":"500M"}'; do
	echo "== iperf3 vers le pair : $spec"
	id=$(job_run "$(echo "$spec" | jq -c --arg p "$PEER" '. + {profile:"net.iperf", peer:$p}')" 2>/dev/null)
	jq -r '.result | "  émis \(.sent_mbps|round) Mbit/s, reçu \(.received_mbps|round) Mbit/s, retrans \(.retransmits), gigue \(.jitter_ms*1000|round/1000) ms, perte \(.lost_pct*100|round/100) %, CPU \(.sender_cpu_pct|round)/\(.receiver_cpu_pct|round) %, \(.intervals|length) intervalles, MSS \(.tcp_mss)",
	  "  profil : iperf \(.version), \(.options.protocol), \(if .options.reverse then "le pair émet (-R)" else "vigie-dev émet" end), \(.options.streams) flux, \(.options.duration_s) s\(if .options.protocol=="udp" then ", cible \(.options.bitrate)" else "" end)"' "dist/job-$id.json"
done
on "$IP2"; echo "serveur iperf3 du pair après les tests : $(api GET /net/iperf/server | jq -c '{running}')"

echo "== attente de 3 salves de surveillance (~3 min)"
sleep 190
on "$IP1"
api GET /net/targets | jq -r '.targets[] | "  \(.label) \(.host) : dernière salve \(.last.r)/\(.last.s), médiane \(.last.med*1000|round/1000) ms, p90 \(.last.p90*1000|round/1000) ms \(.last_error // "")"'
T=$(api GET /net/targets | jq -r '.targets[0].id')
api GET "/net/targets/$T/series?range=3h" | jq -c '{range, step_s, points: (.points|length)}'
