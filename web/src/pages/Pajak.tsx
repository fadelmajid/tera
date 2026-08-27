import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api/client'
import type { Role } from '../api/auth'
import {
  closeTaxRule,
  createTaxRule,
  deleteTaxRule,
  formatRate,
  listTaxRules,
  ppnPosition,
} from '../api/pajak'
import type { PPNPosition, TaxRule, TaxRuleInput } from '../api/pajak'
import { omzetPosition, omzetThresholds, setOmzetBase } from '../api/omzet'
import type { OmzetThreshold } from '../api/omzet'
import { formatIDR } from '../money'
import { Centang, Galat, Memuat, Tabel, Teks } from '../components/dasar'
import { Konfirmasi } from '../components/Modal'

/**
 * Pengaturan Pajak dan Posisi PPN. TASKS 5.9, 5.10.
 *
 * Two things live here, and they answer to different people.
 *
 * The rules are the configuration a konsultan pajak is shown, so every row
 * carries the regulation it implements and the DPP nilai lain as the fraction
 * the regulation states — 11/12, never 0,916666… (SPEC §2.1). A rate is never
 * edited: the old row is closed and a new one opened (INV-4), which is why
 * there is no edit button anywhere on this screen.
 *
 * The position is what the company owes for a masa pajak. Its most important
 * line is the output PPN on sales that issued no faktur: a PKP owes that
 * whether or not the buyer took a document (SPEC §2.3), nothing else in the
 * business mentions it, and it is the most common way a newly registered
 * business loses margin without noticing.
 *
 * Every figure here is an estimate from the data in this system. The caveat
 * comes down with the payload rather than being written into the page, so no
 * screen can show the number without it.
 *
 * # Every change here goes through a real dialog
 *
 * Closing a rule, deleting a staged one, and changing how omzet is counted all
 * used to run through `window.prompt` — two of them through two prompts in a
 * row. These are the changes a konsultan pajak reads, and they are written to
 * the audit log (INV-10). A browser prompt cannot show what is being changed,
 * cannot validate the date it collects, cannot be cancelled halfway without
 * losing the first answer, and looks exactly like a phishing popup.
 */
export function PajakPage({ entityId, role, isPKP }: { entityId: string; role: Role; isPKP: boolean }) {
  const bolehUbah = role === 'owner'

  const [today, setToday] = useState('')
  const [rules, setRules] = useState<TaxRule[]>([])
  const [caveat, setCaveat] = useState('')
  const [galat, setGalat] = useState<string | null>(null)
  const [sibuk, setSibuk] = useState(false)
  const [memuat, setMemuat] = useState(true)

  const muatRules = useCallback(() => {
    setMemuat(true)
    listTaxRules(entityId)
      .then((r) => {
        setRules(r.rules)
        setToday(r.today)
        setCaveat(r.caveat)
        setGalat(null)
      })
      .catch((err: unknown) =>
        setGalat(err instanceof ApiError ? err.message : 'Gagal memuat aturan pajak'),
      )
      .finally(() => setMemuat(false))
  }, [entityId])
  useEffect(muatRules, [muatRules])

  return (
    <>
      <h2>Pajak</h2>
      <p className="sub-judul">Aturan PPN yang berlaku dan posisi PPN per masa pajak.</p>
      <Galat pesan={galat} />

      {caveat && <p className="disclaimer">{caveat}</p>}

      <div className="tumpuk">
        {!isPKP && (
          <div className="card">
            <p>
              <strong>Perusahaan ini bukan PKP.</strong> Tidak memungut PPN, tidak menerbitkan
              faktur pajak, dan tidak dapat mengkreditkan PPN masukan — PPN yang dibayar ke pemasok
              masuk ke harga pokok barang.
            </p>
            <p className="catatan">
              Jika perusahaan sudah dikukuhkan sebagai PKP, tandai dulu perusahaannya, lalu
              tambahkan aturan PPN dengan tanggal mulai sesuai masa pajak pertama kewajiban PPN-nya.
            </p>
          </div>
        )}

        {memuat ? (
          <Memuat apa="aturan pajak" />
        ) : (
          <AturanPajak
            rules={rules}
            today={today}
            bolehUbah={bolehUbah}
            sibuk={sibuk}
            setSibuk={setSibuk}
            setGalat={setGalat}
            muatUlang={muatRules}
            entityId={entityId}
          />
        )}

        <PosisiPPN entityId={entityId} />

        <BatasOmzet entityId={entityId} bolehUbah={bolehUbah} />
      </div>
    </>
  )
}

// --- the PKP threshold (TASKS 7.1, SPEC 5) ---------------------------------

/**
 * The threshold the omzet clock is measured against, and how turnover is
 * counted.
 *
 * Config, not a constant (INV-4). Rp 4,8 miliar has been the figure since
 * PMK 197/2013, and unlike a tax rate a change to it alters no historical
 * transaction — it changes which year a business crossed in.
 *
 * The two policy columns are the unsettled part. SPEC 5.2 reads the deadline as
 * the end of the book year, citing a regulation that governs final PPh rather
 * than PKP registration; the rule usually quoted for registration gives a far
 * shorter one. Both readings are implemented, so answering the question is this
 * row rather than a release.
 */
function BatasOmzet({ entityId, bolehUbah }: { entityId: string; bolehUbah: boolean }) {
  const [rows, setRows] = useState<OmzetThreshold[]>([])
  const [caveat, setCaveat] = useState('')
  // Read from the server rather than assumed. The toggle used to start on
  // NET_OF_VAT whatever the company was actually set to, so it could show the
  // wrong option as the active one indefinitely.
  const [base, setBase] = useState<'NET_OF_VAT' | 'GROSS' | null>(null)
  const [pilihan, setPilihan] = useState<'NET_OF_VAT' | 'GROSS' | null>(null)
  const [sibuk, setSibuk] = useState(false)
  const [galat, setGalat] = useState<string | null>(null)

  const muat = useCallback(() => {
    omzetThresholds(entityId)
      .then((r) => {
        setRows(r.thresholds)
        setCaveat(r.caveat)
        setGalat(null)
      })
      .catch((err: unknown) =>
        setGalat(err instanceof ApiError ? err.message : 'Gagal memuat batas omzet'),
      )
    omzetPosition(entityId)
      .then((p) => setBase(p.base))
      .catch(() => setBase(null))
  }, [entityId])
  useEffect(muat, [muat])

  const simpanBase = (alasan: string) => {
    if (!pilihan) return
    setSibuk(true)
    setOmzetBase(entityId, pilihan, alasan)
      .then(() => {
        setBase(pilihan)
        setPilihan(null)
        setGalat(null)
        muat()
      })
      .catch((err: unknown) =>
        setGalat(err instanceof ApiError ? err.message : 'Gagal menyimpan dasar omzet'),
      )
      .finally(() => setSibuk(false))
  }

  return (
    <div className="card">
      <h3>Batas omzet PKP</h3>
      <Galat pesan={galat} />

      {/* The sentence this whole feature exists to make true on screen. */}
      <p className="catatan">
        Batas dihitung <strong>kumulatif per tahun buku dan diulang setiap tahun buku</strong> —
        bukan 12 bulan berjalan. Angka 12 bulan terakhir di beranda hanya perkiraan laju.
      </p>

      {rows.length === 0 ? (
        <p className="kosong">Belum ada batas omzet untuk perusahaan ini.</p>
      ) : (
        <Tabel label="Batas omzet PKP yang pernah berlaku">
          <thead>
            <tr>
              <th className="angka">Batas</th>
              <th className="angka">Pantau</th>
              <th className="angka">Peringatan</th>
              <th>Batas daftar</th>
              <th>Mulai wajib PPN</th>
              <th>Berlaku</th>
              <th>Dasar hukum</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id}>
                <td className="angka">{formatIDR(r.amount_idr)}</td>
                <td className="angka">{r.watch_bp / 100}%</td>
                <td className="angka">{r.warn_bp / 100}%</td>
                <td>
                  {r.register_by_policy === 'END_OF_BOOK_YEAR'
                    ? 'Akhir tahun buku'
                    : 'Akhir bulan berikutnya'}
                </td>
                <td>
                  {r.vat_starts_policy === 'NEXT_BOOK_YEAR_FIRST_PERIOD'
                    ? 'Masa pajak pertama tahun buku berikutnya'
                    : 'Bulan setelah batas daftar'}
                </td>
                <td>
                  {r.valid_from} — {r.valid_to ?? 'sekarang'}
                </td>
                <td>{r.legal_ref}</td>
                <td>
                  {r.in_force && <span className="lencana lencana-aman">Berlaku</span>}
                  {r.staged && <span className="lencana">Belum berlaku</span>}
                  {!r.in_force && !r.staged && <span className="teks-redup">Sudah berakhir</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </Tabel>
      )}

      {bolehUbah && base !== null && (
        <div className="alat">
          <span className="catatan">Omzet dihitung:</span>
          <div className="chip-baris">
            <button
              type="button"
              className="chip"
              aria-pressed={base === 'NET_OF_VAT'}
              onClick={() => base !== 'NET_OF_VAT' && setPilihan('NET_OF_VAT')}
            >
              Tanpa PPN
            </button>
            <button
              type="button"
              className="chip"
              aria-pressed={base === 'GROSS'}
              onClick={() => base !== 'GROSS' && setPilihan('GROSS')}
            >
              Termasuk PPN
            </button>
          </div>
        </div>
      )}

      {pilihan && (
        <Konfirmasi
          judul="Ubah dasar perhitungan omzet"
          sibuk={sibuk}
          labelJalankan={pilihan === 'NET_OF_VAT' ? 'Hitung tanpa PPN' : 'Hitung termasuk PPN'}
          tutup={() => setPilihan(null)}
          jalankan={simpanBase}
          petunjukAlasan="Mis. mengikuti arahan konsultan pajak"
        >
          <p>
            Omzet akan dihitung{' '}
            <strong>{pilihan === 'NET_OF_VAT' ? 'tanpa PPN' : 'termasuk PPN'}</strong>, bukan{' '}
            {base === 'NET_OF_VAT' ? 'tanpa PPN' : 'termasuk PPN'}.
          </p>
          <p className="catatan">
            Ini mengubah posisi terhadap batas Rp 4,8 miliar untuk seluruh tahun buku, termasuk
            tahun yang sudah lewat, dan karenanya bisa mengubah tanggal perusahaan dianggap
            melewati batas. Perubahan dicatat di log audit.
          </p>
        </Konfirmasi>
      )}

      {caveat && <p className="disclaimer">{caveat}</p>}
    </div>
  )
}

// --- the rules --------------------------------------------------------------

function AturanPajak({
  rules,
  today,
  bolehUbah,
  sibuk,
  setSibuk,
  setGalat,
  muatUlang,
  entityId,
}: {
  rules: TaxRule[]
  today: string
  bolehUbah: boolean
  sibuk: boolean
  setSibuk: (v: boolean) => void
  setGalat: (v: string | null) => void
  muatUlang: () => void
  entityId: string
}) {
  const [tambah, setTambah] = useState(false)
  const [menutup, setMenutup] = useState<TaxRule | null>(null)
  const [menghapus, setMenghapus] = useState<TaxRule | null>(null)
  const [validTo, setValidTo] = useState('')

  const jalankan = async (fn: () => Promise<unknown>) => {
    setSibuk(true)
    try {
      await fn()
      setGalat(null)
      muatUlang()
    } catch (err: unknown) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal menyimpan aturan pajak')
    } finally {
      setSibuk(false)
    }
  }

  const mulaiTutup = (r: TaxRule) => {
    setValidTo(today)
    setMenutup(r)
  }

  return (
    <div className="card">
      <div className="card-kepala">
        <h3>Aturan pajak</h3>
        {bolehUbah && (
          <button className={tambah ? 'sekunder' : undefined} onClick={() => setTambah((v) => !v)}>
            {tambah ? 'Batal' : 'Tambah aturan'}
          </button>
        )}
      </div>

      {/* INV-4 stated on the screen, because it is the rule that keeps
          historical figures from moving and it is not obvious from the UI. */}
      <p className="catatan">
        Tarif tidak pernah diubah. Untuk mengganti tarif, tutup aturan lama dengan tanggal
        berakhirnya lalu buat aturan baru mulai hari berikutnya. Penjualan yang sudah terjadi
        menyimpan tarifnya sendiri dan tidak akan berubah.
      </p>

      {tambah && bolehUbah && (
        <FormAturan
          sibuk={sibuk}
          simpan={(body) =>
            jalankan(async () => {
              await createTaxRule(entityId, body)
              setTambah(false)
            })
          }
        />
      )}

      {rules.length === 0 ? (
        <p className="kosong">Belum ada aturan pajak untuk perusahaan ini.</p>
      ) : (
        <Tabel label="Aturan pajak yang pernah berlaku">
          <thead>
            <tr>
              <th>Jenis</th>
              <th className="angka">Tarif</th>
              <th className="angka">DPP nilai lain</th>
              <th className="angka">Tarif efektif</th>
              <th>Harga</th>
              <th>Pembulatan</th>
              <th>Berlaku</th>
              <th>Dasar hukum</th>
              <th>Status</th>
              {bolehUbah && <th />}
            </tr>
          </thead>
          <tbody>
            {rules.map((r) => (
              <tr key={r.id}>
                <td>{r.tax_type}</td>
                <td className="angka">{formatRate(r.rate_bp)}</td>
                {/* The exact fraction, as the regulation states it. */}
                <td className="angka">
                  {r.dpp_factor_num}/{r.dpp_factor_den}
                </td>
                <td className="angka">
                  <strong>{formatRate(r.effective_rate_bp)}</strong>
                </td>
                <td>{r.is_inclusive ? 'Termasuk PPN' : 'Belum termasuk PPN'}</td>
                <td>
                  {r.calculation_level === 'INVOICE' ? 'Per nota' : 'Per baris'}
                  {r.rounding_unit > 1 ? `, kelipatan ${r.rounding_unit}` : ''}
                </td>
                <td>
                  {r.valid_from} — {r.valid_to ?? 'sekarang'}
                </td>
                <td>{r.legal_ref}</td>
                <td>
                  {r.in_force && <span className="lencana lencana-aman">Berlaku</span>}
                  {r.staged && <span className="lencana">Belum berlaku</span>}
                  {!r.in_force && !r.staged && <span className="teks-redup">Sudah berakhir</span>}
                </td>
                {bolehUbah && (
                  <td>
                    {r.in_force && (
                      <button className="dalam-baris" disabled={sibuk} onClick={() => mulaiTutup(r)}>
                        Tutup
                      </button>
                    )}
                    {/* Only a rule that has priced nothing can be removed. One
                        already in force is closed, because sales were rung
                        under it and the report explaining them cites it. */}
                    {r.staged && (
                      <button
                        className="dalam-baris"
                        disabled={sibuk}
                        onClick={() => setMenghapus(r)}
                      >
                        Hapus
                      </button>
                    )}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </Tabel>
      )}

      {menutup && (
        <Konfirmasi
          judul={`Tutup aturan ${menutup.legal_ref}`}
          gawat
          sibuk={sibuk}
          labelJalankan="Tutup aturan"
          tutup={() => setMenutup(null)}
          jalankan={(alasan) => {
            void jalankan(() => closeTaxRule(entityId, menutup.id, validTo.trim(), alasan)).then(
              () => setMenutup(null),
            )
          }}
          petunjukAlasan="Mis. diganti PMK baru per 1 Januari"
          tambahan={
            <label>
              <span>Tanggal terakhir berlaku *</span>
              <input type="date" value={validTo} onChange={(e) => setValidTo(e.target.value)} />
              <small className="petunjuk">
                Aturan penggantinya harus mulai keesokan harinya, atau penjualan pada hari di
                antaranya tidak punya tarif.
              </small>
            </label>
          }
        >
          <p>
            Aturan <strong>{menutup.legal_ref}</strong> — tarif efektif{' '}
            {formatRate(menutup.effective_rate_bp)}, berlaku sejak {menutup.valid_from} — akan
            berhenti berlaku.
          </p>
          <p className="catatan">
            Penjualan yang sudah terjadi menyimpan tarifnya sendiri dan tidak berubah (INV-3).
            Perubahan dicatat di log audit.
          </p>
        </Konfirmasi>
      )}

      {menghapus && (
        <Konfirmasi
          judul={`Hapus aturan ${menghapus.legal_ref}`}
          gawat
          sibuk={sibuk}
          labelJalankan="Hapus aturan"
          tutup={() => setMenghapus(null)}
          jalankan={(alasan) => {
            void jalankan(() => deleteTaxRule(entityId, menghapus.id, alasan)).then(() =>
              setMenghapus(null),
            )
          }}
          petunjukAlasan="Mis. salah ketik tanggal mulai"
        >
          <p>
            Aturan <strong>{menghapus.legal_ref}</strong> belum berlaku — mulai{' '}
            {menghapus.valid_from} — sehingga belum pernah dipakai untuk menghitung PPN apa pun.
          </p>
          <p className="catatan">
            Hanya aturan yang belum berlaku yang bisa dihapus. Perubahan dicatat di log audit.
          </p>
        </Konfirmasi>
      )}
    </div>
  )
}

function FormAturan({
  sibuk,
  simpan,
}: {
  sibuk: boolean
  simpan: (body: TaxRuleInput) => void
}) {
  // Defaults are the August 2026 seed: 12% statutory on a DPP nilai lain of
  // 11/12, giving an effective 11% (PMK 131/2024).
  const [rateBP, setRateBP] = useState('1200')
  const [num, setNum] = useState('11')
  const [den, setDen] = useState('12')
  const [inclusive, setInclusive] = useState(true)
  const [level, setLevel] = useState<'LINE' | 'INVOICE'>('INVOICE')
  const [validFrom, setValidFrom] = useState('')
  const [legalRef, setLegalRef] = useState('')
  const [note, setNote] = useState('')

  const bp = Number(rateBP) || 0
  const n = Number(num) || 0
  const d = Number(den) || 1
  const efektif = d > 0 ? Math.trunc((bp * n) / d) : 0

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault()
        const body: TaxRuleInput = {
          tax_type: 'PPN',
          rate_bp: bp,
          dpp_factor_num: n,
          dpp_factor_den: d,
          is_inclusive: inclusive,
          calculation_level: level,
          rounding_mode: 'HALF_UP',
          rounding_unit: 1,
          valid_from: validFrom.trim(),
          legal_ref: legalRef.trim(),
        }
        if (note.trim()) body.note = note.trim()
        simpan(body)
      }}
    >
      <div className="baris">
        <Teks
          label="Tarif (basis point)"
          nilai={rateBP}
          ubah={setRateBP}
          wajib
          petunjuk="12% ditulis 1200. Bilangan bulat, bukan desimal."
        />
        <Teks label="DPP pembilang" nilai={num} ubah={setNum} wajib petunjuk="11 untuk 11/12" />
        <Teks label="DPP penyebut" nilai={den} ubah={setDen} wajib petunjuk="12 untuk 11/12" />
      </div>

      {/* The effective rate as the fraction reduces, shown before saving so a
          typo in either half is visible while it is still a typo. */}
      <p className="catatan">
        Tarif efektif: <strong>{formatRate(efektif)}</strong> ({bp} bp × {n}/{d})
      </p>

      <div className="baris">
        <Teks
          label="Berlaku mulai"
          nilai={validFrom}
          ubah={setValidFrom}
          wajib
          tipe="date"
          petunjuk="Tanggal aturan mulai berlaku, bukan tanggal hari ini."
        />
        <Teks
          label="Dasar hukum"
          nilai={legalRef}
          ubah={setLegalRef}
          wajib
          petunjuk="Mis. PMK 131/2024. Wajib diisi — ini yang dibaca konsultan pajak."
        />
      </div>

      <div className="baris">
        <label>
          <span>Pembulatan</span>
          <select
            value={level}
            onChange={(e) => setLevel(e.target.value === 'LINE' ? 'LINE' : 'INVOICE')}
          >
            <option value="INVOICE">Per nota</option>
            <option value="LINE">Per baris</option>
          </select>
        </label>
        <Teks label="Catatan" nilai={note} ubah={setNote} />
      </div>

      <Centang
        label="Harga jual sudah termasuk PPN"
        nilai={inclusive}
        ubah={setInclusive}
        petunjuk="Harga rak di Indonesia umumnya sudah termasuk PPN. Jika perusahaan menagih klinik dengan harga sebelum PPN, matikan pilihan ini."
      />

      {/* A known gap, stated where the choice is made rather than discovered at
          the till. The cashier screen totals the cart from the shelf prices,
          which is exact under inclusive pricing and short by the PPN under
          exclusive — so the change owed would be wrong until the sale is saved
          and the server's figure comes back. The stored sale is correct either
          way; the preview is not. Until the till prices its cart on the server,
          this warning is the control. */}
      {!inclusive && (
        <div className="galat" role="alert">
          <strong>Layar kasir belum mendukung harga sebelum PPN.</strong> Total di keranjang
          dihitung dari harga jual, sehingga PPN yang ditambahkan belum terlihat dan kembalian bisa
          salah sampai penjualan disimpan. Penjualan yang tersimpan tetap benar. Gunakan pilihan ini
          hanya jika penjualan tidak dilayani lewat kasir.
        </div>
      )}

      <button type="submit" disabled={sibuk}>
        Simpan aturan
      </button>
    </form>
  )
}

// --- the position -----------------------------------------------------------

function PosisiPPN({ entityId }: { entityId: string }) {
  const [masa, setMasa] = useState(() => new Date().toISOString().slice(0, 7))
  const [data, setData] = useState<PPNPosition | null>(null)
  const [galat, setGalat] = useState<string | null>(null)

  const muat = useCallback(() => {
    ppnPosition(entityId, masa)
      .then((p) => {
        setData(p)
        setGalat(null)
      })
      .catch((err: unknown) => {
        setData(null)
        setGalat(err instanceof ApiError ? err.message : 'Gagal memuat posisi PPN')
      })
  }, [entityId, masa])
  useEffect(muat, [muat])

  return (
    <div className="card">
      <div className="card-kepala">
        <h3>{data?.title ?? 'Posisi PPN per Masa Pajak'}</h3>
        <label>
          <span>Masa pajak</span>
          <input type="month" value={masa} onChange={(e) => setMasa(e.target.value)} />
        </label>
      </div>

      <Galat pesan={galat} />
      {!data ? (
        <p className="kosong">Belum ada data.</p>
      ) : (
        <>
          <Tabel label="Posisi PPN masa pajak ini">
            <tbody>
              <tr>
                <td>PPN keluaran — penjualan dengan faktur</td>
                <td className="angka">{formatIDR(data.output.with_faktur_idr)}</td>
              </tr>
              {/* TASKS 5.4 on the screen. Not a warning — an ordinary walk-in
                  looks like this — but on its own line, because nothing else
                  in the business will ever mention it. */}
              <tr>
                <td>
                  PPN keluaran — penjualan <strong>tanpa faktur</strong>
                  <br />
                  <small className="catatan">
                    Tetap terutang. Kewajiban PPN timbul saat penyerahan barang, bukan saat faktur
                    diterbitkan.
                  </small>
                </td>
                <td className="angka">{formatIDR(data.output.without_faktur_idr)}</td>
              </tr>
              {data.output.reversed_idr !== 0 && (
                <tr>
                  <td>PPN keluaran dikembalikan lewat retur</td>
                  <td className="angka">−{formatIDR(data.output.reversed_idr)}</td>
                </tr>
              )}
              <tr>
                <td>
                  <strong>PPN keluaran</strong>
                </td>
                <td className="angka">
                  <strong>{formatIDR(data.output.net_idr)}</strong>
                </td>
              </tr>

              <tr>
                <td>PPN masukan yang dapat dikreditkan (ada faktur)</td>
                <td className="angka">{formatIDR(data.input.creditable_idr)}</td>
              </tr>
              {data.input.reversed_idr !== 0 && (
                <tr>
                  <td>PPN masukan dikembalikan lewat retur pembelian</td>
                  <td className="angka">−{formatIDR(data.input.reversed_idr)}</td>
                </tr>
              )}
              {/* Reported, never netted: this PPN is already inside the cost of
                  the goods (SPEC §3.2), and crediting it here as well would
                  claim the same rupiah twice. */}
              <tr>
                <td>
                  PPN masukan <strong>tanpa faktur</strong> — tidak dapat dikreditkan
                  <br />
                  <small className="catatan">
                    Sudah masuk ke harga pokok barang, jadi tidak dikurangkan di sini.
                  </small>
                </td>
                <td className="angka">{formatIDR(data.input.non_creditable_idr)}</td>
              </tr>
              <tr>
                <td>
                  <strong>PPN masukan</strong>
                </td>
                <td className="angka">
                  <strong>{formatIDR(data.input.net_idr)}</strong>
                </td>
              </tr>
            </tbody>
            <tfoot>
              <tr>
                <td>
                  <strong>{data.is_overpaid ? 'Lebih bayar' : 'PPN kurang bayar'}</strong>
                  {data.is_overpaid && (
                    <>
                      <br />
                      <small className="catatan">
                        Dapat dikompensasikan ke masa pajak berikutnya.
                      </small>
                    </>
                  )}
                </td>
                <td className="angka">
                  <strong>{formatIDR(data.payable_idr)}</strong>
                </td>
              </tr>
            </tfoot>
          </Tabel>

          <p className="disclaimer">{data.caveat}</p>

          {/* Every figure decomposes into the documents behind it. This report
              is what goes to a konsultan pajak, and a number they cannot check
              is a number they will not sign off. */}
          <details>
            <summary>Penjualan yang memungut PPN ({data.sales.length})</summary>
            <Tabel label="Penjualan yang memungut PPN">
              <thead>
                <tr>
                  <th>Tanggal</th>
                  <th>No. nota</th>
                  <th>Pelanggan</th>
                  <th>Faktur</th>
                  <th className="angka">DPP</th>
                  <th className="angka">PPN</th>
                </tr>
              </thead>
              <tbody>
                {data.sales.map((s) => (
                  <tr key={s.sale_id}>
                    <td>{s.business_date}</td>
                    <td>{s.invoice_no}</td>
                    <td>{s.customer_name ?? '—'}</td>
                    <td>{s.faktur_issued ? (s.faktur_no ?? 'Ya') : 'Tidak'}</td>
                    <td className="angka">{formatIDR(s.dpp_idr)}</td>
                    <td className="angka">{formatIDR(s.ppn_idr)}</td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          </details>

          <details>
            <summary>Pembelian yang membayar PPN ({data.purchases.length})</summary>
            <Tabel label="Pembelian yang membayar PPN">
              <thead>
                <tr>
                  <th>Tanggal</th>
                  <th>No. faktur pemasok</th>
                  <th>Pemasok</th>
                  <th>Faktur diterima</th>
                  <th className="angka">PPN dibayar</th>
                  <th className="angka">Dapat dikreditkan</th>
                </tr>
              </thead>
              <tbody>
                {data.purchases.map((p) => (
                  <tr key={p.purchase_id}>
                    <td>{p.business_date}</td>
                    <td>{p.faktur_no ?? p.invoice_no ?? '—'}</td>
                    <td>{p.supplier_name ?? '—'}</td>
                    <td>
                      {p.faktur_received ? 'Ya' : <span className="teks-bahaya">Tidak</span>}
                    </td>
                    <td className="angka">{formatIDR(p.ppn_idr)}</td>
                    <td className="angka">{formatIDR(p.creditable_ppn_idr)}</td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          </details>
        </>
      )}
    </div>
  )
}
