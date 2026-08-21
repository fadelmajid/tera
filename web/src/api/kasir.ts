import { api } from './client'
import type { IDR } from '../money'

/** The till: sales, cash sessions, returns, and the receipt. */

export interface Sale {
  id: string
  invoice_no: string
  business_date: string
  occurred_at: number
  status: 'FINAL' | 'VOID'
  customer_id: string | null
  /**
   * Whether a faktur was issued to the buyer. Independent of whether PPN is
   * owed — a PKP owes output PPN either way (SPEC §2.3), and letting these two
   * collapse is the commonest way a newly-PKP business loses margin quietly.
   */
  faktur_issued: boolean
  faktur_no: string | null
  gross_idr: IDR
  discount_idr: IDR
  ppn_idr: IDR
  total_idr: IDR
  cogs_idr: IDR
  is_credit: boolean
  due_date: string | null
  void_reason: string | null
}

export interface SaleLine {
  id: string
  product_id: string
  product_code: string
  product_name: string
  product_unit: string
  owner_id: string | null
  owner_name: string | null
  qty: number
  unit_price_idr: IDR
  gross_idr: IDR
  line_discount_idr: IDR
  alloc_discount_idr: IDR
  net_idr: IDR
  cogs_idr: IDR
}

export interface SaleResult {
  sale: Sale
  lines: SaleLine[]
  cogs_idr: IDR
  margin_idr: IDR
  receivable: { id: string; amount_idr: IDR; due_date: string | null } | null
  /** The sale is committed either way; printing is attempted afterwards. */
  printed: boolean
  print_error: string
}

export interface SaleBody {
  customer_id?: string
  sale_date?: string
  invoice_discount_idr?: IDR
  faktur_issued?: boolean
  faktur_no?: string
  is_credit?: boolean
  due_date?: string
  note?: string
  lines: { product_id: string; qty: number; unit_price_idr?: IDR; line_discount_idr?: IDR }[]
  payments?: { method: string; amount_idr: IDR; reference?: string }[]
}

export const ringSale = (entityId: string, body: SaleBody): Promise<SaleResult> =>
  api<SaleResult>('/sales', { method: 'POST', body, entityId })

export const listSales = (entityId: string): Promise<Sale[]> => api<Sale[]>('/sales', { entityId })

export const getSale = (
  entityId: string,
  id: string,
): Promise<{ sale: Sale; lines: SaleLine[]; payments: { method: string; amount_idr: IDR }[] }> =>
  api(`/sales/${id}`, { entityId })

/** SPEC §4.2: an owner's figure must expand to the individual layers behind it. */
export interface LayerDraw {
  layer_id: string
  product_id: string
  owner_id: string | null
  qty_out: number
  cost_idr: IDR
  layer_acquired_at: number
  layer_cost_total_idr: IDR
  layer_qty_in: number
  layer_faktur_received: boolean
  reverses_id: string | null
}

export const saleLayers = (entityId: string, id: string): Promise<LayerDraw[]> =>
  api<LayerDraw[]>(`/sales/${id}/layers`, { entityId })

export const voidSale = (entityId: string, id: string, reason: string): Promise<Sale> =>
  api<Sale>(`/sales/${id}/void`, { method: 'POST', body: { reason }, entityId })

export const returnSale = (
  entityId: string,
  id: string,
  body: {
    return_date?: string
    reason: string
    refund_method?: string
    lines: { sale_line_id: string; qty: number }[]
  },
): Promise<{ refund_idr: IDR; cogs_reversed_idr: IDR }> =>
  api(`/sales/${id}/returns`, { method: 'POST', body, entityId })

// --- cash session (R9.8) ----------------------------------------------------

export interface CashSession {
  id: string
  status: 'OPEN' | 'CLOSED'
  business_date: string
  opening_float_idr: IDR
  counted_cash_idr: IDR | null
  expected_cash_idr: IDR | null
  variance_idr: IDR | null
}

export interface ZReport {
  session: CashSession
  by_method: Record<string, IDR>
  cash_refunds: IDR
  expected_cash: IDR
  total_takings: IDR
  payment_count: number
}

export const currentSession = (entityId: string): Promise<CashSession | null> =>
  api<CashSession | null>('/cash-sessions/current', { entityId })

export const listSessions = (entityId: string): Promise<CashSession[]> =>
  api<CashSession[]>('/cash-sessions', { entityId })

export const openSession = (entityId: string, opening_float_idr: IDR): Promise<CashSession> =>
  api<CashSession>('/cash-sessions', { method: 'POST', body: { opening_float_idr }, entityId })

export const sessionTotals = (entityId: string, id: string): Promise<ZReport> =>
  api<ZReport>(`/cash-sessions/${id}/totals`, { entityId })

export const closeSession = (
  entityId: string,
  id: string,
  counted_cash_idr: IDR,
  note: string,
): Promise<ZReport> =>
  api<ZReport>(`/cash-sessions/${id}/close`, {
    method: 'POST',
    body: { counted_cash_idr, note },
    entityId,
  })

// --- receipt ----------------------------------------------------------------

export const receiptPreview = (
  entityId: string,
  id: string,
): Promise<{ text: string; printer_configured: boolean }> =>
  api(`/sales/${id}/receipt`, { entityId })

export const printReceipt = (entityId: string, id: string, drawer = true): Promise<{ printed: boolean }> =>
  api(`/sales/${id}/print?drawer=${drawer ? 1 : 0}`, { method: 'POST', entityId })

export const openDrawer = (entityId: string): Promise<{ opened: boolean }> =>
  api('/drawer/open', { method: 'POST', entityId })

/** R9.10: recorded, not processed. The gateway is out of scope. */
export const METODE_BAYAR = [
  ['TUNAI', 'Tunai'],
  ['TRANSFER', 'Transfer'],
  ['QRIS', 'QRIS'],
  ['KARTU', 'Kartu'],
] as const
