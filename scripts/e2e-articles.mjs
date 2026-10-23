#!/usr/bin/env node
// End-to-end check of P4 articles against a running Docker Compose stack, through the Nuxt origin
// (admin API via the /api/v1 proxy, public pages rendered by Nuxt SSR). Needs an admin account:
// E2E_ADMIN_EMAIL / E2E_ADMIN_PASSWORD.
//
//   node scripts/e2e-articles.mjs [WEB]                 full flow (leaves its article in the recycle bin)
//   node scripts/e2e-articles.mjs [WEB] --keep FILE     publish one article and record its slug
//   node scripts/e2e-articles.mjs [WEB] --verify FILE   after a rebuild: still public, then move to recycle bin
import { readFileSync, writeFileSync } from 'node:fs'
import { crc32, deflateSync } from 'node:zlib'

const args = process.argv.slice(2)
const WEB = args[0] && !args[0].startsWith('--') ? args.shift() : 'http://127.0.0.1:3000'
const mode = args[0] ?? 'full'
const stateFile = args[1]
const { E2E_ADMIN_EMAIL: email, E2E_ADMIN_PASSWORD: password } = process.env
if (!email || !password) {
  console.error('E2E_ADMIN_EMAIL and E2E_ADMIN_PASSWORD are required')
  process.exit(2)
}
const run = Date.now().toString(36)
const clientIP = `203.0.113.${1 + Math.floor(Math.random() * 250)}`
let cookie = ''
let csrf = ''
let step = 0
const log = (ok, msg) => {
  step++
  console.log(`${ok ? 'PASS' : 'FAIL'} ${String(step).padStart(2, '0')} ${msg}`)
  if (!ok) process.exit(1)
}

async function http(method, path, { json, body, auth = true, redirect = 'manual' } = {}) {
  const headers = { Origin: WEB, 'X-Forwarded-For': clientIP }
  if (auth && cookie) headers.Cookie = cookie
  if (auth && method !== 'GET' && csrf) headers['X-CSRF-Token'] = csrf
  if (json !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch(WEB + path, { method, headers, redirect, body: json !== undefined ? JSON.stringify(json) : body })
  if (auth) {
    for (const c of res.headers.getSetCookie()) {
      const pair = c.split(';')[0]
      if (!pair.endsWith('=')) {
        const name = pair.split('=')[0]
        cookie = [...cookie.split('; ').filter(p => p && !p.startsWith(name + '=')), pair].join('; ')
      }
    }
  }
  const text = await res.text()
  let data
  try { data = JSON.parse(text) } catch { data = undefined }
  return { status: res.status, headers: res.headers, text, data }
}

async function login() {
  csrf = (await http('GET', '/api/v1/auth/csrf')).data.data.csrf_token
  const r = await http('POST', '/api/v1/auth/login', { json: { email, password } })
  log(r.status === 200 && r.data?.data?.user?.role === 'ADMIN', `admin login → ${r.status}`)
  csrf = (await http('GET', '/api/v1/auth/csrf')).data.data.csrf_token
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
  const raw = Buffer.alloc((w * 3 + 1) * h, 90)
  for (let y = 0; y < h; y++) raw[y * (w * 3 + 1)] = 0 // each scanline starts with filter type 0
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw)), chunk('IEND', Buffer.alloc(0))])
}

const draft = (over = {}) => ({
  title: '', summary: null, body_markdown: '', candidate_slug: null, category_id: null, tag_ids: [], cover_media_id: null,
  seo_title: null, seo_description: null, comments_enabled: true, ...over,
})

async function createPublished(slug, title, body) {
  const created = await http('POST', '/api/v1/admin/articles')
  const id = created.data.data.article_id
  const saved = await http('PUT', `/api/v1/admin/articles/${id}/draft`, { json: { ...draft({ title, body_markdown: body, candidate_slug: slug }), expected_revision: 1 } })
  const pub = await http('POST', `/api/v1/admin/articles/${id}/publish`, { json: { expected_draft_revision: saved.data.data.revision } })
  return { id, pub }
}

await login()

if (mode === '--keep') {
  const slug = `persist-${run}`
  const { id, pub } = await createPublished(slug, `持久化探针 ${run}`, `# 持久化\n\n容器重建前发布的文章 ${run}`)
  log(pub.status === 200, `publish persistence probe /posts/${slug} → ${pub.status}`)
  writeFileSync(stateFile, JSON.stringify({ id, slug, run }))
  process.exit(0)
}
if (mode === '--verify') {
  const { id, slug, run: probe } = JSON.parse(readFileSync(stateFile, 'utf8'))
  const page = await http('GET', `/posts/${slug}`, { auth: false })
  log(page.status === 200 && page.text.includes(`容器重建前发布的文章 ${probe}`), `after rebuild: /posts/${slug} still served by SSR (${page.status})`)
  const t = await http('DELETE', `/api/v1/admin/articles/${id}`)
  log(t.status === 204, 'cleanup: probe moved to recycle bin')
  process.exit(0)
}

// ---- full flow ----
const cat = await http('POST', '/api/v1/admin/categories', { json: { name: `E2E 分类 ${run}`, slug: `e2e-cat-${run}` } })
const tag = await http('POST', '/api/v1/admin/tags', { json: { name: `e2e-${run}`, slug: `e2e-tag-${run}` } })
log(cat.status === 201 && tag.status === 201, 'create category and tag')

const form = new FormData()
form.append('usage_kind', 'ARTICLE')
form.append('file', new Blob([png(320, 200)], { type: 'image/png' }), 'e2e-figure.png')
const img = await http('POST', '/api/v1/admin/media', { body: form })
const imgUrl = img.data?.data?.url
log(img.status === 201, `upload article image → ${img.status} ${img.status === 201 ? '' : img.text}`)

const created = await http('POST', '/api/v1/admin/articles')
const id = created.data?.data?.article_id
log(created.status === 201 && created.data.data.draft.revision === 1 && created.data.data.status === 'DRAFT', 'new article is an empty draft (revision 1)')

const slug = `e2e-${run}`
const body1 = [
  '# 第一版标题', '', '正文 **V1**。', '', `![示意图](${imgUrl})`, '', '```go', 'fmt.Println("hi")', '```', '',
  '- [x] 任务', '', '<script>alert("xss")</script>', '', '[恶意链接](javascript:alert(1))', '',
].join('\n')
let r = await http('PUT', `/api/v1/admin/articles/${id}/draft`, { json: { ...draft({
  title: `E2E V1 ${run}`, body_markdown: body1, candidate_slug: slug, category_id: cat.data.data.id, tag_ids: [tag.data.data.id],
}), expected_revision: 1 } })
let rev = r.data?.data?.revision
log(r.status === 200 && rev === 2, 'autosave draft → revision 2')

r = await http('PUT', `/api/v1/admin/articles/${id}/draft`, { json: { ...draft({ title: 'stale' }), expected_revision: 1 } })
log(r.status === 409 && r.data?.error?.code === 'DRAFT_REVISION_CONFLICT' && r.data.error.details.current_revision === 2, 'stale revision → 409 DRAFT_REVISION_CONFLICT')

log((await http('GET', `/posts/${slug}`, { auth: false })).status === 404, 'unpublished draft is not public (404 page)')
log((await http('GET', imgUrl, { auth: false })).status === 404, 'draft image is private (404)')

r = await http('POST', `/api/v1/admin/articles/${id}/publish`, { json: { expected_draft_revision: rev } })
log(r.status === 200 && r.data.data.published_changed === true && r.data.data.canonical_path === `/posts/${slug}`, 'first publish → 200')
r = await http('POST', `/api/v1/admin/articles/${id}/publish`, { json: { expected_draft_revision: rev } })
log(r.status === 200 && r.data.data.published_changed === false && r.data.data.publication_revision === 1, 'repeat publish is idempotent')

let page = await http('GET', `/posts/${slug}`, { auth: false })
log(page.status === 200 && page.text.includes(`E2E V1 ${run}`) && page.text.includes('<h2 id="第一版标题">第一版标题'), 'SSR public page renders the published Markdown')
log(page.text.includes('class="shiki') && page.text.includes('task-checkbox'), 'code highlighting and task list rendered')
const bodyHtml = page.text.slice(page.text.indexOf('class="md-body'))
log(bodyHtml.includes('&lt;script&gt;alert') && !/<script>alert\("xss"\)/.test(bodyHtml) && !bodyHtml.includes('href="javascript:'), 'XSS payload in Markdown is escaped, javascript: link not rendered')
log((await http('GET', imgUrl, { auth: false })).status === 200, 'published image is public')

r = await http('PUT', `/api/v1/admin/articles/${id}/draft`, { json: { ...draft({
  title: `E2E V2 ${run}`, body_markdown: '# 第二版\n\n正文 V2', candidate_slug: `${slug}-v2`, category_id: cat.data.data.id,
}), expected_revision: rev } })
rev = r.data?.data?.revision
page = await http('GET', `/posts/${slug}`, { auth: false })
log(r.status === 200 && page.status === 200 && page.text.includes(`E2E V1 ${run}`) && !page.text.includes('E2E V2'), 'editing the draft does not change the public page')

const detail = await http('GET', `/api/v1/admin/articles/${id}`)
log(detail.data?.data?.has_unpublished_changes === true && detail.data.data.status === 'PUBLISHED', 'admin sees "has unpublished changes"')

r = await http('POST', `/api/v1/admin/articles/${id}/publish`, { json: { expected_draft_revision: rev } })
log(r.status === 200 && r.data.data.publication_revision === 2, 'publish update → publication #2')
page = await http('GET', `/posts/${slug}-v2`, { auth: false })
log(page.status === 200 && page.text.includes(`E2E V2 ${run}`), 'public page shows the new version')
const old = await http('GET', `/posts/${slug}`, { auth: false })
log(old.status === 301 && (old.headers.get('location') ?? '').endsWith(`/posts/${slug}-v2`), `old slug → 301 ${old.headers.get('location')}`)
log((await http('GET', imgUrl, { auth: false })).status === 404, 'image removed from the new version is private again')

const anonAdmin = await http('GET', '/api/v1/admin/articles', { auth: false })
log(anonAdmin.status === 401, 'anonymous admin API → 401')
const adminPage = await http('GET', '/admin/articles', { auth: false })
log([301, 302, 307].includes(adminPage.status) && (adminPage.headers.get('location') ?? '').includes('/auth/login'), 'anonymous /admin/articles → redirect to login')
const editPage = await http('GET', `/admin/articles/${id}/edit`)
log(editPage.status === 200 && editPage.headers.get('cache-control')?.includes('no-store'), 'admin editor page served (private, no-store)')

r = await http('POST', `/api/v1/admin/articles/${id}/withdraw`)
log(r.status === 200 && r.data.data.status === 'WITHDRAWN', 'withdraw → WITHDRAWN')
log((await http('GET', `/posts/${slug}-v2`, { auth: false })).status === 404 && (await http('GET', `/posts/${slug}`, { auth: false })).status === 404, 'withdrawn article and its old slug are 404')
r = await http('DELETE', `/api/v1/admin/articles/${id}`)
log(r.status === 204, 'move to recycle bin → 204')
r = await http('POST', `/api/v1/admin/articles/${id}/restore`)
log(r.status === 200 && r.data.data.status === 'WITHDRAWN', 'restore → WITHDRAWN (not republished)')
log((await http('GET', `/posts/${slug}-v2`, { auth: false })).status === 404, 'restored article stays private')
r = await http('DELETE', `/api/v1/admin/articles/${id}`)
log(r.status === 204, 'cleanup: article back in the recycle bin')

console.log(`\nAll ${step} checks passed`)
