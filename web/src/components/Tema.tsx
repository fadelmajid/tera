import { useState } from 'react'
import { bacaTema, labelTema, terapkanTema } from '../tema'
import type { Tema as TemaPilihan } from '../tema'

/**
 * The theme switch in the sidebar footer.
 *
 * Three segments rather than a single toggle, because "follow the system" is a
 * real preference and a two-state switch cannot express it — a toggle silently
 * pins whatever the OS happened to be at the moment it was first clicked.
 */
export function Tema() {
  const [tema, setTema] = useState<TemaPilihan>(bacaTema)

  const pilih = (t: TemaPilihan) => {
    terapkanTema(t)
    setTema(t)
  }

  return (
    <div className="tema" role="group" aria-label="Tampilan">
      {(['terang', 'gelap', 'sistem'] as const).map((t) => (
        <button
          key={t}
          type="button"
          className={tema === t ? 'aktif' : ''}
          aria-pressed={tema === t}
          onClick={() => pilih(t)}
        >
          {labelTema[t]}
        </button>
      ))}
    </div>
  )
}
