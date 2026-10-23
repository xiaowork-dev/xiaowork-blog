<script setup lang="ts">
import { parsePage, type ArticlePage } from '~/utils/public'

definePageMeta({ layout: 'blog' })

const route = useRoute()
const slug = computed(() => String(route.params.slug))
const page = computed(() => parsePage(route.query.page))
if (page.value === null) notFoundPage()

const { site } = await useSite()
const requestFetch = useRequestFetch()
const { data, loading, error, refresh } = await usePublicData(
  () => `tag:${slug.value}:${page.value}`,
  () => requestFetch<ArticlePage>(`/api/v1/tags/${encodeURIComponent(slug.value)}/articles`, { query: { page: page.value ?? 1 } }),
  [page, slug],
)
if (page.value! > 1 && data.value && data.value.data.length === 0) notFoundPage()
const tag = computed(() => data.value?.meta.tag)

usePageSeo(() => ({
  title: `#${tag.value?.name ?? '标签'}${page.value! > 1 ? `（第 ${page.value} 页）` : ''} · ${site.value.site_title}`,
  description: `标签“${tag.value?.name ?? ''}”下的文章`,
  path: `/tags/${slug.value}${page.value! > 1 ? `?page=${page.value}` : ''}`,
  siteName: site.value.site_title,
}))
</script>

<template>
  <section aria-labelledby="page-heading">
    <nav class="breadcrumb" aria-label="面包屑"><NuxtLink to="/tags">标签</NuxtLink><span aria-hidden="true">/</span></nav>
    <h1 id="page-heading" class="section-title">#{{ tag?.name ?? '标签' }}</h1>
    <p v-if="data" class="section-desc">共 {{ data.meta.total }} 篇文章</p>
    <ArticleList
      :articles="data?.data" :page="page ?? 1" :total-pages="data?.meta.total_pages ?? 0"
      :pending="loading" :error="!!error" empty-title="这个标签下还没有公开文章" @retry="refresh()"
    >
      <template #empty><NuxtLink to="/tags" class="button button-small button-secondary">查看全部标签</NuxtLink></template>
    </ArticleList>
  </section>
</template>
