<script setup lang="ts">
import { tagTone, type TermWithCount } from '~/utils/public'

definePageMeta({ layout: 'blog' })

const { site } = await useSite()
const requestFetch = useRequestFetch()
const { data, loading, error, refresh } = await usePublicData('tags',
  () => requestFetch<{ data: TermWithCount[] }>('/api/v1/tags').then(r => r.data))

usePageSeo(() => ({ title: `标签 · ${site.value.site_title}`, description: `${site.value.site_title} 的文章标签`, path: '/tags', siteName: site.value.site_title }))
</script>

<template>
  <section aria-labelledby="page-heading">
    <h1 id="page-heading" class="section-title">标签</h1>
    <ListSkeleton v-if="loading && !data" />
    <StateBlock v-else-if="error" kind="error" title="标签加载失败" :retrying="loading" @retry="refresh()" />
    <StateBlock v-else-if="!data?.length" kind="empty" title="还没有标签">
      <NuxtLink to="/" class="button button-small button-secondary">浏览全部文章</NuxtLink>
    </StateBlock>
    <ul v-else class="tag-cloud">
      <li v-for="t in data" :key="t.id">
        <NuxtLink :to="`/tags/${t.slug}`" class="tag-pill tag-pill-large" :class="`tone-${tagTone(t.slug)}`">#{{ t.name }} <span class="tag-count">{{ t.article_count }}</span></NuxtLink>
      </li>
    </ul>
  </section>
</template>
