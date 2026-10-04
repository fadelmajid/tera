#!/usr/bin/env bash
# Put the demo instance back to its pristine seeded state.
#
# Rehearsing changes the data — a sale rung at the till, a transfer committed —
# and the demo script quotes exact figures. Run this before a real demo so the
# numbers on screen are the numbers on the page.
#
# The seeder is deterministic, so the same figures come back every time. The
# admin password does NOT: it is generated on first run of an empty database,
# and there is no way to set it back. The new one is printed at the end.
set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DATA="${TERA_DATA_DIR:-$(dirname "$REPO")/tera-data}"
STAMP="$(date +%Y%m%d-%H%M%S)"
SUPER="supervisorctl -s http://127.0.0.1:29011"

echo "==> menghentikan tera"
$SUPER stop tera >/dev/null

echo "==> menyimpan basis data lama ke $DATA/reset-$STAMP"
mkdir -p "$DATA/reset-$STAMP"
for f in tera.db tera.db-wal tera.db-shm; do
  [ -e "$DATA/$f" ] && mv "$DATA/$f" "$DATA/reset-$STAMP/"
done

echo "==> menyalakan tera dengan basis data kosong"
: > /dev/shm/tera.log
$SUPER start tera >/dev/null

# The first-run password is logged exactly once, on the line that creates the
# user. Wait for it rather than sleeping a guessed number of seconds.
echo "==> menunggu pengguna pertama dibuat"
for _ in $(seq 1 60); do
  PASSWORD="$(grep -o 'password=[^ ]*' /dev/shm/tera.log 2>/dev/null | head -1 | cut -d= -f2 || true)"
  [ -n "${PASSWORD:-}" ] && break
  sleep 1
done
if [ -z "${PASSWORD:-}" ]; then
  echo "gagal: kata sandi pertama tidak muncul di /dev/shm/tera.log" >&2
  exit 1
fi

echo "==> mengisi data demo"
python3 "$REPO/scripts/demo-data.py" --url http://127.0.0.1:8080 --password "$PASSWORD" | tail -8

cat <<MSG

────────────────────────────────────────────────
  Data demo sudah kembali seperti semula.

  Alamat  : https://tera-anahata.zocomputer.io
  Pengguna: admin
  Sandi   : $PASSWORD      <-- BARU, catat ini

  Basis data lama: $DATA/reset-$STAMP
────────────────────────────────────────────────
MSG
