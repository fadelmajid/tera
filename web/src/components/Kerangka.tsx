import { useEffect, useState } from 'react'
import { NavLink, Route, Routes, Navigate, useLocation } from 'react-router-dom'
import type { Session } from '../api/auth'
import {
  canManageMasterData,
  canManageUsers,
  canSeeOwnerMargin,
  canEnterPurchases,
  canSeeTaxPosition,
  canSeeReports,
  canExportEverything,
} from '../api/auth'
import type { Entity } from '../api/masterdata'
import { ProdukPage } from '../pages/Produk'
import { OwnerPage } from '../pages/Owner'
import { PemasokPage } from '../pages/Pemasok'
import { PelangganPage } from '../pages/Pelanggan'
import { PerusahaanPage } from '../pages/Perusahaan'
import { PembelianPage } from '../pages/Pembelian'
import { OpnamePage } from '../pages/Opname'
import { SaldoAwalPage } from '../pages/SaldoAwal'
import { KasirPage } from '../pages/Kasir'
import { SesiKasPage } from '../pages/SesiKas'
import { MarginPage } from '../pages/Margin'
import { TransferPage } from '../pages/Transfer'
import { PajakPage } from '../pages/Pajak'
import { LaporanPage } from '../pages/Laporan'
import { UtangPage } from '../pages/Utang'
import { EksporPage } from '../pages/Ekspor'
import { DasborPage } from '../pages/Dasbor'
import { Tema } from './Tema'
import { Ikon, type NamaIkon } from './ikon'

/**
 * The application shell.
 *
 * Navigation is built from the role held in the *selected* company. A user may
 * be an owner in one and staff in the other (R13.4), so the menu changes when
 * the company does — it is not a property of the account.
 *
 * Hiding a menu item is not a security control. The server refuses the request
 * regardless; this only avoids offering someone a door that is locked.
 *
 * # The order is the order the app is used in
 *
 * Master data sits above stock, and Owner above Produk inside it, because a
 * product cannot be attributed to an owner who does not exist yet and owner
 * attribution is the single field every margin figure rests on (INV-8). Saldo
 * awal opens the stock group rather than hiding in settings: it is the first
 * stock event on a new installation and has to happen before anything else can
 * be sold.
 *
 * An earlier ordering ranked the groups by importance to the business, which
 * put the till second and the owners a person must create first at position
 * thirteen.
 */
export function Kerangka({
  session,
  entities,
  entityId,
  pilihEntity,
  muatUlang,
  onKeluar,
}: {
  session: Session
  entities: Entity[]
  entityId: string
  pilihEntity: (id: string) => void
  /** Re-reads the session and its companies, after one is added. */
  muatUlang: () => void
  onKeluar: () => void
}) {
  const role = session.roles[entityId]
  const entity = entities.find((e) => e.id === entityId)
  const bolehMaster = role !== undefined && canManageMasterData(role)
  const bolehBeli = role !== undefined && canEnterPurchases(role)
  const bolehMargin = role !== undefined && canSeeOwnerMargin(role)
  const bolehPajak = role !== undefined && canSeeTaxPosition(role)
  const bolehLaporan = role !== undefined && canSeeReports(role)
  const bolehEkspor = role !== undefined && canExportEverything(role)
  const bolehPerusahaan = role !== undefined && canManageUsers(role)

  const [menuBuka, setMenuBuka] = useState(false)
  const lokasi = useLocation()

  // A tap on a link closes the drawer; otherwise the new page opens behind a
  // full-height menu on a phone.
  useEffect(() => setMenuBuka(false), [lokasi.pathname])

  const menu: Grup[] = [
    {
      label: 'Toko',
      item: [
        { ke: '/', teks: 'Beranda', ikon: 'beranda', tepat: true },
        { ke: '/kasir', teks: 'Kasir', ikon: 'kasir' },
        { ke: '/sesi-kas', teks: 'Sesi kas', ikon: 'sesi-kas' },
      ],
    },
    {
      label: 'Data master',
      item: [
        { ke: '/owner', teks: 'Owner', ikon: 'owner' },
        { ke: '/produk', teks: 'Produk', ikon: 'produk' },
        { ke: '/pemasok', teks: 'Pemasok', ikon: 'pemasok', tampil: bolehMaster },
        { ke: '/pelanggan', teks: 'Pelanggan', ikon: 'pelanggan', tampil: bolehMaster },
        { ke: '/perusahaan', teks: 'Perusahaan', ikon: 'perusahaan', tampil: bolehPerusahaan },
      ],
    },
    {
      label: 'Barang & stok',
      item: [
        { ke: '/saldo-awal', teks: 'Saldo awal', ikon: 'saldo-awal', tampil: bolehBeli },
        { ke: '/pembelian', teks: 'Pembelian', ikon: 'pembelian', tampil: bolehBeli },
        { ke: '/opname', teks: 'Opname stok', ikon: 'opname', tampil: bolehBeli },
        {
          ke: '/transfer',
          teks: 'Transfer antar PT',
          ikon: 'transfer',
          tampil: bolehBeli && entities.length > 1,
        },
      ],
    },
    {
      label: 'Laporan',
      item: [
        { ke: '/margin', teks: 'Margin per owner', ikon: 'margin', tampil: bolehMargin },
        { ke: '/laporan', teks: 'Penjualan & stok', ikon: 'laporan', tampil: bolehLaporan },
        { ke: '/hutang', teks: 'Hutang', ikon: 'hutang', tampil: bolehLaporan },
        { ke: '/piutang', teks: 'Piutang', ikon: 'piutang', tampil: bolehLaporan },
        { ke: '/pajak', teks: 'Pajak & PPN', ikon: 'pajak', tampil: bolehPajak },
      ],
    },
    {
      label: 'Pengaturan',
      item: [{ ke: '/ekspor', teks: 'Ekspor data', ikon: 'ekspor', tampil: bolehEkspor }],
    },
  ]

  return (
    <div className="app">
      <aside className={menuBuka ? 'sidebar terbuka' : 'sidebar'}>
        <div className="sidebar-atas">
          <div className="merek">
            <span className="merek-tanda" aria-hidden="true">
              T
            </span>
            <h1>Tera</h1>
          </div>
          <button
            type="button"
            className="sekunder menu-tombol"
            onClick={() => setMenuBuka((b) => !b)}
            aria-expanded={menuBuka}
            aria-label={menuBuka ? 'Tutup menu' : 'Buka menu'}
          >
            <Ikon nama={menuBuka ? 'tutup' : 'menu'} className="" ukuran={18} />
            Menu
          </button>
        </div>

        <p className="entity">
          {session.user.full_name} · {role ?? 'tanpa peran'}
        </p>

        {entities.length > 1 && (
          <div className="pilih-perusahaan">
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
          </div>
        )}

        <nav aria-label="Navigasi utama">
          {menu.map((grup) => (
            <Seksi key={grup.label} grup={grup} />
          ))}
        </nav>

        <div className="sidebar-kaki">
          <Tema />
          <button className="sekunder" onClick={onKeluar}>
            Keluar
          </button>
        </div>
      </aside>

      <main className="main">
        <div className="main-isi">
          <Routes>
            <Route
              path="/"
              element={
                <DasborPage
                  entity={entity}
                  entityId={entityId}
                  namaPengguna={session.user.full_name}
                  bolehMargin={bolehMargin}
                  bolehPajak={bolehPajak}
                  bolehLaporan={bolehLaporan}
                  bolehBeli={bolehBeli}
                />
              }
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
              path="/perusahaan"
              element={
                bolehPerusahaan ? (
                  <PerusahaanPage
                    entityId={entityId}
                    entities={entities}
                    muatUlang={muatUlang}
                  />
                ) : (
                  <TidakBoleh />
                )
              }
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
            <Route
              path="/opname"
              element={bolehBeli ? <OpnamePage entityId={entityId} /> : <TidakBoleh />}
            />
            <Route
              path="/transfer"
              element={
                bolehBeli ? (
                  <TransferPage entityId={entityId} entities={entities} />
                ) : (
                  <TidakBoleh />
                )
              }
            />
            <Route
              path="/saldo-awal"
              element={bolehBeli ? <SaldoAwalPage entityId={entityId} /> : <TidakBoleh />}
            />
            <Route
              path="/kasir"
              element={<KasirPage entityId={entityId} isPKP={entity?.is_pkp ?? false} />}
            />
            <Route path="/sesi-kas" element={<SesiKasPage entityId={entityId} />} />
            <Route
              path="/margin"
              element={
                bolehMargin && role !== undefined ? (
                  <MarginPage entityId={entityId} role={role} />
                ) : (
                  <TidakBoleh />
                )
              }
            />
            <Route
              path="/pajak"
              element={
                bolehPajak && role !== undefined ? (
                  <PajakPage entityId={entityId} role={role} isPKP={entity?.is_pkp ?? false} />
                ) : (
                  <TidakBoleh />
                )
              }
            />
            <Route
              path="/laporan"
              element={bolehLaporan ? <LaporanPage entityId={entityId} /> : <TidakBoleh />}
            />
            <Route
              path="/hutang"
              element={
                bolehLaporan ? <UtangPage entityId={entityId} kind="hutang" /> : <TidakBoleh />
              }
            />
            <Route
              path="/piutang"
              element={
                bolehLaporan ? <UtangPage entityId={entityId} kind="piutang" /> : <TidakBoleh />
              }
            />
            <Route
              path="/ekspor"
              element={bolehEkspor ? <EksporPage entityId={entityId} /> : <TidakBoleh />}
            />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </div>
      </main>
    </div>
  )
}

/** One link in the sidebar. */
interface Tautan {
  ke: string
  teks: string
  ikon: NamaIkon
  /** Exact matching, for the dashboard — otherwise "/" matches everything. */
  tepat?: boolean
  /** Hidden when the role does not reach it. Defaults to shown. */
  tampil?: boolean
}

/** A group of links under one heading. */
interface Grup {
  label: string
  item: Tautan[]
}

/**
 * One heading and its links.
 *
 * Renders nothing at all when the role reaches none of them. Hiding a menu
 * item is a courtesy and never a security control — the server refuses the
 * request regardless — but an empty heading is just clutter that says a person
 * is missing something.
 */
function Seksi({ grup }: { grup: Grup }) {
  const terlihat = grup.item.filter((i) => i.tampil !== false)
  if (terlihat.length === 0) return null

  return (
    <div className="nav-grup">
      <div className="nav-label">{grup.label}</div>
      {terlihat.map((i) => (
        <NavLink key={i.ke} to={i.ke} end={i.tepat === true}>
          <Ikon nama={i.ikon} />
          {i.teks}
        </NavLink>
      ))}
    </div>
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
