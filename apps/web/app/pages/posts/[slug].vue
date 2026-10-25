<script setup lang="ts">
// Article page (UI/UX §4.2): only the public snapshot of GET /api/v1/articles/{slug}. Old slugs answer
// an HTTP 301 from SSR; drafts, withdrawn and trashed articles are a 404. Comments (P6) are loaded in
// the browser by <CommentSection>, so the article HTML is the same for every reader.
import { useDocumentRenderer } from '~/composables/useMarkdown'
import { formatDay, isoDay, tagTone, type PublicArticle } from '~/utils/public'
import type { TocEntry } from '~/utils/markdown'

definePageMeta({ layout: 'blog' })

const route = useRoute()
const slug = computed(() => String(route.params.slug))
const { site } = await useSite()
const requestFetch = useRequestFetch()
const api = useApi()

const { data: article, error, loading, refresh } = await usePublicData(
  () => `post:${slug.value}`,
  () => requestFetch<{ data: PublicArticle }>(`/api/v1/articles/${encodeURIComponent(slug.value)}`).then(r => r.data),
  [slug],
)
if (article.value?.resolved_from_old_slug) {
  await navigateTo(article.value.canonical_path, { redirectCode: 301, replace: true })
}

const renderDoc = await useDocumentRenderer()
const rendered = computed(() => (article.value ? renderDoc(article.value.body_markdown) : { html: '', toc: [] as TocEntry[] }))
const updatedLater = computed(() => !!article.value && isoDay(article.value.public_updated_at) !== isoDay(article.value.first_published_at))
const showCover = computed(() => !!article.value?.cover && !article.value.body_markdown.includes(article.value.cover.url))

// ---- reading aids: TOC highlight, progress bar, back to top, image zoom, code copy ----
const body = ref<HTMLElement | null>(null)
const lightbox = ref<{ open: (img: HTMLImageElement) => void } | null>(null)
const { decorate } = useReadingEnhancements(body, img => lightbox.value?.open(img))

const activeId = ref('')
const progress = ref(0)
const showTop = ref(false)
let observer: IntersectionObserver | null = null

function onScroll() {
  const el = body.value
  if (!el) return
  const rect = el.getBoundingClientRect()
  const total = rect.height - window.innerHeight * 0.6
  progress.value = total > 0 ? Math.min(1, Math.max(0, -rect.top / total)) : 1
  showTop.value = window.scrollY > 600
}

function toTop() {
  const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  window.scrollTo({ top: 0, behavior: reduce ? 'auto' : 'smooth' })
  document.querySelector<HTMLElement>('#main')?.focus({ preventScroll: true })
}

function observeHeadings() {
  observer?.disconnect()
  if (!body.value || !rendered.value.toc.length) return
  observer = new IntersectionObserver((entries) => {
    const visible = entries.filter(e => e.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
    if (visible[0]) activeId.value = visible[0].target.id
  }, { rootMargin: '-80px 0px -70% 0px' })
  for (const t of rendered.value.toc) {
    const h = document.getElementById(t.id)
    if (h) observer.observe(h)
  }
}

// A read is reported once the article has been visible for 5 s in total (API-DD §7: never from SSR).
const VIEW_AFTER_MS = 5000
let viewSent = false
let visibleMs = 0
let visibleSince = 0
let viewTimer: ReturnType<typeof setTimeout> | null = null

function sendView() {
  viewTimer = null
  viewSent = true
  const id = article.value?.id
  if (id) api.post(`/api/v1/articles/${id}/views`).catch(() => { /* approximate counter: ignore failures */ })
}
function startViewTimer() {
  if (viewSent || viewTimer || !article.value) return
  visibleSince = Date.now()
  viewTimer = setTimeout(sendView, Math.max(0, VIEW_AFTER_MS - visibleMs))
}
function stopViewTimer() {
  if (!viewTimer) return
  clearTimeout(viewTimer)
  viewTimer = null
  visibleMs += Date.now() - visibleSince
}
function onVisibility() {
  if (document.visibilityState === 'visible') startViewTimer()
  else stopViewTimer()
}

onMounted(() => {
  window.addEventListener('scroll', onScroll, { passive: true })
  document.addEventListener('visibilitychange', onVisibility)
  onScroll()
  observeHeadings()
  onVisibility()
})
// Client-side navigation to another article reuses this component: start over for the new one.
watch(() => article.value?.id, async () => {
  stopViewTimer()
  viewSent = false
  visibleMs = 0
  activeId.value = ''
  await nextTick()
  decorate()
  observeHeadings()
  onScroll()
  onVisibility()
})
onBeforeUnmount(() => {
  window.removeEventListener('scroll', onScroll)
  document.removeEventListener('visibilitychange', onVisibility)
  observer?.disconnect()
  stopViewTimer()
})

// ---- SEO (DISCOVER-04) ----
usePageSeo(() => ({
  title: article.value ? `${article.value.seo_title || article.value.title} · ${site.value.site_title}` : site.value.site_title,
  description: article.value?.seo_description || article.value?.summary,
  path: article.value?.canonical_path,
  image: article.value?.cover?.url,
  type: 'article',
  publishedTime: article.value?.first_published_at,
  modifiedTime: article.value?.public_updated_at,
  tags: article.value?.tags.map(t => t.name),
  siteName: site.value.site_title,
}))
const config = useRuntimeConfig()
useHead(() => {
  const a = article.value
  if (!a) return {}
  const base = String(config.public.siteUrl).replace(/\/$/, '')
  const ld = {
    '@context': 'https://schema.org', '@type': 'BlogPosting', 'headline': a.title, 'description': a.summary,
    'datePublished': a.first_published_at, 'dateModified': a.public_updated_at, 'mainEntityOfPage': base + a.canonical_path,
    'author': { '@type': 'Person', 'name': site.value.profile.name },
    ...(a.cover ? { image: base + a.cover.url } : {}),
  }
  // "<" is escaped so no article text can close the script element.
  return { script: [{ type: 'application/ld+json', innerHTML: JSON.stringify(ld).replace(/</g, '\\u003c') }] }
})
</script>

<template>
  <div v-if="error && !article" class="post-error">
    <StateBlock kind="error" title="文章加载失败" message="可能是网络问题或服务暂时不可用。" :retrying="loading" @retry="refresh()">
      <NuxtLink to="/" class="button button-small button-secondary">返回首页</NuxtLink>
    </StateBlock>
  </div>
  <div v-else-if="article" class="post-layout" :class="{ 'has-toc': rendered.toc.length > 1 }">
    <div class="reading-progress" aria-hidden="true"><span :style="{ transform: `scaleX(${progress})` }" /></div>
    <div class="post-main">
      <article class="post-article" aria-labelledby="post-title">
        <header class="post-header">
          <nav class="breadcrumb" aria-label="面包屑">
            <NuxtLink to="/">首页</NuxtLink><span aria-hidden="true">/</span>
            <NuxtLink v-if="article.category" :to="`/categories/${article.category.slug}`">{{ article.category.name }}</NuxtLink>
            <span v-else>未分类</span>
          </nav>
          <h1 id="post-title" class="post-title">{{ article.title }}</h1>
          <div class="post-meta">
            <NuxtLink v-if="article.category" :to="`/categories/${article.category.slug}`" class="card-category">{{ article.category.name }}</NuxtLink>
            <span><SiteIcon name="calendar" :size="15" />发布于 <time :datetime="article.first_published_at">{{ formatDay(article.first_published_at) }}</time></span>
            <span v-if="updatedLater">更新于 <time :datetime="article.public_updated_at">{{ formatDay(article.public_updated_at) }}</time></span>
            <span :aria-label="`阅读 ${article.view_count} 次`"><SiteIcon name="eye" :size="15" />{{ article.view_count }} 次阅读</span>
          </div>
          <p v-if="article.summary" class="post-summary">{{ article.summary }}</p>
          <!-- Header image; skipped when the body already shows the same picture, so it never appears twice. -->
          <img v-if="showCover" :src="article.cover!.url" alt="" class="post-cover" width="1280" height="720" decoding="async" fetchpriority="high">
        </header>

        <details v-if="rendered.toc.length > 1" class="toc-inline">
          <summary><SiteIcon name="list" :size="16" />目录</summary>
          <ArticleToc :toc="rendered.toc" :active-id="activeId" />
        </details>

        <div ref="body" class="md-body post-body" v-html="rendered.html" />

        <footer class="post-footer">
          <ul v-if="article.tags.length" class="tag-list" aria-label="文章标签">
            <li v-for="t in article.tags" :key="t.id"><NuxtLink :to="`/tags/${t.slug}`" class="tag-pill" :class="`tone-${tagTone(t.slug)}`">#{{ t.name }}</NuxtLink></li>
          </ul>
          <nav class="post-neighbours" aria-label="上一篇和下一篇">
            <NuxtLink v-if="article.previous_article" :to="article.previous_article.canonical_path" class="neighbour prev" rel="prev">
              <span class="neighbour-label">上一篇</span><span class="neighbour-title">{{ article.previous_article.title }}</span>
            </NuxtLink>
            <span v-else class="neighbour placeholder" />
            <NuxtLink v-if="article.next_article" :to="article.next_article.canonical_path" class="neighbour next" rel="next">
              <span class="neighbour-label">下一篇</span><span class="neighbour-title">{{ article.next_article.title }}</span>
            </NuxtLink>
          </nav>
        </footer>
      </article>
      <CommentSection :article-id="article.id" :comments-enabled="article.comments_enabled" />
    </div>

    <aside v-if="rendered.toc.length > 1" class="toc-side" aria-label="文章目录">
      <p class="toc-title">目录</p>
      <ArticleToc :toc="rendered.toc" :active-id="activeId" />
    </aside>

    <button v-show="showTop" type="button" class="back-to-top icon-button" aria-label="返回顶部" title="返回顶部" @click="toTop">
      <SiteIcon name="arrow-up" />
    </button>
    <ImageLightbox ref="lightbox" />
  </div>
</template>
