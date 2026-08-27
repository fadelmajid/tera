# Tera in a container. One binary, one SQLite file on a mounted volume.
#
# This is for a hosted test box, not for the shop. The shop model is a machine
# on the LAN with the printer attached (ARCHITECTURE §1) — a container in a
# datacentre cannot reach a USB receipt printer, and the whole point of INV-11
# is that the business keeps trading when the internet does not.
#
# Read docs/HOSTING.md before pointing anything real at this.

# --- 1. the browser client --------------------------------------------------
FROM node:22-alpine AS web

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build

# --- 2. the binary ----------------------------------------------------------
FROM golang:1.25-alpine AS build

# modernc.org/sqlite is pure Go, so CGO stays off and the binary is one
# self-contained file. That is the whole deployment story; do not turn it on.
ENV CGO_ENABLED=0

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# The dist the Go build embeds has to be the one just compiled, not whatever
# was committed.
COPY --from=web /src/web/dist ./web/dist

ARG VERSION=docker
RUN go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /tera ./cmd/tera

# --- 3. the runtime ---------------------------------------------------------
FROM alpine:3.20

# No CA certificates and no tzdata on purpose. Nothing here makes an outbound
# call (INV-11), and the IANA zone database is compiled into the binary via
# time/tzdata — book-year boundaries resolve in the entity's zone (INV-5) on a
# host that carries no zoneinfo at all.
RUN adduser -D -u 10001 tera && mkdir -p /data && chown tera:tera /data

COPY --from=build /tera /usr/local/bin/tera

USER tera
WORKDIR /data

# The database lives on the mounted volume, never in the image layer.
ENV TERA_DB_PATH=/data/tera.db \
    TERA_ADDR=0.0.0.0:8080 \
    TERA_BACKUP_DIR=/data/cadangan \
    TERA_BACKUP_ALLOW_SAME_DISK=1

EXPOSE 8080

# TERA_BACKUP_ALLOW_SAME_DISK is set because a single mounted volume is the
# only disk this container has. It is honest about what that buys: a snapshot
# beside the database survives a bad migration and nothing else. Pull real
# backups off the box with `tera backup --to` and copy them elsewhere.

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
  CMD wget -qO- http://127.0.0.1:8080/readyz || exit 1

ENTRYPOINT ["tera"]
