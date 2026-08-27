import { api } from './client'
import type { IDR } from '../money'

/**
 * Laporan Margin per Owner.
 *
 * The highest-stakes screen in the product: family members settle money between
 * themselves monthly on these figures (R2.4). Two consequences shape this file.
 *
 * The whole tree arrives in one response — owner totals, the sales behind them,
 * the products in each sale, and the individual FIFO layers each drew. Drill-
 * down is a hard requirement, not a screen feature (SPEC §4.2), and nothing on
 * the page should be able to show a figure the rows behind it are one request
 * away from.
 *
 * Nothing here recomputes money. Every rupiah below was summed on the server
 * from the actual stock_consumption rows; the browser formats and never adds.
 * Two implementations of the same arithmetic is how a settlement ends up being
 * argued about twice.
 *
 * D-009: the endpoints refuse a cashier. The menu simply does not offer them.
 */

/** One slice of one FIFO layer — the bottom of the drill-down. */
export interface LayerDraw {
  consumption_id: string
  layer_id: string
  qty: number
  cost_idr: IDR
  /** True on a return putting the goods back on the layer they came from (D-010). */
  is_reversal: boolean
  acquired_at: number
  source: string
  layer_qty_in: number
  layer_cost_total_idr: IDR
  /**
   * INV-9. Without the faktur the PPN paid on this layer was never creditable
   * and became cost, so the same purchase price yields a layer ~11% dearer.
   * On this screen that is usually the entire answer to "why is mine lower".
   */
  faktur_received: boolean
}

export interface ProductLine {
  product_id: string
  product_code: string
  product_name: string
  qty: number
  /** Net of PPN on a sale; the revenue given back on a return. */
  revenue_idr: IDR
  /** The PPN collected or handed back alongside it. Never part of the margin. */
  ppn_idr: IDR
  cogs_idr: IDR
  margin_idr: IDR
  layers: LayerDraw[]
}

/** One sale as it appears under one owner — this owner's slice of it, not the
 *  invoice total. A cart holding two owners' products appears under both. */
export interface MarginSale {
  sale_id: string
  invoice_no: string
  business_date: string
  occurred_at: number
  customer_name: string
  revenue_idr: IDR
  ppn_idr: IDR
  tendered_idr: IDR
  cogs_idr: IDR
  margin_idr: IDR
  products: ProductLine[]
}

export interface MarginReturn {
  return_id: string
  sale_id: string
  sale_invoice_no: string
  /** When the goods came back. */
  business_date: string
  /** When they were sold. */
  sale_business_date: string
  /** Which of the two the rule in force counted (SPEC §4.4). */
  effective_date: string
  crosses_period: boolean
  reason: string
  /** The whole sum handed back, PPN included — what the customer received. */
  refund_idr: IDR
  /** The PPN inside it, which goes back to the state rather than the owner. */
  ppn_reversed_idr: IDR
  /** refund_idr less ppn_reversed_idr: what actually comes off the margin. */
  revenue_reversed_idr: IDR
  cogs_reversed_idr: IDR
  /** The margin the return took back out; positive means margin was removed. */
  margin_idr: IDR
  products: ProductLine[]
}

export interface OwnerMargin {
  /** Empty for the company bucket (R2.2) — the same null the database carries. */
  owner_id: string
  owner_name: string
  is_company: boolean

  /**
   * Net of PPN, because COGS is net of creditable PPN (SPEC §3.2) and the two
   * have to compare. Under inclusive pricing this is smaller than what the
   * customer handed over; tendered_idr is that figure, so the difference is
   * explicable on the screen rather than a mystery.
   */
  revenue_idr: IDR
  ppn_idr: IDR
  tendered_idr: IDR
  cogs_idr: IDR
  gross_margin_idr: IDR
  return_refund_idr: IDR
  return_ppn_idr: IDR
  return_revenue_idr: IDR
  return_cogs_idr: IDR
  return_margin_idr: IDR
  net_revenue_idr: IDR
  net_cogs_idr: IDR
  margin_idr: IDR

  sales: MarginSale[]
  returns: MarginReturn[]
  /**
   * Goods sold in this period that came back in another one and were counted
   * there. Context only — no figure above includes them.
   *
   * This is why booking a cross-boundary return to the month it came back is
   * safe (DECISIONS D-012): the settled month keeps its figures *and* still
   * says what came back afterwards.
   */
  later_returns: MarginReturn[]
}

export type ReturnPeriodRule = 'RETURN_DATE' | 'SALE_DATE'

export interface ReturnRule {
  rule: ReturnPeriodRule
  /** False while nobody has touched it and the company runs on the default. */
  chosen: boolean
  note: string
  updated_at: number
}

export interface MarginReport {
  /** Sent by the server so no client can relabel it. Never "Laba Rugi". */
  title: string
  note: string
  caveat: string
  period: { from: string; to: string }
  /** The rule the figures below were produced under (SPEC §4.4, D-012). */
  return_rule: ReturnPeriodRule
  owners: OwnerMargin[]
  totals: {
    revenue_idr: IDR
    ppn_idr: IDR
    tendered_idr: IDR
    cogs_idr: IDR
    gross_margin_idr: IDR
    return_refund_idr: IDR
    return_ppn_idr: IDR
    return_revenue_idr: IDR
    return_cogs_idr: IDR
    return_margin_idr: IDR
    net_revenue_idr: IDR
    net_cogs_idr: IDR
    margin_idr: IDR
  }
}

export const getMarginReport = (entityId: string, from: string, to: string): Promise<MarginReport> =>
  api<MarginReport>(`/reports/margin?from=${from}&to=${to}`, { entityId })

export const getReturnRule = (entityId: string): Promise<ReturnRule> =>
  api<ReturnRule>('/reports/margin/return-rule', { entityId })

/** Owner only: this changes what every past report says (SPEC §4.4, R7.3). */
export const setReturnRule = (
  entityId: string,
  rule: ReturnPeriodRule,
  note: string,
): Promise<ReturnRule> =>
  api<ReturnRule>('/reports/margin/return-rule', {
    method: 'PUT',
    entityId,
    body: { rule, note },
  })

/** The month containing a date, as the two ISO days the report takes. */
export function monthWindow(date: Date): { from: string; to: string } {
  const iso = (d: Date) =>
    `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  return {
    from: iso(new Date(date.getFullYear(), date.getMonth(), 1)),
    to: iso(new Date(date.getFullYear(), date.getMonth() + 1, 0)),
  }
}
