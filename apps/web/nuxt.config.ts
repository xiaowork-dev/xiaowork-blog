export default defineNuxtConfig({
  compatibilityDate: '2026-10-01',
  devtools: { enabled: false },
  css: ['~/assets/css/main.css'],
  runtimeConfig: {
    // Server-only: where Nitro reaches the Go API (Docker network). Override with NUXT_API_INTERNAL_URL.
    apiInternalUrl: 'http://127.0.0.1:8080',
  },
  routeRules: {
    // Auth pages carry one-time tokens and personal state: never cache, never leak the URL via Referer.
    '/auth/**': { headers: { 'cache-control': 'private, no-store', 'referrer-policy': 'no-referrer' } },
  },
  app: {
    head: {
      htmlAttrs: { lang: 'zh-CN' },
      title: 'XiaoWork Blog',
      meta: [{ name: 'robots', content: 'noindex,nofollow' }],
    },
  },
})
