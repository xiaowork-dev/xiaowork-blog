export default defineNuxtConfig({
  devtools: { enabled: false },
  css: ['~/assets/css/main.css'],
  app: {
    head: {
      title: 'XiaoWork Blog · P0 骨架',
      meta: [{ name: 'robots', content: 'noindex,nofollow' }],
    },
  },
})
