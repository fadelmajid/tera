import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { ApiError } from '../api/client'

/** Loads a list and exposes a reload, so a create refreshes what is on screen. */
export function useDaftar<T>(muat: () => Promise<T[]>, deps: unknown[]) {
  const [data, setData] = useState<T[]>([])
  const [galat, setGalat] = useState<string | null>(null)
  const [memuat, setMemuat] = useState(true)

  // eslint-disable-next-line react-hooks/exhaustive-deps
  const jalankan = useCallback(muat, deps)

  const muatUlang = useCallback(() => {
    setMemuat(true)
    jalankan()
      .then((rows) => {
        setData(rows)
        setGalat(null)
      })
      .catch((err: unknown) => setGalat(err instanceof ApiError ? err.message : 'Gagal memuat data'))
      .finally(() => setMemuat(false))
  }, [jalankan])

  useEffect(muatUlang, [muatUlang])

  return { data, galat, memuat, muatUlang, setGalat }
}

export function Galat({ pesan }: { pesan: string | null }) {
  if (!pesan) return null
  return <div className="galat">{pesan}</div>
}

export function Teks({
  label,
  nilai,
  ubah,
  wajib,
  petunjuk,
  tipe = 'text',
}: {
  label: string
  nilai: string
  ubah: (v: string) => void
  wajib?: boolean
  petunjuk?: string
  tipe?: string
}) {
  return (
    <label>
      <span>
        {label}
        {wajib ? ' *' : ''}
      </span>
      <input type={tipe} value={nilai} onChange={(e) => ubah(e.target.value)} required={wajib} />
      {petunjuk && <small style={{ color: 'var(--muted)' }}>{petunjuk}</small>}
    </label>
  )
}

export function Centang({
  label,
  nilai,
  ubah,
  petunjuk,
}: {
  label: string
  nilai: boolean
  ubah: (v: boolean) => void
  petunjuk?: string
}) {
  return (
    <label style={{ display: 'flex', gap: 10, alignItems: 'flex-start' }}>
      <input
        type="checkbox"
        checked={nilai}
        onChange={(e) => ubah(e.target.checked)}
        style={{ width: 20, height: 20, marginTop: 2, flex: '0 0 auto' }}
      />
      <span style={{ margin: 0 }}>
        {label}
        {petunjuk && (
          <>
            <br />
            <small style={{ color: 'var(--muted)' }}>{petunjuk}</small>
          </>
        )}
      </span>
    </label>
  )
}

/** A master-data page: heading, error slot, add form, and the table. */
export function Halaman({
  judul,
  keterangan,
  galat,
  form,
  children,
}: {
  judul: string
  keterangan?: string
  galat: string | null
  form: ReactNode
  children: ReactNode
}) {
  return (
    <>
      <h2>{judul}</h2>
      {keterangan && <p style={{ color: 'var(--muted)', marginTop: -8 }}>{keterangan}</p>}
      <Galat pesan={galat} />
      <div className="card" style={{ marginBottom: 20 }}>{form}</div>
      <div className="card">{children}</div>
    </>
  )
}
