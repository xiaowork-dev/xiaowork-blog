#!/usr/bin/env node
// End-to-end check of the P5 public blog against a running Docker Compose stack, through the Nuxt origin:
// SSR pages, pagination, categories/tags/archives, search (Chinese samples), SEO tags, sitemap/RSS/robots,
// old-slug 301, private content 404 and the view counter. Needs an admin: E2E_ADMIN_EMAIL / E2E_ADMIN_PASSWORD.
//
//   node scripts/e2e-public.mjs [WEB]                  full flow; its articles end in the recycle bin
//   node scripts/e2e-public.mjs [WEB] --seed FILE      publish readable sample articles and keep them (manual checks)
//   node scripts/e2e-public.mjs [WEB] --cleanup FILE   move the seeded articles to the recycle bin
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
const BROWSER_UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36'
const run = Date.now().toString(36)
const clientIP = `198.51.100.${1 + Math.floor(Math.random() * 250)}`
let step = 0
const log = (ok, msg) => {
  step++
  console.log(`${ok ? 'PASS' : 'FAIL'} ${String(step).padStart(2, '0')} ${msg}`)
  if (!ok) process.exit(1)
}

/** A cookie jar + CSRF token per simulated browser. */
function browser(ua = BROWSER_UA, ip = clientIP) {
  let cookie = ''
  let csrf = ''
  async function http(method, path, { json, body, redirect = 'manual', headers: extra = {} } = {}) {
    // Accept like a browser: Nuxt answers errors as HTML pages only when HTML is acceptable.
    const headers = { Origin: WEB, 'X-Forwarded-For': ip, 'User-Agent': ua, Accept: 'text/html,application/json;q=0.9,*/*;q=0.8', ...extra }
    if (cookie) headers.Cookie = cookie
    if (method !== 'GET' && csrf) headers['X-CSRF-Token'] = csrf
    if (json !== undefined) headers['Content-Type'] = 'application/json'
    const res = await fetch(WEB + path, { method, headers, redirect, body: json !== undefined ? JSON.stringify(json) : body })
    for (const c of res.headers.getSetCookie()) {
      const pair = c.split(';')[0]
      if (!pair.endsWith('=')) {
        const name = pair.split('=')[0]
        cookie = [...cookie.split('; ').filter(p => p && !p.startsWith(name + '=')), pair].join('; ')
      }
    }
    const text = await res.text()
    let data
    try { data = JSON.parse(text) } catch { data = undefined }
    return { status: res.status, headers: res.headers, text, data }
  }
  async function token() {
    csrf = (await http('GET', '/api/v1/auth/csrf')).data.data.csrf_token
  }
  return { http, token }
}

const anon = browser()
const get = path => anon.http('GET', path)
const count = (text, needle) => text.split(needle).length - 1
const meta = (html, attr, name) => {
  const m = new RegExp(`<meta[^>]*${attr}="${name}"[^>]*content="([^"]*)"`, 'i').exec(html)
    ?? new RegExp(`<meta[^>]*content="([^"]*)"[^>]*${attr}="${name}"`, 'i').exec(html)
  return m ? m[1] : null
}
const canonical = html => /<link[^>]*rel="canonical"[^>]*href="([^"]*)"/i.exec(html)?.[1] ?? null
const title = html => /<title>([^<]*)<\/title>/i.exec(html)?.[1] ?? null

function png(w, h, shade) {
  const chunk = (type, data) => {
    const len = Buffer.alloc(4); len.writeUInt32BE(data.length)
    const td = Buffer.concat([Buffer.from(type), data])
    const crc = Buffer.alloc(4); crc.writeUInt32BE(crc32(td))
    return Buffer.concat([len, td, crc])
  }
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4); ihdr[8] = 8; ihdr[9] = 2
  const row = w * 3 + 1
  const raw = Buffer.alloc(row * h)
  for (let y = 0; y < h; y++) {
    raw[y * row] = 0
    for (let x = 0; x < w; x++) {
      const o = y * row + 1 + x * 3
      raw[o] = shade[0] + Math.round(40 * y / h); raw[o + 1] = shade[1] + Math.round(30 * x / w); raw[o + 2] = shade[2]
    }
  }
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', ihdr),
    chunk('IDAT', deflateSync(raw)), chunk('IEND', Buffer.alloc(0))])
}

const admin = browser()
async function login() {
  await admin.token()
  const r = await admin.http('POST', '/api/v1/auth/login', { json: { email, password } })
  log(r.status === 200 && r.data?.data?.user?.role === 'ADMIN', `admin login → ${r.status}`)
  await admin.token()
}

const draft = (over = {}) => ({
  title: '', summary: null, body_markdown: '', candidate_slug: null, category_id: null, tag_ids: [], cover_media_id: null,
  seo_title: null, seo_description: null, comments_enabled: true, ...over,
})

async function publish(fields) {
  const created = await admin.http('POST', '/api/v1/admin/articles')
  const id = created.data.data.article_id
  const saved = await admin.http('PUT', `/api/v1/admin/articles/${id}/draft`, { json: { ...draft(fields), expected_revision: 1 } })
  if (saved.status !== 200) throw new Error(`save ${fields.candidate_slug}: ${saved.status} ${saved.text}`)
  const pub = await admin.http('POST', `/api/v1/admin/articles/${id}/publish`, { json: { expected_draft_revision: saved.data.data.revision } })
  if (pub.status !== 200) throw new Error(`publish ${fields.candidate_slug}: ${pub.status} ${pub.text}`)
  return { id, revision: saved.data.data.revision }
}

async function upload(name, w, h, shade) {
  const form = new FormData()
  form.append('usage_kind', 'ARTICLE')
  form.append('file', new Blob([png(w, h, shade)], { type: 'image/png' }), name)
  const r = await admin.http('POST', '/api/v1/admin/media', { body: form })
  if (r.status !== 201) throw new Error(`upload: ${r.status} ${r.text}`)
  return r.data.data
}

const LONG_BODY = (n, img) => [
  `本文是第 ${n} 篇示例文章，用于验证博客前台的阅读体验。`, '',
  '## 项目背景', '', '我一直想拥有一个属于自己的博客，用来记录技术学习、工作经验和生活感悟。', '',
  '## 技术选型', '', '### 前端', '', '前端使用 Nuxt 4 与 Vue 3，服务端渲染保证搜索引擎可以索引正文。', '',
  '### 后端', '', '后端使用 Go 与 Chi，数据库为 PostgreSQL 18。', '',
  '```go', 'package main', '', 'import "fmt"', '', 'func main() {', '\tfmt.Println("你好，XiaoWork Blog：这一行很长很长，用来测试代码块在小屏幕上是否可以横向滚动而不撑破页面布局")', '}', '```', '',
  '| 组件 | 版本 | 说明 |', '|---|---|---|', '| Nuxt | 4 | 前台 SSR |', '| Go | 1.27 | API 服务 |', '| PostgreSQL | 18 | 数据库，持久化到数据卷，备份后可以恢复到隔离环境验证 |', '',
  img ? `![架构示意图](${img})` : '', '',
  '## 部署过程', '', '- [x] 编写 Docker Compose', '- [ ] 配置 Nginx Proxy Manager', '', '> 引用：先完成，再完美。', '',
  '一个很长的链接：https://example.com/a/very/long/path/that/should/wrap/instead/of/overflowing/the/layout/on/small/screens', '',
  '## 总结', '', '以上就是本文的全部内容。',
].join('\n')

if (mode === '--cleanup') {
  await login()
  const { ids } = JSON.parse(readFileSync(stateFile, 'utf8'))
  let ok = true
  for (const id of ids) ok &&= (await admin.http('DELETE', `/api/v1/admin/articles/${id}`)).status === 204
  log(ok, `cleanup: ${ids.length} seeded articles moved to the recycle bin`)
  process.exit(0)
}

await login()

// ---- fixtures ----
const cat = (await admin.http('POST', '/api/v1/admin/categories', { json: { name: `开发笔记 ${run}`, slug: `pub-${run}`, description: '技术学习与开发记录' } })).data.data
const cat2 = (await admin.http('POST', '/api/v1/admin/categories', { json: { name: `生活随笔 ${run}`, slug: `life-${run}` } })).data.data
const tagGo = (await admin.http('POST', '/api/v1/admin/tags', { json: { name: `Go-${run}`, slug: `go-${run}` } })).data.data
const tagDocker = (await admin.http('POST', '/api/v1/admin/tags', { json: { name: `Docker-${run}`, slug: `docker-${run}` } })).data.data
const cover = await upload('cover.png', 640, 360, [40, 90, 160])
const figure = await upload('figure.png', 960, 420, [200, 160, 80])
log(!!cat?.id && !!tagGo?.id && !!cover?.id && !!figure?.id, 'fixtures: categories, tags, cover and figure uploaded')

const ids = []
const slugs = []
// 11 articles in one category → two pages of 10 everywhere.
for (let i = 1; i <= 11; i++) {
  const n = String(i).padStart(2, '0')
  const slug = `pub-${run}-${n}`
  const special = i === 11
  const fields = {
    title: special ? `从零搭建个人博客系统 ${run}` : `示例文章 ${n} · ${run}`,
    summary: special ? '记录如何使用 Nuxt 4、Go 和 PostgreSQL 从零搭建一个现代化的个人博客系统。' : `第 ${n} 篇示例文章的摘要。`,
    body_markdown: special ? LONG_BODY(i, figure.url) : (i === 5 ? '使用 Docker Compose 部署容器化应用，配置数据卷持久化。' : `第 ${n} 篇正文。`),
    candidate_slug: slug,
    category_id: i % 4 === 0 ? cat2.id : cat.id,
    tag_ids: i % 2 ? [tagGo.id] : [tagGo.id, tagDocker.id],
    cover_media_id: special || i === 9 ? cover.id : null,
    seo_title: special ? `从零搭建博客 SEO 标题 ${run}` : null,
    seo_description: special ? `SEO 描述：Nuxt 与 Go 的博客实践 ${run}` : null,
  }
  const { id } = await publish(fields)
  ids.push(id)
  slugs.push(slug)
}
log(ids.length === 11, 'published 11 public articles')

if (mode === '--seed') {
  writeFileSync(stateFile, JSON.stringify({ ids, slugs, run }))
  console.log(`seeded: ${WEB}/posts/${slugs[10]}`)
  process.exit(0)
}

// Non-public articles with unique marker words.
const draftWord = `草稿暗号${run}`
const withdrawnWord = `撤回暗号${run}`
const trashedWord = `删除暗号${run}`
const draftArticle = (await admin.http('POST', '/api/v1/admin/articles')).data.data.article_id
await admin.http('PUT', `/api/v1/admin/articles/${draftArticle}/draft`, { json: { ...draft({ title: draftWord, body_markdown: draftWord, candidate_slug: `draft-${run}`, category_id: cat.id }), expected_revision: 1 } })
const withdrawn = await publish({ title: withdrawnWord, body_markdown: withdrawnWord, candidate_slug: `withdrawn-${run}`, category_id: cat.id, tag_ids: [tagGo.id] })
await admin.http('POST', `/api/v1/admin/articles/${withdrawn.id}/withdraw`)
const trashed = await publish({ title: trashedWord, body_markdown: trashedWord, candidate_slug: `trashed-${run}`, category_id: cat.id })
await admin.http('DELETE', `/api/v1/admin/articles/${trashed.id}`)
ids.push(draftArticle, withdrawn.id, trashed.id)

// ---- home & pagination ----
let r = await get('/')
log(r.status === 200 && count(r.text, 'class="article-card') === 10, `home SSR lists 10 articles (${count(r.text, 'class="article-card')})`)
log(r.text.includes(`从零搭建个人博客系统 ${run}`) && r.text.includes('href="/?page=2"') && r.text.includes('rel="next"'), 'home shows the newest article first and a crawlable link to page 2')
log(canonical(r.text) === `${WEB}/` && meta(r.text, 'name', 'robots') === 'index, follow', 'home canonical and robots meta')
log(r.text.includes('class="profile-card"') && r.text.includes('xwb:theme'), 'profile column rendered, theme applied before paint')
log(!/(标签云|文章统计|天气)/.test(r.text.slice(r.text.indexOf('class="profile-card"'), r.text.indexOf('</aside>'))), 'profile column has no extra widgets')
r = await get('/?page=2')
log(r.status === 200 && canonical(r.text) === `${WEB}/?page=2`, 'home page 2 → 200 with its own canonical')
log((await get('/?page=99999')).status === 404 && (await get('/?page=abc')).status === 404 && (await get('/?page=0')).status === 404, 'out-of-range or malformed ?page= → 404')

// ---- categories / tags / archives ----
r = await get('/categories')
log(r.status === 200 && r.text.includes(`开发笔记 ${run}`) && r.text.includes('9 篇'), 'categories list with public counts (withdrawn/trashed excluded)')
r = await get(`/categories/pub-${run}`)
log(r.status === 200 && count(r.text, 'class="article-card') === 9 && r.text.includes('技术学习与开发记录'), 'category detail lists its 9 public articles')
r = await get(`/tags/docker-${run}?page=1`)
log(r.status === 200 && count(r.text, 'class="article-card') === 5, `tag detail lists 5 articles (${count(r.text, 'class="article-card')})`)
r = await get(`/tags/go-${run}`)
log(r.status === 200 && count(r.text, 'class="article-card') === 10 && r.text.includes(`href="/tags/go-${run}?page=2"`), 'tag detail paginates (11 articles → 2 pages)')
log((await get(`/categories/nope-${run}`)).status === 404 && (await get(`/tags/nope-${run}`)).status === 404, 'unknown category/tag → 404')
r = await get('/archives')
log(r.status === 200 && r.text.includes(`示例文章 01 · ${run}`) && !r.text.includes(withdrawnWord) && !r.text.includes(trashedWord), 'archives list public articles only')
log((await get('/archives?year=1999')).status === 404, 'empty archive year → 404')

// ---- article page ----
const special = slugs[10]
r = await get(`/posts/${special}`)
const html = r.text
log(r.status === 200 && title(html) === `从零搭建博客 SEO 标题 ${run} · XiaoWork Blog`, `article <title> = ${title(html)}`)
log(meta(html, 'name', 'description') === `SEO 描述：Nuxt 与 Go 的博客实践 ${run}` && canonical(html) === `${WEB}/posts/${special}`, 'article description and canonical')
log(meta(html, 'property', 'og:type') === 'article' && meta(html, 'property', 'og:image') === `${WEB}${cover.url}` && meta(html, 'property', 'og:url') === `${WEB}/posts/${special}` && !!meta(html, 'property', 'article:published_time'), 'Open Graph tags (type, url, absolute cover image, published time)')
log(html.includes('"@type":"BlogPosting"'), 'JSON-LD BlogPosting present')
log(html.includes('以上就是本文的全部内容') && html.includes('<h2 id="项目背景">') && html.includes('class="toc-side"') && html.includes('href="#技术选型"'), 'SSR body is indexable, headings have anchors, TOC rendered')
log(html.includes('class="code-copy"') && html.includes('class="shiki') && html.includes('class="md-table"') && /<img[^>]*loading="lazy"/.test(html), 'code highlight + copy button, table wrapper, lazy images')
log(html.includes(`/tags/go-${run}`) && html.includes('rel="prev"') && html.includes('次阅读'), 'tags, previous-article link and view count shown')
log(html.includes('id="comments"') && !html.includes('class="comment-thread"'), 'comment section present; comments load in the browser (nothing pre-rendered or faked by SSR)')

// ---- old slug 301, private content 404 ----
const sp = ids[10]
const detail = await admin.http('GET', `/api/v1/admin/articles/${sp}`)
const rev = detail.data.data.draft.revision
const resaved = await admin.http('PUT', `/api/v1/admin/articles/${sp}/draft`, { json: { ...draft({
  title: `从零搭建个人博客系统 ${run}`, body_markdown: LONG_BODY(11, figure.url), candidate_slug: `${special}-new`, category_id: cat.id,
  tag_ids: [tagGo.id], cover_media_id: cover.id, summary: '记录如何使用 Nuxt 4、Go 和 PostgreSQL 从零搭建一个现代化的个人博客系统。',
}), expected_revision: rev } })
await admin.http('POST', `/api/v1/admin/articles/${sp}/publish`, { json: { expected_draft_revision: resaved.data.data.revision } })
r = await get(`/posts/${special}`)
log(r.status === 301 && r.headers.get('location')?.endsWith(`/posts/${special}-new`), `old slug → 301 ${r.headers.get('location')}`)
slugs[10] = `${special}-new`
log((await get(`/posts/draft-${run}`)).status === 404, 'draft → 404')
const w404 = await get(`/posts/withdrawn-${run}`)
log(w404.status === 404 && !w404.text.includes(withdrawnWord) && meta(w404.text, 'name', 'robots')?.includes('noindex'), 'withdrawn → 404 without its title, noindex')
log((await get(`/posts/trashed-${run}`)).status === 404, 'trashed → 404')

// ---- search ----
const search = q => get(`/api/v1/search/articles?q=${encodeURIComponent(q)}`)
r = await search('容器化')
log(r.status === 200 && r.data.data.some(a => a.slug === slugs[4]), 'search “容器化” finds the Docker article')
r = await search(`搭建 ${run}`)
log(r.data.meta.total === 1 && r.data.data[0].slug === slugs[10], 'multi-term search (Chinese + run id) finds exactly one article')
r = await search(`示例文章 ${run}`)
// 10 titles + the long article whose body says “第 11 篇示例文章”
log(r.data.meta.total === 11 && r.data.meta.total_pages === 2 && !r.data.data.some(a => a.slug === slugs[10]) && (await get(`/api/v1/search/articles?q=${encodeURIComponent(`示例文章 ${run}`)}&page=2`)).data.data[0]?.slug === slugs[10] && (await get(`/api/v1/search/articles?q=${encodeURIComponent(`示例文章 ${run}`)}&page_size=3&page=4`)).data.data.length === 2, 'search paginates (11 hits; the body-only hit ranks after title hits, on page 2)')
for (const word of [draftWord, withdrawnWord, trashedWord]) {
  r = await search(word)
  log(r.status === 200 && r.data.meta.total === 0, `search never returns non-public content (${word.slice(0, 4)})`)
}
r = await get(`/search?q=${encodeURIComponent('容器')}`)
log(r.status === 200 && meta(r.text, 'name', 'robots') === 'noindex, follow' && r.text.includes('value="容器"'), 'search page prefilled from URL and not indexed')
log((await search('')).status === 422, 'empty query → 422')

// ---- sitemap / rss / robots ----
r = await get('/sitemap.xml')
log(r.status === 200 && r.headers.get('content-type')?.includes('xml') && r.text.includes(`<loc>${WEB}/posts/${slugs[0]}</loc>`) && r.text.includes(`<loc>${WEB}/posts/${slugs[10]}</loc>`), 'sitemap lists public articles with absolute URLs')
log(!r.text.includes(`withdrawn-${run}`) && !r.text.includes(`draft-${run}`) && !r.text.includes(`trashed-${run}`) && !r.text.includes(`<loc>${WEB}/posts/${special}</loc>`), 'sitemap excludes draft, withdrawn, trashed and old slugs')
log(r.text.includes(`/categories/pub-${run}`) && r.text.includes(`/tags/go-${run}`), 'sitemap lists categories and tags with public articles')
r = await get('/rss.xml')
log(r.status === 200 && r.headers.get('content-type')?.includes('rss') && r.text.includes(`<link>${WEB}/posts/${slugs[10]}</link>`) && !r.text.includes(withdrawnWord) && !r.text.includes(trashedWord), 'RSS lists the newest public articles only')
r = await get('/robots.txt')
log(r.status === 200 && r.text.includes('Disallow: /admin') && r.text.includes('Disallow: /account') && r.text.includes(`Sitemap: ${WEB}/sitemap.xml`), 'robots.txt blocks private areas and points to the sitemap')
log((await get('/admin/articles')).headers.get('x-robots-tag')?.includes('noindex') && (await get('/auth/login')).headers.get('x-robots-tag')?.includes('noindex'), 'admin and auth pages send X-Robots-Tag: noindex')

// ---- view counter ----
const views = async () => (await get(`/api/v1/articles/${slugs[10]}`)).data.data.view_count
const before = await views()
const reader = browser(BROWSER_UA, `198.51.100.${251 + Math.floor(Math.random() * 4)}`)
await reader.token()
log((await reader.http('POST', `/api/v1/articles/${sp}/views`)).status === 204, 'view report → 204')
await reader.http('POST', `/api/v1/articles/${sp}/views`)
await reader.http('POST', `/api/v1/articles/${sp}/views`)
log(await views() === before + 1, 'three reports from one browser count once')
const bot = browser('Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)')
await bot.token()
await bot.http('POST', `/api/v1/articles/${sp}/views`)
const noToken = browser()
log((await noToken.http('POST', `/api/v1/articles/${sp}/views`)).status === 403, 'view report without CSRF token → 403')
log(await views() === before + 1, 'bot report not counted')
log((await reader.http('POST', `/api/v1/articles/${draftArticle}/views`)).status === 404, 'views on a draft → 404')
log((await get(`/posts/${slugs[10]}`)).text.includes(`${before + 1} 次阅读`), 'article page shows the counted views (SSR does not count)')
log(await views() === before + 1, 'rendering the page did not add a view')

// ---- cleanup ----
let cleaned = true
for (const id of ids) {
  const st = (await admin.http('DELETE', `/api/v1/admin/articles/${id}`)).status
  cleaned &&= st === 204
}
log(cleaned, `cleanup: ${ids.length} test articles moved to the recycle bin`)
log(!(await get('/sitemap.xml')).text.includes(`pub-${run}-01`), 'after cleanup the sitemap no longer lists them')

console.log(`\nAll ${step} checks passed`)
