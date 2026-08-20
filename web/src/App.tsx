import { useEffect, useState } from 'react'
import { me, logout } from './api/auth'
import type { Session } from './api/auth'
import { Masuk } from './pages/Masuk'
import { Kerangka } from './components/Kerangka'

type State = { status: 'memuat' } | { status: 'keluar' } | { status: 'masuk'; session: Session }

export function App() {
  const [state, setState] = useState<State>({ status: 'memuat' })

  // A session survives a refresh — it lives in an HttpOnly cookie, so the only
  // way to know whether one is valid is to ask.
  useEffect(() => {
    let batal = false
    me()
      .then((session) => !batal && setState({ status: 'masuk', session }))
      .catch(() => !batal && setState({ status: 'keluar' }))
    return () => {
      batal = true
    }
  }, [])

  if (state.status === 'memuat') return <div className="masuk">Memuat…</div>

  if (state.status === 'keluar') {
    return <Masuk onMasuk={(session) => setState({ status: 'masuk', session })} />
  }

  return (
    <Kerangka
      session={state.session}
      onKeluar={async () => {
        await logout().catch(() => undefined)
        setState({ status: 'keluar' })
      }}
    />
  )
}
