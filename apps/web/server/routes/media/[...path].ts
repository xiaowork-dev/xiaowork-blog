// Same-origin proxy for GET/HEAD /media/{id}/file (API-DD §2). The Go API decides visibility from the
// session cookie; this route only forwards. In production Nginx Proxy Manager routes /media straight to the API.
export default defineEventHandler((event) => {
  const method = event.method
  if (method !== 'GET' && method !== 'HEAD') {
    throw createError({ statusCode: 405, statusMessage: 'Method Not Allowed' })
  }
  const { apiInternalUrl } = useRuntimeConfig(event)
  const target = new URL(event.path, apiInternalUrl)
  const peer = getRequestIP(event)
  const prior = getRequestHeader(event, 'x-forwarded-for')
  const forwardedFor = [prior, peer].filter(Boolean).join(', ')
  return proxyRequest(event, target.toString(), {
    headers: forwardedFor ? { 'x-forwarded-for': forwardedFor } : {},
    // h3 drops the upstream Content-Length (GET bodies are re-streamed); HEAD has no body, so restore it.
    onResponse: (ev, response) => {
      const length = response.headers.get('content-length')
      if (ev.method === 'HEAD' && length) setResponseHeader(ev, 'content-length', Number(length))
    },
  })
})
