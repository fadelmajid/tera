import { api } from './client'
import type { IDR } from '../money'

/**
 * A company. Two exist: one PKP, one not, and they behave differently on every
 * sale and purchase (R1.3, SPEC §2.3).
 */
export interface Entity {
  id: string
  code: string
  name: string
  is_pkp: boolean
  npwp: string | null
  timezone: string
  book_year_start_month: number
  is_active: boolean
}

/** A family member products are attributed to. An attribution tag, not legal ownership (R2). */
export interface Owner {
  id: string
  code: string
  name: string
  note: string | null
  is_active: boolean
}

export interface Product {
  id: string
  code: string
  barcode: string | null
  name: string
  unit: string
  category: string | null
  /** null is the company bucket — its own line on the margin report (R2.2). */
  owner_id: string | null
  /**
   * Whole rupiah. The server stores it as int64 in a STRICT column, so the
   * value arriving here is always an integer (INV-1); user input goes through
   * parseIDR, which refuses anything fractional.
   */
  sale_price_idr: IDR
  is_active: boolean
}

export interface Supplier {
  id: string
  code: string
  name: string
  npwp: string | null
  address: string | null
  phone: string | null
  /**
   * Whether this supplier NORMALLY issues a faktur pajak. It defaults the
   * purchasing screen and lets suppliers be compared on true cost (R10.6). It
   * is never the truth for a given purchase — that is recorded per delivery and
   * decides the stock layer's cost basis (INV-9).
   */
  issues_faktur: boolean
  is_active: boolean
}

export interface Customer {
  id: string
  code: string
  name: string
  npwp: string | null
  nik: string | null
  address: string | null
  phone: string | null
  is_active: boolean
}

export const listEntities = () => api<Entity[]>('/entities')

export const setupFirstEntity = (body: {
  code: string
  name: string
  is_pkp: boolean
  npwp?: string
  timezone?: string
  book_year_start_month?: number
}) => api<Entity>('/setup/entity', { method: 'POST', body })

/**
 * Adds another company. Owner only.
 *
 * setupFirstEntity handles exactly the first one; every company after it comes
 * through here. The two companies are what make transfers, per-entity PKP
 * behaviour, and the omzet clock mean anything (R1.1, R1.2).
 */
export const createEntity = (
  entityId: string,
  body: {
    code: string
    name: string
    is_pkp: boolean
    npwp?: string
    timezone?: string
    book_year_start_month?: number
  },
) => api<Entity>('/entities', { method: 'POST', entityId, body })

const scoped =
  <T>(path: string) =>
  (entityId: string) =>
    api<T[]>(path, { entityId })

const creator =
  <T, B>(path: string) =>
  (entityId: string, body: B) =>
    api<T>(path, { method: 'POST', body, entityId })

const updater =
  <T, B>(path: string) =>
  (entityId: string, id: string, body: B) =>
    api<T>(`${path}/${id}`, { method: 'PUT', body, entityId })

export type OwnerBody = { code: string; name: string; note?: string; is_active?: boolean }
export type ProductBody = {
  code: string
  name: string
  unit: string
  barcode?: string
  category?: string
  owner_id?: string
  sale_price_idr: IDR
  is_active?: boolean
}
export type SupplierBody = {
  code: string
  name: string
  npwp?: string
  address?: string
  phone?: string
  issues_faktur: boolean
  is_active?: boolean
}
export type CustomerBody = {
  code: string
  name: string
  npwp?: string
  nik?: string
  address?: string
  phone?: string
  is_active?: boolean
}

export const listOwners = scoped<Owner>('/owners')
export const createOwner = creator<Owner, OwnerBody>('/owners')
export const updateOwner = updater<Owner, OwnerBody>('/owners')

export const listProducts = scoped<Product>('/products')
export const createProduct = creator<Product, ProductBody>('/products')
export const updateProduct = updater<Product, ProductBody>('/products')

export const listSuppliers = scoped<Supplier>('/suppliers')
export const createSupplier = creator<Supplier, SupplierBody>('/suppliers')
export const updateSupplier = updater<Supplier, SupplierBody>('/suppliers')

export const listCustomers = scoped<Customer>('/customers')
export const createCustomer = creator<Customer, CustomerBody>('/customers')
export const updateCustomer = updater<Customer, CustomerBody>('/customers')
