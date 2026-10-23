<script setup lang="ts">
// Search (UI/UX §4.3): the query lives in the URL (?q=&page=); results come from the browser so the
// per-IP search limit applies to the real reader, and result pages are not indexed. Keyword matching
// only — the page never promises semantic search.
import type { ArticleSummary, ListMeta } from '~/utils/public'
import { parsePage } from '~/utils/public'
import { describeError } from '~/composables/useApi'

definePageMeta({ layout: 'blog' })

const MAX_QUERY = 100
const route = useRoute()
const api = useApi()
const { site } = await useSite()

const q = computed(() => String((Array.isArray(route.query.q) ? route.query.q[0] : route.query.q) ?? '').trim())
const page = computed(() => parsePage(route.query.page) ?? 1)
const input = ref(q.value)
watch(q, (v) => { input.value = v })

const tooLong = computed(() => Array.from(q.value).length > MAX_QUERY)
// The ApiError is kept as is (useAsyncData would wrap it) so 429 can show its Retry-After wait.
const failure = ref<unknown>(null)
const { data, status, refresh } = await useAsyncData(
  () => `search:${q.value}:${page.value}`,
  async () => {
    failure.value = null
    if (!q.value || tooLong.value) return null
    try {
      return await api.getPage<ArticleSummary>('/api/v1/search/articles', { q: q.value, page: page.value }) as { data: ArticleSummary[], meta: ListMeta & { terms?: string[] } }
    }
    catch (err) {
      failure.value = err
      return null
    }
  },
  { server: false, watch: [q, page] },
)
const loading = computed(() => status.value === 'pending')
const terms = computed(() => data.value?.meta.terms ?? (q.value ? q.value.split(/\s+/) : []))

function submit() {
  const value = input.value.trim()
  navigateTo({ path: '/search', query: value ? { q: value } : {} })
}

function clear() {
  input.value = ''
  navigateTo('/search')
}

usePageSeo(() => ({
  title: q.value ? `搜索“${q.value}” · ${site.value.site_title}` : `搜索 · ${site.value.site_title}`,
  description: `在 ${site.value.site_title} 中搜索文章`,
  noindex: true,
  siteName: site.value.site_title,
}))
</script>

<template>
  <section aria-labelledby="page-heading">
    <h1 id="page-heading" class="section-title">搜索文章</h1>
    <form class="search-form" role="search" @submit.prevent="submit">
      <label for="search-input" class="visually-hidden">搜索关键词</label>
      <input
        id="search-input" v-model="input" type="search" name="q" :maxlength="MAX_QUERY" placeholder="输入标题、摘要或正文中的关键词"
        autocomplete="off" enterkeyhint="search"
      >
      <button type="submit" class="button">搜索</button>
    </form>
    <p class="muted search-hint">按关键词匹配标题、摘要和正文；多个词用空格分隔，需同时出现。</p>

    <div aria-live="polite">
      <StateBlock v-if="tooLong" kind="error" title="关键词过长" :message="`请把关键词控制在 ${MAX_QUERY} 个字以内。`" />
      <template v-else-if="q">
        <!-- Results load in the browser only: SSR and hydration both start from this skeleton. -->
        <ListSkeleton v-if="!data && !failure" />
        <StateBlock
          v-else-if="failure" kind="error" title="搜索失败" :message="describeError(failure)" :retrying="loading"
          @retry="refresh()"
        />
        <template v-else-if="data">
          <p v-if="data.meta.total" class="section-desc">找到 {{ data.meta.total }} 篇与“{{ q }}”相关的文章</p>
          <ArticleList
            :articles="data.data" :page="page" :total-pages="data.meta.total_pages" :query="{ q }" :highlight="terms"
            :empty-title="`没有找到与“${q}”相关的文章`"
          >
            <template #empty>
              <button type="button" class="button button-small button-secondary" @click="clear">清空搜索</button>
              <NuxtLink to="/" class="button button-small button-secondary">浏览全部文章</NuxtLink>
            </template>
          </ArticleList>
        </template>
      </template>
    </div>
  </section>
</template>
