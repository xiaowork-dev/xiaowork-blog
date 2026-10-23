/** Error envelope of the Go API (API-DD §3.3), normalised for the UI. */
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public fields: Record<string, string[]> = {},
    public retryAfter?: number,
  ) {
    super(message)
  }
}

interface ErrorBody {
  error?: { code?: string, message?: string, details?: { fields?: Record<string, string[]> } }
}

/** Shape of the error thrown by $fetch (ofetch FetchError). */
interface FetchFailure {
  response?: Response
  data?: unknown
}

function toApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err
  const failure = (typeof err === 'object' && err !== null ? err : {}) as FetchFailure
  if (failure.response) {
    const body = (failure.data ?? {}) as ErrorBody
    const retry = Number(failure.response.headers.get('retry-after'))
    return new ApiError(
      failure.response.status,
      body.error?.code ?? 'INTERNAL_ERROR',
      body.error?.message ?? '服务暂时不可用，请稍后重试。',
      body.error?.details?.fields ?? {},
      Number.isFinite(retry) && retry > 0 ? retry : undefined,
    )
  }
  // No HTTP response at all: offline, DNS, connection reset (UI/UX §8 "网络断开").
  return new ApiError(0, 'NETWORK_ERROR', '网络连接失败，请检查网络后重试。')
}

/**
 * Browser-side API client: same-origin requests, X-CSRF-Token on writes (API-DD §3.4).
 * The token is bound to the anonymous/session context, so it is refreshed after login/logout
 * and once more if the API reports CSRF_INVALID.
 */
export function useApi() {
  const csrf = useState<string | null>('api:csrf', () => null)

  async function csrfToken(force = false): Promise<string> {
    if (!csrf.value || force) {
      try {
        const res = await $fetch<{ data: { csrf_token: string } }>('/api/v1/auth/csrf')
        csrf.value = res.data.csrf_token
      }
      catch (err) {
        throw toApiError(err)
      }
    }
    return csrf.value
  }

  async function send<T>(method: 'POST' | 'PATCH' | 'PUT' | 'DELETE', path: string, body?: object): Promise<T | undefined> {
    for (let attempt = 0; attempt < 2; attempt++) {
      const token = await csrfToken(attempt > 0)
      try {
        const res = await $fetch<{ data?: T } | undefined>(path, {
          method, body, headers: { 'X-CSRF-Token': token }, credentials: 'same-origin',
        })
        return res?.data
      }
      catch (err) {
        const apiErr = toApiError(err)
        if (apiErr.code === 'CSRF_INVALID' && attempt === 0) continue
        throw apiErr
      }
    }
    throw new ApiError(403, 'CSRF_INVALID', '安全校验失败，请刷新页面后重试。')
  }

  return {
    post: <T>(path: string, body?: object) => send<T>('POST', path, body),
    resetCsrf: () => { csrf.value = null },
  }
}

/** Human readable text for an error, including the rate-limit wait (UI/UX §8). */
export function describeError(err: unknown): string {
  if (!(err instanceof ApiError)) return '发生未知错误，请稍后重试。'
  if (err.code === 'RATE_LIMITED' && err.retryAfter) {
    const wait = err.retryAfter >= 120 ? `${Math.ceil(err.retryAfter / 60)} 分钟` : `${err.retryAfter} 秒`
    return `操作过于频繁，请在 ${wait}后重试。`
  }
  return err.message
}
