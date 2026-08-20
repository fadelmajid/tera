import type { Session } from '../api/auth'

/**
 * The application shell: which company you are in, and what you may do in it.
 *
 * Navigation is built from the role held in the *selected* company. A user may
 * be an owner in one and staff in the other (R13.4), so the menu changes when
 * the company does — it is not a property of the account.
 */
export function Kerangka({ session, onKeluar }: { session: Session; onKeluar: () => void }) {
  const entityIds = Object.keys(session.roles)

  return (
    <div className="app">
      <aside className="sidebar">
        <h1>Tera</h1>
        <p className="entity">{session.user.full_name}</p>

        <nav>
          <a className="active" href="#/">
            Beranda
          </a>
        </nav>

        <p style={{ marginTop: 24 }}>
          <button className="sekunder" onClick={onKeluar}>
            Keluar
          </button>
        </p>
      </aside>

      <main className="main">
        <h2>Beranda</h2>

        {entityIds.length === 0 ? (
          <div className="card">
            <p>
              Akun ini belum memiliki akses ke perusahaan mana pun. Minta pemilik untuk
              memberikan akses.
            </p>
            <p className="kosong">
              Data master dan menu lainnya akan muncul setelah akses diberikan.
            </p>
          </div>
        ) : (
          <div className="card">
            <p>Perusahaan yang dapat diakses:</p>
            <table>
              <thead>
                <tr>
                  <th>Perusahaan</th>
                  <th>Peran</th>
                </tr>
              </thead>
              <tbody>
                {entityIds.map((id) => (
                  <tr key={id}>
                    <td>{id}</td>
                    <td>{session.roles[id]}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </main>
    </div>
  )
}
