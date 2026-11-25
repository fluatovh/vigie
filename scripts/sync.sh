#!/usr/bin/env bash
# Synchronise le dépôt local vers une machine de build distante (Linux + Docker).
# Cible dans remote.env (non versionné) : VIGIE_REMOTE=utilisateur@hôte, VIGIE_PORT=22.
# Usage : scripts/sync.sh            (depuis la racine du dépôt, ou via `make sync`)
set -euo pipefail
cd "$(dirname "$0")/.."

[ -f remote.env ] && . ./remote.env
REMOTE="${VIGIE_REMOTE:?VIGIE_REMOTE manquant : créer remote.env (VIGIE_REMOTE=utilisateur@hôte)}"
PORT="${VIGIE_PORT:-22}"
DEST="${VIGIE_DEST:-vigie}"

# Le .git n'est pas synchronisé : la version est dérivée ici et transmise dans version.env.
scripts/version.sh
ver=$(sed -n 's/^VERSION=//p' version.env)

rsync -az --delete \
  --exclude .git --exclude dist/ --exclude cache/ --exclude '.env*' --exclude lab.env --exclude remote.env --exclude CLAUDE.local.md \
  -e "ssh -p $PORT -o BatchMode=yes" \
  ./ "$REMOTE:$DEST/"
echo "sync -> $REMOTE:$DEST ($ver)"
