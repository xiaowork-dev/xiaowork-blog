export interface CurrentUser {
  id: string
  email: string
  nickname: string
  avatar_url: string | null
  role: 'USER' | 'ADMIN'
  email_verified: boolean
  status: string
  created_at: string
}

/**
 * Current-user state. On the server the request's cookies are forwarded to the API by useRequestFetch,
 * and the result is only kept in this request's payload (never in a shared cache).
 */
export function useAuth() {
  const user = useState<CurrentUser | null>('auth:user', () => null)
  const loaded = useState<boolean>('auth:loaded', () => false)
  const requestFetch = useRequestFetch()
  const api = useApi()

  async function refresh() {
    try {
      const res = await requestFetch<{ data: CurrentUser }>('/api/v1/auth/me')
      user.value = res.data
    }
    catch {
      user.value = null // 401/403/network: treat as signed out
    }
    loaded.value = true
  }

  async function ensureLoaded() {
    if (!loaded.value) await refresh()
  }

  async function logout() {
    try {
      await api.post('/api/v1/auth/logout')
    }
    finally {
      api.resetCsrf()
      user.value = null
    }
  }

  return { user, loaded, refresh, ensureLoaded, logout }
}

/** Only same-site paths are allowed as post-login redirects (no open redirects). */
export function safeRedirect(target: unknown): string {
  return typeof target === 'string' && target.startsWith('/') && !target.startsWith('//') && !target.startsWith('/\\')
    ? target
    : '/'
}
