import { useState, type FormEvent } from 'react'
import { login } from '../api/auth'
import type { Session } from '../api/auth'
import { ApiError } from '../api/client'

/** The login screen. */
export function Masuk({ onMasuk }: { onMasuk: (session: Session) => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [galat, setGalat] = useState<string | null>(null)
  const [sedang, setSedang] = useState(false)

  async function kirim(e: FormEvent) {
    e.preventDefault()
    setGalat(null)
    setSedang(true)
    try {
      onMasuk(await login(username, password))
    } catch (err) {
      // The server returns one message for a wrong password and an unknown
      // user alike; repeating it verbatim keeps it that way.
      setGalat(err instanceof ApiError ? err.message : 'Tidak dapat terhubung ke server')
    } finally {
      setSedang(false)
    }
  }

  return (
    <div className="masuk">
      <form className="card" onSubmit={kirim}>
        <h1>Tera</h1>
        <p className="sub">Sistem persediaan dan penjualan</p>

        {galat && <div className="galat">{galat}</div>}

        <label>
          <span>Nama pengguna</span>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoFocus
            required
          />
        </label>

        <label>
          <span>Kata sandi</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </label>

        <button type="submit" disabled={sedang}>
          {sedang ? 'Memproses…' : 'Masuk'}
        </button>
      </form>
    </div>
  )
}
