/**
 * The API client.
 *
 * Two headers carry the invariants the server enforces:
 *
 *   X-Client-Request-Id  every mutating call is idempotent on it (INV-6). The
 *                        browser mints one per logical operation, so a retry
 *                        after a stuttering shop WiFi replays the original
 *                        answer instead of ringing the sale twice.
 *   X-Entity-Id          which company the call applies to. Authorization is
 *                        always per entity (R13.4) and the server refuses a
 *                        mutating request that names none rather than guessing.
 */

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
    readonly replay = false,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
  body?: unknown
  entityId?: string | undefined
  /**
   * Reuse a key across retries of the *same* logical operation. Omit and one is
   * minted per call — correct for a fresh action, wrong for a retry.
   */
  clientRequestId?: string
  signal?: AbortSignal
}

const MUTATING = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

/**
 * Mints an idempotency key. Lowercase canonical UUID, as the server requires.
 *
 * `crypto.randomUUID()` exists only in a *secure context* — HTTPS, or localhost.
 * Tera is a LAN server serving plain HTTP to every other machine in the shop
 * (ARCHITECTURE §1: bind 0.0.0.0:8080, everyone opens a URL), so on every client
 * except the one running the server it is `undefined`.
 *
 * Every mutating call carries one of these (INV-6), so calling it threw a
 * TypeError before `fetch` was ever reached — which surfaced as "tidak dapat
 * terhubung ke server" while the server was perfectly healthy and had no record
 * of the request. Nothing that writes worked from the shop floor: no sale, no
 * purchase, not even signing in.
 *
 * `crypto.getRandomValues()` carries no secure-context restriction and is the
 * same CSPRNG, so the v4 below is generated the same way — it is only the
 * convenience wrapper that is unavailable.
 */
export function newRequestId(): string {
  const c = globalThis.crypto
  if (typeof c?.randomUUID === 'function') return c.randomUUID().toLowerCase()

  const b = new Uint8Array(16)
  c.getRandomValues(b)
  b[6] = ((b[6] ?? 0) & 0x0f) | 0x40 // version 4
  b[8] = ((b[8] ?? 0) & 0x3f) | 0x80 // variant 10xx
  const hex = Array.from(b, (n) => n.toString(16).padStart(2, '0'))
  return [
    hex.slice(0, 4).join(''),
    hex.slice(4, 6).join(''),
    hex.slice(6, 8).join(''),
    hex.slice(8, 10).join(''),
    hex.slice(10, 16).join(''),
  ].join('-')
}

export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET'
  const headers: Record<string, string> = { Accept: 'application/json' }

  if (options.body !== undefined) headers['Content-Type'] = 'application/json'
  if (options.entityId) headers['X-Entity-Id'] = options.entityId
  if (MUTATING.has(method)) {
    headers['X-Client-Request-Id'] = options.clientRequestId ?? newRequestId()
  }

  const init: RequestInit = {
    method,
    headers,
    // The session is an HttpOnly cookie; it is never readable from JS.
    credentials: 'same-origin',
  }
  if (options.body !== undefined) init.body = JSON.stringify(options.body)
  if (options.signal) init.signal = options.signal

  const response = await fetch(`/api/v1${path}`, init)

  if (response.status === 204) return undefined as T

  const text = await response.text()
  const payload: unknown = text ? JSON.parse(text) : null

  if (!response.ok) {
    const message =
      payload && typeof payload === 'object' && 'error' in payload
        ? String((payload as { error: unknown }).error)
        : `Terjadi kesalahan (${response.status})`
    throw new ApiError(response.status, message, response.headers.get('Idempotent-Replay') === 'true')
  }

  return payload as T
}
