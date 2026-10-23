<script setup lang="ts">
import type { TermWithCount } from '~/utils/public'

definePageMeta({ layout: 'blog' })

const { site } = await useSite()
const requestFetch = useRequestFetch()
const { data, loading, error, refresh } = await usePublicData('categories',
  () => requestFetch<{ data: TermWithCount[] }>('/api/v1/categories').then(r => r.data))

usePageSeo(() => ({ title: `分类 · ${site.value.site_title}`, description: `${site.value.site_title} 的文章分类`, path: '/categories', siteName: site.value.site_title }))
</script>

<template>
  <section aria-labelledby="page-heading">
    <h1 id="page-heading" class="section-title">分类</h1>
    <ListSkeleton v-if="loading && !data" />
    <StateBlock v-else-if="error" kind="error" title="分类加载失败" :retrying="loading" @retry="refresh()" />
    <StateBlock v-else-if="!data?.length" kind="empty" title="还没有分类" message="文章发布后会按分类整理在这里。">
      <NuxtLink to="/" class="button button-small button-secondary">浏览全部文章</NuxtLink>
    </StateBlock>
    <ul v-else class="term-grid">
      <li v-for="c in data" :key="c.id">
        <NuxtLink :to="`/categories/${c.slug}`" class="term-card">
          <span class="term-name"><SiteIcon name="folder" :size="18" />{{ c.name }}</span>
          <span class="term-count">{{ c.article_count }} 篇</span>
          <span v-if="c.description" class="term-desc">{{ c.description }}</span>
        </NuxtLink>
      </li>
    </ul>
  </section>
</template>
