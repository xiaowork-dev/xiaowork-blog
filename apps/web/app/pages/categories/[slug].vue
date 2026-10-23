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
  () => `category:${slug.value}:${page.value}`,
  () => requestFetch<ArticlePage>(`/api/v1/categories/${encodeURIComponent(slug.value)}/articles`, { query: { page: page.value ?? 1 } }),
  [page, slug],
)
if (page.value! > 1 && data.value && data.value.data.length === 0) notFoundPage()
const category = computed(() => data.value?.meta.category)

usePageSeo(() => ({
  title: `${category.value?.name ?? '分类'}${page.value! > 1 ? `（第 ${page.value} 页）` : ''} · ${site.value.site_title}`,
  description: category.value?.description || `分类“${category.value?.name ?? ''}”下的文章`,
  path: `/categories/${slug.value}${page.value! > 1 ? `?page=${page.value}` : ''}`,
  siteName: site.value.site_title,
}))
</script>

<template>
  <section aria-labelledby="page-heading">
    <nav class="breadcrumb" aria-label="面包屑"><NuxtLink to="/categories">分类</NuxtLink><span aria-hidden="true">/</span></nav>
    <h1 id="page-heading" class="section-title">{{ category?.name ?? '分类' }}</h1>
    <p v-if="category?.description" class="section-desc">{{ category.description }}</p>
    <p v-if="data" class="section-desc">共 {{ data.meta.total }} 篇文章</p>
    <ArticleList
      :articles="data?.data" :page="page ?? 1" :total-pages="data?.meta.total_pages ?? 0"
      :pending="loading" :error="!!error" empty-title="这个分类下还没有公开文章" @retry="refresh()"
    >
      <template #empty><NuxtLink to="/categories" class="button button-small button-secondary">查看其他分类</NuxtLink></template>
    </ArticleList>
  </section>
</template>
