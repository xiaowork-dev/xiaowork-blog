<script setup lang="ts">
// One entry of an article list (UI/UX §4.1): title is the primary link; category and tags are separate
// links so the card has no nested or competing click targets. No cover → plain text card.
import { formatDay, highlightParts, isoDay, type ArticleSummary } from '~/utils/public'

const props = defineProps<{ article: ArticleSummary, highlight?: string[], headingLevel?: 'h2' | 'h3' }>()
const titleParts = computed(() => highlightParts(props.article.title, props.highlight ?? []))
const summaryParts = computed(() => highlightParts(props.article.summary, props.highlight ?? []))
</script>

<template>
  <article class="article-card" :class="{ 'has-cover': article.cover }">
    <NuxtLink v-if="article.cover" :to="article.canonical_path" class="card-cover" tabindex="-1" aria-hidden="true">
      <img :src="article.cover.url" alt="" loading="lazy" decoding="async">
    </NuxtLink>
    <div class="card-body">
      <component :is="headingLevel ?? 'h2'" class="card-title">
        <NuxtLink :to="article.canonical_path">
          <template v-for="(p, i) in titleParts" :key="i"><mark v-if="p.hit">{{ p.text }}</mark><template v-else>{{ p.text }}</template></template>
        </NuxtLink>
      </component>
      <p v-if="article.summary" class="card-summary">
        <template v-for="(p, i) in summaryParts" :key="i"><mark v-if="p.hit">{{ p.text }}</mark><template v-else>{{ p.text }}</template></template>
      </p>
      <div class="card-meta">
        <time :datetime="isoDay(article.first_published_at)"><SiteIcon name="calendar" :size="15" />{{ formatDay(article.first_published_at) }}</time>
        <NuxtLink v-if="article.category" :to="`/categories/${article.category.slug}`" class="card-category">
          <SiteIcon name="folder" :size="15" />{{ article.category.name }}
        </NuxtLink>
        <span v-else class="card-category muted-inline"><SiteIcon name="folder" :size="15" />未分类</span>
        <span class="card-views" :aria-label="`阅读 ${article.view_count} 次`"><SiteIcon name="eye" :size="15" />{{ article.view_count }}</span>
      </div>
      <ul v-if="article.tags.length" class="tag-list" aria-label="标签">
        <li v-for="t in article.tags" :key="t.id"><NuxtLink :to="`/tags/${t.slug}`" class="tag-pill">#{{ t.name }}</NuxtLink></li>
      </ul>
    </div>
  </article>
</template>
