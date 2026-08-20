import { NavLink, Route, Routes, Navigate } from 'react-router-dom'
import type { Session } from '../api/auth'
import { canManageMasterData, canSeeOwnerMargin } from '../api/auth'
import type { Entity } from '../api/masterdata'
import { ProdukPage } from '../pages/Produk'
import { OwnerPage } from '../pages/Owner'
import { PemasokPage } from '../pages/Pemasok'
import { PelangganPage } from '../pages/Pelanggan'

/**
 * The application shell.
 *
 * Navigation is built from the role held in the *selected* company. A user may
 * be an owner in one and staff in the other (R13.4), so the menu changes when
 * the company does — it is not a property of the account.
 *
 * Hiding a menu item is not a security control. The server refuses the request
 * regardless; this only avoids offering someone a door that is locked.
 */
export function Kerangka({
  session,
  entities,
  entityId,
  pilihEntity,
  onKeluar,
}: {
  session: Session
  entities: Entity[]
  entityId: string
  pilihEntity: (id: string) => void
  onKeluar: () => void
}) {
  const role = session.roles[entityId]
  const entity = entities.find((e) => e.id === entityId)
  const bolehMaster = role !== undefined && canManageMasterData(role)

  return (
    <div className="app">
      <aside className="sidebar">
        <h1>Tera</h1>
        <p className="entity">
          {session.user.full_name} · {role ?? 'tanpa peran'}
        </p>

        {entities.length > 1 && (
          <label>
            <span>Perusahaan</span>
            <select value={entityId} onChange={(e) => pilihEntity(e.target.value)}>
              {entities.map((e) => (
                <option key={e.id} value={e.id}>
                  {e.name}
                  {e.is_pkp ? ' (PKP)' : ''}
                </option>
              ))}
            </select>
          </label>
        )}

        <nav>
          <NavLink to="/" end>
            Beranda
          </NavLink>
          <NavLink to="/produk">Produk</NavLink>
          <NavLink to="/owner">Owner</NavLink>
          <NavLink to="/pemasok">Pemasok</NavLink>
          <NavLink to="/pelanggan">Pelanggan</NavLink>
        </nav>

        <p style={{ marginTop: 24 }}>
          <button className="sekunder" onClick={onKeluar}>
            Keluar
          </button>
        </p>
      </aside>

      <main className="main">
        <Routes>
          <Route
            path="/"
            element={<Beranda entity={entity} bolehMargin={role !== undefined && canSeeOwnerMargin(role)} />}
          />
          <Route path="/produk" element={<ProdukPage entityId={entityId} />} />
          <Route path="/owner" element={<OwnerPage entityId={entityId} />} />
          <Route
            path="/pemasok"
            element={bolehMaster ? <PemasokPage entityId={entityId} /> : <TidakBoleh />}
          />
          <Route
            path="/pelanggan"
            element={bolehMaster ? <PelangganPage entityId={entityId} /> : <TidakBoleh />}
          />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  )
}

function Beranda({ entity, bolehMargin }: { entity: Entity | undefined; bolehMargin: boolean }) {
  return (
    <>
      <h2>Beranda</h2>
      <div className="card">
        <p>
          <strong>{entity?.name ?? 'Perusahaan'}</strong>
          {entity?.is_pkp ? ' — PKP, wajib memungut PPN.' : ' — non-PKP, tidak memungut PPN.'}
        </p>
        <p className="kosong">
          Pembelian, penjualan, dan laporan menyusul. Saat ini yang tersedia adalah data master.
        </p>
        {!bolehMargin && (
          <p className="kosong">
            Laporan margin per owner hanya dapat dilihat oleh pemilik dan manajer.
          </p>
        )}
      </div>
    </>
  )
}

function TidakBoleh() {
  return (
    <>
      <h2>Tidak diizinkan</h2>
      <div className="card">
        <p>Peran Anda di perusahaan ini tidak mencakup halaman tersebut.</p>
      </div>
    </>
  )
}
