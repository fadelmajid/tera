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

/** Mints an idempotency key. Lowercase canonical UUID, as the server requires. */
export function newRequestId(): string {
  return crypto.randomUUID().toLowerCase()
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
