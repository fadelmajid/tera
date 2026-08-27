import { useCallback, useEffect, useState } from 'react'
import { BrowserRouter } from 'react-router-dom'
import { me, logout } from './api/auth'
import type { Session } from './api/auth'
import { listEntities } from './api/masterdata'
import type { Entity } from './api/masterdata'
import { Masuk } from './pages/Masuk'
import { Setup } from './pages/Setup'
import { Kerangka } from './components/Kerangka'

const ENTITY_KEY = 'tera.entity'

type State =
  | { status: 'memuat' }
  | { status: 'keluar' }
  | { status: 'masuk'; session: Session; entities: Entity[] }

export function App() {
  const [state, setState] = useState<State>({ status: 'memuat' })
  const [entityId, setEntityId] = useState<string>(() => localStorage.getItem(ENTITY_KEY) ?? '')

  const muat = useCallback(async () => {
    const session = await me()
    const entities = await listEntities()
    setState({ status: 'masuk', session, entities })

    // Keep the previously chosen company if the user still has a role there;
    // otherwise fall back to one they can actually use.
    setEntityId((current) => {
      if (current && session.roles[current] !== undefined) return current
      return Object.keys(session.roles)[0] ?? ''
    })
  }, [])

  useEffect(() => {
    muat().catch(() => setState({ status: 'keluar' }))
  }, [muat])

  useEffect(() => {
    if (entityId) localStorage.setItem(ENTITY_KEY, entityId)
  }, [entityId])

  if (state.status === 'memuat') {
    return (
      <div className="masuk">
        <div className="card" role="status" aria-busy="true">
          <div className="merek">
            <span className="merek-tanda" aria-hidden="true">
              T
            </span>
            <h1>Tera</h1>
          </div>
          <p className="catatan">Menghubungi server…</p>
        </div>
      </div>
    )
  }

  if (state.status === 'keluar') {
    return <Masuk onMasuk={() => void muat()} />
  }

  // No company exists yet: the first account has no role anywhere, so setup is
  // the only thing it can usefully do.
  if (state.entities.length === 0) {
    return <Setup selesai={() => void muat()} />
  }

  if (!entityId) {
    return (
      <div className="masuk">
        <div className="card">
          <div className="merek">
            <span className="merek-tanda" aria-hidden="true">
              T
            </span>
            <h1>Belum ada akses</h1>
          </div>
          <p>Akun ini belum diberi peran di perusahaan mana pun. Minta pemilik untuk memberikan akses.</p>
          <button
            className="sekunder"
            onClick={() => {
              void logout().then(() => setState({ status: 'keluar' }))
            }}
          >
            Keluar
          </button>
        </div>
      </div>
    )
  }

  return (
    <BrowserRouter>
      <Kerangka
        session={state.session}
        entities={state.entities}
        entityId={entityId}
        pilihEntity={setEntityId}
        muatUlang={() => void muat()}
        onKeluar={() => {
          void logout()
            .catch(() => undefined)
            .then(() => setState({ status: 'keluar' }))
        }}
      />
    </BrowserRouter>
  )
}
