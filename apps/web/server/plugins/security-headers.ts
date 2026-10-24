// Security headers for every server-rendered page (P8-A review). The Go API already sends its own strict
// set on /api/v1 and /media; this covers the HTML produced by Nuxt.
//
// CSP notes: Nuxt hydration needs its inline payload/bootstrap scripts and the pre-paint theme script, and
// Shiki highlighting uses inline style attributes, so 'unsafe-inline' is allowed for script/style. XSS is
// prevented at the source instead (Markdown html:false + URL allow-list, comments rendered as text). The
// policy still blocks plugins, foreign frames, <base> hijacking, cross-origin form posts and connections.
// Article images may be absolute https URLs, hence img-src https:.
const CSP = [
  "default-src 'self'",
  "script-src 'self' 'unsafe-inline'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: blob: https:",
  "font-src 'self' data:",
  "connect-src 'self'",
  "object-src 'none'",
  "base-uri 'self'",
  "form-action 'self'",
  "frame-ancestors 'none'",
].join('; ')

export default defineNitroPlugin((nitroApp) => {
  const https = String(useRuntimeConfig().public.siteUrl).startsWith('https://')
  nitroApp.hooks.hook('render:response', (response) => {
    const h = response.headers ?? (response.headers = {})
    delete h['x-powered-by']
    h['x-content-type-options'] = 'nosniff'
    h['x-frame-options'] = 'DENY'
    h['referrer-policy'] ??= 'strict-origin-when-cross-origin' // auth pages keep their stricter no-referrer
    h['permissions-policy'] = 'camera=(), microphone=(), geolocation=(), payment=(), usb=()'
    h['content-security-policy'] = CSP
    // Only over https (production): tells browsers to keep using https for a year. Subdomains are not
    // included because the blog may share its parent domain with other services.
    if (https) h['strict-transport-security'] = 'max-age=31536000'
  })
})
