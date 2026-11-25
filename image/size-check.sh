#!/usr/bin/env bash
# Contrôle de taille (§4.4 du plan) : plafond dur, avertissement, budget par composant, dérive vs baseline.
set -euo pipefail
cd "$(dirname "$0")/.."
S=dist/sizes.json; B=image/size-budget.json; BL=image/size-baseline.json
[ -f "$S" ] || { echo "size-check: $S manquant (make image)"; exit 1; }
mb() { awk -v b="$1" 'BEGIN{printf "%.1f", b/1000000}'; }
fail=0
printf '%-14s %9s %9s\n' composant Mo budget
for k in kernel initramfs modloop apks apks_ovt vigie_bin apkovl media vmdk ova iso qcow2; do
	v=$(jq -r ".$k // empty" "$S"); [ -n "$v" ] || continue
	bud=$(jq -r ".components_mb.$k // empty" "$B")
	flag=""
	if [ -n "$bud" ] && awk -v v="$v" -v b="$bud" 'BEGIN{exit !(v > b*1000000)}'; then flag="  DEPASSE"; fail=1; fi
	printf '%-14s %9s %9s%s\n' "$k" "$(mb "$v")" "${bud:--}" "$flag"
done
ova=$(jq -r '.ova // empty' "$S")
if [ -n "$ova" ]; then
	hard=$(jq -r .hard_limit_mb "$B"); warn=$(jq -r .warn_mb "$B"); tol=$(jq -r .baseline_tolerance_mb "$B")
	awk -v v="$ova" -v h="$hard" 'BEGIN{exit !(v > h*1000000)}' && { echo "ECHEC: OVA $(mb "$ova") Mo > plafond $hard Mo"; fail=1; }
	awk -v v="$ova" -v w="$warn" 'BEGIN{exit !(v > w*1000000)}' && echo "AVERTISSEMENT: OVA $(mb "$ova") Mo > $warn Mo"
	if [ -f "$BL" ]; then
		base=$(jq -r .ova "$BL")
		if awk -v v="$ova" -v b="$base" -v t="$tol" 'BEGIN{exit !(v - b > t*1000000)}'; then
			echo "ECHEC: OVA $(mb "$ova") Mo, +$(mb $((ova - base))) Mo vs baseline $(mb "$base") Mo (> $tol Mo)."
			echo "       Mettre à jour $BL dans le même commit, avec la justification dans le message."
			fail=1
		fi
	else
		echo "(pas de baseline : créer $BL avec {\"ova\": $ova})"
	fi
fi
# ISO et qcow2 : même plafond dur que l'OVA
for k in iso qcow2; do
	v=$(jq -r ".$k // empty" "$S"); [ -n "$v" ] || continue
	hard=$(jq -r .hard_limit_mb "$B")
	awk -v v="$v" -v h="$hard" 'BEGIN{exit !(v > h*1000000)}' && { echo "ECHEC: $k $(mb "$v") Mo > plafond $hard Mo"; fail=1; }
done
exit $fail
