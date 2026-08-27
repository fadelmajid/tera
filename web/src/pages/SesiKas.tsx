import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { currentSession, listSessions, sessionTotals, closeSession, openDrawer } from '../api/kasir'
import type { CashSession, ZReport } from '../api/kasir'
import { ApiError } from '../api/client'
import { formatIDR, parseIDR, ZERO, sub } from '../money'
import type { IDR } from '../money'
import { Galat, Memuat, Stat, Tabel, Teks } from '../components/dasar'
import { Modal } from '../components/Modal'

/**
 * Sesi kas — open/close and the Z-report (TASKS 2.6, R9.8).
 *
 * Closing is also what shuts the void window. While the till is open a sale
 * rung by mistake can be voided outright; once it is counted, a correction is a
 * return, because by then the goods are with the customer. That makes closing
 * irreversible, so it goes through a confirmation showing the variance the
 * close will record — the number somebody will be asked about tomorrow.
 *
 * Non-cash takings are shown but never expected in the drawer — counting a QRIS
 * payment as cash is how a till appears to "lose" money that was never in it.
 */
export function SesiKasPage({ entityId }: { entityId: string }) {
  const [sesi, setSesi] = useState<CashSession | null>(null)
  const [riwayat, setRiwayat] = useState<CashSession[]>([])
  const [z, setZ] = useState<ZReport | null>(null)
  const [terhitung, setTerhitung] = useState('')
  const [catatan, setCatatan] = useState('')
  const [galat, setGalat] = useState<string | null>(null)
  const [konfirmasi, setKonfirmasi] = useState(false)
  const [sedang, setSedang] = useState(false)
  const [memuat, setMemuat] = useState(true)

  const muat = useCallback(async () => {
    setMemuat(true)
    try {
      const open = await currentSession(entityId)
      setSesi(open)
      const [r, tot] = await Promise.all([
        listSessions(entityId),
        open ? sessionTotals(entityId, open.id) : Promise.resolve(null),
      ])
      setRiwayat(r)
      setZ(tot)
      setGalat(null)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal memuat sesi kas')
    } finally {
      setMemuat(false)
    }
  }, [entityId])

  useEffect(() => {
    void muat()
  }, [muat])

  let terhitungIdr: IDR = ZERO
  let salahAngka = false
  try {
    terhitungIdr = terhitung.trim() === '' ? ZERO : parseIDR(terhitung)
  } catch {
    salahAngka = true
  }
  const selisih = z && !salahAngka ? sub(terhitungIdr, z.expected_cash) : ZERO

  async function tutup() {
    if (!sesi) return
    setSedang(true)
    try {
      await closeSession(entityId, sesi.id, terhitungIdr, catatan)
      setTerhitung('')
      setCatatan('')
      setKonfirmasi(false)
      await muat()
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Jumlah harus rupiah bulat')
    } finally {
      setSedang(false)
    }
  }

  return (
    <>
      <div className="card-kepala">
        <h2>Sesi kas</h2>
        {sesi ? (
          <span className="lencana lencana-aman">Terbuka · {sesi.business_date}</span>
        ) : (
          <span className="lencana">Tidak ada sesi terbuka</span>
        )}
      </div>
      <Galat pesan={galat} />

      <div className="tumpuk">
        {memuat ? (
          <Memuat apa="sesi kas" />
        ) : sesi && z ? (
          <div className="card">
            <h3>Sesi berjalan — {sesi.business_date}</h3>

            <Tabel label="Rincian uang sesi berjalan">
              <tbody>
                <tr>
                  <td>Modal awal</td>
                  <td className="angka">{formatIDR(sesi.opening_float_idr)}</td>
                </tr>
                {Object.entries(z.by_method).map(([metode, jumlah]) => (
                  <tr key={metode}>
                    <td>{metode}</td>
                    <td className="angka">{formatIDR(jumlah)}</td>
                  </tr>
                ))}
                {z.cash_refunds > 0 && (
                  <tr>
                    <td>Retur tunai</td>
                    <td className="angka">-{formatIDR(z.cash_refunds)}</td>
                  </tr>
                )}
              </tbody>
              <tfoot>
                <tr>
                  <td>
                    <strong>Seharusnya di laci</strong>
                    <br />
                    <small className="catatan">
                      Hanya tunai. Transfer dan QRIS tercatat tetapi tidak ada di laci.
                    </small>
                  </td>
                  <td className="angka">
                    <strong>{formatIDR(z.expected_cash)}</strong>
                  </td>
                </tr>
              </tfoot>
            </Tabel>

            <div className="baris">
              <Teks
                label="Uang terhitung di laci"
                nilai={terhitung}
                ubah={setTerhitung}
                wajib
                inputMode="numeric"
                petunjuk="Hitung fisik uang di laci, termasuk modal awal."
              />
              <Teks label="Catatan" nilai={catatan} ubah={setCatatan} />
            </div>

            {terhitung.trim() !== '' && !salahAngka && (
              <div className="statistik">
                <Stat
                  label="Selisih"
                  nilai={
                    <span className={selisih < 0 ? 'teks-bahaya' : undefined}>
                      {selisih > 0 ? `+${formatIDR(selisih)}` : formatIDR(selisih)}
                    </span>
                  }
                  kaki={
                    selisih === 0
                      ? 'Cocok persis.'
                      : selisih < 0
                        ? 'Uang di laci kurang dari yang seharusnya.'
                        : 'Uang di laci lebih dari yang seharusnya.'
                  }
                />
              </div>
            )}

            <p className="catatan">
              Setelah ditutup, penjualan dalam sesi ini tidak bisa dibatalkan lagi — koreksinya
              lewat retur.
            </p>

            <div className="aksi">
              <button
                onClick={() => setKonfirmasi(true)}
                disabled={sedang || terhitung.trim() === '' || salahAngka}
              >
                Tutup sesi
              </button>
              <button
                className="sekunder"
                onClick={() => {
                  void openDrawer(entityId).catch((err: unknown) =>
                    setGalat(err instanceof ApiError ? err.message : 'Gagal membuka laci'),
                  )
                }}
              >
                Buka laci
              </button>
            </div>
          </div>
        ) : (
          <div className="card">
            <h3>Tidak ada sesi yang terbuka</h3>
            <p className="catatan">
              Kasir belum bisa dipakai sampai sebuah sesi kas dibuka dan modal awal lacinya dicatat.
            </p>
            <div className="aksi">
              <Link className="tombol" to="/kasir">
                Buka sesi di kasir
              </Link>
            </div>
          </div>
        )}

        <div className="card">
          <h3>Riwayat</h3>
          {riwayat.length === 0 ? (
            <p className="kosong">Belum ada sesi kas.</p>
          ) : (
            <Tabel label="Riwayat sesi kas">
              <thead>
                <tr>
                  <th>Tanggal</th>
                  <th>Status</th>
                  <th className="angka">Seharusnya</th>
                  <th className="angka">Terhitung</th>
                  <th className="angka">Selisih</th>
                </tr>
              </thead>
              <tbody>
                {riwayat.map((s) => (
                  <tr key={s.id}>
                    <td>{s.business_date}</td>
                    <td>
                      {s.status === 'OPEN' ? (
                        <span className="lencana lencana-aman">Terbuka</span>
                      ) : (
                        <span className="lencana">Ditutup</span>
                      )}
                    </td>
                    <td className="angka">
                      {s.expected_cash_idr === null ? '—' : formatIDR(s.expected_cash_idr)}
                    </td>
                    <td className="angka">
                      {s.counted_cash_idr === null ? '—' : formatIDR(s.counted_cash_idr)}
                    </td>
                    <td className={(s.variance_idr ?? 0) < 0 ? 'angka teks-bahaya' : 'angka'}>
                      {s.variance_idr === null ? '—' : formatIDR(s.variance_idr)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          )}
        </div>
      </div>

      {konfirmasi && sesi && z && (
        <Modal
          judul="Tutup sesi kas"
          gawat
          tutup={() => setKonfirmasi(false)}
          labelTutup="Batal"
          aksi={
            <>
              <button className="bahaya" disabled={sedang} onClick={() => void tutup()}>
                {sedang ? 'Menutup…' : 'Tutup sesi'}
              </button>
              <button className="sekunder" disabled={sedang} onClick={() => setKonfirmasi(false)}>
                Batal
              </button>
            </>
          }
        >
          <p>Sesi {sesi.business_date} akan ditutup dengan angka berikut.</p>
          <div className="statistik">
            <Stat label="Seharusnya di laci" nilai={formatIDR(z.expected_cash)} />
            <Stat label="Terhitung" nilai={formatIDR(terhitungIdr)} />
            <Stat
              label="Selisih"
              nilai={
                <span className={selisih < 0 ? 'teks-bahaya' : undefined}>
                  {selisih > 0 ? `+${formatIDR(selisih)}` : formatIDR(selisih)}
                </span>
              }
              besar
            />
          </div>
          {selisih !== ZERO && (
            <div className="disclaimer">
              Selisih ini tersimpan sebagai bagian dari sesi dan muncul di riwayat. Jika angkanya
              belum masuk akal, batalkan dan hitung ulang lacinya sekarang — sesudah ditutup,
              selisihnya sudah menjadi catatan.
            </div>
          )}
          <p className="catatan">
            Penjualan dalam sesi ini tidak bisa dibatalkan lagi setelah penutupan. Koreksi
            berikutnya harus lewat retur, karena barangnya sudah di tangan pembeli.
          </p>
        </Modal>
      )}
    </>
  )
}
