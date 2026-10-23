<script setup lang="ts">
// 404 / error page (UI/UX §1.1): no hint whether an unpublished article exists, links back to home
// and search, never indexed.
import type { NuxtError } from '#app'

const props = defineProps<{ error: NuxtError }>()
const notFound = computed(() => props.error.statusCode === 404)
const home = () => clearError({ redirect: '/' })
const toSearch = () => clearError({ redirect: '/search' })

useHead({
  title: notFound.value ? '页面不存在 · XiaoWork Blog' : '出错了 · XiaoWork Blog',
  meta: [{ name: 'robots', content: 'noindex, nofollow' }],
})
</script>

<template>
  <div class="site">
    <SiteHeader />
    <main id="main" class="error-page">
      <p class="error-code" aria-hidden="true">{{ notFound ? '404' : error.statusCode }}</p>
      <h1>{{ notFound ? '页面不存在' : '页面暂时无法显示' }}</h1>
      <p class="muted">
        {{ notFound ? '你访问的页面不存在，或文章已不再公开。' : '服务暂时不可用，请稍后刷新重试。' }}
      </p>
      <div class="error-actions">
        <button type="button" class="button" @click="home">返回首页</button>
        <button type="button" class="button button-secondary" @click="toSearch">搜索文章</button>
      </div>
    </main>
    <SiteFooter />
  </div>
</template>
