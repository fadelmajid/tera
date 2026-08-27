#!/usr/bin/env python3
"""Fill a Tera instance with a believable six months of trading.

    ./scripts/demo-data.py --url http://localhost:8080 --user admin --password '...'

# Why this drives the API rather than writing rows

Everything here goes through the real endpoints, so every figure it produces was
computed by the system rather than asserted by the fixture: FIFO layers are
drawn oldest-first and owner-scoped, tax is priced against the rules in force on
the day, and the omzet ledger is written inside each sale's own transaction.

A seeder that INSERTed rows directly would be able to produce a margin report
that the software itself could never have produced, which is the one thing demo
data must not do.

# What it sets up

Two companies under one family, as the product is built for:

  PT Sehat Sentosa   PKP     — the retail counter, charges PPN
  PT Medika Nusantara non-PKP — wholesale to clinics, and the one that can
                                still cross the Rp 4,8 miliar threshold

Only stdlib. No dependencies to install on a machine that is meant to be
showing the software, not preparing to.
"""

import argparse
import json
import random
import sys
import urllib.error
import urllib.request
import uuid
from datetime import date, timedelta

# Deterministic: the same demo every time, so a figure someone asks about on
# Tuesday is still there on Thursday.
random.seed(20260821)

TODAY = date(2026, 8, 21)


class Api:
    """A tiny session-holding client. Cookies by hand — no dependencies."""

    def __init__(self, base):
        self.base = base.rstrip("/")
        self.cookie = None
        self.entity = None

    def call(self, method, path, body=None):
        url = f"{self.base}/api/v1{path}"
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(url, data=data, method=method)
        req.add_header("Accept", "application/json")
        if data:
            req.add_header("Content-Type", "application/json")
        if method != "GET":
            # INV-6: every mutating call is idempotent on this.
            req.add_header("X-Client-Request-Id", str(uuid.uuid4()))
        if self.entity:
            req.add_header("X-Entity-Id", self.entity)
        if self.cookie:
            req.add_header("Cookie", self.cookie)

        try:
            with urllib.request.urlopen(req) as r:
                raw = r.read().decode()
                setc = r.headers.get("Set-Cookie")
                if setc:
                    self.cookie = setc.split(";")[0]
                return json.loads(raw) if raw else None
        except urllib.error.HTTPError as e:
            detail = e.read().decode()
            raise SystemExit(f"\n  {method} {path} -> {e.code}\n  {detail}\n") from e
        except urllib.error.URLError as e:
            raise SystemExit(f"\n  tidak bisa menghubungi {self.base}: {e.reason}\n") from e

    get = lambda self, p: self.call("GET", p)                  # noqa: E731
    post = lambda self, p, b=None: self.call("POST", p, b)     # noqa: E731
    put = lambda self, p, b=None: self.call("PUT", p, b)       # noqa: E731


def say(msg):
    print(f"  {msg}", flush=True)


def head(msg):
    print(f"\n\033[1m{msg}\033[0m", flush=True)


def rupiah(n):
    return f"Rp {n:,.0f}".replace(",", ".")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--url", default="http://localhost:8080")
    ap.add_argument("--user", default="admin")
    ap.add_argument("--password", required=True, help="the first-run password from the log")
    args = ap.parse_args()

    api = Api(args.url)

    head("Masuk")
    api.post("/auth/login", {"username": args.user, "password": args.password})
    say(f"masuk sebagai {args.user}")

    # ---------------------------------------------------------------- companies
    head("1. Dua perusahaan")

    existing = api.get("/entities") or []
    if existing:
        raise SystemExit(
            "\n  Basis data ini sudah berisi perusahaan.\n"
            "  Demo ini butuh basis data kosong — hentikan server, hapus foldernya,\n"
            "  jalankan ulang, lalu pakai kata sandi baru dari log.\n"
        )

    pkp = api.post("/setup/entity", {
        "code": "SEHAT", "name": "PT Sehat Sentosa", "is_pkp": True,
        "npwp": "01.234.567.8-901.000",
        "timezone": "Asia/Jakarta", "book_year_start_month": 1,
    })
    say(f"{pkp['name']} (PKP) — konter ritel, memungut PPN")

    api.entity = pkp["id"]
    nonpkp = api.post("/entities", {
        "code": "MEDIKA", "name": "PT Medika Nusantara", "is_pkp": False,
        "timezone": "Asia/Jakarta", "book_year_start_month": 1,
    })
    say(f"{nonpkp['name']} (non-PKP) — grosir ke klinik")

    # ------------------------------------------------------------------- owners
    head("2. Pemilik, produk, pemasok, pelanggan")

    def mk(path, body):
        return api.post(path, body)["id"]

    budi = mk("/owners", {"code": "BUDI", "name": "Budi"})
    sari = mk("/owners", {"code": "SARI", "name": "Sari"})
    andi = mk("/owners", {"code": "ANDI", "name": "Andi"})
    say("owner: Budi, Sari, Andi — plus stok tanpa owner (bucket perusahaan)")

    # owner=None is the company bucket (R2.2): a real line on the margin report
    # beside the family, not a residual.
    katalog = [
        ("P-GLOVE", "Sarung Tangan Latex M", "box", budi, 95_000, 62_000),
        ("P-MASK", "Masker Bedah 3 Ply", "box", budi, 65_000, 41_000),
        ("P-SYR3", "Spuit 3ml", "box", sari, 88_000, 55_000),
        ("P-INFUS", "Infus Set Dewasa", "pcs", sari, 22_000, 13_500),
        ("P-KASA", "Kasa Steril 16x16", "pack", andi, 34_000, 20_000),
        ("P-SWAB", "Alkohol Swab", "box", andi, 28_000, 17_000),
        ("P-THERM", "Termometer Digital", "pcs", None, 145_000, 92_000),
        ("P-TENSI", "Tensimeter Aneroid", "pcs", None, 320_000, 210_000),
        ("P-WHEEL", "Kursi Roda Standar", "unit", budi, 1_850_000, 1_240_000),
        ("P-NEBU", "Nebulizer Kompresor", "unit", sari, 780_000, 505_000),
    ]
    produk = {}
    biaya = {}
    for code, name, unit, owner, jual, beli in katalog:
        body = {"code": code, "name": name, "unit": unit, "sale_price_idr": jual,
                "category": "Alat Kesehatan"}
        if owner:
            body["owner_id"] = owner
        produk[code] = mk("/products", body)
        biaya[code] = beli
    say(f"{len(produk)} produk alat kesehatan")

    # Three suppliers, deliberately differing on the one thing that changes the
    # cost basis: whether a faktur actually arrives (INV-9).
    pemasok = {
        "RAPI": mk("/suppliers", {"code": "S-RAPI", "name": "PT Anugrah Rapi Medika",
                                  "issues_faktur": True, "npwp": "02.111.222.3-444.000"}),
        "CEPAT": mk("/suppliers", {"code": "S-CEPAT", "name": "CV Cepat Sehat",
                                   "issues_faktur": False}),
        "CAMPUR": mk("/suppliers", {"code": "S-CAMPUR", "name": "PT Sumber Alkes",
                                    "issues_faktur": True}),
    }
    say("3 pemasok: satu selalu beri faktur, satu tidak pernah, satu campur")

    pelanggan = {
        "KLINIK": mk("/customers", {"code": "C-KLINIK", "name": "Klinik Harapan Bunda",
                                    "npwp": "03.555.666.7-888.000", "phone": "0281-555123"}),
        "APOTEK": mk("/customers", {"code": "C-APOTEK", "name": "Apotek Sehat Selalu",
                                    "phone": "0281-555987"}),
        "RS": mk("/customers", {"code": "C-RS", "name": "RS Mitra Medika",
                                "npwp": "04.777.888.9-000.000"}),
        "BIDAN": mk("/customers", {"code": "C-BIDAN", "name": "Praktik Bidan Sri",
                                   "nik": "3301014505800001"}),
        "PUSK": mk("/customers", {"code": "C-PUSK", "name": "Puskesmas Kalibagor"}),
    }
    say(f"{len(pelanggan)} pelanggan")

    # ---------------------------------------------------------------- purchases
    head("3. Pembelian — dengan dan tanpa faktur")

    def beli(entity, supplier, day, faktur, lines, credit=False, due=None, inv=None):
        api.entity = entity
        body = {
            "supplier_id": supplier, "purchase_date": day,
            "faktur_received": faktur, "lines": lines,
        }
        if inv:
            body["invoice_no"] = inv
        if faktur:
            body["faktur_no"] = f"010.000-26.{random.randint(10000000, 99999999)}"
        if credit:
            body["is_credit"] = True
            if due:
                body["due_date"] = due
        return api.post("/purchases", body)

    def baris(code, qty, unit_cost, kena_ppn=True):
        # 11% PPN on the purchase price. Whether it is creditable is decided by
        # the entity and the faktur, not here (SPEC 3.2).
        ppn = round(qty * unit_cost * 11 / 100) if kena_ppn else 0
        return {"product_id": produk[code], "qty": qty,
                "unit_price_idr": unit_cost, "ppn_idr": ppn}

    # The thesis, made visible: the same goods at the same price from the same
    # supplier, one purchase with a faktur and one without. At the PKP entity
    # the second costs ~11% more per unit and every margin on it is thinner.
    beli(pkp["id"], pemasok["CAMPUR"], "2026-02-10", True,
         [baris("P-GLOVE", 200, 62_000), baris("P-MASK", 300, 41_000)], inv="INV-CMP-0210")
    beli(pkp["id"], pemasok["CAMPUR"], "2026-02-24", False,
         [baris("P-GLOVE", 200, 62_000), baris("P-MASK", 300, 41_000)], inv="INV-CMP-0224")
    say("dua nota identik di PT Sehat Sentosa, hanya beda faktur — bandingkan biaya persediaannya")

    # The rest of the year's stock, at both companies.
    jadwal = [
        (pkp["id"], "RAPI", "2026-03-05", True, [baris("P-SYR3", 250, 55_000), baris("P-INFUS", 600, 13_500)]),
        (pkp["id"], "CEPAT", "2026-03-18", False, [baris("P-KASA", 400, 20_000), baris("P-SWAB", 500, 17_000)]),
        (pkp["id"], "RAPI", "2026-04-08", True, [baris("P-THERM", 80, 92_000), baris("P-TENSI", 40, 210_000)]),
        (pkp["id"], "CAMPUR", "2026-04-22", True, [baris("P-GLOVE", 250, 63_500), baris("P-MASK", 350, 42_000)]),
        (pkp["id"], "CEPAT", "2026-05-12", False, [baris("P-SWAB", 400, 17_500), baris("P-KASA", 300, 20_500)]),
        (pkp["id"], "RAPI", "2026-06-03", True, [baris("P-SYR3", 300, 56_000), baris("P-INFUS", 700, 13_800)]),
        (pkp["id"], "CAMPUR", "2026-06-19", False, [baris("P-GLOVE", 200, 64_000)]),
        (pkp["id"], "RAPI", "2026-07-07", True, [baris("P-THERM", 100, 93_000), baris("P-NEBU", 25, 505_000)]),
        (pkp["id"], "CAMPUR", "2026-07-21", True, [baris("P-MASK", 400, 42_500), baris("P-SWAB", 300, 17_800)]),
        (pkp["id"], "RAPI", "2026-08-05", True, [baris("P-GLOVE", 300, 64_500), baris("P-SYR3", 200, 56_500)]),
        # The wholesale side buys big-ticket items in volume. It is non-PKP, so
        # the PPN it pays is never creditable and always lands in cost — which
        # is why its margins look thinner than the retail counter's on the same
        # goods.
        #
        # Each buy is dated before the sales that draw on it: FIFO consumes
        # oldest-first and refuses a sale it cannot cover, so a delivery booked
        # after the sale it supplies would simply be rejected.
        (nonpkp["id"], "RAPI", "2026-02-14", True, [baris("P-WHEEL", 800, 1_240_000)]),
        (nonpkp["id"], "RAPI", "2026-04-05", True, [baris("P-WHEEL", 900, 1_250_000)]),
        (nonpkp["id"], "CAMPUR", "2026-06-08", False, [baris("P-NEBU", 900, 505_000)]),
        (nonpkp["id"], "RAPI", "2026-06-05", True, [baris("P-WHEEL", 900, 1_252_000)]),
        (nonpkp["id"], "RAPI", "2026-08-01", True,
         [baris("P-TENSI", 400, 208_000), baris("P-THERM", 300, 91_000)]),
    ]
    for ent, sup, day, fak, lines in jadwal:
        beli(ent, pemasok[sup], day, fak, lines)
    say(f"{len(jadwal) + 2} nota pembelian, Februari–Agustus")

    # Unpaid invoices, so the hutang report has something to age (R5.8).
    beli(pkp["id"], pemasok["RAPI"], "2026-08-12", True,
         [baris("P-INFUS", 400, 14_000)], credit=True, due="2026-09-11", inv="INV-RAPI-0812")
    beli(pkp["id"], pemasok["CEPAT"], "2026-06-20", False,
         [baris("P-KASA", 250, 21_000)], credit=True, due="2026-07-20", inv="INV-CEPAT-0620")
    beli(pkp["id"], pemasok["CAMPUR"], "2026-05-02", True,
         [baris("P-SWAB", 200, 18_000)], credit=True, due="2026-06-01", inv="INV-CMP-0502")
    say("3 pembelian kredit: satu belum jatuh tempo, dua sudah menunggak")

    # An opening balance with no term recorded at all — the case the aging
    # report refuses to guess about and counts on screen instead (D-018).
    api.entity = pkp["id"]
    api.post("/opening/payables", {
        "party_id": pemasok["CEPAT"], "invoice_no": "SALDO-AWAL-01",
        "amount_idr": 4_500_000, "incurred_on": "2026-01-15",
        "note": "Saldo awal saat pindah dari sistem lama — tempo tidak tercatat",
    })
    say("1 saldo awal tanpa tanggal jatuh tempo — lihat kolom 'Tanpa jatuh tempo'")

    # ------------------------------------------------------------------- sales
    head("4. Penjualan — Februari sampai Agustus")

    def till(entity, day, float_idr=500_000):
        api.entity = entity
        return api.post("/cash-sessions", {"opening_float_idr": float_idr, "business_date": day})

    def jual(entity, day, lines, customer=None, faktur=False, credit=False,
             due=None, payments=None, diskon=0):
        api.entity = entity
        body = {"sale_date": day, "lines": lines}
        if customer:
            body["customer_id"] = customer
        if faktur:
            body["faktur_issued"] = True
            body["faktur_no"] = f"010.001-26.{random.randint(10000000, 99999999)}"
        if credit:
            body["is_credit"] = True
            if due:
                body["due_date"] = due
        if payments:
            body["payments"] = payments
        if diskon:
            body["invoice_discount_idr"] = diskon
        return api.post("/sales", body)

    def item(code, qty, harga=None):
        line = {"product_id": produk[code], "qty": qty}
        if harga is not None:
            line["unit_price_idr"] = harga
        return line

    # --- the retail counter ------------------------------------------------
    #
    # The till is opened and closed month by month rather than left open for six
    # months. That is how a shop actually works, it gives the Sesi Kas screen a
    # history to show, and it means each Z-report covers a plausible day's
    # takings instead of half a year's.
    def tutup(entity, catatan):
        api.entity = entity
        sesi = api.get("/cash-sessions/current")
        if not sesi or not sesi.get("id"):
            return
        z = api.get(f"/cash-sessions/{sesi['id']}/totals") or {}
        harap = int(z.get("expected_cash", 0))
        # Counted a little short, so the Z-report shows a real variance rather
        # than a suspiciously perfect one.
        api.post(f"/cash-sessions/{sesi['id']}/close", {
            "counted_cash_idr": max(0, harap - random.choice([0, 5_000, 15_000, 25_000])),
            "note": catatan,
        })

    till(pkp["id"], "2026-02-12")
    eceran = ["P-GLOVE", "P-MASK", "P-SYR3", "P-INFUS", "P-KASA", "P-SWAB", "P-THERM"]
    harga = {c: j for c, _, _, _, j, _ in [(k[0], k[1], k[2], k[3], k[4], k[5]) for k in katalog]}

    hari = date(2026, 2, 12)
    n_ritel = 0
    bulan_sesi = hari.month
    while hari <= TODAY:
        # New month, new till: close the old session and open the next.
        if hari.month != bulan_sesi:
            tutup(pkp["id"], "Tutup kas akhir bulan")
            till(pkp["id"], hari.isoformat())
            bulan_sesi = hari.month
            api.entity = pkp["id"]
        # Two or three walk-ins on most days; Sundays are quiet.
        if hari.weekday() != 6:
            for _ in range(random.randint(2, 3)):
                lines = [item(c, random.randint(1, 4))
                         for c in random.sample(eceran, random.randint(1, 3))]
                # Most walk-ins take no faktur. A PKP still owes the output PPN
                # on every one of them (SPEC 2.3) — that is the figure the PPN
                # position separates out.
                jual(pkp["id"], hari.isoformat(), lines,
                     faktur=random.random() < 0.15,
                     payments=[{"method": random.choice(
                         ["TUNAI", "TUNAI", "TUNAI", "QRIS", "TRANSFER", "KARTU"]),
                         "amount_idr": 0}] if False else None)
                n_ritel += 1
        hari += timedelta(days=random.randint(2, 4))
    say(f"{n_ritel} penjualan eceran di konter PT Sehat Sentosa")

    # A bulk order that draws MORE than one layer holds.
    #
    # This is the sale to open on the margin report: 260 boxes of gloves is more
    # than the 200 in February's faktur layer, so FIFO empties that one and
    # takes the rest from the layer bought without a faktur — and the drill-down
    # shows the two slices at visibly different costs on one line. Ordinary
    # counter sales of two or three boxes never span a layer, so nothing else in
    # this data makes INV-9 visible.
    jual(pkp["id"], "2026-03-24", [item("P-GLOVE", 260), item("P-MASK", 340)],
         customer=pelanggan["RS"], faktur=True)
    say("1 pesanan borongan 24 Maret — satu baris menarik dua lapisan FIFO, "
        "yang satu ada fakturnya dan yang satu tidak")

    # A few named-customer sales on credit, so piutang has something to age.
    kredit = [
        ("2026-06-15", "KLINIK", [item("P-SYR3", 20), item("P-INFUS", 50)], "2026-07-15"),
        ("2026-07-10", "RS", [item("P-TENSI", 8), item("P-THERM", 12)], "2026-08-09"),
        ("2026-08-14", "APOTEK", [item("P-GLOVE", 30), item("P-MASK", 40)], "2026-09-13"),
    ]
    for day, cust, lines, due in kredit:
        jual(pkp["id"], day, lines, customer=pelanggan[cust], faktur=True,
             credit=True, due=due)
    say(f"{len(kredit)} penjualan kredit — jadi piutang dengan jatuh tempo")

    # --- the wholesale side, which is the one that can cross ---------------
    till(nonpkp["id"], "2026-02-20", float_idr=1_000_000)

    # Sized so the book year crosses Rp 4,8 miliar in the seventh month, which
    # is TASKS 7.11's scenario and the most useful thing on the dashboard: by
    # the end of June the counter reads about 86% and the alarm is at WARN, and
    # the July delivery takes it over.
    #
    # Institutional volumes, which is what makes Rp 4,8 miliar reachable at all
    # for a business this size — a hospital equipping wards buys wheelchairs by
    # the hundred, not the pair.
    grosir = [
        ("2026-02-20", "RS", [item("P-WHEEL", 350, 1_950_000)]),
        ("2026-03-16", "PUSK", [item("P-WHEEL", 360, 1_960_000)]),
        ("2026-04-14", "RS", [item("P-WHEEL", 370, 1_955_000)]),
        ("2026-05-19", "KLINIK", [item("P-WHEEL", 350, 1_970_000)]),
        ("2026-06-16", "PUSK", [item("P-NEBU", 800, 840_000)]),
        ("2026-06-25", "RS", [item("P-WHEEL", 340, 1_975_000)]),
        # The seventh month takes it over the line.
        ("2026-07-14", "RS", [item("P-WHEEL", 360, 1_980_000)]),
        ("2026-08-06", "PUSK", [item("P-TENSI", 320, 295_000), item("P-THERM", 240, 138_000)]),
    ]
    for day, cust, lines in grosir:
        jual(nonpkp["id"], day, lines, customer=pelanggan[cust])
    say(f"{len(grosir)} penjualan grosir di PT Medika Nusantara — melewati batas PKP di bulan ke-7")

    # ------------------------------------------------------- returns and voids
    head("5. Retur, void, dan pembayaran sebagian")

    api.entity = pkp["id"]
    riwayat = api.get("/sales?from=2026-07-01&to=2026-07-31") or []

    # A return of a July sale, taken in August: the cross-boundary case that
    # D-012 is about. It reduces August, and July still says what came back.
    if riwayat:
        target = riwayat[len(riwayat) // 2]
        detail = api.get(f"/sales/{target['id']}")
        lines = detail.get("lines") or []
        if lines and lines[0]["qty"] >= 1:
            api.post(f"/sales/{target['id']}/returns", {
                "return_date": "2026-08-04", "reason": "Ukuran tidak sesuai pesanan",
                "refund_method": "TUNAI",
                "lines": [{"sale_line_id": lines[0]["id"], "qty": 1}],
            })
            say(f"retur bulan Agustus atas nota Juli {target['invoice_no']} — lintas periode (D-012)")

    # A void of a sale rung today, while the till is still open.
    salah = jual(pkp["id"], TODAY.isoformat(), [item("P-THERM", 2)])
    api.post(f"/sales/{salah['sale']['id']}/void", {"reason": "Salah input jumlah"})
    say(f"1 nota dibatalkan hari ini ({salah['sale']['invoice_no']}) — lihat di laporan penjualan")

    # A partial payment against one of the overdue payables (R5.8).
    hutang = api.get("/payables") or []
    menunggak = [h for h in hutang if h.get("outstanding_idr", 0) > 0 and h.get("due_date")]
    if menunggak:
        h = menunggak[0]
        api.post(f"/payables/{h['id']}/payments", {
            "amount_idr": int(h["outstanding_idr"] * 0.4),
            "paid_on": "2026-08-10", "method": "TRANSFER",
            "note": "Pembayaran sebagian, sisanya menyusul",
        })
        say("1 hutang dibayar sebagian — sisanya tetap menunggak dan tetap berumur")

    # ---------------------------------------------------------------- transfer
    head("6. Transfer antar perusahaan")

    # non-PKP -> PKP destroys the input PPN credit on the goods, permanently
    # (R4.5). The server refuses without an explicit acknowledgement, so this is
    # the flow with the blocking modal behind it.
    api.entity = nonpkp["id"]
    rencana = api.post("/transfers/preview", {
        "to_entity_id": pkp["id"], "transfer_date": "2026-08-08",
        "lines": [{"product_id": produk["P-WHEEL"], "qty": 5}],
    })
    hangus = rencana.get("forfeited_ppn_idr", 0)
    api.post("/transfers", {
        "to_entity_id": pkp["id"], "transfer_date": "2026-08-08",
        "acknowledge_credit_loss": True,
        "note": "Kursi roda dipindah ke konter ritel",
        "lines": [{"product_id": produk["P-WHEEL"], "qty": 5}],
    })
    say(f"5 kursi roda: Medika (non-PKP) → Sehat (PKP), kredit PPN hangus {rupiah(hangus)}")

    # And the harmless direction, for comparison.
    api.entity = pkp["id"]
    api.post("/transfers", {
        "to_entity_id": nonpkp["id"], "transfer_date": "2026-08-15",
        "note": "Masker dipindah ke gudang grosir",
        "lines": [{"product_id": produk["P-MASK"], "qty": 40, "ppn_idr": 0}],
    })
    say("40 box masker: Sehat → Medika — arah ini tidak menghanguskan apa pun")

    # ------------------------------------------------------------------ opname
    head("7. Opname stok")

    api.entity = pkp["id"]
    op = api.post("/opname", {"count_date": "2026-08-18",
                              "note": "Opname rutin bulanan"})
    lembar = api.get("/opname/count-sheet") or []
    dihitung = 0
    for row in lembar[:4]:
        # A small variance on two lines, none on the rest. Real counts find
        # differences; a count that never does is a count nobody did.
        selisih = [-2, 0, 1, 0][dihitung % 4]
        body = {"product_id": row["product_id"],
                "counted_qty": max(0, row["qty_on_hand"] + selisih)}
        if selisih:
            # One of the six the schema allows (R12.5). A shortfall found on a
            # shelf is usually miscounted paperwork; a surplus is usually a
            # delivery booked twice.
            body["reason_code"] = "SALAH_CATAT" if selisih < 0 else "RETUR_TIDAK_TERCATAT"
            body["reason_note"] = "Selisih ditemukan saat hitung fisik"
        if row.get("owner_id"):
            body["owner_id"] = row["owner_id"]
        api.post(f"/opname/{op['id']}/lines", body)
        dihitung += 1
    say(f"1 opname dengan {dihitung} baris dihitung — belum diposting, siap ditinjau")

    # --------------------------------------------------------------- till close
    head("8. Tutup sesi kas")

    tutup(pkp["id"], "Selisih kecil, kemungkinan kembalian")
    say("sesi kas terakhir PT Sehat Sentosa ditutup dengan selisih kecil")

    # Reopen one so the till is usable the moment somebody clicks Kasir.
    till(pkp["id"], TODAY.isoformat())
    say("sesi kas baru dibuka — kasir siap dipakai")

    # ------------------------------------------------------------------ summary
    head("Selesai")

    api.entity = pkp["id"]
    margin = api.get("/reports/margin?from=2026-08-01&to=2026-08-31") or {}
    api.entity = nonpkp["id"]
    omzet = api.get("/omzet") or {}

    print()
    say(f"PT Sehat Sentosa — margin Agustus: {rupiah(margin.get('totals', {}).get('margin_idr', 0))}")
    kum = omzet.get("cumulative", {}).get("amount_idr", 0)
    say(f"PT Medika Nusantara — omzet tahun buku: {rupiah(kum)}  status: {omzet.get('state')}")
    if omzet.get("crossed"):
        c = omzet["crossed"]
        say(f"  melewati batas {c['on']} · daftar PKP paling lambat {c['register_by']}"
            f" · mulai memungut PPN {c['vat_starts']}")
    print()
    print("  Buka aplikasinya dan mulai dari Beranda.")
    print()


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        sys.exit(130)
