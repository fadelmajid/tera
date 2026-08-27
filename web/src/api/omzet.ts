/**
 * The omzet clock: turnover against the Rp 4,8 miliar PKP threshold.
 * TASKS 7.7-7.12, SPEC 5.
 *
 * # Two figures, and they are not interchangeable
 *
 * The threshold is measured per book year, cumulative, reset annually
 * (PMK 197/2013). Most guidance online says rolling twelve months and is wrong.
 *
 * So they arrive as two differently named objects, each carrying its own label
 * from the server, and the trailing one carries `is_estimate: true`. Nothing in
 * this file computes either of them: they are money, so they are computed on the
 * server (INV-1).
 */
import type { IDR } from '../money'
import { api } from './client'

export type OmzetState = 'OK' | 'WATCH' | 'WARN' | 'CROSSED'

export interface OmzetWindow {
  from: string
  to: string
}

export interface OmzetPosition {
  book_year: number
  as_of: string
  /** How far into the book year the figures run — the day it was read on. */
  counted_to: string
  window: OmzetWindow

  threshold: {
    amount_idr: IDR
    watch_bp: number
    warn_bp: number
    /** The regulation, so a konsultan pajak can check the figure. */
    legal_ref: string
  }

  /** The legally binding figure. Drives the alarm. */
  cumulative: {
    label: string
    amount_idr: IDR
    percent_bp: number
    remaining_idr: IDR
    entries: number
  }

  /** A pace estimate. Never the legal number, never what the alarm reads. */
  trailing_12m: {
    label: string
    amount_idr: IDR
    window: OmzetWindow
    is_estimate: true
  }

  state: OmzetState
  /** Both companies are tracked; only a non-PKP one can still cross. */
  is_pkp: boolean
  base: 'NET_OF_VAT' | 'GROSS'

  /** Present only once crossed. */
  crossed?: {
    on: string
    /** Registration is due by this date. */
    register_by: string
    /** PPN has to be charged from this one. The gap is the point. */
    vat_starts: string
    /** What the year peaked at, which explains a crossing that outlives it. */
    peak_idr: IDR
    peak_on: string
  }

  book_years: number[]
  caveat: string
}

export interface OmzetLedgerEntry {
  id: string
  book_year: number
  /** Not always the day the row was written: a void carries the sale's date. */
  effective_date: string
  event_type: 'SALE' | 'VOID' | 'REFUND' | 'RETURN' | 'ADJUSTMENT'
  amount_idr: IDR
  source_txn_id: string | null
  invoice_no: string | null
  note: string | null
  created_at: number
}

export interface OmzetThreshold {
  id: string
  amount_idr: IDR
  watch_bp: number
  warn_bp: number
  register_by_policy: 'END_OF_BOOK_YEAR' | 'END_OF_FOLLOWING_MONTH'
  vat_starts_policy: 'NEXT_BOOK_YEAR_FIRST_PERIOD' | 'MONTH_AFTER_REGISTRATION'
  valid_from: string
  valid_to: string | null
  in_force: boolean
  staged: boolean
  legal_ref: string
  note: string | null
  created_at: number
  closed_at: number | null
}

export const omzetPosition = (
  entityId: string,
  bookYear?: number,
  asOf?: string,
): Promise<OmzetPosition> => {
  const q = new URLSearchParams()
  if (bookYear !== undefined) q.set('book_year', String(bookYear))
  if (asOf) q.set('as_of', asOf)
  const query = q.toString()
  return api<OmzetPosition>(`/omzet${query ? `?${query}` : ''}`, { entityId })
}

export const omzetLedger = (
  entityId: string,
  bookYear: number,
): Promise<{ book_year: number; entries: OmzetLedgerEntry[] }> =>
  api(`/omzet/ledger?book_year=${bookYear}`, { entityId })

export const omzetThresholds = (
  entityId: string,
): Promise<{ today: string; thresholds: OmzetThreshold[]; caveat: string }> =>
  api('/omzet/thresholds', { entityId })

export const setOmzetBase = (
  entityId: string,
  base: 'NET_OF_VAT' | 'GROSS',
  note: string,
): Promise<{ base: string }> =>
  api('/omzet/base', { method: 'PUT', body: { base, note }, entityId })

/** How each state reads on screen, and how alarming it should look. */
export const stateLabel: Record<OmzetState, string> = {
  OK: 'Aman',
  WATCH: 'Perlu dipantau',
  WARN: 'Mendekati batas',
  CROSSED: 'Sudah melewati batas',
}

/**
 * Renders a basis-point ratio for display only.
 *
 * The number is never used in arithmetic and never sent anywhere: every figure
 * on this screen is computed on the server (INV-1).
 */
export function formatPercent(basisPoints: number): string {
  const whole = Math.trunc(basisPoints / 100)
  const fraction = Math.abs(basisPoints % 100)
  return fraction === 0 ? `${whole}%` : `${whole},${String(fraction).padStart(2, '0')}%`
}
