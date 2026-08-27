/**
 * The icon set.
 *
 * Inline SVG, drawn on one 24-unit grid at one stroke weight, so every glyph
 * lands on the same optical baseline and the same visual mass. Nothing is
 * fetched — INV-11 means the shop keeps trading with no internet, and an icon
 * font or a CDN sprite is one more thing that fails silently when the line
 * drops.
 *
 * This replaces a set of Unicode symbols borrowed from technical blocks: ⌗ for
 * the till, § for tax, ◑ for owner. Those resolve to a different font on every
 * machine, sit on different baselines, and were not learnable — ▤ and ▦ are
 * indistinguishable at 14px, and ⇤ / ⇥ differed only in direction while
 * labelling Pemasok and Pelanggan, the pair a new user most needs to tell
 * apart.
 *
 * Icons are decoration here; every one sits beside a text label, so they carry
 * aria-hidden and never become the accessible name of anything.
 */

import type { ReactElement } from 'react'

export type NamaIkon =
  | 'beranda'
  | 'kasir'
  | 'sesi-kas'
  | 'pembelian'
  | 'opname'
  | 'transfer'
  | 'margin'
  | 'laporan'
  | 'hutang'
  | 'piutang'
  | 'pajak'
  | 'produk'
  | 'owner'
  | 'pemasok'
  | 'pelanggan'
  | 'saldo-awal'
  | 'ekspor'
  | 'perusahaan'
  | 'menu'
  | 'tutup'
  | 'buka-rincian'
  | 'tutup-rincian'

const gambar: Record<NamaIkon, ReactElement> = {
  beranda: (
    <>
      <path d="M3 11 12 3.2 21 11" />
      <path d="M5.6 9.6V20.4h12.8V9.6" />
    </>
  ),
  kasir: (
    <>
      <path d="M6 2.8h12v18.4l-2-1.4-2 1.4-2-1.4-2 1.4-2-1.4z" />
      <path d="M9.4 7.6h5.2" />
      <path d="M9.4 11.6h5.2" />
    </>
  ),
  'sesi-kas': (
    <>
      <path d="M5 9.2 7.3 4.4a1 1 0 0 1 .9-.6h7.6a1 1 0 0 1 .9.6L19 9.2" />
      <rect x="2.8" y="9.2" width="18.4" height="11.2" rx="1.6" />
      <path d="M9.6 14.6h4.8" />
    </>
  ),
  pembelian: (
    <>
      <path d="M12 3.2v10.6" />
      <path d="M8 10 12 14l4-4" />
      <path d="M3.6 15.4v3.2a2 2 0 0 0 2 2h12.8a2 2 0 0 0 2-2v-3.2" />
    </>
  ),
  opname: (
    <>
      <rect x="4.6" y="4.2" width="14.8" height="16.4" rx="2" />
      <path d="M9.2 4.2V3a1 1 0 0 1 1-1h3.6a1 1 0 0 1 1 1v1.2z" />
      <path d="M8.8 13.2 11 15.4l4.4-4.4" />
    </>
  ),
  transfer: (
    <>
      <path d="M6.6 7.4h13.2" />
      <path d="M16.4 4 19.8 7.4l-3.4 3.4" />
      <path d="M17.4 16.6H4.2" />
      <path d="M7.6 13.2 4.2 16.6 7.6 20" />
    </>
  ),
  margin: (
    <>
      <circle cx="12" cy="12" r="8.8" />
      <path d="M12 3.2V12h8.8" />
    </>
  ),
  laporan: (
    <>
      <path d="M3.6 20.6h16.8" />
      <path d="M6.8 17.6v-5.2" />
      <path d="M12 17.6V7.2" />
      <path d="M17.2 17.6v-7.6" />
    </>
  ),
  hutang: (
    <>
      <path d="M6.8 17.2 17.2 6.8" />
      <path d="M8.6 6.8h8.6v8.6" />
    </>
  ),
  piutang: (
    <>
      <path d="M17.2 6.8 6.8 17.2" />
      <path d="M15.4 17.2H6.8V8.6" />
    </>
  ),
  pajak: (
    <>
      <path d="M6.2 3.4h7.4L18 7.8v12.8H6.2z" />
      <path d="M13.6 3.4v4.4H18" />
      <path d="M9.8 16.4 14 11.2" />
      <circle cx="10.1" cy="11.7" r="0.9" />
      <circle cx="13.7" cy="15.9" r="0.9" />
    </>
  ),
  produk: (
    <>
      <path d="M12 2.9 20.6 7v10L12 21.1 3.4 17V7z" />
      <path d="M3.4 7 12 11.6 20.6 7" />
      <path d="M12 11.6v9.5" />
    </>
  ),
  owner: (
    <>
      <circle cx="9.4" cy="8.2" r="3.3" />
      <path d="M3.4 20.4c0-3.4 2.7-5.7 6-5.7s6 2.3 6 5.7" />
      <path d="M16.4 5.4a3.3 3.3 0 0 1 0 5.9" />
      <path d="M17.9 15.1c1.7.8 2.7 2.4 2.7 4.3" />
    </>
  ),
  pemasok: (
    <>
      <path d="M2.6 7h10.8v9.4H2.6z" />
      <path d="M13.4 10.6h3.7l3 3.1v2.7h-6.7z" />
      <circle cx="7" cy="18.6" r="1.7" />
      <circle cx="16.4" cy="18.6" r="1.7" />
    </>
  ),
  pelanggan: (
    <>
      <circle cx="12" cy="8" r="3.6" />
      <path d="M4.6 20.6c0-4.1 3.3-6.6 7.4-6.6s7.4 2.5 7.4 6.6" />
    </>
  ),
  'saldo-awal': (
    <>
      <path d="M12 2.9 20.8 7.4 12 11.9 3.2 7.4z" />
      <path d="M3.2 12.1 12 16.6l8.8-4.5" />
      <path d="M3.2 16.6 12 21.1l8.8-4.5" />
    </>
  ),
  ekspor: (
    <>
      <path d="M12 3.4v10.8" />
      <path d="M7.6 10.4 12 14.8l4.4-4.4" />
      <path d="M4 17v2.6a1.6 1.6 0 0 0 1.6 1.6h12.8a1.6 1.6 0 0 0 1.6-1.6V17" />
    </>
  ),
  perusahaan: (
    <>
      <path d="M4.2 20.6V5.4a1 1 0 0 1 1-1h8.2a1 1 0 0 1 1 1v15.2" />
      <path d="M14.4 10.6h4.4a1 1 0 0 1 1 1v9" />
      <path d="M2.6 20.6h18.8" />
      <path d="M7.4 8.4h1.4M11 8.4h1.4M7.4 12.4h1.4M11 12.4h1.4M7.4 16.4h1.4M11 16.4h1.4" />
    </>
  ),
  menu: (
    <>
      <path d="M3.6 6.6h16.8" />
      <path d="M3.6 12h16.8" />
      <path d="M3.6 17.4h16.8" />
    </>
  ),
  tutup: (
    <>
      <path d="M6 6l12 12" />
      <path d="M18 6 6 18" />
    </>
  ),
  'buka-rincian': <path d="M9 5.4 15.6 12 9 18.6" />,
  'tutup-rincian': <path d="M5.4 9 12 15.6 18.6 9" />,
}

export function Ikon({
  nama,
  className = 'nav-ikon',
  ukuran,
}: {
  nama: NamaIkon
  className?: string
  ukuran?: number
}) {
  return (
    <svg
      className={className}
      viewBox="0 0 24 24"
      width={ukuran}
      height={ukuran}
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {gambar[nama]}
    </svg>
  )
}
