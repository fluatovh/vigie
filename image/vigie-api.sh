#!/usr/bin/env bash
# Petit client de l'API vigie pour la QA (à sourcer). Variables : VIGIE_IP, VIGIE_TOKEN.
# HTTPS avec le certificat auto-signé de l'appliance : -k sur le lab (VIGIE_CURL_TLS pour épingler).
#   api METHOD /chemin [json]        -> corps de la réponse (échec si HTTP >= 400)
#   job_run '<json>'                 -> soumet un job, suit le flux SSE, écrit dist/job-<id>.json, renvoie l'id
api() {
	local m=$1 p=$2 b=${3:-} out code
	out=$(curl -sS -m 600 -X "$m" -H "Authorization: Bearer $VIGIE_TOKEN" -H "Content-Type: application/json" ${b:+-d "$b"} \
		${VIGIE_CURL_TLS:--k} -w '\n%{http_code}' "https://$VIGIE_IP/api$p") || return 1
	code=${out##*$'\n'}; out=${out%$'\n'*}
	if [ "$code" -ge 400 ]; then echo "$m $p -> HTTP $code : $out" >&2; return 1; fi
	printf '%s' "$out"
}
job_follow() { # id : affiche le journal en direct (SSE) jusqu'à la fin du job
	local id=$1
	curl -sS -N -m 7200 ${VIGIE_CURL_TLS:--k} -H "Authorization: Bearer $VIGIE_TOKEN" "https://$VIGIE_IP/api/jobs/$id/events" |
		while IFS= read -r line; do
			case "$line" in
				data:*) printf '%s\n' "${line#data: }" | jq -r 'select(.type=="log") | "  " + .msg' ;;
			esac
		done
}
job_run() { # json : soumet, suit, renvoie l'id sur stdout (le journal va sur stderr)
	local j id
	j=$(api POST /jobs "$1") || return 1
	id=$(echo "$j" | jq -r .id)
	echo "job $id ($(echo "$j" | jq -r .profile)) soumis" >&2
	job_follow "$id" >&2
	api GET "/jobs/$id" > "dist/job-$id.json"
	echo "job $id : $(jq -r '.state + (if .error then " — " + .error else "" end)' "dist/job-$id.json")" >&2
	echo "$id"
}
