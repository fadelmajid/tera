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
