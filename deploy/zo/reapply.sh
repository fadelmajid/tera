#!/usr/bin/env bash
# Re-register Tera with supervisord after a sandbox restart.
#
# /etc/zo/supervisord-user.conf is platform-managed and restored from the image
# on every restart, which drops any hand-added program. Run this to put the
# [program:tera] block back.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONF=/etc/zo/supervisord-user.conf

if grep -q '^\[program:tera\]' "$CONF"; then
  echo "already registered"
else
  printf '\n' >> "$CONF"
  cat "$HERE/supervisord-tera.conf" >> "$CONF"
  echo "appended [program:tera] to $CONF"
fi

supervisorctl -s http://127.0.0.1:29011 reread
supervisorctl -s http://127.0.0.1:29011 update
supervisorctl -s http://127.0.0.1:29011 status tera
