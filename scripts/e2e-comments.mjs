#!/usr/bin/env node
// End-to-end check of P6 (comments, user centre, moderation) against a running Docker Compose stack,
// through the Nuxt origin. Real accounts are registered and verified through Mailpit, like e2e-auth.
// Needs an admin: E2E_ADMIN_EMAIL / E2E_ADMIN_PASSWORD. Its articles end in the recycle bin.
//
//   node scripts/e2e-comments.mjs [WEB] [MAILPIT]
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

/** One browser: cookie jar, CSRF token and its own documentation-range client IP. */
function browser() {
  const ip = `192.0.2.${1 + Math.floor(Math.random() * 250)}`
  const jar = new Map()
  let csrf = ''
  async function http(method, path, { json, body, csrfHeader = true } = {}) {
    const headers = { Origin: WEB, 'X-Forwarded-For': ip, Accept: 'text/html,application/json;q=0.9,*/*;q=0.8' }
    if (jar.size) headers.Cookie = [...jar].map(([k, v]) => `${k}=${v}`).join('; ')
    if (json !== undefined) headers['Content-Type'] = 'application/json'
    if (csrfHeader && method !== 'GET' && csrf) headers['X-CSRF-Token'] = csrf
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
  async function token() {
    csrf = (await http('GET', '/api/v1/auth/csrf')).data.data.csrf_token
  }
  async function login(email, password) {
    await token()
    const r = await http('POST', '/api/v1/auth/login', { json: { email, password } })
    await token()
    return r
  }
  return { http, token, login, jar }
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

/** Registers, verifies through the mail link and signs in a real reader account. */
async function reader(name) {
  const b = browser()
  const email = `e2e-${name}-${run}@example.test`.toLowerCase()
  const password = `e2e-${name}-password-${run}`
  await b.token()
  const reg = await b.http('POST', '/api/v1/auth/register', { json: { email, nickname: `${name}${run.slice(-3)}`, password } })
  if (reg.status !== 201) throw new Error(`register ${name}: ${reg.status} ${reg.text}`)
  const t = await mailToken(email, '/auth/verify')
  const v = await b.http('POST', '/api/v1/auth/verify-email', { json: { token: t } })
  if (v.status !== 200) throw new Error(`verify ${name}: ${v.status} ${v.text}`)
  const l = await b.login(email, password)
  if (l.status !== 200) throw new Error(`login ${name}: ${l.status} ${l.text}`)
  return Object.assign(b, { email, password, nickname: `${name}${run.slice(-3)}` })
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
  const raw = Buffer.alloc((w * 3 + 1) * h, 120)
  for (let y = 0; y < h; y++) raw[y * (w * 3 + 1)] = 0
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', ihdr), chunk('IDAT', deflateSync(raw)), chunk('IEND', Buffer.alloc(0))])
}

const draft = (over = {}) => ({
  title: '', summary: null, body_markdown: '', candidate_slug: null, category_id: null, tag_ids: [], cover_media_id: null,
  seo_title: null, seo_description: null, comments_enabled: true, ...over,
})

// ---- setup ----
const admin = browser()
log((await admin.login(adminEmail, adminPassword)).status === 200, 'admin login')
async function publish(slug, title, commentsEnabled = true) {
  const id = (await admin.http('POST', '/api/v1/admin/articles')).data.data.article_id
  const saved = await admin.http('PUT', `/api/v1/admin/articles/${id}/draft`, { json: { ...draft({ title, body_markdown: `${title} 的正文`, candidate_slug: slug, comments_enabled: commentsEnabled }), expected_revision: 1 } })
  const pub = await admin.http('POST', `/api/v1/admin/articles/${id}/publish`, { json: { expected_draft_revision: saved.data.data.revision } })
  if (pub.status !== 200) throw new Error(`publish ${slug}: ${pub.status} ${pub.text}`)
  return { id, slug, revision: saved.data.data.revision }
}
const art = await publish(`comments-${run}`, `评论测试 ${run}`)
const art2 = await publish(`comments2-${run}`, `限流测试 ${run}`)
const alice = await reader('alice')
const bob = await reader('bob')
const dave = await reader('dave')
log(!!alice && !!bob && !!dave, 'three readers registered and verified through Mailpit')

const anon = browser()
await anon.token()
const listPath = `/api/v1/articles/${art.id}/comments`

// ---- reading and permissions ----
let r = await anon.http('GET', listPath)
log(r.status === 200 && r.data.data.length === 0 && r.data.meta.comments_enabled === true, 'guest reads the (empty) comment list')
r = await anon.http('GET', `/posts/${art.slug}`)
log(r.status === 200 && r.text.includes('id="comments"') && !r.text.includes('@example.test'), 'SSR article page has the comment section and no account data')
log((await anon.http('POST', listPath, { json: { body_text: '游客' } })).code === 'AUTH_REQUIRED', 'guest cannot comment (401)')
const carol = browser()
await carol.token()
await carol.http('POST', '/api/v1/auth/register', { json: { email: `e2e-carol-${run}@example.test`, nickname: 'carol', password: `e2e-carol-password-${run}` } })
r = await carol.login(`e2e-carol-${run}@example.test`, `e2e-carol-password-${run}`)
log(r.code === 'EMAIL_NOT_VERIFIED' && (await carol.http('POST', listPath, { json: { body_text: '未验证' } })).status === 401, 'unverified account cannot sign in or comment')
log((await alice.http('POST', listPath, { json: { body_text: 'x' }, csrfHeader: false })).code === 'CSRF_INVALID', 'comment without CSRF token → 403')

// ---- posting, replies, tree ----
r = await alice.http('POST', listPath, { json: { body_text: '第一条评论 👍\n第二行 https://example.com' } })
const root = r.data?.data
log(r.status === 201 && root.status === 'VISIBLE' && root.author.nickname === alice.nickname, 'verified reader comments → 201 VISIBLE at once')
r = await anon.http('GET', listPath)
log(r.data.data[0]?.id === root.id && r.data.data[0].body_text.includes('第二行') && r.data.meta.comment_count === 1, 'the comment is public immediately (no review)')
log(!r.text.includes('@example.test') && !r.text.includes('blog_session') && !r.text.includes('csrf'), 'public comment JSON has no e-mail, session or token')
r = await bob.http('POST', `/api/v1/comments/${root.id}/replies`, { json: { body_text: `@${alice.nickname} 同意` } })
const reply = r.data?.data
log(r.status === 201 && reply.root_id === root.id && reply.parent_id === root.id && reply.reply_to.nickname === alice.nickname, 'reply binds the real parent and root')
r = await alice.http('POST', `/api/v1/comments/${reply.id}/replies`, { json: { body_text: '楼中楼回复' } })
const nested = r.data?.data
log(r.status === 201 && nested.root_id === root.id && nested.parent_id === reply.id && nested.reply_to.nickname === bob.nickname, 'nested reply keeps parent = reply, root = thread')
r = await anon.http('GET', `/api/v1/comments/${root.id}/replies`)
log(r.data.data.length === 2 && r.data.meta.total === 2, 'replies endpoint pages the thread')
r = await alice.http('POST', listPath, { json: { body_text: '<img src=x onerror=alert(1)><script>alert(2)</script>' } })
log(r.status === 201 && r.data.data.body_text === '<img src=x onerror=alert(1)><script>alert(2)</script>' && r.headers.get('content-type').includes('application/json'), 'HTML is stored and returned as plain text (rendered escaped by the UI)')
log((await alice.http('POST', listPath, { json: { body_text: '   ' } })).status === 422 && (await alice.http('POST', listPath, { json: { body_text: '长'.repeat(2001) } })).status === 422, 'empty / over-long comments → 422')

// ---- deletion and moderation ----
log((await bob.http('DELETE', `/api/v1/comments/${root.id}`)).status === 403, "a reader cannot delete someone else's comment")
log((await bob.http('POST', `/api/v1/admin/comments/${root.id}/hide`)).status === 403 && (await bob.http('GET', '/api/v1/admin/comments')).status === 403, 'a reader cannot use moderation endpoints')
log((await alice.http('DELETE', `/api/v1/comments/${root.id}`)).status === 204, 'author deletes own comment (with replies)')
r = await anon.http('GET', listPath)
const tomb = r.data.data.find(c => c.id === root.id)
log(tomb.status === 'DELETED_AUTHOR' && tomb.body_text === null && tomb.author === null && tomb.reply_count === 2 && !r.text.includes('第二行'), 'tombstone keeps the thread; text gone')
r = await admin.http('POST', `/api/v1/admin/comments/${reply.id}/hide`)
log(r.status === 200 && r.data.data.status === 'HIDDEN', 'admin hides a reply')
r = await anon.http('GET', `/api/v1/comments/${root.id}/replies`)
log(!r.text.includes('同意') && r.data.data.find(c => c.id === reply.id).body_text === null, 'hidden text is absent from public responses')
r = await admin.http('GET', `/api/v1/admin/comments?article_id=${art.id}&status=HIDDEN`)
log(r.data.data.length === 1 && r.data.data[0].body_text.includes('同意') && r.data.data[0].author.email === bob.email, 'admin list filters by article + status and shows the author')
const aliceId = (await alice.http('GET', '/api/v1/auth/me')).data.data.id
r = await admin.http('GET', `/api/v1/admin/comments?author_id=${aliceId}`)
log(r.status === 200 && r.data.data.length === 3 && r.data.data.every(c => c.author.id === aliceId), 'admin list filters by author')
log((await admin.http('POST', `/api/v1/admin/comments/${reply.id}/unhide`)).data.data.status === 'VISIBLE', 'admin restores the hidden reply')
log((await admin.http('DELETE', `/api/v1/admin/comments/${nested.id}`)).status === 204, 'admin deletes a comment (tombstone)')
r = await anon.http('GET', `/api/v1/comments/${root.id}/replies`)
log(!r.data.data.some(c => c.id === nested.id) && r.data.data.length === 1 && r.data.data[0].id === reply.id, 'admin-deleted leaf reply leaves no empty placeholder; the rest of the tree stays')

// ---- rate limit ----
let codes = []
// Fixed one-minute windows: a run crossing a boundary may get up to two windows' worth before the 429.
for (let i = 0; i < 12 && !codes.includes(429); i++) codes.push((await bob.http('POST', `/api/v1/articles/${art2.id}/comments`, { json: { body_text: `限流 ${i}` } })).status)
log(codes.filter(c => c === 201).length <= 10 && codes.at(-1) === 429, `comment rate limit (5/min): ${codes.join(',')}`)

// ---- closed and withdrawn ----
const detail = await admin.http('GET', `/api/v1/admin/articles/${art.id}`)
const saved = await admin.http('PUT', `/api/v1/admin/articles/${art.id}/draft`, { json: { ...draft({ title: `评论测试 ${run}`, body_markdown: '正文', candidate_slug: art.slug, comments_enabled: false }), expected_revision: detail.data.data.draft.revision } })
await admin.http('POST', `/api/v1/admin/articles/${art.id}/publish`, { json: { expected_draft_revision: saved.data.data.revision } })
r = await anon.http('GET', listPath)
log(r.data.meta.comments_enabled === false && r.data.data.length >= 2, 'comments closed: history still visible')
log((await dave.http('POST', listPath, { json: { body_text: '关闭后' } })).code === 'COMMENT_CLOSED' && (await dave.http('POST', `/api/v1/comments/${reply.id}/replies`, { json: { body_text: '关闭后回复' } })).code === 'COMMENT_CLOSED', 'comments closed: new comments and replies → 422 COMMENT_CLOSED')
await admin.http('POST', `/api/v1/admin/articles/${art.id}/withdraw`)
log((await anon.http('GET', listPath)).status === 404 && (await anon.http('GET', `/api/v1/comments/${root.id}/replies`)).status === 404, 'withdrawn article: comments are 404 for guests')
r = await alice.http('GET', '/api/v1/me/comments')
const onWithdrawn = r.data.data.find(c => c.id === root.id)
log(r.status === 200 && onWithdrawn.article.available === false && onWithdrawn.article.title === null, 'my comments: withdrawn article shown as unavailable without its title')

// ---- user centre ----
r = await anon.http('GET', '/account/profile')
log([301, 302, 307].includes(r.status) && r.headers.get('location').includes('/auth/login'), 'signed-out /account/profile → login')
r = await alice.http('GET', '/account/profile')
log(r.status === 200 && r.headers.get('cache-control').includes('no-store') && r.headers.get('x-robots-tag').includes('noindex'), 'user centre page: private, no-store, noindex')
r = await alice.http('PATCH', '/api/v1/me/profile', { json: { nickname: `爱丽丝${run.slice(-3)}`, bio: '喜欢写代码' } })
const me = (await alice.http('GET', '/api/v1/auth/me')).data.data
log(r.status === 200 && me.nickname === `爱丽丝${run.slice(-3)}` && me.bio === '喜欢写代码', 'profile: nickname and bio updated')
const form = new FormData()
form.append('file', new Blob([png(96, 96)], { type: 'image/png' }), 'avatar.png')
r = await alice.http('POST', '/api/v1/me/avatar', { body: form })
const avatarUrl = r.data?.data?.avatar_url
log(r.status === 200 && avatarUrl && (await anon.http('GET', avatarUrl)).status === 200, 'avatar uploaded and publicly readable')
r = await alice.http('POST', `/api/v1/articles/${art2.id}/comments`, { json: { body_text: '新头像的评论' } })
log(r.data.data.author.avatar_url === avatarUrl && r.data.data.author.nickname === `爱丽丝${run.slice(-3)}`, 'new comments carry the new nickname and avatar')

const newEmail = `e2e-alice-new-${run}@example.test`
r = await alice.http('POST', '/api/v1/me/change-email/request', { json: { new_email: newEmail } })
const changeToken = await mailToken(newEmail, '/auth/confirm-email')
log(r.status === 202 || r.status === 200, `change-email request → ${r.status}`)
log((await anon.http('POST', '/api/v1/me/change-email/confirm', { json: { token: changeToken } })).status === 401, 'change-email link alone (no session) cannot change the address')
r = await alice.http('POST', '/api/v1/me/change-email/confirm', { json: { token: changeToken } })
log(r.status === 200 && r.data.data.email === newEmail && (await alice.http('GET', '/api/v1/auth/me')).data.data.email === newEmail, 'change-email confirmed with session + CSRF')
const again = browser()
log((await again.login(newEmail, alice.password)).status === 200, 'login works with the new e-mail')

r = await bob.http('POST', '/api/v1/me/change-password', { json: { current_password: 'wrong-password-123', new_password: `bob-new-password-${run}` } })
log(r.status === 422, 'change-password with a wrong current password → 422')
r = await bob.http('POST', '/api/v1/me/change-password', { json: { current_password: bob.password, new_password: `bob-new-password-${run}` } })
log(r.status === 200 && r.data.data.reauthentication_required === true, 'change-password → re-login required')
log((await bob.http('GET', '/api/v1/me/comments')).status === 401, 'old session no longer opens the user centre')
const bob2 = browser()
log((await bob2.login(bob.email, `bob-new-password-${run}`)).status === 200 && (await bob2.login(bob.email, bob.password)).status === 401, 'new password works, old one does not')

log((await again.http('POST', '/api/v1/auth/logout-all')).status === 204, 'logout-all → 204')
log((await alice.http('GET', '/api/v1/me/comments')).status === 401 && (await again.http('GET', '/api/v1/me/comments')).status === 401, 'every session of the account is revoked')

// ---- cleanup ----
let ok = true
for (const a of [art, art2]) ok &&= [204].includes((await admin.http('DELETE', `/api/v1/admin/articles/${a.id}`)).status)
log(ok, 'cleanup: test articles moved to the recycle bin')
console.log(`\nAll ${step} checks passed`)
