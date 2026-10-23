#!/usr/bin/env node
// End-to-end check of P3 media against a running Docker Compose stack, through the Nuxt origin
// (browser path: Nuxt → /api/v1 and /media proxies → Go API → PostgreSQL + media volume).
// Needs an admin account: E2E_ADMIN_EMAIL / E2E_ADMIN_PASSWORD (create it with blog-admin).
//
//   node scripts/e2e-media.mjs [WEB]                    full flow; leaves nothing behind
//   node scripts/e2e-media.mjs [WEB] --keep FILE        upload one image and record its id + sha256 in FILE
//   node scripts/e2e-media.mjs [WEB] --verify FILE      after a rebuild: same bytes served, then delete it
import { createHash } from 'node:crypto'
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

const clientIP = `203.0.113.${1 + Math.floor(Math.random() * 250)}`
let cookie = ''
let csrf = ''
let step = 0
function log(ok, msg) {
  step++
  console.log(`${ok ? 'PASS' : 'FAIL'} ${String(step).padStart(2, '0')} ${msg}`)
  if (!ok) process.exit(1)
}

async function http(method, path, { body, json, auth = true } = {}) {
  const headers = { Origin: WEB, 'X-Forwarded-For': clientIP }
  if (auth && cookie) headers.Cookie = cookie
  if (method !== 'GET' && method !== 'HEAD' && csrf) headers['X-CSRF-Token'] = csrf
  if (json !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch(WEB + path, { method, headers, body: json !== undefined ? JSON.stringify(json) : body })
  for (const c of res.headers.getSetCookie()) {
    const pair = c.split(';')[0]
    if (auth && !pair.endsWith('=')) {
      const name = pair.split('=')[0]
      cookie = [...cookie.split('; ').filter(p => p && !p.startsWith(name + '=')), pair].join('; ')
    }
  }
  const buf = Buffer.from(await res.arrayBuffer())
  let data
  try { data = JSON.parse(buf.toString()) } catch { data = undefined }
  return { status: res.status, headers: res.headers, buf, data }
}

async function login() {
  csrf = (await http('GET', '/api/v1/auth/csrf')).data.data.csrf_token
  const r = await http('POST', '/api/v1/auth/login', { json: { email, password } })
  log(r.status === 200 && r.data?.data?.user?.role === 'ADMIN', `admin login → ${r.status}`)
  csrf = (await http('GET', '/api/v1/auth/csrf')).data.data.csrf_token
}

// Minimal PNG encoder (RGB gradient) so the script needs no image libraries.
function png(w, h) {
  const chunk = (type, data) => {
    const len = Buffer.alloc(4); len.writeUInt32BE(data.length)
    const td = Buffer.concat([Buffer.from(type), data])
    const crc = Buffer.alloc(4); crc.writeUInt32BE(crc32(td))
    return Buffer.concat([len, td, crc])
  }
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4); ihdr[8] = 8; ihdr[9] = 2
  const raw = Buffer.alloc((w * 3 + 1) * h)
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const o = y * (w * 3 + 1) + 1 + x * 3
      raw[o] = x % 256; raw[o + 1] = y % 256; raw[o + 2] = 160
    }
  }
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', ihdr), chunk('IDAT', deflateSync(raw)), chunk('IEND', Buffer.alloc(0))])
}

async function upload(name, bytes, type = 'image/png') {
  const form = new FormData()
  form.append('usage_kind', 'ARTICLE')
  form.append('file', new Blob([bytes], { type }), name)
  return http('POST', '/api/v1/admin/media', { body: form })
}

const sha = b => createHash('sha256').update(b).digest('hex')

await login()

if (mode === '--keep') {
  const img = png(640, 400)
  const r = await upload(`persist-${Date.now()}.png`, img)
  log(r.status === 201, `upload persistence probe → ${r.status}`)
  writeFileSync(stateFile, JSON.stringify({ id: r.data.data.id, sha256: sha(img) }))
  console.log(`recorded ${r.data.data.id} in ${stateFile}`)
  process.exit(0)
}

if (mode === '--verify') {
  const { id, sha256 } = JSON.parse(readFileSync(stateFile, 'utf8'))
  const r = await http('GET', `/media/${id}/file`)
  log(r.status === 200 && sha(r.buf) === sha256, `after rebuild: ${id} served with identical SHA-256 (${r.status})`)
  const t = await http('GET', `/media/${id}/file?variant=thumb`)
  log(t.status === 200 && t.buf.length < r.buf.length, 'after rebuild: thumbnail still present')
  const d = await http('DELETE', `/api/v1/admin/media/${id}`)
  log(d.status === 204, 'cleanup: probe image deleted')
  process.exit(0)
}

const img = png(800, 600)
const up = await upload('e2e-gradient.png', img)
const media = up.data?.data
log(up.status === 201 && media?.width === 800 && media?.visibility === 'PRIVATE_UNTIL_REFERENCED', `upload PNG via Nuxt proxy → ${up.status}`)

const list = await http('GET', '/api/v1/admin/media?q=e2e-gradient')
log(list.status === 200 && list.data.data.some(m => m.id === media.id), 'media library lists and searches the upload')

const get = await http('GET', media.url)
log(get.status === 200 && sha(get.buf) === sha(img) && get.headers.get('content-type') === 'image/png'
  && get.headers.get('cache-control') === 'private, no-store', 'admin reads the original byte-for-byte (private, no-store)')

const head = await http('HEAD', media.url)
log(head.status === 200 && head.headers.get('content-length') === String(img.length), 'HEAD returns headers only')

const thumb = await http('GET', media.thumbnail_url)
log(thumb.status === 200 && thumb.headers.get('content-type') === 'image/png' && thumb.buf.length < img.length, `thumbnail served (${thumb.buf.length} < ${img.length} bytes)`)

const anon = await http('GET', media.url, { auth: false })
log(anon.status === 404 && anon.data?.error?.code === 'NOT_FOUND', 'anonymous visitor cannot read an unreferenced image (404)')

const fake = await upload('fake.png', Buffer.from('<html><script>alert(1)</script></html>'))
log(fake.status === 415 && fake.data?.error?.code === 'UNSUPPORTED_MEDIA_TYPE', 'HTML disguised as .png → 415')

const bomb = Buffer.from(png(1, 1)); bomb.writeUInt32BE(60000, 16); bomb.writeUInt32BE(60000, 20); bomb.writeUInt32BE(crc32(bomb.subarray(12, 29)), 29)
const b = await upload('bomb.png', bomb)
log(b.status === 422, 'PNG claiming 60000×60000 pixels → 422 (rejected before decoding)')

const del = await http('DELETE', `/api/v1/admin/media/${media.id}`)
log(del.status === 204, 'delete unreferenced image → 204')
const gone = await http('GET', media.url)
log(gone.status === 404, 'deleted image is no longer served')

console.log(`\nAll ${step} checks passed`)
