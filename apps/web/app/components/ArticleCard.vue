<script setup lang="ts">
// One entry of an article list (UI/UX §4.1): title is the primary link; category and tags are separate
// links so the card has no nested or competing click targets. No cover → plain text card.
// P8-B8: 16:9 cover with a fixed box (no layout shift), a soft fallback when the image fails, eager
// loading only for the first cards on screen, and low-saturation tag tones.
import { formatDay, highlightParts, isoDay, tagTone, type ArticleSummary } from '~/utils/public'

const props = defineProps<{ article: ArticleSummary, highlight?: string[], headingLevel?: 'h2' | 'h3', priority?: boolean }>()
const titleParts = computed(() => highlightParts(props.article.title, props.highlight ?? []))
const summaryParts = computed(() => highlightParts(props.article.summary, props.highlight ?? []))
const coverFailed = ref(false)
const coverImg = ref<HTMLImageElement | null>(null)
// An image that failed before hydration fired its error event before the listener existed.
onMounted(() => {
  const img = coverImg.value
  if (img?.complete && img.naturalWidth === 0) coverFailed.value = true
})
</script>

<template>
  <article class="article-card" :class="{ 'has-cover': article.cover }">
    <NuxtLink v-if="article.cover" :to="article.canonical_path" class="card-cover" :class="{ 'is-broken': coverFailed }" tabindex="-1" aria-hidden="true">
      <img
        v-if="!coverFailed" ref="coverImg" :src="article.cover.url" alt="" width="640" height="360"
        :loading="priority ? 'eager' : 'lazy'" :fetchpriority="priority ? 'high' : 'auto'" decoding="async"
        @error="coverFailed = true"
      >
      <SiteIcon v-else name="image" :size="28" />
    </NuxtLink>
    <div class="card-body">
      <div class="card-kicker">
        <NuxtLink v-if="article.category" :to="`/categories/${article.category.slug}`" class="card-category">{{ article.category.name }}</NuxtLink>
        <span v-else class="card-category is-none">未分类</span>
        <time :datetime="isoDay(article.first_published_at)">{{ formatDay(article.first_published_at) }}</time>
      </div>
      <component :is="headingLevel ?? 'h2'" class="card-title">
        <NuxtLink :to="article.canonical_path">
          <template v-for="(p, i) in titleParts" :key="i"><mark v-if="p.hit">{{ p.text }}</mark><template v-else>{{ p.text }}</template></template>
        </NuxtLink>
      </component>
      <p v-if="article.summary" class="card-summary">
        <template v-for="(p, i) in summaryParts" :key="i"><mark v-if="p.hit">{{ p.text }}</mark><template v-else>{{ p.text }}</template></template>
      </p>
      <div class="card-foot">
        <ul v-if="article.tags.length" class="tag-list" aria-label="标签">
          <li v-for="t in article.tags" :key="t.id"><NuxtLink :to="`/tags/${t.slug}`" class="tag-pill" :class="`tone-${tagTone(t.slug)}`">#{{ t.name }}</NuxtLink></li>
        </ul>
        <span class="card-views" :aria-label="`阅读 ${article.view_count} 次`"><SiteIcon name="eye" :size="15" />{{ article.view_count }}</span>
      </div>
    </div>
  </article>
</template>
