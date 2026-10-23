<script setup lang="ts">
// Standard, crawlable pagination links (UI/UX §4.1: links, not infinite scroll). Page 1 has no ?page=.
import { pageWindow } from '~/utils/public'

const props = defineProps<{ page: number, totalPages: number, query?: Record<string, string> }>()
const route = useRoute()
const pages = computed(() => pageWindow(props.page, props.totalPages))

function link(p: number) {
  const query: Record<string, string> = { ...(props.query ?? {}) }
  if (p > 1) query.page = String(p)
  return { path: route.path, query }
}
</script>

<template>
  <nav v-if="totalPages > 1" class="pager-nav" aria-label="分页">
    <NuxtLink v-if="page > 1" :to="link(page - 1)" class="pager-step" rel="prev">上一页</NuxtLink>
    <span v-else class="pager-step disabled" aria-disabled="true">上一页</span>
    <ol class="pager-pages">
      <li v-for="(p, i) in pages" :key="i">
        <span v-if="p === null" class="pager-gap" aria-hidden="true">…</span>
        <span v-else-if="p === page" class="pager-current" aria-current="page">{{ p }}</span>
        <NuxtLink v-else :to="link(p)" :aria-label="`第 ${p} 页`">{{ p }}</NuxtLink>
      </li>
    </ol>
    <NuxtLink v-if="page < totalPages" :to="link(page + 1)" class="pager-step" rel="next">下一页</NuxtLink>
    <span v-else class="pager-step disabled" aria-disabled="true">下一页</span>
  </nav>
</template>
