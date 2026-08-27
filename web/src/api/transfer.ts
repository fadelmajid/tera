import { api } from './client'
import type { IDR } from '../money'

/**
 * Inter-company stock transfer.
 *
 * The flow Olsera gets wrong: it records the transaction and does not move the
 * stock, which is why the user's quantities are drifting from reality today.
 * Here one call moves both sides or neither.
 *
 * The screen's real job is R4.5. Moving stock from the non-PKP company into the
 * PKP one destroys the input PPN credit on it permanently — the chain cannot be
 * reconnected afterwards, and it costs roughly 11% of margin on that stock. So
 * the flow is preview → blocking confirmation → commit, and the server refuses
 * the commit without `acknowledge_credit_loss`. The dialog is not the control;
 * it is how the person finds out in time to change their mind.
 */

export interface TransferLineBody {
  product_id: string
  qty: number
  /** Only a PKP sender may charge PPN (SPEC §2.3); the server enforces it. */
  ppn_idr?: IDR
}

export interface TransferBody {
  to_entity_id: string
  transfer_date?: string
  faktur_issued?: boolean
  faktur_no?: string
  note?: string
  /** R4.5. Required for non-PKP → PKP, refused on any other direction. */
  acknowledge_credit_loss?: boolean
  lines: TransferLineBody[]
}

export interface PreviewLayer {
  layer_id: string
  qty: number
  cost_idr: IDR
}

export interface PreviewLine {
  product_id: string
  product_code: string
  product_name: string
  owner_id: string | null
  owner_name: string | null
  qty: number
  cost_idr: IDR
  ppn_idr: IDR
  /** Input PPN already paid on this stock that can never now be credited. */
  forfeited_ppn_idr: IDR
  layers: PreviewLayer[]
}

export interface TransferPreview {
  /** e.g. "non-PKP → PKP". */
  direction: string
  taxable_delivery: boolean
  /** R4.5. True means the client must block and confirm before committing. */
  destroys_input_credit: boolean
  cost_total_idr: IDR
  ppn_idr: IDR
  amount_idr: IDR
  forfeited_ppn_idr: IDR
  lines: PreviewLine[]
}

export interface TransferSummary {
  id: string
  transfer_no: string
  business_date: string
  occurred_at: number
  from_entity_id: string
  from_entity_name: string
  to_entity_id: string
  to_entity_name: string
  from_is_pkp: boolean
  to_is_pkp: boolean
  credit_loss_ack: boolean
  forfeited_ppn_idr: IDR
  cost_total_idr: IDR
  ppn_idr: IDR
  amount_idr: IDR
  faktur_issued: boolean
}

export interface TransferResult {
  transfer: TransferSummary
  lines: { id: string; product_id: string; qty: number; cost_total_idr: IDR }[]
  /** How many source layers were drawn — proof the stock actually left. */
  consumptions: number
  cost_idr: IDR
  forfeited_ppn_idr: IDR
}

/** What the two companies owe each other, netted (D-014). Not hutang/piutang. */
export interface InterCompanyPosition {
  counterparty_id: string
  counterparty_name: string
  out_idr: IDR
  in_idr: IDR
  out_count: number
  in_count: number
  /** Positive means the counterparty owes this company. */
  net_idr: IDR
}

export const previewTransfer = (entityId: string, body: TransferBody): Promise<TransferPreview> =>
  api<TransferPreview>('/transfers/preview', { method: 'POST', entityId, body })

export const createTransfer = (entityId: string, body: TransferBody): Promise<TransferResult> =>
  api<TransferResult>('/transfers', { method: 'POST', entityId, body })

export const listTransfers = (entityId: string): Promise<TransferSummary[]> =>
  api<TransferSummary[]>('/transfers', { entityId })

export const getInterCompanyPosition = (entityId: string): Promise<InterCompanyPosition[]> =>
  api<InterCompanyPosition[]>('/reports/inter-company', { entityId })
