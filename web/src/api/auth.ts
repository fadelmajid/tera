import { api } from './client'

export type Role = 'owner' | 'manager' | 'staff'

export interface User {
  id: string
  username: string
  full_name: string
}

export interface Session {
  user: User
  /** Role held in each company. A user may hold a different one in each (R13.4). */
  roles: Record<string, Role>
}

export const login = (username: string, password: string): Promise<Session> =>
  api<Session>('/auth/login', { method: 'POST', body: { username, password } })

export const logout = (): Promise<void> => api<void>('/auth/logout', { method: 'POST' })

export const me = (): Promise<Session> => api<Session>('/auth/me')

/** R7.1: owners and managers may edit past transactions; staff may not. */
export const canEditHistory = (role: Role): boolean => role === 'owner' || role === 'manager'

/** R10.4: purchases are entered by admin or manager, not cashier staff. */
export const canEnterPurchases = (role: Role): boolean => role === 'owner' || role === 'manager'

export const canManageMasterData = (role: Role): boolean => role === 'owner' || role === 'manager'

export const canManageUsers = (role: Role): boolean => role === 'owner'

/**
 * R13.3 / D-009: owners and managers see owner margin figures, staff see none.
 *
 * This hides the menu item. It is not the control — the server refuses the
 * request, because a margin figure that reaches the browser has already left
 * the building.
 */
export const canSeeOwnerMargin = (role: Role): boolean => role === 'owner' || role === 'manager'

/**
 * SPEC §2.4: the PPN position is a figure the business owes and plans around,
 * not something a cashier needs at the till.
 */
export const canSeeTaxPosition = (role: Role): boolean => role === 'owner' || role === 'manager'

/**
 * A tax rule decides what every subsequent sale charges, and it is the
 * configuration a konsultan pajak is shown (TASKS 5.10). Owner only — the
 * person who answers for the rate is the person who sets it.
 */
export const canManageTaxRules = (role: Role): boolean => role === 'owner'

/**
 * TASKS 6.1-6.5: every report carries a cost figure — what stock is worth, what
 * a supplier was paid, which customers owe money — and a cashier needs none of
 * it to ring a sale.
 */
export const canSeeReports = (role: Role): boolean => role === 'owner' || role === 'manager'

/**
 * R14.3: the export is the whole database, so the server additionally requires
 * ownership of every active company. This hides the menu item; the endpoint
 * decides.
 */
export const canExportEverything = (role: Role): boolean => role === 'owner'
