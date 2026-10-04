#!/usr/bin/env bash
# Start Tera on this Zo computer.
#
# Lives in the repo on purpose: /etc/zo/supervisord-user.conf is
# platform-managed and restored from the image on every sandbox restart, so the
# supervisord block that calls this script may need re-applying — the script
# itself must not be part of what gets lost.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DATA="${TERA_DATA_DIR:-$(dirname "$REPO")/tera-data}"

mkdir -p "$DATA" "$DATA/backups"

# Bind 0.0.0.0 (R8.4): clients are browsers elsewhere on the network, and the
# published URL reaches this process through the frp proxy, not over loopback.
export TERA_ADDR="${TERA_ADDR:-0.0.0.0:8080}"
export TERA_DB_PATH="${TERA_DB_PATH:-$DATA/tera.db}"
export TERA_BACKUP_DIR="${TERA_BACKUP_DIR:-$DATA/backups}"

# The public URL is HTTPS, terminated at the edge proxy. Reaching the server
# directly over plain HTTP will not carry the session cookie — that is the
# intended trade for a service on the public internet (DECISIONS D-008).
export TERA_COOKIE_SECURE="${TERA_COOKIE_SECURE:-1}"

# This box has one disk, so the guard that refuses a backup target on the same
# volume as the database has to be waived. On the shop PC it must not be: a
# backup beside the database is not a backup (R14.2).
export TERA_BACKUP_ALLOW_SAME_DISK="${TERA_BACKUP_ALLOW_SAME_DISK:-1}"

# No printer attached to this box. Supported state: receipts render on screen.
exec "$REPO/bin/tera"
