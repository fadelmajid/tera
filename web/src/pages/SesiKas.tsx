import { useCallback, useEffect, useState } from 'react'
import { currentSession, listSessions, sessionTotals, closeSession, openDrawer } from '../api/kasir'
import type { CashSession, ZReport } from '../api/kasir'
import { ApiError } from '../api/client'
import { formatIDR, parseIDR } from '../money'
import { Galat, Teks } from '../components/dasar'

/**
 * Sesi kas — open/close and the Z-report (TASKS 2.6, R9.8).
 *
 * Closing is also what shuts the void window. While the till is open a sale
 * rung by mistake can be voided outright; once it is counted, a correction is a
 * return, because by then the goods are with the customer.
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
  const [sedang, setSedang] = useState(false)

  const muat = useCallback(async () => {
    try {
      const open = await currentSession(entityId)
      setSesi(open)
      setRiwayat(await listSessions(entityId))
      setZ(open ? await sessionTotals(entityId, open.id) : null)
      setGalat(null)
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Gagal memuat sesi kas')
    }
  }, [entityId])

  useEffect(() => {
    void muat()
  }, [muat])

  async function tutup() {
    if (!sesi) return
    setSedang(true)
    try {
      await closeSession(entityId, sesi.id, parseIDR(terhitung), catatan)
      setTerhitung('')
      setCatatan('')
      await muat()
    } catch (err) {
      setGalat(err instanceof ApiError ? err.message : 'Jumlah harus rupiah bulat')
    } finally {
      setSedang(false)
    }
  }

  return (
    <>
      <h2>Sesi kas</h2>
      <Galat pesan={galat} />

      {sesi && z ? (
        <div className="card" style={{ marginBottom: 20 }}>
          <h3 style={{ marginTop: 0 }}>Sesi berjalan — {sesi.business_date}</h3>

          <table>
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
              <tr>
                <td>
                  <strong>Seharusnya di laci</strong>
                  <br />
                  <small style={{ color: 'var(--muted)' }}>
                    Hanya tunai. Transfer dan QRIS tercatat tetapi tidak ada di laci.
                  </small>
                </td>
                <td className="angka">
                  <strong>{formatIDR(z.expected_cash)}</strong>
                </td>
              </tr>
            </tbody>
          </table>

          <div style={{ marginTop: 12 }}>
            <Teks label="Uang terhitung di laci" nilai={terhitung} ubah={setTerhitung} wajib />
            <Teks label="Catatan" nilai={catatan} ubah={setCatatan} />
            <p style={{ color: 'var(--muted)', fontSize: 14 }}>
              Setelah ditutup, penjualan dalam sesi ini tidak bisa dibatalkan lagi — koreksinya lewat
              retur.
            </p>
            <button onClick={() => void tutup()} disabled={sedang || terhitung.trim() === ''}>
              {sedang ? 'Menutup…' : 'Tutup sesi'}
            </button>{' '}
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
        <div className="card" style={{ marginBottom: 20 }}>
          <p className="kosong">Tidak ada sesi yang terbuka. Buka dari halaman Kasir.</p>
        </div>
      )}

      <div className="card">
        <h3>Riwayat</h3>
        {riwayat.length === 0 ? (
          <p className="kosong">Belum ada sesi kas.</p>
        ) : (
          <table>
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
                  <td>{s.status === 'OPEN' ? 'Terbuka' : 'Ditutup'}</td>
                  <td className="angka">
                    {s.expected_cash_idr === null ? '—' : formatIDR(s.expected_cash_idr)}
                  </td>
                  <td className="angka">
                    {s.counted_cash_idr === null ? '—' : formatIDR(s.counted_cash_idr)}
                  </td>
                  <td
                    className="angka"
                    style={{ color: (s.variance_idr ?? 0) < 0 ? 'var(--danger)' : undefined }}
                  >
                    {s.variance_idr === null ? '—' : formatIDR(s.variance_idr)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  )
}
