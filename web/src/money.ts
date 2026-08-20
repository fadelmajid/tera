/**
 * Rupiah, as a branded integer.
 *
 * INV-1: money is never a float — not in Go, not here, not in JSON. The brand
 * exists so an amount cannot be passed where a rate or a quantity is expected;
 * they are all `number` at runtime and the compiler is the only thing standing
 * between them.
 *
 * Rp 4.8 billion is far inside Number.MAX_SAFE_INTEGER, so the wire value
 * arrives losslessly as a JSON integer (SPEC §1).
 */
export type IDR = number & { readonly __brand: 'IDR' }

/** Thrown when a value that must be whole rupiah is not. */
export class FractionalRupiahError extends Error {
  constructor(value: number) {
    super(`nilai rupiah harus bilangan bulat, diterima ${value}`)
    this.name = 'FractionalRupiahError'
  }
}

/**
 * Narrows a number to IDR, refusing anything fractional or unsafe.
 *
 * This mirrors the server's UnmarshalJSON, which refuses a body containing
 * '.', 'e' or 'E'. Both ends refuse rather than truncate: a silent rounding
 * here is a settlement that does not balance three weeks later.
 */
export function idr(value: number): IDR {
  if (!Number.isSafeInteger(value)) throw new FractionalRupiahError(value)
  return value as IDR
}

/** Zero rupiah. */
export const ZERO: IDR = 0 as IDR

export const add = (a: IDR, b: IDR): IDR => idr(a + b)
export const sub = (a: IDR, b: IDR): IDR => idr(a - b)
/** Multiplies by a whole quantity — counts of physical units stay integers. */
export const mulQty = (a: IDR, qty: number): IDR => idr(a * Math.trunc(qty))
export const sum = (values: readonly IDR[]): IDR => values.reduce(add, ZERO)

const groups = new Intl.NumberFormat('id-ID', { maximumFractionDigits: 0 })

/** Renders the Indonesian way: Rp 1.234.567. */
export function formatIDR(value: IDR): string {
  const sign = value < 0 ? '-' : ''
  return `${sign}Rp ${groups.format(Math.abs(value))}`
}

/** Renders digits with separators but no prefix, for columns headed "Rp". */
export function formatPlain(value: IDR): string {
  const sign = value < 0 ? '-' : ''
  return `${sign}${groups.format(Math.abs(value))}`
}

/**
 * Reads what a user typed into a rupiah field: "1.234.567", "Rp 1.000", "1000".
 * A comma is Indonesia's decimal separator, so anything containing one is
 * fractional and refused rather than truncated.
 */
export function parseIDR(input: string): IDR {
  const trimmed = input.trim()
  if (trimmed === '') throw new FractionalRupiahError(NaN)
  if (trimmed.includes(',')) throw new FractionalRupiahError(NaN)

  let text = trimmed
  let negative = false
  if (text.startsWith('-')) {
    negative = true
    text = text.slice(1).trim()
  }
  text = text.replace(/^rp/i, '').trim().replace(/\./g, '').replace(/\s/g, '')

  if (!/^\d+$/.test(text)) throw new FractionalRupiahError(NaN)
  const value = Number(text)
  return idr(negative ? -value : value)
}
