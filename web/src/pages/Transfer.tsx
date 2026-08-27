import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../api/client'
import type { Entity } from '../api/masterdata'
import { listProducts } from '../api/masterdata'
import type { Product } from '../api/masterdata'
import {
  createTransfer,
  getInterCompanyPosition,
  listTransfers,
  previewTransfer,
} from '../api/transfer'
import type {
  InterCompanyPosition,
  TransferBody,
  TransferPreview,
  TransferSummary,
} from '../api/transfer'
import { formatIDR, parseIDR, ZERO } from '../money'
import type { IDR } from '../money'
import { Berhasil, Galat, Tabel, useDaftar } from '../components/dasar'
import { Modal } from '../components/Modal'

/**
 * Transfer antar perusahaan — the flow Olsera gets wrong.
 *
 * Olsera records the inter-company transaction and does not move the stock,
 * which is why the user's quantities are drifting from reality today. Here one
 * call moves both sides or neither, and the screen shows both halves.
 *
 * The screen's real job is R4.5. Moving stock from the non-PKP company into the
 * PKP one destroys the input PPN credit on it permanently — the chain cannot be
 * reconnected afterwards. So the flow is preview → blocking confirmation →
 * commit, and the confirmation quotes the rupiah about to be destroyed rather
 * than warning in the abstract. A dialog that cannot say what is at stake
 * teaches people to click through it.
 */
export function TransferPage({
  entityId,
  entities,
}: {
  entityId: string
  entities: Entity[]
}) {
  const asal = entities.find((e) => e.id === entityId)
  const tujuanTersedia = entities.filter((e) => e.id !== entityId && e.is_active)

  const [tujuan, setTujuan] = useState(tujuanTersedia[0]?.id ?? '')
  const [tanggal, setTanggal] = useState(() => new Date().toISOString().slice(0, 10))
  const [baris, setBaris] = useState<Baris[]>([])
  const [fakturDiterbitkan, setFakturDiterbitkan] = useState(false)
  const [noFaktur, setNoFaktur] = useState('')
  const [catatan, setCatatan] = useState('')

  const [rencana, setRencana] = useState<TransferPreview | null>(null)
  const [galat, setGalat] = useState<string | null>(null)
  const [sibuk, setSibuk] = useState(false)
  const [hasil, setHasil] = useState<string | null>(null)

  const produk = useDaftar<Product>(() => listProducts(entityId), [entityId])
  const riwayat = useDaftar<TransferSummary>(() => listTransfers(entityId), [entityId])
  const [posisi, setPosisi] = useState<InterCompanyPosition[]>([])

  const muatPosisi = useCallback(() => {
    getInterCompanyPosition(entityId)
      .then(setPosisi)
      .catch(() => setPosisi([]))
  }, [entityId])
  useEffect(muatPosisi, [muatPosisi])

  const entitasTujuan = entities.find((e) => e.id === tujuan)
  const menghapusKredit =
    asal !== undefined && entitasTujuan !== undefined && !asal.is_pkp && entitasTujuan.is_pkp
  const kirimanKenaPPN = asal?.is_pkp ?? false

  const body = (): TransferBody => {
    const out: TransferBody = {
      to_entity_id: tujuan,
      transfer_date: tanggal,
      lines: baris.map((b) =>
        kirimanKenaPPN
          ? { product_id: b.productId, qty: b.qty, ppn_idr: b.ppn }
          : { product_id: b.productId, qty: b.qty },
      ),
    }
    if (kirimanKenaPPN && fakturDiterbitkan) {
      out.faktur_issued = true
      out.faktur_no = noFaktur
    }
    if (catatan) out.note = catatan
    return out
  }

  const lapor = (err: unknown) =>
    setGalat(err instanceof ApiError ? err.message : 'Terjadi kesalahan')

  /** Always previews first. The commit needs a plan the person has seen. */
  const hitung = () => {
    setSibuk(true)
    setHasil(null)
    previewTransfer(entityId, body())
      .then((p) => {
        setRencana(p)
        setGalat(null)
      })
      .catch((err: unknown) => {
        setRencana(null)
        lapor(err)
      })
      .finally(() => setSibuk(false))
  }

  const kirim = (akui: boolean) => {
    setSibuk(true)
    const payload = body()
    if (akui) payload.acknowledge_credit_loss = true
    createTransfer(entityId, payload)
      .then((r) => {
        setHasil(
          `${r.transfer.transfer_no}: ${r.consumptions} lapisan stok keluar, ` +
            `${r.lines.length} lapisan masuk di ${entitasTujuan?.name ?? 'perusahaan tujuan'}.`,
        )
        setGalat(null)
        setRencana(null)
        setBaris([])
        produk.muatUlang()
        riwayat.muatUlang()
        muatPosisi()
      })
      .catch(lapor)
      .finally(() => setSibuk(false))
  }

  const bisaHitung = tujuan !== '' && baris.length > 0 && baris.every((b) => b.qty > 0)

  return (
    <>
      <h2>Transfer antar perusahaan</h2>
      <p className="sub-judul">
        Stok benar-benar berpindah: keluar dari {asal?.name ?? 'perusahaan ini'} dan masuk ke
        perusahaan tujuan dalam satu proses. Harga transfer adalah harga pokok, tanpa markup.
      </p>

      <Galat pesan={galat} />
      {hasil && <Berhasil pesan={`Transfer tersimpan. ${hasil}`} />}

      <div className="tumpuk">
        {tujuanTersedia.length === 0 ? (
          <div className="card">
            <h3>Belum ada perusahaan tujuan</h3>
            <p className="catatan">
              Transfer membutuhkan dua perusahaan, dan baru ada satu. Tambahkan perusahaan kedua di
              layar Perusahaan, lalu kembali ke sini.
            </p>
            <div className="aksi">
              <Link className="tombol" to="/perusahaan">
                Ke Perusahaan
              </Link>
            </div>
          </div>
        ) : (
          <div className="card">
            <div className="baris">
              <label>
                <span>Perusahaan tujuan *</span>
                <select
                  value={tujuan}
                  onChange={(e) => {
                    setTujuan(e.target.value)
                    setRencana(null)
                  }}
                >
                  {tujuanTersedia.map((e) => (
                    <option key={e.id} value={e.id}>
                      {e.name} {e.is_pkp ? '(PKP)' : '(non-PKP)'}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>Tanggal</span>
                <input
                  type="date"
                  value={tanggal}
                  onChange={(e) => {
                    setTanggal(e.target.value)
                    setRencana(null)
                  }}
                />
              </label>
            </div>

            <ArahTransfer
              asal={asal}
              tujuan={entitasTujuan}
              menghapusKredit={menghapusKredit}
              kenaPPN={kirimanKenaPPN}
            />

            <BarisTransfer
              produk={produk.data}
              baris={baris}
              ubah={(b) => {
                setBaris(b)
                setRencana(null)
              }}
              mintaPPN={kirimanKenaPPN}
            />

            {kirimanKenaPPN && (
              <div className="baris">
                <label className="centang">
                  <input
                    type="checkbox"
                    checked={fakturDiterbitkan}
                    onChange={(e) => {
                      setFakturDiterbitkan(e.target.checked)
                      setRencana(null)
                    }}
                  />
                  <span>
                    Faktur pajak diterbitkan
                    <small className="petunjuk">
                      {entitasTujuan?.is_pkp
                        ? 'Perusahaan tujuan PKP: PPN-nya bisa dikreditkan.'
                        : 'Perusahaan tujuan non-PKP: PPN tetap menjadi biaya, faktur tidak menolong.'}
                    </small>
                  </span>
                </label>
                {fakturDiterbitkan && (
                  <label>
                    <span>Nomor faktur</span>
                    <input value={noFaktur} onChange={(e) => setNoFaktur(e.target.value)} />
                  </label>
                )}
              </div>
            )}

            <label>
              <span>Catatan</span>
              <input value={catatan} onChange={(e) => setCatatan(e.target.value)} />
            </label>

            <div className="aksi">
              <button disabled={!bisaHitung || sibuk} onClick={hitung}>
                {sibuk ? 'Menghitung…' : 'Hitung transfer'}
              </button>
            </div>
          </div>
        )}

        {rencana && !rencana.destroys_input_credit && (
          <RingkasanRencana
            rencana={rencana}
            sibuk={sibuk}
            lanjut={() => kirim(false)}
            batal={() => setRencana(null)}
          />
        )}

        <PosisiAntarPerusahaan posisi={posisi} />
        <Riwayat transfers={riwayat.data} entityId={entityId} />
      </div>

      {rencana?.destroys_input_credit && (
        <PeringatanKreditPPN
          rencana={rencana}
          asal={asal}
          tujuan={entitasTujuan}
          sibuk={sibuk}
          lanjut={() => kirim(true)}
          batal={() => setRencana(null)}
        />
      )}
    </>
  )
}

interface Baris {
  productId: string
  qty: number
  ppn: IDR
  /** What the person typed, kept so the field stays controlled while editing. */
  ppnTeks: string
}

function ArahTransfer({
  asal,
  tujuan,
  menghapusKredit,
  kenaPPN,
}: {
  asal: Entity | undefined
  tujuan: Entity | undefined
  menghapusKredit: boolean
  kenaPPN: boolean
}) {
  if (!asal || !tujuan) return null

  return (
    <div className={menghapusKredit ? 'sorotan' : undefined}>
      <p>
        <strong>
          {asal.name} ({asal.is_pkp ? 'PKP' : 'non-PKP'}) → {tujuan.name} (
          {tujuan.is_pkp ? 'PKP' : 'non-PKP'})
        </strong>
      </p>
      {menghapusKredit ? (
        <p className="teks-bahaya">
          Arah ini menghapus kredit PPN masukan secara permanen. Akan dikonfirmasi sebelum
          disimpan, dengan nilai rupiah yang hangus.
        </p>
      ) : kenaPPN ? (
        <p className="catatan">
          Penyerahan kena PPN.{' '}
          {tujuan.is_pkp
            ? 'Perusahaan tujuan PKP dan dapat mengkreditkannya bila ada faktur.'
            : 'Perusahaan tujuan tidak dapat mengkreditkannya — PPN masuk ke harga pokok.'}
        </p>
      ) : (
        <p className="catatan">
          Perusahaan asal non-PKP: tidak memungut PPN dan tidak menerbitkan faktur.
        </p>
      )}
    </div>
  )
}

function BarisTransfer({
  produk,
  baris,
  ubah,
  mintaPPN,
}: {
  produk: Product[]
  baris: Baris[]
  ubah: (b: Baris[]) => void
  mintaPPN: boolean
}) {
  const tambah = () => {
    const pertama = produk[0]
    if (!pertama) return
    ubah([...baris, { productId: pertama.id, qty: 1, ppn: ZERO, ppnTeks: '' }])
  }

  const set = (i: number, patch: Partial<Baris>) =>
    ubah(baris.map((b, j) => (i === j ? { ...b, ...patch } : b)))

  return (
    <>
      <Tabel label="Barang yang dipindahkan">
        <thead>
          <tr>
            <th>Produk</th>
            <th style={{ width: 110 }}>Jumlah</th>
            {mintaPPN && <th style={{ width: 170 }}>PPN penyerahan</th>}
            <th style={{ width: 44 }} />
          </tr>
        </thead>
        <tbody>
          {baris.length === 0 && (
            <tr>
              <td colSpan={mintaPPN ? 4 : 3} className="teks-redup">
                Belum ada barang. Tambahkan minimal satu.
              </td>
            </tr>
          )}
          {baris.map((b, i) => (
            <tr key={i}>
              <td>
                <select
                  value={b.productId}
                  aria-label={`Produk baris ${i + 1}`}
                  onChange={(e) => set(i, { productId: e.target.value })}
                >
                  {produk.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.code} · {p.name}
                    </option>
                  ))}
                </select>
              </td>
              <td>
                <input
                  type="number"
                  min={1}
                  value={b.qty}
                  aria-label={`Jumlah baris ${i + 1}`}
                  onChange={(e) => set(i, { qty: Number(e.target.value) })}
                />
              </td>
              {mintaPPN && (
                <td>
                  {/* Controlled. It used to be a defaultValue-only input that
                      silently kept the last parseable value when the text was
                      edited to something invalid. */}
                  <input
                    inputMode="numeric"
                    value={b.ppnTeks}
                    placeholder="0"
                    aria-label={`PPN penyerahan baris ${i + 1}`}
                    onChange={(e) => {
                      const teks = e.target.value
                      let nilai: IDR = ZERO
                      try {
                        nilai = teks.trim() === '' ? ZERO : parseIDR(teks)
                      } catch {
                        nilai = b.ppn
                      }
                      set(i, { ppnTeks: teks, ppn: nilai })
                    }}
                  />
                </td>
              )}
              <td>
                <button
                  type="button"
                  className="sekunder ikon-saja"
                  aria-label={`Hapus baris ${i + 1}`}
                  onClick={() => ubah(baris.filter((_, j) => j !== i))}
                >
                  ×
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </Tabel>
      <div className="aksi">
        <button type="button" className="sekunder" onClick={tambah} disabled={produk.length === 0}>
          Tambah barang
        </button>
      </div>
    </>
  )
}

function RingkasanRencana({
  rencana,
  sibuk,
  lanjut,
  batal,
}: {
  rencana: TransferPreview
  sibuk: boolean
  lanjut: () => void
  batal: () => void
}) {
  return (
    <div className="card">
      <h3>Periksa sebelum menyimpan</h3>
      <TabelRencana rencana={rencana} />
      <div className="aksi">
        <button disabled={sibuk} onClick={lanjut}>
          {sibuk ? 'Menyimpan…' : 'Simpan transfer'}
        </button>
        <button className="sekunder" disabled={sibuk} onClick={batal}>
          Batal
        </button>
      </div>
    </div>
  )
}

function TabelRencana({ rencana }: { rencana: TransferPreview }) {
  return (
    <Tabel label="Rencana transfer">
      <thead>
        <tr>
          <th>Produk</th>
          <th>Owner</th>
          <th className="angka">Jumlah</th>
          <th className="angka">Harga pokok</th>
          {rencana.taxable_delivery && <th className="angka">PPN</th>}
        </tr>
      </thead>
      <tbody>
        {rencana.lines.map((l) => (
          <tr key={l.product_id}>
            <td>
              {l.product_code} · {l.product_name}
            </td>
            <td>{l.owner_name ?? 'Perusahaan'}</td>
            <td className="angka">{l.qty}</td>
            <td className="angka">{formatIDR(l.cost_idr)}</td>
            {rencana.taxable_delivery && <td className="angka">{formatIDR(l.ppn_idr)}</td>}
          </tr>
        ))}
      </tbody>
      <tfoot>
        <tr>
          <td colSpan={3}>Nilai transfer</td>
          <td className="angka">
            <strong>{formatIDR(rencana.cost_total_idr)}</strong>
          </td>
          {rencana.taxable_delivery && (
            <td className="angka">
              <strong>{formatIDR(rencana.ppn_idr)}</strong>
            </td>
          )}
        </tr>
      </tfoot>
    </Tabel>
  )
}

/**
 * R4.5, and the reason this screen exists in the shape it does.
 *
 * Once the transfer commits, the input PPN credit on this stock is gone and
 * cannot be reconstructed by any document produced later. So the warning is a
 * modal that has to be answered, it quotes the actual rupiah at stake rather
 * than a percentage, and it offers the alternative that would have avoided it.
 */
function PeringatanKreditPPN({
  rencana,
  asal,
  tujuan,
  sibuk,
  lanjut,
  batal,
}: {
  rencana: TransferPreview
  asal: Entity | undefined
  tujuan: Entity | undefined
  sibuk: boolean
  lanjut: () => void
  batal: () => void
}) {
  const [mengerti, setMengerti] = useState(false)

  return (
    <Modal
      judul="Kredit PPN masukan akan hilang permanen"
      gawat
      tutup={batal}
      labelTutup="Batalkan transfer"
      aksi={
        <>
          <button className="bahaya" disabled={!mengerti || sibuk} onClick={lanjut}>
            {sibuk ? 'Menyimpan…' : 'Lanjutkan, kredit PPN hilang'}
          </button>
          <button className="sekunder" disabled={sibuk} onClick={batal}>
            Batalkan transfer
          </button>
        </>
      }
    >
      <p>
        Stok ini pindah dari <strong>{asal?.name}</strong> (non-PKP) ke{' '}
        <strong>{tujuan?.name}</strong> (PKP). Perusahaan non-PKP tidak dapat menerbitkan faktur
        pajak, sehingga {tujuan?.name} menerima stok <strong>tanpa kredit PPN masukan</strong>.
      </p>

      {rencana.forfeited_ppn_idr > 0 ? (
        <p>
          PPN yang sudah terlanjur dibayar atas stok ini dan tidak akan pernah bisa dikreditkan oleh
          siapa pun:
          <span className="angka-besar teks-bahaya">{formatIDR(rencana.forfeited_ppn_idr)}</span>
        </p>
      ) : (
        <p>
          Stok ini memang tidak pernah membawa PPN masukan, jadi tidak ada nilai yang hangus
          sekarang. Tetapi {tujuan?.name} tetap wajib memungut PPN keluaran saat menjualnya, tanpa
          ada yang bisa dikurangkan.
        </p>
      )}

      <p>
        Saat {tujuan?.name} menjual stok ini, PPN keluaran tetap wajib dipungut penuh. Rantai
        kreditnya <strong>tidak bisa disambung kembali</strong> setelah transfer ini tersimpan.
      </p>

      <p className="catatan">
        Alternatifnya: batalkan transfer ini, dan beli stok untuk {tujuan?.name} langsung dari
        pemasok dengan faktur pajak. Keputusan itu hanya bisa diambil sebelum barang berpindah.
      </p>

      <TabelRencana rencana={rencana} />

      <label className="centang">
        <input
          type="checkbox"
          checked={mengerti}
          onChange={(e) => setMengerti(e.target.checked)}
        />
        <span>
          Saya mengerti kredit PPN masukan atas stok ini hilang permanen, dan tetap ingin
          melanjutkan.
        </span>
      </label>

      <p className="disclaimer">
        Estimasi berdasarkan data di sistem ini. Konfirmasikan dengan konsultan pajak Anda.
      </p>
    </Modal>
  )
}

/** D-014: what the two companies owe each other, netted. Not hutang/piutang. */
function PosisiAntarPerusahaan({ posisi }: { posisi: InterCompanyPosition[] }) {
  if (posisi.length === 0) return null

  return (
    <div className="card">
      <h3>Posisi antar-perusahaan</h3>
      <p className="catatan">
        Nilai transfer yang belum diselesaikan antara kedua perusahaan, dihitung pada harga pokok.
        Tidak masuk laporan hutang maupun piutang, yang khusus untuk pemasok dan pelanggan.
      </p>
      <Tabel label="Posisi antar-perusahaan">
        <thead>
          <tr>
            <th>Perusahaan lawan</th>
            <th className="angka">Dikirim ke</th>
            <th className="angka">Diterima dari</th>
            <th className="angka">Selisih</th>
          </tr>
        </thead>
        <tbody>
          {posisi.map((p) => (
            <tr key={p.counterparty_id}>
              <td>{p.counterparty_name}</td>
              <td className="angka">
                {formatIDR(p.out_idr)}
                <br />
                <small className="catatan">{p.out_count} transfer</small>
              </td>
              <td className="angka">
                {formatIDR(p.in_idr)}
                <br />
                <small className="catatan">{p.in_count} transfer</small>
              </td>
              <td className="angka">
                <strong>{formatIDR(p.net_idr)}</strong>
                <br />
                <small className="catatan">
                  {p.net_idr === 0
                    ? 'seimbang'
                    : p.net_idr > 0
                      ? `${p.counterparty_name} berutang`
                      : `berutang ke ${p.counterparty_name}`}
                </small>
              </td>
            </tr>
          ))}
        </tbody>
      </Tabel>
    </div>
  )
}

function Riwayat({ transfers, entityId }: { transfers: TransferSummary[]; entityId: string }) {
  return (
    <div className="card">
      <h3>Riwayat transfer</h3>
      {transfers.length === 0 ? (
        <p className="kosong">Belum ada transfer.</p>
      ) : (
        <Tabel label="Riwayat transfer">
          <thead>
            <tr>
              <th>No.</th>
              <th>Tanggal</th>
              <th>Arah</th>
              <th className="angka">Harga pokok</th>
              <th className="angka">PPN</th>
              <th className="angka">Nilai</th>
            </tr>
          </thead>
          <tbody>
            {transfers.map((t) => (
              <tr key={t.id}>
                <td>{t.transfer_no}</td>
                <td>{t.business_date}</td>
                <td>
                  {t.from_entity_id === entityId ? 'Keluar → ' : 'Masuk ← '}
                  {t.from_entity_id === entityId ? t.to_entity_name : t.from_entity_name}
                  {t.credit_loss_ack && (
                    <>
                      <br />
                      <small className="teks-bahaya">
                        kredit PPN hilang {formatIDR(t.forfeited_ppn_idr)}
                      </small>
                    </>
                  )}
                </td>
                <td className="angka">{formatIDR(t.cost_total_idr)}</td>
                <td className="angka">{t.ppn_idr === 0 ? '—' : formatIDR(t.ppn_idr)}</td>
                <td className="angka">{formatIDR(t.amount_idr)}</td>
              </tr>
            ))}
          </tbody>
        </Tabel>
      )}
    </div>
  )
}
