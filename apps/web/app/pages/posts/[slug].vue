<script setup lang="ts">
// Minimal public article page so a published version can be viewed (P4 "发布成功后可以查看公开版本").
// It renders only the public snapshot from GET /api/v1/articles/{slug}; the full reading experience
// (TOC, comments, SEO tags, related navigation styling) is P5. Old slugs get a server-side 301.
import { useMarkdownRenderer } from '~/composables/useMarkdown'
import { formatDate } from '~/utils/media'

interface PublicArticle {
  id: string
  slug: string
  title: string
  summary: string
  body_markdown: string
  cover: { url: string } | null
  category: { name: string, slug: string } | null
  tags: { name: string, slug: string }[]
  first_published_at: string
  public_updated_at: string
  canonical_path: string
  resolved_from_old_slug: boolean
  previous_article: { title: string, canonical_path: string } | null
  next_article: { title: string, canonical_path: string } | null
}

const route = useRoute()
const requestFetch = useRequestFetch()
const { data: article, error } = await useAsyncData(`post:${route.params.slug}`, () =>
  requestFetch<{ data: PublicArticle }>(`/api/v1/articles/${encodeURIComponent(String(route.params.slug))}`).then(r => r.data))

if (error.value || !article.value) {
  throw createError({ statusCode: 404, statusMessage: '文章不存在', fatal: true })
}
if (article.value.resolved_from_old_slug) {
  await navigateTo(article.value.canonical_path, { redirectCode: 301, replace: true })
}

const render = await useMarkdownRenderer()
const html = computed(() => (article.value ? render(article.value.body_markdown) : ''))
useHead({ title: computed(() => `${article.value?.title ?? ''} · XiaoWork Blog`) })
</script>

<template>
  <main v-if="article" class="post">
    <header class="post-head">
      <h1>{{ article.title }}</h1>
      <p class="muted">
        发布于 {{ formatDate(article.first_published_at) }}
        <template v-if="article.public_updated_at !== article.first_published_at"> · 更新于 {{ formatDate(article.public_updated_at) }}</template>
        <template v-if="article.category"> · {{ article.category.name }}</template>
      </p>
      <img v-if="article.cover" :src="article.cover.url" alt="" class="post-cover">
    </header>
    <article class="md-body" v-html="html" />
    <footer class="post-foot">
      <p v-if="article.tags.length" class="tag-chips">
        <span v-for="t in article.tags" :key="t.slug" class="chip">{{ t.name }}</span>
      </p>
      <nav class="post-nav" aria-label="上一篇和下一篇">
        <NuxtLink v-if="article.next_article" :to="article.next_article.canonical_path">← {{ article.next_article.title }}</NuxtLink>
        <NuxtLink v-if="article.previous_article" :to="article.previous_article.canonical_path">{{ article.previous_article.title }} →</NuxtLink>
      </nav>
    </footer>
  </main>
</template>
