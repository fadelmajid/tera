/**
 * Tax configuration and the PPN position. TASKS 5.9, 5.10.
 *
 * Nothing here computes a rate. Rates arrive as the integers the tax_rule row
 * holds — basis points and an exact fraction — and every rupiah figure is
 * computed on the server (INV-4, INV-1).
 *
 * The DPP nilai lain crosses the wire as `dpp_factor_num` / `dpp_factor_den`,
 * never as a decimal: 11/12 has no finite decimal form, and 0,916666… is a
 * number no regulation contains (SPEC §2.1).
 */
import type { IDR } from '../money'
import { api } from './client'

export type TaxType = 'PPN' | 'PPNBM'
export type CalculationLevel = 'LINE' | 'INVOICE'

export interface TaxRule {
  id: string
  entity_id: string
  tax_type: TaxType

  /** Basis points. 12% is 1200. */
  rate_bp: number
  /** The DPP nilai lain, as an exact fraction. */
  dpp_factor_num: number
  dpp_factor_den: number
  /** rate_bp x num / den, in basis points: 1100 for the August 2026 rule. */
  effective_rate_bp: number

  /** Whether the listed price already contains the PPN. */
  is_inclusive: boolean
  calculation_level: CalculationLevel
  rounding_mode: string
  rounding_unit: number

  valid_from: string
  valid_to: string | null
  /** Covers today, in the company's own timezone (INV-5). */
  in_force: boolean
  /** Not started yet — the correct state for a registration taking effect later. */
  staged: boolean

  /** The regulation the row implements. Shown so a konsultan pajak can check it. */
  legal_ref: string
  note: string | null

  created_at: number
  closed_at: number | null
}

export interface TaxRulesResponse {
  /** The company's own date, so the browser's clock decides nothing (INV-5). */
  today: string
  rules: TaxRule[]
  caveat: string
}

export interface TaxRuleInput {
  tax_type: TaxType
  rate_bp: number
  dpp_factor_num: number
  dpp_factor_den: number
  is_inclusive: boolean
  calculation_level: CalculationLevel
  rounding_mode: string
  rounding_unit: number
  valid_from: string
  valid_to?: string
  legal_ref: string
  note?: string
}

export interface PPNSale {
  sale_id: string
  invoice_no: string
  business_date: string
  faktur_issued: boolean
  faktur_no: string | null
  customer_name: string | null
  dpp_idr: IDR
  ppn_idr: IDR
  total_idr: IDR
}

export interface PPNPurchase {
  purchase_id: string
  invoice_no: string | null
  business_date: string
  faktur_received: boolean
  faktur_no: string | null
  supplier_name: string | null
  ppn_idr: IDR
  /** Zero whenever no faktur arrived, whatever was paid (INV-9). */
  creditable_ppn_idr: IDR
  total_idr: IDR
}

export interface PPNPosition {
  title: string
  masa: string
  period: { from: string; to: string }
  output: {
    with_faktur_idr: IDR
    /**
     * The liability nothing else in the business will mention. A PKP owes
     * output PPN on the delivery whether or not the buyer took a faktur
     * (SPEC §2.3), and this is that half on its own line.
     */
    without_faktur_idr: IDR
    reversed_idr: IDR
    net_idr: IDR
  }
  input: {
    creditable_idr: IDR
    reversed_idr: IDR
    /** Reported, never netted: this PPN is already in the cost of the goods. */
    non_creditable_idr: IDR
    net_idr: IDR
  }
  /** Negative is a lebih bayar, carried to the next masa. Not clamped. */
  payable_idr: IDR
  is_overpaid: boolean
  sales: PPNSale[]
  purchases: PPNPurchase[]
  caveat: string
}

export const listTaxRules = (entityId: string): Promise<TaxRulesResponse> =>
  api<TaxRulesResponse>('/tax/rules', { entityId })

export const createTaxRule = (entityId: string, body: TaxRuleInput): Promise<{ rule: TaxRule }> =>
  api<{ rule: TaxRule }>('/tax/rules', { method: 'POST', body, entityId })

/** The only edit a rule ever gets (INV-4): a rate is changed by closing one row. */
export const closeTaxRule = (
  entityId: string,
  id: string,
  validTo: string,
  reason: string,
): Promise<{ rule: TaxRule }> =>
  api<{ rule: TaxRule }>(`/tax/rules/${id}/close`, {
    method: 'POST',
    body: { valid_to: validTo, reason },
    entityId,
  })

export const deleteTaxRule = (entityId: string, id: string, reason: string): Promise<void> =>
  api<void>(`/tax/rules/${id}`, { method: 'DELETE', body: { reason }, entityId })

export const ppnPosition = (entityId: string, masa: string): Promise<PPNPosition> =>
  api<PPNPosition>(`/reports/ppn${masa ? `?masa=${encodeURIComponent(masa)}` : ''}`, { entityId })

/**
 * Renders a rate as a percentage for display only.
 *
 * Basis points in, a string out — the number is never used in arithmetic, and
 * no rate is ever stored or sent in this form. Money is computed on the server
 * (INV-1, INV-4).
 */
export function formatRate(basisPoints: number): string {
  const whole = Math.trunc(basisPoints / 100)
  const fraction = basisPoints % 100
  return fraction === 0
    ? `${whole}%`
    : `${whole},${String(fraction).padStart(2, '0').replace(/0$/, '')}%`
}
