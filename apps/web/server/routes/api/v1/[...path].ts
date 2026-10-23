// Same-origin proxy for /api/v1/* to the Go API (API-DD §2). In production Nginx Proxy Manager routes
// /api/v1 straight to the API; this keeps local Docker/dev and SSR on the same contract.
// Cookies, Origin and CSRF headers pass through unchanged; the Go API makes every security decision.
export default defineEventHandler((event) => {
  const { apiInternalUrl } = useRuntimeConfig(event)
  const target = new URL(event.path, apiInternalUrl)

  const peer = getRequestIP(event) // direct peer only; the API decides which hops to trust
  const prior = getRequestHeader(event, 'x-forwarded-for')
  const forwardedFor = [prior, peer].filter(Boolean).join(', ')

  return proxyRequest(event, target.toString(), {
    headers: forwardedFor ? { 'x-forwarded-for': forwardedFor } : {},
  })
})
