// Applies the stored theme before first paint (no light/dark flash); see composables/useTheme.ts.
const themeScript = `try{var t=localStorage.getItem('xwb:theme');if(t==='light'||t==='dark')document.documentElement.dataset.theme=t}catch(e){}`

const privatePage = { 'cache-control': 'private, no-store', 'x-robots-tag': 'noindex, nofollow' }

export default defineNuxtConfig({
  compatibilityDate: '2026-10-01',
  devtools: { enabled: false },
  css: ['~/assets/css/main.css', '~/assets/css/articles.css', '~/assets/css/blog.css'],
  runtimeConfig: {
    // Server-only: where Nitro reaches the Go API (Docker network). Override with NUXT_API_INTERNAL_URL.
    apiInternalUrl: 'http://127.0.0.1:8080',
    public: {
      // Browser-facing origin for canonical, Open Graph, RSS and sitemap URLs (NUXT_PUBLIC_SITE_URL,
      // set from PUBLIC_BASE_URL by compose.yaml).
      siteUrl: 'http://127.0.0.1:3000',
    },
  },
  routeRules: {
    // Auth pages carry one-time tokens and personal state: never cache, never leak the URL via Referer.
    '/auth/**': { headers: { ...privatePage, 'referrer-policy': 'no-referrer' } },
    // Admin and account pages are personalised and must never be cached or indexed (SAD §2.2, DISCOVER-05).
    '/admin/**': { headers: privatePage },
    '/account/**': { headers: privatePage },
  },
  app: {
    head: {
      htmlAttrs: { lang: 'zh-CN' },
      title: 'XiaoWork Blog',
      meta: [{ name: 'viewport', content: 'width=device-width, initial-scale=1' }],
      link: [{ rel: 'alternate', type: 'application/rss+xml', title: 'RSS', href: '/rss.xml' }],
      script: [{ innerHTML: themeScript, tagPosition: 'head' }],
    },
  },
})
