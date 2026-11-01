<script setup lang="ts">
// "相关文章" (P8-B12): up to three other public articles, most related first (shared tags, then the same
// category, then newest). Loaded in the browser so it never delays the article itself.
import { formatDay, type ArticleSummary } from '~/utils/public'

const props = defineProps<{ articleId: string }>()
const { data } = useAsyncData(
  () => `related:${props.articleId}`,
  () => $fetch<{ data: ArticleSummary[] }>(`/api/v1/articles/${props.articleId}/related`).then(r => r.data).catch(() => []),
  { server: false, lazy: true, watch: [() => props.articleId] },
)
</script>

<template>
  <section v-if="data?.length" class="side-card related" aria-labelledby="related-title">
    <h2 id="related-title" class="side-title">相关文章</h2>
    <ul class="related-list">
      <li v-for="a in data" :key="a.id">
        <NuxtLink :to="a.canonical_path" class="related-item">
          <span class="related-thumb" aria-hidden="true">
            <img v-if="a.cover" :src="a.cover.url" alt="" width="96" height="64" loading="lazy" decoding="async">
            <SiteIcon v-else name="image" :size="18" />
          </span>
          <span class="related-text">
            <span class="related-title">{{ a.title }}</span>
            <time :datetime="a.first_published_at">{{ formatDay(a.first_published_at) }}</time>
          </span>
        </NuxtLink>
      </li>
    </ul>
  </section>
</template>
