#!/usr/bin/env node
// End-to-end check of the P2 auth flow against a running Docker Compose stack.
// Goes through the Nuxt origin (same path as a browser: Nuxt → /api/v1 proxy → Go API) and reads mail
// from Mailpit's HTTP API. Creates a uniquely named test account; nothing else is touched.
//
//   node scripts/e2e-auth.mjs [WEB_ORIGIN] [MAILPIT_URL]
const WEB = process.argv[2] ?? 'http://127.0.0.1:3000'
const MAILPIT = process.argv[3] ?? 'http://127.0.0.1:8025'
const run = Date.now().toString(36)
const email = `e2e-${run}@example.test`
const password = `e2e-password-${run}-A`
const newPassword = `e2e-new-password-${run}-B`

// Each run presents its own documentation-range client IP, so the per-IP registration limit (3/hour)
// does not block re-runs. The API only honours it because the local Docker gateway is a trusted proxy.
const clientIP = `203.0.113.${1 + Math.floor(Math.random() * 250)}`
const jar = new Map()
let csrf = ''
let step = 0

function log(ok, msg) {
  step++
  console.log(`${ok ? 'PASS' : 'FAIL'} ${String(step).padStart(2, '0')} ${msg}`)
  if (!ok) process.exit(1)
}

async function http(method, path, body, { csrfHeader = true } = {}) {
  const headers = { Origin: WEB, 'X-Forwarded-For': clientIP }
  if (jar.size) headers.Cookie = [...jar].map(([k, v]) => `${k}=${v}`).join('; ')
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (csrfHeader && method !== 'GET' && csrf) headers['X-CSRF-Token'] = csrf
  const res = await fetch(WEB + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), redirect: 'manual' })
  for (const c of res.headers.getSetCookie()) {
    const [pair, ...attrs] = c.split(';')
    const [name, ...rest] = pair.split('=')
    const value = rest.join('=')
    if (attrs.some(a => /max-age=-1|max-age=0/i.test(a.trim())) || value === '') jar.delete(name.trim())
    else jar.set(name.trim(), value)
  }
  const text = await res.text()
  let json
  try { json = JSON.parse(text) } catch { json = undefined }
  return { status: res.status, headers: res.headers, json, text }
}

async function refreshCsrf() {
  const r = await http('GET', '/api/v1/auth/csrf')
  csrf = r.json?.data?.csrf_token ?? ''
  return r
}

async function mailToken(to, pathPart, previous = 0) {
  for (let i = 0; i < 40; i++) {
    const res = await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent(`to:${to}`)}`)
    const found = await res.json()
    if (found.messages_count > previous) {
      const id = found.messages[0].ID // newest first
      const msg = await (await fetch(`${MAILPIT}/api/v1/message/${id}`)).json()
      const m = msg.Text.match(new RegExp(`${pathPart}\\?token=([A-Za-z0-9_-]{43})`))
      return { token: m?.[1], absolute: Boolean(m) && msg.Text.includes(WEB + m[0]), count: found.messages_count, subject: msg.Subject }
    }
    await new Promise(r => setTimeout(r, 250))
  }
  return { count: previous }
}

const health = await http('GET', '/health')
log(health.status === 200, `web health ${health.status}`)

const c1 = await refreshCsrf()
log(c1.status === 200 && csrf && jar.size === 1, 'GET /api/v1/auth/csrf via Nuxt proxy sets anonymous CSRF cookie')

const noCsrf = await http('POST', '/api/v1/auth/register', { email, nickname: 'E2E', password }, { csrfHeader: false })
log(noCsrf.status === 403 && noCsrf.json?.error?.code === 'CSRF_INVALID', 'register without CSRF token is rejected (403 CSRF_INVALID)')

const reg = await http('POST', '/api/v1/auth/register', { email, nickname: 'E2E 用户', password })
log(reg.status === 201 && reg.json?.data?.email_delivery === 'accepted', `register → ${reg.status} ${reg.json?.data?.email_delivery}`)

const dup = await http('POST', '/api/v1/auth/register', { email: email.toUpperCase(), nickname: 'dup', password })
log(dup.status === 409 && dup.json?.error?.code === 'EMAIL_ALREADY_USED', `duplicate e-mail (other case) → ${dup.status} ${dup.json?.error?.code}`)

const early = await http('POST', '/api/v1/auth/login', { email, password })
log(early.status === 403 && early.json?.error?.code === 'EMAIL_NOT_VERIFIED', 'login before verification → 403 EMAIL_NOT_VERIFIED')

const verifyMail = await mailToken(email, '/auth/verify')
log(Boolean(verifyMail.token) && verifyMail.absolute,
  `verification mail delivered through SMTP to Mailpit with ${WEB}/auth/verify link ("${verifyMail.subject}")`)

const page = await http('GET', `/auth/verify?token=${verifyMail.token}`)
log(page.status === 200 && page.text.includes('确认验证') && /no-store/.test(page.headers.get('cache-control') ?? '')
  && page.headers.get('referrer-policy') === 'no-referrer', 'SSR /auth/verify page renders confirm step with no-store + no-referrer')

const tokenStillUnused = await http('GET', '/api/v1/auth/me')
log(tokenStillUnused.status === 401, 'opening the link (GET) does not consume the token or sign in')

const ver = await http('POST', '/api/v1/auth/verify-email', { token: verifyMail.token })
log(ver.status === 200 && ver.json?.data?.email_verified === true, 'POST verify-email → 200')

const reuse = await http('POST', '/api/v1/auth/verify-email', { token: verifyMail.token })
log(reuse.status === 422 && reuse.json?.error?.code === 'TOKEN_INVALID_OR_EXPIRED', 'reusing the verification link → 422')

const wrong = await http('POST', '/api/v1/auth/login', { email, password: 'definitely-wrong-pw' })
log(wrong.status === 401 && wrong.json?.error?.code === 'INVALID_CREDENTIALS', 'wrong password → 401 INVALID_CREDENTIALS')

const login = await http('POST', '/api/v1/auth/login', { email, password })
const sessionCookie = [...jar.keys()].find(k => k.endsWith('blog_session'))
log(login.status === 200 && Boolean(sessionCookie), `login → 200, session cookie "${sessionCookie}" set (HttpOnly)`)
await refreshCsrf()

const me = await http('GET', '/api/v1/auth/me')
log(me.status === 200 && me.json?.data?.email === email && me.json?.data?.role === 'USER', 'GET /api/v1/auth/me → current user')

const home = await http('GET', '/')
log(home.status === 200 && home.text.includes('E2E 用户') && home.text.includes('退出'), 'SSR home page shows the signed-in nickname')

const admin = await http('GET', '/api/v1/admin/overview')
log(admin.status === 403 && admin.json?.error?.code === 'FORBIDDEN', 'normal user on /api/v1/admin/* → 403 FORBIDDEN')

const out = await http('POST', '/api/v1/auth/logout')
log(out.status === 204 && !jar.has(sessionCookie), 'logout → 204, cookie cleared')
const after = await http('GET', '/api/v1/auth/me')
log(after.status === 401, 'after logout /auth/me → 401')

await refreshCsrf()
const before = (await mailToken(email, '/auth/verify')).count
const forgot = await http('POST', '/api/v1/auth/forgot-password', { email })
log(forgot.status === 202, 'forgot-password → 202 (uniform response)')
const resetMail = await mailToken(email, '/auth/reset-password', before)
log(Boolean(resetMail.token), 'reset mail delivered')
const reset = await http('POST', '/api/v1/auth/reset-password', { token: resetMail.token, new_password: newPassword })
log(reset.status === 200, 'reset-password → 200')
const oldPw = await http('POST', '/api/v1/auth/login', { email, password })
log(oldPw.status === 401, 'old password rejected after reset')
const newPw = await http('POST', '/api/v1/auth/login', { email, password: newPassword })
log(newPw.status === 200, 'new password accepted')

console.log(`\nAll ${step} checks passed for ${email}`)
