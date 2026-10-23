<script setup lang="ts">
// Home (UI/UX §4.1): latest public articles, 10 per page, crawlable pagination.
import { parsePage, type ArticlePage } from '~/utils/public'

definePageMeta({ layout: 'blog' })

const route = useRoute()
const page = computed(() => parsePage(route.query.page))
if (page.value === null) notFoundPage()

const { site } = await useSite()
const requestFetch = useRequestFetch()
const { data, loading, error, refresh } = await usePublicData(
  () => `home:${page.value}`,
  () => requestFetch<ArticlePage>('/api/v1/articles', { query: { page: page.value ?? 1 } }),
  [page],
)
if (page.value! > 1 && data.value && data.value.data.length === 0) notFoundPage()

usePageSeo(() => ({
  title: page.value! > 1 ? `最新文章（第 ${page.value} 页） · ${site.value.site_title}` : (site.value.default_seo_title || site.value.site_title),
  description: site.value.default_seo_description || site.value.site_description || site.value.profile.bio,
  path: page.value! > 1 ? `/?page=${page.value}` : '/',
  siteName: site.value.site_title,
}))
</script>

<template>
  <section aria-labelledby="latest-heading">
    <h1 id="latest-heading" class="section-title">最新文章</h1>
    <ArticleList
      :articles="data?.data" :page="page ?? 1" :total-pages="data?.meta.total_pages ?? 0"
      :pending="loading" :error="!!error" @retry="refresh()"
    />
  </section>
</template>
