import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api/client'
import { exportManifest } from '../api/laporan'
import type { ExportManifest } from '../api/laporan'
import { Galat, Tabel } from '../components/dasar'

/**
 * Ekspor data. TASKS 6.6, R5.7, R14.3.
 *
 * R14.3 says it plainly: this is the exit route if the family ever leaves this
 * software. The screen is written to be honest about that rather than to make
 * leaving feel difficult — a lock-in that has to be enforced by a missing
 * button is not a product anybody should be proud of shipping.
 *
 * So it lists exactly what the archive contains, with row counts, before
 * anybody downloads anything. A list somebody can check is the only way to tell
 * a complete export from a plausible one, and the table list comes from the
 * database itself rather than from a list kept by hand, so it cannot quietly
 * stop being complete.
 */
export function EksporPage({ entityId }: { entityId: string }) {
  const [manifest, setManifest] = useState<ExportManifest | null>(null)
  const [galat, setGalat] = useState<string | null>(null)

  const muat = useCallback(() => {
    exportManifest(entityId)
      .then((m) => {
        setManifest(m)
        setGalat(null)
      })
      .catch((err: unknown) => {
        setManifest(null)
        setGalat(err instanceof ApiError ? err.message : 'Gagal memuat daftar isi ekspor')
      })
  }, [entityId])
  useEffect(muat, [muat])

  const tables = manifest?.objects.filter((o) => o.kind === 'table') ?? []
  const views = manifest?.objects.filter((o) => o.kind === 'view') ?? []

  return (
    <>
      <h2>Ekspor data</h2>
      <p className="sub-judul">Seluruh isi basis data dalam format terbuka, apa adanya.</p>
      <Galat pesan={galat} />

      <div className="tumpuk">
      <div className="card">
        <p>
          Berkas <code>.zip</code> berisi satu <code>.csv</code> per tabel, bisa dibuka langsung di
          Excel, Google Sheets, atau LibreOffice. Di dalamnya ada <code>README.md</code> yang
          menjelaskan cara membaca setiap kolom, dan <code>manifest.json</code> berisi jumlah baris
          agar bisa dicocokkan.
        </p>
        <p>
          Data ini milik Anda. Tidak ada bagian yang terkunci di dalam aplikasi: jika suatu saat
          pindah ke sistem lain, ini yang dibawa.
        </p>

        {/* A plain link, not a fetch. The browser handles the download, the
            session cookie rides along, and a several-megabyte file never has to
            sit in the page's memory. */}
        <p>
          <a className="tombol" href="/api/v1/export" download>
            Unduh seluruh data
          </a>
        </p>

        {manifest && (
          <p className="catatan">
            {manifest.objects.length} tabel dan turunan, total{' '}
            {manifest.total_rows.toLocaleString('id-ID')} baris, versi skema{' '}
            {manifest.schema_version}.
          </p>
        )}
      </div>

      {manifest && (
        <>
          <div className="card">
            <h3>Isi ekspor</h3>
            {/* Read from the database, not from a list maintained by hand: a
                table added next year appears here the moment it exists. */}
            <p className="catatan">
              Daftar ini dibaca langsung dari basis data, jadi selalu mencakup seluruh tabel yang
              ada — bukan daftar yang harus diperbarui manual.
            </p>
            <Tabel label="Isi ekspor">
              <thead>
                <tr>
                  <th>Tabel</th>
                  <th className="angka">Baris</th>
                  <th className="angka">Kolom</th>
                </tr>
              </thead>
              <tbody>
                {tables.map((o) => (
                  <tr key={o.name}>
                    <td>
                      {o.name}
                      <br />
                      <small className="catatan">{o.file}</small>
                    </td>
                    <td className="angka">{o.rows.toLocaleString('id-ID')}</td>
                    <td className="angka">{o.columns.length}</td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          </div>

          <div className="card">
            <h3>Hitungan turunan</h3>
            <p className="catatan">
              Bukan data baru — semuanya bisa dihitung ulang dari tabel di atas. Disertakan supaya
              tidak perlu menghitung sendiri sisa stok atau sisa hutang di Excel.
            </p>
            <Tabel label="Hitungan turunan">
              <thead>
                <tr>
                  <th>Nama</th>
                  <th className="angka">Baris</th>
                </tr>
              </thead>
              <tbody>
                {views.map((o) => (
                  <tr key={o.name}>
                    <td>{o.name}</td>
                    <td className="angka">{o.rows.toLocaleString('id-ID')}</td>
                  </tr>
                ))}
              </tbody>
            </Tabel>
          </div>
        </>
      )}
      </div>
    </>
  )
}
