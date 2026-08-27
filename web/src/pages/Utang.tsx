import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api/client'
import { agingReport, bucketLabel, debtDetail, payDebt } from '../api/laporan'
import type { AgedCounterparty, AgedItem, AgingReport, DebtDetail } from '../api/laporan'
import { formatIDR, parseIDR, ZERO } from '../money'
import type { IDR } from '../money'
import { Galat, Memuat, Pemicu, Tabel } from '../components/dasar'
import { Modal } from '../components/Modal'

/**
 * Hutang and piutang, aged. TASKS 6.4-6.5, R5.5-5.6, R5.8.
 *
 * One component for both because they are the same screen: the arithmetic is
 * identical and only the counterparty and the direction of the money differ.
 * Two copies would drift, and the one that drifted would be whichever is read
 * less often.
 *
 * # Read worst-first
 *
 * R5.8's wording is that a balance without an age is not actionable. So the
 * counterparty whose oldest debt has been outstanding longest is at the top,
 * not whoever is alphabetically first, and the number beside their name is how
 * many days late they are rather than how much they owe.
 *
 * # What is not claimed
 *
 * An invoice with no due date is shown as exactly that. It is not folded into
 * "belum jatuh tempo", which would report it as fine, and it is not counted as
 * overdue either, because nobody recorded a term and this system does not know.
 * The count sits at the top of the screen where it can be fixed.
 *
 * # Recording a payment happens here and only here
 *
 * The saldo-awal screen used to offer the same action through a bare
 * `window.prompt` that took an amount and nothing else — no date, no method,
 * no note, no validation, and no way to see what was owed while typing. Two
 * interfaces for one action, one of them much worse. This is the one that
 * survived.
 */
export function UtangPage({
  entityId,
  kind,
}: {
  entityId: string
  kind: 'hutang' | 'piutang'
}) {
  const [asOf, setAsOf] = useState(() => new Date().toISOString().slice(0, 10))
  const [data, setData] = useState<AgingReport | null>(null)
  const [galat, setGalat] = useState<string | null>(null)
  const [memuat, setMemuat] = useState(true)
  const [terbuka, setTerbuka] = useState<Set<string>>(new Set())
  const [bayar, setBayar] = useState<AgedItem | null>(null)

  const muat = useCallback(() => {
    setMemuat(true)
    agingReport(entityId, kind, asOf)
      .then((r) => {
        setData(r)
        setGalat(null)
      })
      .catch((err: unknown) => {
        setData(null)
        setGalat(err instanceof ApiError ? err.message : 'Gagal memuat laporan')
      })
      .finally(() => setMemuat(false))
  }, [entityId, kind, asOf])
  useEffect(muat, [muat])

  const alih = (id: string) =>
    setTerbuka((prev) => {
      const next = new Set(prev)
      if (!next.delete(id)) next.add(id)
      return next
    })

  const judul = kind === 'hutang' ? 'Hutang' : 'Piutang'
  const lawan = kind === 'hutang' ? 'Pemasok' : 'Pelanggan'

  return (
    <>
      <h2>{data?.title ?? `Laporan ${judul}`}</h2>
      <p className="sub-judul">
        {kind === 'hutang'
          ? 'Yang belum dibayar ke pemasok, diurutkan dari yang paling lama menunggak.'
          : 'Yang belum dibayar pelanggan, diurutkan dari yang paling lama menunggak.'}
      </p>
      <Galat pesan={galat} />

      <div className="tumpuk">
        <div className="card">
          <div className="alat">
            <label>
              <span>Dihitung per tanggal</span>
              <input type="date" value={asOf} onChange={(e) => setAsOf(e.target.value)} />
            </label>
            {data && (
              <span className="hitung">
                Menunggak <strong className="angka-kiri">{formatIDR(data.buckets.overdue_idr)}</strong>{' '}
                dari <span className="angka-kiri">{formatIDR(data.buckets.total_idr)}</span>
              </span>
            )}
          </div>

          {data && (
            <>
              <Tabel label={`Umur ${judul.toLowerCase()} per kelompok`}>
                <thead>
                  <tr>
                    {data.bucket_order.map((b) => (
                      <th key={b} className="angka">
                        {bucketLabel[b]}
                      </th>
                    ))}
                    <th className="angka">Total</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    {data.bucket_order.map((b) => (
                      <td key={b} className="angka">
                        {formatIDR(data.buckets[b])}
                      </td>
                    ))}
                    <td className="angka">
                      <strong>{formatIDR(data.buckets.total_idr)}</strong>
                    </td>
                  </tr>
                </tbody>
              </Tabel>

              {/* The gap this system cannot close on its own. Ageing needs a
                  credit term, and the fix is data entry, so the count goes where
                  somebody will see it rather than into a footnote. */}
              {data.without_due_date > 0 && (
                <div className="disclaimer">
                  <strong>{data.without_due_date} dokumen tanpa tanggal jatuh tempo.</strong>{' '}
                  Nilainya {formatIDR(data.buckets.NO_DUE_DATE)} dan tidak bisa dihitung umurnya —
                  bisa jadi sudah lama lewat, bisa jadi memang tanpa tempo. Isi jatuh temponya agar
                  muncul di kolom yang benar.
                </div>
              )}

              {/* Money paid beyond what was owed. Not netted against the debt and
                  not dropped: an invoice paid twice is exactly what a ledger
                  should not lose quietly. */}
              {data.credit_idr !== 0 && (
                <div className="disclaimer">
                  <strong>Kelebihan bayar {formatIDR(data.credit_idr)}</strong> pada{' '}
                  {data.credits.length} dokumen:{' '}
                  {data.credits
                    .map((c) => `${c.counterparty_name} ${c.document_no || '(tanpa nomor)'}`)
                    .join(', ')}
                  .
                </div>
              )}
            </>
          )}
        </div>

        <div className="card">
          {memuat ? (
            <Memuat apa={judul.toLowerCase()} />
          ) : !data || data.counterparties.length === 0 ? (
            <p className="kosong">
              {kind === 'hutang' ? 'Tidak ada hutang yang belum dibayar.' : 'Tidak ada piutang.'}
            </p>
          ) : (
            <Tabel label={`${judul} per ${lawan.toLowerCase()}`} lengket={2}>
              <thead>
                <tr>
                  <th style={{ width: 40 }} />
                  <th>{lawan}</th>
                  <th>Dokumen</th>
                  <th>Jatuh tempo</th>
                  <th>Umur</th>
                  <th className="angka">Nilai</th>
                  <th className="angka">Dibayar</th>
                  <th className="angka">Sisa</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {data.counterparties.map((c) => (
                  <BarisLawan
                    key={c.id}
                    lawan={c}
                    terbuka={terbuka}
                    alih={alih}
                    entityId={entityId}
                    kind={kind}
                    pilihBayar={setBayar}
                  />
                ))}
              </tbody>
            </Tabel>
          )}
        </div>
      </div>

      {bayar && (
        <FormBayar
          entityId={entityId}
          kind={kind}
          item={bayar}
          tutup={() => setBayar(null)}
          selesai={() => {
            setBayar(null)
            muat()
          }}
        />
      )}
    </>
  )
}

/**
 * One counterparty and, when opened, their documents — in the same table and
 * the same column grid, so a document's outstanding balance sits under the
 * counterparty total it contributes to.
 */
function BarisLawan({
  lawan,
  terbuka,
  alih,
  entityId,
  kind,
  pilihBayar,
}: {
  lawan: AgedCounterparty
  terbuka: Set<string>
  alih: (id: string) => void
  entityId: string
  kind: 'hutang' | 'piutang'
  pilihBayar: (item: AgedItem) => void
}) {
  const buka = terbuka.has(lawan.id)

  return (
    <>
      <tr>
        <td>
          <Pemicu buka={buka} alih={() => alih(lawan.id)} label={lawan.name} />
        </td>
        <td>
          <strong>{lawan.name}</strong>
          {lawan.without_due_date > 0 && (
            <>
              <br />
              <small className="catatan">
                {lawan.without_due_date} dokumen tanpa jatuh tempo
              </small>
            </>
          )}
        </td>
        <td className="teks-redup">{lawan.items.length} dokumen</td>
        <td />
        <td>{lawan.oldest_days > 0 ? `${lawan.oldest_days} hari` : '—'}</td>
        <td className="angka">—</td>
        <td className="angka">—</td>
        {/* Outstanding, the same quantity this column holds on the document
            rows below. The overdue share is a note under it, not a different
            number wearing the same heading. */}
        <td className="angka">
          <strong>{formatIDR(lawan.total_idr)}</strong>
          {lawan.buckets.overdue_idr !== 0 && (
            <>
              <br />
              <small className="catatan">
                {formatIDR(lawan.buckets.overdue_idr)} menunggak
              </small>
            </>
          )}
        </td>
        <td />
      </tr>

      {buka &&
        lawan.items.map((it) => (
          <BarisDokumen
            key={it.id}
            item={it}
            entityId={entityId}
            kind={kind}
            terbuka={terbuka}
            alih={alih}
            bayar={() => pilihBayar(it)}
          />
        ))}
    </>
  )
}

function BarisDokumen({
  item,
  entityId,
  kind,
  terbuka,
  alih,
  bayar,
}: {
  item: AgedItem
  entityId: string
  kind: 'hutang' | 'piutang'
  terbuka: Set<string>
  alih: (id: string) => void
  bayar: () => void
}) {
  const kunci = `dok:${item.id}`
  const buka = terbuka.has(kunci)
  const [detail, setDetail] = useState<DebtDetail | null>(null)

  // R5.8's partial payments. "Rp 3.000.000 sisa" answers nothing when the
  // question is which invoice last month's transfer was against, so the
  // payments are one click away from the balance they produced.
  const lihat = () => {
    alih(kunci)
    if (!detail) {
      debtDetail(entityId, kind, item.id)
        .then(setDetail)
        .catch(() => setDetail(null))
    }
  }

  return (
    <>
      <tr className="baris-2">
        <td>
          <Pemicu
            buka={buka}
            alih={lihat}
            label={`pembayaran ${item.document_no || 'tanpa nomor'}`}
          />
        </td>
        <td />
        <td className="tingkat-1">
          {item.document_no || <span className="teks-redup">tanpa nomor</span>}
          <br />
          <small className="catatan">{item.incurred_on}</small>
        </td>
        <td>{item.due_date || <span className="teks-redup">belum diisi</span>}</td>
        <td>
          {item.bucket === 'NO_DUE_DATE' ? (
            <span className="lencana">tidak diketahui</span>
          ) : item.days_overdue > 0 ? (
            <span className="teks-bahaya">{item.days_overdue} hari</span>
          ) : (
            <span className="teks-redup">belum</span>
          )}
        </td>
        <td className="angka">{formatIDR(item.amount_idr)}</td>
        <td className="angka">{item.paid_idr !== 0 ? formatIDR(item.paid_idr) : '—'}</td>
        <td className="angka">
          <strong>{formatIDR(item.outstanding_idr)}</strong>
        </td>
        <td>
          <button className="dalam-baris" onClick={bayar}>
            Bayar
          </button>
        </td>
      </tr>

      {buka && (
        <tr className="baris-3">
          <td />
          <td colSpan={8} className="tingkat-2">
            {!detail ? (
              <p className="kosong rapat" aria-busy="true">
                Memuat pembayaran…
              </p>
            ) : detail.payments.length === 0 ? (
              <p className="kosong rapat">Belum ada pembayaran atas dokumen ini.</p>
            ) : (
              <Tabel label={`Pembayaran ${item.document_no || 'tanpa nomor'}`}>
                <thead>
                  <tr>
                    <th>Tanggal</th>
                    <th>Cara</th>
                    <th>Catatan</th>
                    <th className="angka">Jumlah</th>
                  </tr>
                </thead>
                <tbody>
                  {detail.payments.map((p) => (
                    <tr key={p.id}>
                      <td>{p.paid_on}</td>
                      <td>{p.method}</td>
                      <td>{p.note || '—'}</td>
                      <td className="angka">{formatIDR(p.amount_idr)}</td>
                    </tr>
                  ))}
                </tbody>
              </Tabel>
            )}
          </td>
        </tr>
      )}
    </>
  )
}

function FormBayar({
  entityId,
  kind,
  item,
  tutup,
  selesai,
}: {
  entityId: string
  kind: 'hutang' | 'piutang'
  item: AgedItem
  tutup: () => void
  selesai: () => void
}) {
  // Defaults to the whole balance, because paying in full is the ordinary case
  // and a partial payment is a deliberate act (R5.8).
  const [jumlah, setJumlah] = useState(String(item.outstanding_idr))
  const [tanggal, setTanggal] = useState(() => new Date().toISOString().slice(0, 10))
  const [cara, setCara] = useState('TRANSFER')
  const [catatan, setCatatan] = useState('')
  const [galat, setGalat] = useState<string | null>(null)
  const [sibuk, setSibuk] = useState(false)

  let nilai: IDR = ZERO
  let salahAngka: string | null = null
  try {
    nilai = parseIDR(jumlah)
  } catch {
    salahAngka = 'Jumlah harus angka rupiah bulat, tanpa koma'
  }
  const lebih = !salahAngka && nilai > item.outstanding_idr

  const kirim = () => {
    if (salahAngka) {
      setGalat(salahAngka)
      return
    }
    setSibuk(true)
    const body = catatan.trim()
      ? { amount_idr: nilai, paid_on: tanggal, method: cara, note: catatan.trim() }
      : { amount_idr: nilai, paid_on: tanggal, method: cara }
    payDebt(entityId, kind, item.id, body)
      .then(selesai)
      .catch((err: unknown) =>
        setGalat(err instanceof ApiError ? err.message : 'Gagal menyimpan pembayaran'),
      )
      .finally(() => setSibuk(false))
  }

  return (
    <Modal
      judul={kind === 'hutang' ? 'Bayar hutang' : 'Terima pembayaran'}
      tutup={tutup}
      labelTutup="Batal"
      aksi={
        <>
          <button onClick={kirim} disabled={sibuk || salahAngka !== null}>
            {sibuk ? 'Menyimpan…' : 'Simpan pembayaran'}
          </button>
          <button className="sekunder" onClick={tutup} disabled={sibuk}>
            Batal
          </button>
        </>
      }
    >
      <p>
        {item.document_no || 'Tanpa nomor'} — sisa{' '}
        <strong className="angka-kiri">{formatIDR(item.outstanding_idr)}</strong>
      </p>
      <Galat pesan={galat} />

      <label>
        <span>Jumlah *</span>
        <input value={jumlah} onChange={(e) => setJumlah(e.target.value)} inputMode="numeric" />
        {salahAngka ? (
          <small className="petunjuk">{salahAngka}</small>
        ) : (
          <small className="petunjuk">{formatIDR(nilai)}</small>
        )}
      </label>

      {lebih && (
        <div className="disclaimer">
          Lebih besar dari sisa tagihan. Kelebihannya akan muncul sebagai kelebihan bayar di
          laporan — tidak hilang, tetapi juga tidak otomatis dipindahkan ke dokumen lain.
        </div>
      )}

      <div className="baris">
        <label>
          <span>Tanggal</span>
          <input type="date" value={tanggal} onChange={(e) => setTanggal(e.target.value)} />
        </label>
        <label>
          <span>Cara</span>
          <select value={cara} onChange={(e) => setCara(e.target.value)}>
            <option value="TUNAI">Tunai</option>
            <option value="TRANSFER">Transfer</option>
            <option value="GIRO">Giro</option>
            <option value="LAINNYA">Lainnya</option>
          </select>
        </label>
      </div>

      <label>
        <span>Catatan</span>
        <input value={catatan} onChange={(e) => setCatatan(e.target.value)} />
      </label>
    </Modal>
  )
}
