<script setup lang="ts">
// Article list with skeleton, empty and error states and pagination (UI/UX §4.1, §8).
import type { ArticleSummary } from '~/utils/public'

defineProps<{
  articles: ArticleSummary[] | undefined
  page: number
  totalPages: number
  pending?: boolean
  error?: boolean
  emptyTitle?: string
  query?: Record<string, string>
  highlight?: string[]
}>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <ListSkeleton v-if="pending && !articles?.length" />
  <StateBlock v-else-if="error" kind="error" title="文章加载失败" message="可能是网络问题或服务暂时不可用。" :retrying="pending" @retry="$emit('retry')">
    <NuxtLink to="/" class="button button-small button-secondary">返回首页</NuxtLink>
  </StateBlock>
  <StateBlock v-else-if="!articles?.length" kind="empty" :title="emptyTitle ?? '还没有发布文章'">
    <slot name="empty" />
  </StateBlock>
  <template v-else>
    <div class="article-list">
      <ArticleCard v-for="a in articles" :key="a.id" :article="a" :highlight="highlight" />
    </div>
    <PagerNav :page="page" :total-pages="totalPages" :query="query" />
  </template>
</template>
