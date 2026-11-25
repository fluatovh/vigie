#!/bin/sh
# QA phase 8c : ouvre hors ligne chaque rapport HTML d'un dossier (défaut dist/report-samples),
# vérifie graphs, CSP et absence de requête réseau, imprime en PDF A4 et rend les pages en PNG.
# Usage : image/qa-report.sh [dossier]
set -u
IN=$(cd "${1:-dist/report-samples}" && pwd)
OUT="$IN/qa"; rm -rf "$OUT"; mkdir -p "$OUT"
docker build -q -t vigie-ui-qa test/ui >/dev/null
docker run --rm --network none -u "$(id -u):$(id -g)" -v "$IN:/in:ro" -v "$OUT:/out" --entrypoint node vigie-ui-qa report.mjs
for p in "$OUT"/*.pdf; do
  docker run --rm --network none -u "$(id -u):$(id -g)" -v "$OUT:/out" --entrypoint pdftoppm vigie-ui-qa -r 50 -png "/out/$(basename "$p")" "/out/$(basename "$p" .pdf)-p"
done
ls "$OUT"
