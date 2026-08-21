import { api } from './client'
import type { IDR } from '../money'

/**
 * Purchasing, stock opname, and opening balances.
 *
 * Every route here is gated on the manager role (R10.4). These screens set the
 * faktur status that decides a stock layer's cost basis and post stock
 * adjustments — margin-bearing, not data entry.
 */

export interface Purchase {
  id: string
  supplier_id: string
  invoice_no: string | null
  business_date: string
  /** INV-9. The single field that decides what every layer on this invoice cost. */
  faktur_received: boolean
  faktur_no: string | null
  subtotal_idr: IDR
  ppn_idr: IDR
  total_idr: IDR
  is_credit: boolean
  due_date: string | null
  note: string | null
}

export interface PurchaseLine {
  id: string
  product_id: string
  product_code: string
  product_name: string
  product_unit: string
  owner_id: string | null
  qty: number
  unit_price_idr: IDR
  subtotal_idr: IDR
  ppn_idr: IDR
  gross_idr: IDR
  /** What the stock layer carries. Net of creditable PPN — see SPEC §3.2. */
  cost_total_idr: IDR
  /** What goes to the PPN position instead. Zero unless PKP with a faktur. */
  creditable_ppn_idr: IDR
  stock_layer_id: string
}

export interface PurchaseLineBody {
  product_id: string
  qty: number
  unit_price_idr: IDR
  ppn_idr: IDR
  expiry_date?: string
}

export interface PurchaseBody {
  supplier_id: string
  invoice_no?: string
  purchase_date?: string
  faktur_received: boolean
  faktur_no?: string
  is_credit?: boolean
  due_date?: string
  note?: string
  lines: PurchaseLineBody[]
}

export interface PurchaseResult {
  purchase: Purchase
  lines: PurchaseLine[]
  layers: { id: string; cost_total_idr: IDR; faktur_received: boolean; ppn_paid_idr: IDR }[]
  payable: { id: string; amount_idr: IDR; due_date: string | null } | null
}

export const listPurchases = (entityId: string): Promise<Purchase[]> =>
  api<Purchase[]>('/purchases', { entityId })

export const getPurchase = (
  entityId: string,
  id: string,
): Promise<{ purchase: Purchase; lines: PurchaseLine[] }> =>
  api(`/purchases/${id}`, { entityId })

export const createPurchase = (entityId: string, body: PurchaseBody): Promise<PurchaseResult> =>
  api<PurchaseResult>('/purchases', { method: 'POST', body, entityId })

export const returnPurchase = (
  entityId: string,
  id: string,
  body: { return_date?: string; reason: string; lines: { purchase_line_id: string; qty: number }[] },
): Promise<{ cost_idr: IDR; ppn_reversed_idr: IDR; compliance_notice: string }> =>
  api(`/purchases/${id}/returns`, { method: 'POST', body, entityId })

export interface PPNPosition {
  creditable_idr: IDR
  reversed_idr: IDR
  net_idr: IDR
  compliance_notice: string
}

export const inputPPN = (entityId: string, from: string, to: string): Promise<PPNPosition> =>
  api<PPNPosition>(`/ppn/input?from=${from}&to=${to}`, { entityId })

// --- stock opname -----------------------------------------------------------

export interface OnHand {
  product_id: string
  product_code: string
  product_name: string
  product_unit: string
  /** null is the company bucket — counted as its own line (R2.2). */
  owner_id: string | null
  owner_name: string | null
  qty_on_hand: number
}

export interface Opname {
  id: string
  status: 'DRAFT' | 'POSTED'
  business_date: string
  note: string | null
  posted_at: number | null
}

export interface OpnameLine {
  id: string
  product_id: string
  product_code: string
  product_name: string
  product_unit: string
  owner_id: string | null
  owner_name: string | null
  system_qty: number
  counted_qty: number
  variance: number
  reason_code: string | null
  reason_note: string | null
  unit_cost_idr: IDR | null
}

/** R12.5: every adjustment carries one of these. */
export const KODE_ALASAN = [
  ['RUSAK', 'Rusak'],
  ['HILANG', 'Hilang'],
  ['KADALUARSA', 'Kadaluarsa'],
  ['SALAH_CATAT', 'Salah catat'],
  ['RETUR_TIDAK_TERCATAT', 'Retur tidak tercatat'],
  ['LAINNYA', 'Lainnya'],
] as const

export const countSheet = (entityId: string): Promise<OnHand[]> =>
  api<OnHand[]>('/opname/count-sheet', { entityId })

export const listOpnames = (entityId: string): Promise<Opname[]> =>
  api<Opname[]>('/opname', { entityId })

export const startOpname = (entityId: string, body: { count_date?: string; note?: string }): Promise<Opname> =>
  api<Opname>('/opname', { method: 'POST', body, entityId })

export const getOpname = (
  entityId: string,
  id: string,
): Promise<{ opname: Opname; lines: OpnameLine[] }> => api(`/opname/${id}`, { entityId })

export const saveOpnameLine = (
  entityId: string,
  id: string,
  body: {
    product_id: string
    owner_id: string
    counted_qty: number
    reason_code?: string
    reason_note?: string
    unit_cost_idr?: IDR
  },
): Promise<{ system_qty: number; counted_qty: number; variance: number; unit_cost_idr: IDR | null }> =>
  api(`/opname/${id}/lines`, { method: 'POST', body, entityId })

export const postOpname = (
  entityId: string,
  id: string,
  reason: string,
): Promise<{ opname: Opname; lines_posted: number; surplus_units: number; short_units: number; cost_delta: IDR }> =>
  api(`/opname/${id}/post`, { method: 'POST', body: { reason }, entityId })

// --- opening balances (R11.5) -----------------------------------------------

export interface Debt {
  id: string
  party_id: string
  source: 'PURCHASE' | 'SALE' | 'OPENING'
  invoice_no: string | null
  amount_idr: IDR
  paid_idr: IDR
  outstanding_idr: IDR
  incurred_on: string
  due_date: string | null
  note: string | null
}

export const carryInStock = (
  entityId: string,
  body: {
    as_of_date?: string
    note?: string
    lines: {
      product_id: string
      owner_id: string
      qty: number
      cost_total_idr: IDR
      faktur_received?: boolean
      ppn_paid_idr?: IDR
      expiry_date?: string
    }[]
  },
): Promise<{ total_qty: number; total_cost_idr: IDR }> =>
  api('/opening/stock', { method: 'POST', body, entityId })

export interface DebtBody {
  party_id: string
  invoice_no?: string
  amount_idr: IDR
  incurred_on?: string
  due_date?: string
  note?: string
}

export const carryInPayable = (entityId: string, body: DebtBody): Promise<{ id: string }> =>
  api('/opening/payables', { method: 'POST', body, entityId })

export const carryInReceivable = (entityId: string, body: DebtBody): Promise<{ id: string }> =>
  api('/opening/receivables', { method: 'POST', body, entityId })

export const listPayables = (entityId: string): Promise<Debt[]> =>
  api<Debt[]>('/payables', { entityId })

export const listReceivables = (entityId: string): Promise<Debt[]> =>
  api<Debt[]>('/receivables', { entityId })

export const payPayable = (
  entityId: string,
  id: string,
  body: { amount_idr: IDR; paid_on?: string; method?: string; note?: string },
): Promise<{ id: string }> => api(`/payables/${id}/payments`, { method: 'POST', body, entityId })

export const payReceivable = (
  entityId: string,
  id: string,
  body: { amount_idr: IDR; paid_on?: string; method?: string; note?: string },
): Promise<{ id: string }> => api(`/receivables/${id}/payments`, { method: 'POST', body, entityId })
