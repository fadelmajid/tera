#!/usr/bin/env bash
# Restore drill — TASKS 8.2, R8.7, R14.1.
#
# Takes a backup from a live database, restores it into a directory that has
# never held one, starts the server against the restored file, and checks over
# HTTP that the numbers came back.
#
# It is written to be run on the SPARE LAPTOP, not only on the build machine.
# Everything it needs is the tera binary and a backup file; it never reads the
# repository, and it makes no network call beyond localhost.
#
#   ./scripts/restore-drill.sh                 # take a fresh backup and drill it
#   ./scripts/restore-drill.sh <backup.db>     # drill a backup from the shop's stick
#
# Exit status is the drill result. Anything other than 0 means do not rely on
# these backups.
set -euo pipefail

BIN="${TERA_BIN:-./bin/tera}"
# Resolved absolutely before anything else. The drill deliberately runs the
# restore from inside the clean directory, the way somebody would on a laptop
# with the binary on a stick, and a relative path stops working the moment it
# does. This is the first thing the drill caught.
if [ -x "$BIN" ]; then
  BIN="$(cd "$(dirname "$BIN")" && pwd)/$(basename "$BIN")"
fi
PORT="${TERA_DRILL_PORT:-18431}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
fail() { printf '\n\033[31mDRILL GAGAL: %s\033[0m\n' "$*" >&2; exit 1; }

[ -x "$BIN" ] || fail "tera binary tidak ditemukan di $BIN (jalankan: make build)"

# ---------------------------------------------------------------------------
# Helpers for driving the API the way a person would.
JAR="$WORK/cookies.txt"
rid() { uuidgen | tr '[:upper:]' '[:lower:]'; }

post() { # post <base> <path> <json>
  curl -sf -b "$JAR" -c "$JAR" -X POST "$1$2" \
    -H 'Content-Type: application/json' -H "X-Client-Request-Id: $(rid)" \
    -H "X-Entity-Id: ${ENTITY:-}" -d "$3"
}
get() { curl -sf -b "$JAR" -c "$JAR" -H "X-Entity-Id: ${ENTITY:-}" "$1$2"; }

wait_ready() { # wait_ready <base>
  for _ in $(seq 1 80); do
    curl -sf "$1/readyz" >/dev/null 2>&1 && return 0
    sleep 0.25
  done
  return 1
}

# ---------------------------------------------------------------------------
say "1. Sumber: sebuah toko yang benar-benar berdagang"

# A drill that restores an empty database proves the file copy works and
# nothing else. This one buys stock, sells some of it, and then checks those
# figures came back — which is the only version of this that answers the
# question anybody is actually asking.
if [ $# -ge 1 ]; then
  BACKUP="$1"
  [ -f "$BACKUP" ] || fail "berkas cadangan tidak ada: $BACKUP"
  echo "memakai cadangan yang sudah ada: $BACKUP"
  EXPECT_SALES=""
else
  SOURCE="$WORK/sumber/tera.db"
  SEED_BASE="http://127.0.0.1:$((PORT+1))"
  mkdir -p "$WORK/sumber"

  TERA_DB_PATH="$SOURCE" TERA_ADDR="127.0.0.1:$((PORT+1))" TERA_BACKUP_DIR="" \
    "$BIN" > "$WORK/seed.log" 2>&1 &
  SEED_PID=$!
  wait_ready "$SEED_BASE" || { cat "$WORK/seed.log"; fail "server sumber tidak siap"; }

  # The first-run password is logged exactly once, which is the only time a
  # credential is ever printed.
  USER="$(sed -n 's/.*username=\([^ ]*\).*/\1/p' "$WORK/seed.log" | head -1)"
  PASS="$(sed -n 's/.*password=\([^ ]*\).*/\1/p' "$WORK/seed.log" | head -1)"
  [ -n "$USER" ] && [ -n "$PASS" ] || { cat "$WORK/seed.log"; fail "pengguna pertama tidak terbaca"; }

  curl -sf -c "$JAR" -X POST "$SEED_BASE/api/v1/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" >/dev/null \
    || fail "gagal masuk ke server sumber"

  ENTITY=""
  ENTITY="$(post "$SEED_BASE" /api/v1/setup/entity \
    '{"code":"DRILL","name":"PT Uji Pulih","is_pkp":true,"timezone":"Asia/Jakarta","book_year_start_month":1}' \
    | sed 's/.*"id":"\([^"]*\)".*/\1/')"
  [ -n "$ENTITY" ] || fail "perusahaan tidak terbentuk"

  OWNER="$(post "$SEED_BASE" /api/v1/owners '{"code":"BUDI","name":"Budi"}' \
    | sed 's/.*"id":"\([^"]*\)".*/\1/')"
  PRODUCT="$(post "$SEED_BASE" /api/v1/products \
    "{\"code\":\"P-DRILL\",\"name\":\"Sarung Tangan\",\"unit\":\"box\",\"owner_id\":\"$OWNER\",\"sale_price_idr\":111000}" \
    | sed 's/.*"id":"\([^"]*\)".*/\1/')"
  SUPPLIER="$(post "$SEED_BASE" /api/v1/suppliers \
    '{"code":"S-DRILL","name":"PT Medika","issues_faktur":true}' \
    | sed 's/.*"id":"\([^"]*\)".*/\1/')"
  [ -n "$PRODUCT" ] && [ -n "$SUPPLIER" ] || fail "data master tidak terbentuk"

  post "$SEED_BASE" /api/v1/purchases \
    "{\"supplier_id\":\"$SUPPLIER\",\"purchase_date\":\"2026-08-01\",\"faktur_received\":true,\"faktur_no\":\"010.000-26.1\",\"lines\":[{\"product_id\":\"$PRODUCT\",\"qty\":100,\"unit_price_idr\":10000,\"ppn_idr\":110000}]}" \
    >/dev/null || fail "pembelian gagal"
  post "$SEED_BASE" /api/v1/cash-sessions \
    '{"opening_float_idr":0,"business_date":"2026-08-21"}' >/dev/null || fail "sesi kas gagal"
  post "$SEED_BASE" /api/v1/sales \
    "{\"sale_date\":\"2026-08-21\",\"lines\":[{\"product_id\":\"$PRODUCT\",\"qty\":3,\"unit_price_idr\":111000}]}" \
    >/dev/null || fail "penjualan gagal"

  # What must come back. Read from the source, through the same endpoint the
  # check below uses, so the two figures are comparable by construction.
  EXPECT_SALES="$(get "$SEED_BASE" '/api/v1/sales?from=2026-08-01&to=2026-08-31')"
  EXPECT_TOTAL="$(echo "$EXPECT_SALES" | sed 's/.*"total_idr":\([0-9]*\).*/\1/')"
  EXPECT_STOCK="$(get "$SEED_BASE" '/api/v1/reports/stock?from=2026-08-01&to=2026-08-31' \
    | sed 's/.*"qty_on_hand":\([0-9]*\).*/\1/')"
  echo "sumber: penjualan total=$EXPECT_TOTAL, sisa stok=$EXPECT_STOCK"
  [ "$EXPECT_TOTAL" = "333000" ] || fail "sumber tidak berdagang seperti yang diharapkan (total=$EXPECT_TOTAL)"
  [ "$EXPECT_STOCK" = "97" ] || fail "sumber tidak berdagang seperti yang diharapkan (stok=$EXPECT_STOCK)"

  kill "$SEED_PID" 2>/dev/null || true
  wait "$SEED_PID" 2>/dev/null || true

  say "2. Ambil cadangan"
  TERA_DB_PATH="$SOURCE" "$BIN" backup --to "$WORK/flashdisk" --izinkan-disk-sama \
    2>&1 | tee "$WORK/backup.log"
  BACKUP="$(find "$WORK/flashdisk" -name 'tera-*.db' | head -1)"
  [ -n "$BACKUP" ] || fail "cadangan tidak terbentuk"
fi

# ---------------------------------------------------------------------------
say "3. Mesin lain: folder yang belum pernah berisi basis data"

FRESH="$WORK/laptop-cadangan"
mkdir -p "$FRESH"
# Nothing but the binary and the backup. No config, no repository, no .env.
cp "$BACKUP" "$FRESH/cadangan.db"
cp "${BACKUP%.db}.json" "$FRESH/cadangan.json" 2>/dev/null || true

say "4. Pulihkan"
( cd "$FRESH" && TERA_DB_PATH="$FRESH/tera.db" "$BIN" restore --from "$FRESH/cadangan.db" ) \
  2>&1 | tee "$WORK/restore.log"
[ -f "$FRESH/tera.db" ] || fail "basis data hasil restore tidak ada"

# ---------------------------------------------------------------------------
say "5. Jalankan server dari basis data hasil restore"

TERA_DB_PATH="$FRESH/tera.db" TERA_ADDR="127.0.0.1:$PORT" TERA_BACKUP_DIR="" \
  "$BIN" > "$WORK/restored.log" 2>&1 &
PID=$!
trap 'kill "$PID" 2>/dev/null || true; rm -rf "$WORK"' EXIT

READY=""
for _ in $(seq 1 60); do
  if curl -sf "http://127.0.0.1:$PORT/readyz" >/dev/null 2>&1; then READY=yes; break; fi
  sleep 0.25
done
[ -n "$READY" ] || { cat "$WORK/restored.log"; fail "server tidak siap dari basis data hasil restore"; }

say "6. Periksa lewat HTTP"

BASE="http://127.0.0.1:$PORT"
READYZ="$(curl -sf "$BASE/readyz")"
echo "readyz: $READYZ"
echo "$READYZ" | grep -q '"status":"ok"' || fail "readyz tidak ok"

SCHEMA="$(echo "$READYZ" | sed 's/.*"schema_version":\([0-9]*\).*/\1/')"
[ -n "$SCHEMA" ] && [ "$SCHEMA" -gt 0 ] || fail "versi skema tidak terbaca"
echo "versi skema: $SCHEMA"

# The browser client has to load too: a restored database behind a server that
# serves no UI is not a working shop.
curl -sf "$BASE/" | grep -qi '<div id="root"' || fail "halaman aplikasi tidak termuat"
echo "halaman aplikasi: termuat"

CODE="$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/v1/products")"
[ "$CODE" = "401" ] || fail "API tidak menjawab seperti seharusnya (dapat $CODE, harusnya 401)"
echo "API: hidup dan meminta login (401)"

# ---------------------------------------------------------------------------
say "7. Periksa angkanya, bukan hanya bahwa server hidup"

if [ -n "${EXPECT_SALES:-}" ]; then
  rm -f "$JAR"
  # The same credentials as before: a restore that loses the users is a restore
  # nobody can log into.
  curl -sf -c "$JAR" -X POST "$BASE/api/v1/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$USER\",\"password\":\"$PASS\"}" >/dev/null \
    || fail "tidak bisa masuk ke basis data hasil restore dengan kata sandi yang sama"
  echo "login: berhasil dengan kredensial yang sama"

  GOT_TOTAL="$(get "$BASE" '/api/v1/sales?from=2026-08-01&to=2026-08-31' \
    | sed 's/.*"total_idr":\([0-9]*\).*/\1/')"
  GOT_STOCK="$(get "$BASE" '/api/v1/reports/stock?from=2026-08-01&to=2026-08-31' \
    | sed 's/.*"qty_on_hand":\([0-9]*\).*/\1/')"

  echo "hasil restore: penjualan total=$GOT_TOTAL, sisa stok=$GOT_STOCK"
  [ "$GOT_TOTAL" = "$EXPECT_TOTAL" ] \
    || fail "penjualan tidak sama: sumber $EXPECT_TOTAL, hasil restore $GOT_TOTAL"
  [ "$GOT_STOCK" = "$EXPECT_STOCK" ] \
    || fail "sisa stok tidak sama: sumber $EXPECT_STOCK, hasil restore $GOT_STOCK"
  echo "angka cocok dengan sumber"
else
  # Drilling a backup from the shop's stick: there is nothing to compare
  # against here, so the operator compares by eye against what the shop knows.
  echo "cadangan dari luar: cocokkan angka di layar dengan catatan toko sendiri"
  get "$BASE/api/v1" /readyz >/dev/null 2>&1 || true
fi

say "DRILL LULUS"
printf 'Cadangan   : %s\n' "$BACKUP"
printf 'Dipulihkan : %s\n' "$FRESH/tera.db"
printf 'Skema      : %s\n' "$SCHEMA"
printf '\nCatat tanggal hari ini di docs/RESTORE-DRILL.md.\n'
