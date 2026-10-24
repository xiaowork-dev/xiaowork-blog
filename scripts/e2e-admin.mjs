#!/usr/bin/env node
// End-to-end check of the P7 admin console against a running Docker Compose stack, through the Nuxt origin:
// overview, user disable/enable with session revocation, site settings and links taking effect on the
// public pages, about page (saved = public, XSS-safe) and media reference protection.
// Needs an admin: E2E_ADMIN_EMAIL / E2E_ADMIN_PASSWORD. Site settings, links and the about page are
// restored to their previous values at the end.
//
//   node scripts/e2e-admin.mjs [WEB] [MAILPIT]
import { crc32, deflateSync } from 'node:zlib'

const WEB = process.argv[2] ?? 'http://127.0.0.1:3000'
const MAILPIT = process.argv[3] ?? 'http://127.0.0.1:8025'
const { E2E_ADMIN_EMAIL: adminEmail, E2E_ADMIN_PASSWORD: adminPassword } = process.env
if (!adminEmail || !adminPassword) {
  console.error('E2E_ADMIN_EMAIL and E2E_ADMIN_PASSWORD are required')
  process.exit(2)
}
const run = Date.now().toString(36)
let step = 0
const log = (ok, msg) => {
  step++
  console.log(`${ok ? 'PASS' : 'FAIL'} ${String(step).padStart(2, '0')} ${msg}`)
  if (!ok) process.exit(1)
}

function browser() {
  const ip = `198.18.0.${1 + Math.floor(Math.random() * 250)}`
  const jar = new Map()
  let csrf = ''
  async function http(method, path, { json, body } = {}) {
    if (method === 'GET') json = undefined // GET never carries a body
    const headers = { Origin: WEB, 'X-Forwarded-For': ip, Accept: 'text/html,application/json;q=0.9,*/*;q=0.8' }
    if (jar.size) headers.Cookie = [...jar].map(([k, v]) => `${k}=${v}`).join('; ')
    if (json !== undefined) headers['Content-Type'] = 'application/json'
    if (method !== 'GET' && csrf) headers['X-CSRF-Token'] = csrf
    const res = await fetch(WEB + path, { method, headers, redirect: 'manual', body: json !== undefined ? JSON.stringify(json) : body })
    for (const c of res.headers.getSetCookie()) {
      const [pair, ...attrs] = c.split(';')
      const [name, ...rest] = pair.split('=')
      const value = rest.join('=')
      if (attrs.some(a => /max-age=-1|max-age=0/i.test(a.trim())) || value === '') jar.delete(name.trim())
      else jar.set(name.trim(), value)
    }
    const text = await res.text()
    let data
    try { data = JSON.parse(text) } catch { data = undefined }
    return { status: res.status, headers: res.headers, text, data, code: data?.error?.code }
  }
  async function token() { csrf = (await http('GET', '/api/v1/auth/csrf')).data.data.csrf_token }
  async function login(email, password) {
    await token()
    const r = await http('POST', '/api/v1/auth/login', { json: { email, password } })
    await token()
    return r
  }
  return { http, token, login }
}

async function mailToken(to, pathPart) {
  for (let i = 0; i < 60; i++) {
    const found = await (await fetch(`${MAILPIT}/api/v1/search?query=${encodeURIComponent(`to:${to}`)}`)).json()
    if (found.messages_count > 0) {
      const msg = await (await fetch(`${MAILPIT}/api/v1/message/${found.messages[0].ID}`)).json()
      const m = msg.Text.match(new RegExp(`${pathPart}\\?token=([A-Za-z0-9_-]{43})`))
      if (m) return m[1]
    }
    await new Promise(r => setTimeout(r, 250))
  }
  return null
}

function png(w, h) {
  const chunk = (type, data) => {
    const len = Buffer.alloc(4); len.writeUInt32BE(data.length)
    const td = Buffer.concat([Buffer.from(type), data])
    const crc = Buffer.alloc(4); crc.writeUInt32BE(crc32(td))
    return Buffer.concat([len, td, crc])
  }
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4); ihdr[8] = 8; ihdr[9] = 2
  const raw = Buffer.alloc((w * 3 + 1) * h, 70)
  for (let y = 0; y < h; y++) raw[y * (w * 3 + 1)] = 0
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', ihdr), chunk('IDAT', deflateSync(raw)), chunk('IEND', Buffer.alloc(0))])
}

const admin = browser()
log((await admin.login(adminEmail, adminPassword)).status === 200, 'admin login')
const anon = browser()
await anon.token()

// A real reader, registered and verified through Mailpit.
const reader = browser()
const readerEmail = `e2e-admin-reader-${run}@example.test`
const readerPassword = `reader-password-${run}`
await reader.token()
await reader.http('POST', '/api/v1/auth/register', { json: { email: readerEmail, nickname: `读者${run.slice(-4)}`, password: readerPassword } })
await reader.http('POST', '/api/v1/auth/verify-email', { json: { token: await mailToken(readerEmail, '/auth/verify') } })
log((await reader.login(readerEmail, readerPassword)).status === 200, 'reader registered, verified and signed in')

// ---- permissions ----
let denied = true
for (const [m, p] of [['GET', '/api/v1/admin/overview'], ['GET', '/api/v1/admin/users'], ['GET', '/api/v1/admin/site'], ['PATCH', '/api/v1/admin/site'], ['PUT', '/api/v1/admin/pages/about'], ['PUT', '/api/v1/admin/site/social-links']]) {
  denied &&= (await reader.http(m, p, { json: {} })).status === 403 && (await anon.http(m, p, { json: {} })).status === 401
}
log(denied, 'reader → 403 and guest → 401 on every P7 admin API')
let r = await reader.http('GET', '/admin/settings')
log(r.status === 403, `reader opening /admin/settings → ${r.status}`)
for (const page of ['/admin', '/admin/users', '/admin/settings', '/admin/about']) {
  r = await admin.http('GET', page)
  if (r.status !== 200 || !r.headers.get('cache-control')?.includes('no-store') || !r.headers.get('x-robots-tag')?.includes('noindex')) log(false, `${page} → ${r.status}`)
}
log(true, 'admin pages served privately (no-store, noindex)')

// ---- overview ----
r = await admin.http('GET', '/api/v1/admin/overview')
const o = r.data?.data
log(r.status === 200 && Number.isInteger(o.user_count) && o.user_count >= 2 && Array.isArray(o.recent_articles) && Array.isArray(o.recent_events) && !/审核|pending_review/i.test(r.text), 'overview returns real counts and recent lists, no review queue')

// ---- users ----
r = await admin.http('GET', `/api/v1/admin/users?q=${encodeURIComponent(readerEmail)}`)
const readerId = r.data?.data?.[0]?.id
log(r.data?.meta?.total === 1 && r.data.data[0].email_verified === true, 'user search by e-mail')
r = await admin.http('GET', `/api/v1/admin/users/${readerId}`)
log(r.data?.data?.can_disable === true && r.data.data.active_session_count >= 1, 'user detail shows sessions and can_disable')
r = await admin.http('POST', `/api/v1/admin/users/${readerId}/disable`)
log(r.status === 200 && r.data.data.status === 'DISABLED' && r.data.data.revoked_sessions >= 1, `disable reader → sessions revoked (${r.data?.data?.revoked_sessions})`)
log((await reader.http('GET', '/api/v1/auth/me')).status === 401, "reader's existing session stops working at once")
const again = browser()
r = await again.login(readerEmail, readerPassword)
log(r.status === 403 && r.code === 'ACCOUNT_DISABLED', `disabled reader cannot log in (${r.status} ${r.code})`)
const meId = (await admin.http('GET', '/api/v1/auth/me')).data.data.id
log((await admin.http('POST', `/api/v1/admin/users/${meId}/disable`)).code === 'CANNOT_DISABLE_SELF', 'admin cannot disable own account')
r = await admin.http('GET', '/api/v1/admin/users?role=ADMIN')
const otherAdmin = r.data.data.find(u => u.id !== meId)
if (otherAdmin) log((await admin.http('POST', `/api/v1/admin/users/${otherAdmin.id}/disable`)).code === 'ADMIN_PROTECTED', 'another admin cannot be disabled')
log((await admin.http('POST', `/api/v1/admin/users/${readerId}/enable`)).data?.data?.status === 'ACTIVE' && (await again.login(readerEmail, readerPassword)).status === 200, 'enable → the reader can log in again')

// ---- site settings take effect on the public site ----
const before = (await admin.http('GET', '/api/v1/admin/site')).data.data
const form = new FormData()
form.append('usage_kind', 'SITE')
form.append('file', new Blob([png(64, 64)], { type: 'image/png' }), 'site-logo.png')
const logo = (await admin.http('POST', '/api/v1/admin/media', { body: form })).data.data
log(!!logo?.id && (await anon.http('GET', logo.url)).status === 404, 'uploaded site image is private until referenced')
r = await admin.http('PATCH', '/api/v1/admin/site', { json: {
  site_title: `E2E 博客 ${run}`, profile_name: `博主${run.slice(-4)}`, profile_bio: `E2E 简介 ${run}`,
  default_seo_description: `E2E SEO 描述 ${run}`, logo_media_id: logo.id, favicon_media_id: logo.id, profile_avatar_media_id: logo.id,
} })
log(r.status === 200, 'site settings saved')
r = await anon.http('GET', '/')
log(r.text.includes(`E2E 博客 ${run}`) && r.text.includes(`E2E 简介 ${run}`) && r.text.includes(`博主${run.slice(-4)}`), 'home SSR shows the new site name, profile name and bio')
log(r.text.includes(`<link rel="icon" href="${logo.url}"`) && r.text.includes('class="brand-logo"') && r.text.includes(`content="E2E SEO 描述 ${run}"`), 'favicon, logo and default SEO description rendered')
log((await anon.http('GET', logo.url)).status === 200, 'referenced site image becomes public')
log((await admin.http('DELETE', `/api/v1/admin/media/${logo.id}`)).code === 'RESOURCE_IN_USE', 'image used by the site settings cannot be deleted')
log((await admin.http('PATCH', '/api/v1/admin/site', { json: { smtp_password: 'x' } })).status === 400 && !(await admin.http('GET', '/api/v1/admin/site')).text.toLowerCase().includes('smtp'), 'deployment secrets are neither shown nor writable')
log((await admin.http('PATCH', '/api/v1/admin/site', { json: { site_title: '' } })).status === 422, 'empty site title → 422')

r = await admin.http('PUT', '/api/v1/admin/site/social-links', { json: { links: [{ icon_key: 'link', label: 'x', url: 'javascript:alert(1)' }] } })
log(r.status === 422 && r.data.error.details.fields['links[0].url'], 'javascript: profile link rejected')
r = await admin.http('PUT', '/api/v1/admin/site/social-links', { json: { links: [
  { icon_key: 'github', label: 'GitHub', url: `https://github.com/e2e-${run}` },
  { icon_key: 'mail', label: '邮箱', url: `mailto:e2e-${run}@example.test` },
  { icon_key: 'rss', label: '隐藏', url: `https://example.com/hidden-${run}`, is_enabled: false },
] } })
log(r.status === 200, 'profile links saved')
r = await anon.http('GET', '/about')
log(r.text.includes(`href="https://github.com/e2e-${run}"`) && r.text.includes(`mailto:e2e-${run}@example.test`) && !r.text.includes(`hidden-${run}`), 'public profile column shows enabled links only')
log((await admin.http('PUT', '/api/v1/admin/site/navigation-links', { json: { links: [{ label: '后台', link_type: 'INTERNAL', url: '/admin' }] } })).status === 422, 'navigation to /admin rejected')
await admin.http('PUT', '/api/v1/admin/site/navigation-links', { json: { links: [
  { label: `导航${run.slice(-4)}`, link_type: 'INTERNAL', url: '/archives' },
  { label: '外链', link_type: 'EXTERNAL', url: 'https://github.com' },
] } })
r = await anon.http('GET', '/')
log(r.text.includes(`导航${run.slice(-4)}`) && r.text.includes('href="https://github.com"'), 'custom navigation rendered in the header')

// ---- about page: saved = public, rendered safely ----
const aboutBefore = (await admin.http('GET', '/api/v1/admin/pages/about')).data.data
const pageForm = new FormData()
pageForm.append('usage_kind', 'PAGE')
pageForm.append('file', new Blob([png(48, 48)], { type: 'image/png' }), 'about.png')
const pageImg = (await admin.http('POST', '/api/v1/admin/media', { body: pageForm })).data.data
const aboutBody = `# 关于 ${run}\n\n你好！<script>alert("xss")</script>\n\n![我](${pageImg.url})\n\n[坏链接](javascript:alert(1))\n\n<img src=x onerror=alert(1)>`
r = await admin.http('PUT', '/api/v1/admin/pages/about', { json: { title: `关于我 ${run}`, body_markdown: aboutBody } })
log(r.status === 200 && r.data.data.exists === true, 'about page saved')
r = await anon.http('GET', '/about')
const body = r.text.slice(r.text.indexOf('class="md-body'))
log(r.text.includes(`关于我 ${run}`) && r.text.includes(`关于 ${run}`), 'about page public immediately (SSR)')
log(body.includes('&lt;script&gt;') && !body.includes('<script>alert') && !body.includes('href="javascript:') && !/<img[^>]*onerror/i.test(body), 'about Markdown rendered safely (raw HTML escaped, javascript: link dropped)')
log((await anon.http('GET', pageImg.url)).status === 200 && (await admin.http('DELETE', `/api/v1/admin/media/${pageImg.id}`)).code === 'RESOURCE_IN_USE', 'about image public and protected from deletion')

// ---- restore the previous state ----
await admin.http('PATCH', '/api/v1/admin/site', { json: {
  site_title: before.site_title, site_description: before.site_description, profile_name: before.profile_name,
  profile_bio: before.profile_bio, contact_email: before.contact_email, default_seo_title: before.default_seo_title,
  default_seo_description: before.default_seo_description, logo_media_id: before.logo?.id ?? null,
  favicon_media_id: before.favicon?.id ?? null, profile_avatar_media_id: before.profile_avatar?.id ?? null,
} })
await admin.http('PUT', '/api/v1/admin/site/social-links', { json: { links: before.social_links } })
await admin.http('PUT', '/api/v1/admin/site/navigation-links', { json: { links: before.navigation_links } })
if (aboutBefore.exists) await admin.http('PUT', '/api/v1/admin/pages/about', { json: { title: aboutBefore.title, body_markdown: aboutBefore.body_markdown } })
else await admin.http('PUT', '/api/v1/admin/pages/about', { json: { title: '关于', body_markdown: '' } })
const restored = (await admin.http('GET', '/api/v1/admin/site')).data.data
const deleted = [(await admin.http('DELETE', `/api/v1/admin/media/${logo.id}`)).status, (await admin.http('DELETE', `/api/v1/admin/media/${pageImg.id}`)).status]
log(restored.site_title === before.site_title && restored.social_links.length === before.social_links.length && deleted.every(s => s === 204), 'settings restored; test images released and deleted')
console.log(`\nAll ${step} checks passed`)
