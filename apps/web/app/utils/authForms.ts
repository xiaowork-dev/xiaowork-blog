// Client-side checks mirror the API limits (API-DD §5.6) for fast feedback; the API remains authoritative.
export const PASSWORD_MIN = 12
export const PASSWORD_MAX = 128

const FIELD_MESSAGES: Record<string, string> = {
  INVALID: '格式不正确',
  INVALID_LENGTH: '长度需为 1–40 个字符',
  TOO_SHORT: `至少 ${PASSWORD_MIN} 个字符`,
  TOO_LONG: `最多 ${PASSWORD_MAX} 个字符`,
  INCORRECT: '当前密码不正确',
  SAME_AS_CURRENT: '不能与当前值相同',
  MISMATCH: '两次输入的密码不一致',
  REQUIRED: '必填',
}

export function fieldMessage(codes?: string[]): string {
  if (!codes?.length) return ''
  return codes.map(c => FIELD_MESSAGES[c] ?? c).join('；')
}

export function charCount(s: string): number {
  return [...s].length
}

export function checkPassword(password: string, confirm?: string): string[] | undefined {
  const n = charCount(password)
  if (n < PASSWORD_MIN) return ['TOO_SHORT']
  if (n > PASSWORD_MAX) return ['TOO_LONG']
  if (confirm !== undefined && password !== confirm) return ['MISMATCH']
  return undefined
}

/** Reads ?token= once, then removes it from the address bar so it is not kept in history (API-DD §5.6). */
export function takeTokenFromUrl(): string {
  const route = useRoute()
  const token = typeof route.query.token === 'string' ? route.query.token : ''
  if (import.meta.client && token) {
    const url = new URL(window.location.href)
    url.searchParams.delete('token')
    window.history.replaceState(window.history.state, '', url.pathname + url.search)
  }
  return token
}
