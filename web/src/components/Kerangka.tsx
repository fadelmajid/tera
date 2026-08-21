import { NavLink, Route, Routes, Navigate } from 'react-router-dom'
import type { Session } from '../api/auth'
import { canManageMasterData, canSeeOwnerMargin, canEnterPurchases } from '../api/auth'
import type { Entity } from '../api/masterdata'
import { ProdukPage } from '../pages/Produk'
import { OwnerPage } from '../pages/Owner'
import { PemasokPage } from '../pages/Pemasok'
import { PelangganPage } from '../pages/Pelanggan'
import { PembelianPage } from '../pages/Pembelian'
import { OpnamePage } from '../pages/Opname'
import { SaldoAwalPage } from '../pages/SaldoAwal'

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
  // R10.4: purchases and stock adjustments are entered by admin or manager,
  // never cashier staff. Hiding the link is a courtesy; the server refuses the
  // request regardless.
  const bolehBeli = role !== undefined && canEnterPurchases(role)

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
          {bolehBeli && (
            <>
              <NavLink to="/pembelian">Pembelian</NavLink>
              <NavLink to="/opname">Opname stok</NavLink>
              <NavLink to="/saldo-awal">Saldo awal</NavLink>
            </>
          )}
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
          <Route
            path="/pembelian"
            element={
              bolehBeli ? (
                <PembelianPage entityId={entityId} isPKP={entity?.is_pkp ?? false} />
              ) : (
                <TidakBoleh />
              )
            }
          />
          <Route path="/opname" element={bolehBeli ? <OpnamePage entityId={entityId} /> : <TidakBoleh />} />
          <Route
            path="/saldo-awal"
            element={bolehBeli ? <SaldoAwalPage entityId={entityId} /> : <TidakBoleh />}
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
          Penjualan dan laporan menyusul. Yang tersedia saat ini: data master, pembelian, opname
          stok, dan saldo awal.
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
