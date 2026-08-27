/**
 * Reports: sales, purchases, stock, hutang, piutang, and the export.
 * TASKS 6.1-6.6.
 *
 * Every rupiah figure is computed on the server. Nothing here adds money up
 * (INV-1); the totals that arrive are the ones the server stands behind, and a
 * screen that re-summed them would be a second implementation waiting to
 * disagree with the first.
 */
import type { IDR } from '../money'
import { api } from './client'

export interface Periode {
  from: string
  to: string
}

// --- penjualan (TASKS 6.1) --------------------------------------------------

export interface SalesSummary {
  sale_count: number
  gross_idr: IDR
  discount_idr: IDR
  /** Revenue net of PPN. The tax is owed to the state and was never margin. */
  dpp_idr: IDR
  ppn_idr: IDR
  total_idr: IDR
  cogs_idr: IDR
  gross_margin_idr: IDR
  net_margin_idr: IDR
  credit_idr: IDR
  with_faktur_idr: IDR
  /** Counted, not hidden: a week of voids is a training problem. */
  void_count: number
  void_total_idr: IDR
  return_count: number
  refund_idr: IDR
  refund_ppn_idr: IDR
  return_cogs_idr: IDR
}

export interface SalesDay {
  business_date: string
  sale_count: number
  dpp_idr: IDR
  ppn_idr: IDR
  total_idr: IDR
  cogs_idr: IDR
  margin_idr: IDR
}

export interface SalesProduct {
  product_id: string
  product_code: string
  product_name: string
  product_unit: string
  owner_id: string | null
  owner_name: string | null
  qty: number
  revenue_idr: IDR
  ppn_idr: IDR
  cogs_idr: IDR
  margin_idr: IDR
}

export interface SalesMethod {
  method: string
  payment_count: number
  amount_idr: IDR
}

export interface SalesReport {
  title: string
  caveat: string
  period: Periode
  summary: SalesSummary
  by_day: SalesDay[]
  by_product: SalesProduct[]
  by_method: SalesMethod[]
}

// --- pembelian (TASKS 6.2) --------------------------------------------------

export interface PurchasesReport {
  title: string
  period: Periode
  summary: {
    purchase_count: number
    subtotal_idr: IDR
    ppn_idr: IDR
    total_idr: IDR
    with_faktur_idr: IDR
    without_faktur_idr: IDR
    /** PPN that will never be credited and is sitting in the cost of the goods. */
    ppn_into_cost_idr: IDR
    return_count: number
    return_cost_idr: IDR
    return_ppn_idr: IDR
  }
  by_supplier: {
    supplier_id: string
    supplier_code: string
    supplier_name: string
    issues_faktur: boolean
    purchase_count: number
    total_idr: IDR
    ppn_idr: IDR
    with_faktur_count: number
    ppn_into_cost_idr: IDR
  }[]
  by_product: {
    product_id: string
    product_code: string
    product_name: string
    product_unit: string
    qty: number
    gross_idr: IDR
    cost_total_idr: IDR
    creditable_ppn_idr: IDR
  }[]
}

// --- stok (TASKS 6.3) -------------------------------------------------------

export interface StockReport {
  title: string
  period: Periode
  on_hand_as_of: string
  summary: { lines: number; qty_total: number; value_idr: IDR }
  on_hand: {
    product_id: string
    product_code: string
    product_name: string
    product_unit: string
    category: string | null
    owner_id: string | null
    owner_name: string | null
    qty_on_hand: number
    value_idr: IDR
    layer_count: number
    layers_without_faktur: number
    earliest_expiry: string | null
  }[]
  out_of_stock: {
    product_id: string
    product_code: string
    product_name: string
    product_unit: string
  }[]
  movement: {
    product_code: string
    product_name: string
    movement_type: string
    qty: number
    cost_idr: IDR
  }[]
  intake: {
    product_code: string
    product_name: string
    source: string
    qty: number
    cost_idr: IDR
  }[]
}

// --- hutang dan piutang (TASKS 6.4-6.5) -------------------------------------

export type Bucket = 'NOT_YET_DUE' | '1_30' | '31_60' | '61_90' | 'OVER_90' | 'NO_DUE_DATE'

/** Buckets plus the two totals the server derives from them. */
export type Buckets = Record<Bucket, IDR> & {
  total_idr: IDR
  /**
   * Deliberately excludes what could not be aged. Those invoices may well be
   * late; nobody knows, because nobody recorded a term.
   */
  overdue_idr: IDR
}

export interface AgedItem {
  id: string
  document_no: string
  source: string
  incurred_on: string
  /** Empty when no term was recorded. A real state, not a missing value. */
  due_date: string
  amount_idr: IDR
  paid_idr: IDR
  outstanding_idr: IDR
  bucket: Bucket
  /** Zero when not yet due AND when undated — read it with the bucket. */
  days_overdue: number
}

export interface AgedCounterparty {
  id: string
  name: string
  buckets: Buckets
  total_idr: IDR
  oldest_days: number
  without_due_date: number
  items: AgedItem[]
}

export interface AgingReport {
  title: string
  kind: 'HUTANG' | 'PIUTANG'
  as_of: string
  buckets: Buckets
  bucket_order: Bucket[]
  counterparties: AgedCounterparty[]
  /** Documents that could not be aged. The number is the nudge to fix the data. */
  without_due_date: number
  credit_idr: IDR
  credits: {
    id: string
    counterparty_name: string
    document_no: string
    incurred_on: string
    amount_idr: IDR
    paid_idr: IDR
    overpaid_idr: IDR
  }[]
}

export interface DebtDetail {
  document: {
    id: string
    counterparty_id: string
    source: string
    invoice_no: string | null
    amount_idr: IDR
    paid_idr: IDR
    outstanding_idr: IDR
    incurred_on: string
    due_date: string | null
    note: string | null
  }
  payments: {
    id: string
    amount_idr: IDR
    paid_on: string
    method: string
    note: string
  }[]
}

/** What each bucket is called on screen. */
export const bucketLabel: Record<Bucket, string> = {
  NOT_YET_DUE: 'Belum jatuh tempo',
  '1_30': '1-30 hari',
  '31_60': '31-60 hari',
  '61_90': '61-90 hari',
  OVER_90: '> 90 hari',
  NO_DUE_DATE: 'Tanpa jatuh tempo',
}

// --- ekspor (TASKS 6.6) -----------------------------------------------------

export interface ExportManifest {
  application: string
  generated_at: string
  schema_version: number
  total_rows: number
  objects: {
    name: string
    kind: 'table' | 'view'
    file: string
    rows: number
    columns: string[]
  }[]
}

// --- calls ------------------------------------------------------------------

const range = (from: string, to: string) =>
  `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`

export const salesReport = (entityId: string, from: string, to: string): Promise<SalesReport> =>
  api<SalesReport>(`/reports/sales${range(from, to)}`, { entityId })

export const purchasesReport = (
  entityId: string,
  from: string,
  to: string,
): Promise<PurchasesReport> => api<PurchasesReport>(`/reports/purchases${range(from, to)}`, { entityId })

export const stockReport = (entityId: string, from: string, to: string): Promise<StockReport> =>
  api<StockReport>(`/reports/stock${range(from, to)}`, { entityId })

export const agingReport = (
  entityId: string,
  kind: 'hutang' | 'piutang',
  asOf: string,
): Promise<AgingReport> =>
  api<AgingReport>(
    `/reports/${kind === 'hutang' ? 'payables' : 'receivables'}?as_of=${encodeURIComponent(asOf)}`,
    { entityId },
  )

export const debtDetail = (
  entityId: string,
  kind: 'hutang' | 'piutang',
  id: string,
): Promise<DebtDetail> =>
  api<DebtDetail>(`/${kind === 'hutang' ? 'payables' : 'receivables'}/${id}`, { entityId })

export interface PaymentBody {
  amount_idr: IDR
  paid_on: string
  method: string
  note?: string
}

export const payDebt = (
  entityId: string,
  kind: 'hutang' | 'piutang',
  id: string,
  body: PaymentBody,
): Promise<unknown> =>
  api(`/${kind === 'hutang' ? 'payables' : 'receivables'}/${id}/payments`, {
    method: 'POST',
    body,
    entityId,
  })

export const exportManifest = (entityId: string): Promise<ExportManifest> =>
  api<ExportManifest>('/export/manifest', { entityId })
