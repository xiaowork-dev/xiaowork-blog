<script setup lang="ts">
// Archive (UI/UX §4.3): public articles by year and month of first publication (site time zone).
import { formatDay, isoDay, type ArchiveResponse } from '~/utils/public'

definePageMeta({ layout: 'blog' })

const route = useRoute()
const year = computed(() => {
  const raw = route.query.year
  if (raw === undefined) return null
  const s = String(Array.isArray(raw) ? raw[0] : raw)
  return /^\d{4}$/.test(s) ? Number(s) : NaN
})
if (Number.isNaN(year.value)) notFoundPage()

const { site } = await useSite()
const requestFetch = useRequestFetch()
const { data, loading, error, refresh } = await usePublicData(
  () => `archives:${year.value ?? 'all'}`,
  () => requestFetch<ArchiveResponse>('/api/v1/archives', { query: year.value ? { year: year.value } : {} }),
  [year],
)
if (year.value && data.value && data.value.data.length === 0) notFoundPage()

const byYear = computed(() => {
  const out: { year: number, months: ArchiveResponse['data'], count: number }[] = []
  for (const m of data.value?.data ?? []) {
    let y = out[out.length - 1]
    if (!y || y.year !== m.year) {
      y = { year: m.year, months: [], count: 0 }
      out.push(y)
    }
    y.months.push(m)
    y.count += m.articles.length
  }
  return out
})

usePageSeo(() => ({
  title: `${year.value ? `${year.value} 年` : ''}文章归档 · ${site.value.site_title}`,
  description: `${site.value.site_title} 按年月整理的全部公开文章`,
  path: year.value ? `/archives?year=${year.value}` : '/archives',
  siteName: site.value.site_title,
}))
</script>

<template>
  <section aria-labelledby="page-heading">
    <h1 id="page-heading" class="section-title">归档</h1>
    <p v-if="data" class="section-desc">共 {{ data.meta.total }} 篇公开文章</p>
    <nav v-if="data?.meta.years.length" class="year-tabs" aria-label="按年份筛选">
      <NuxtLink to="/archives" :class="{ active: !year }" :aria-current="!year ? 'page' : undefined">全部</NuxtLink>
      <NuxtLink
        v-for="y in data.meta.years" :key="y.year" :to="{ path: '/archives', query: { year: String(y.year) } }"
        :class="{ active: year === y.year }" :aria-current="year === y.year ? 'page' : undefined"
      >{{ y.year }}（{{ y.article_count }}）</NuxtLink>
    </nav>
    <ListSkeleton v-if="loading && !data" />
    <StateBlock v-else-if="error" kind="error" title="归档加载失败" :retrying="loading" @retry="refresh()" />
    <StateBlock v-else-if="!byYear.length" kind="empty" title="还没有发布文章" />
    <div v-else class="archive">
      <details v-for="y in byYear" :key="y.year" class="archive-year" open>
        <summary><h2>{{ y.year }} 年</h2><span class="muted-inline">{{ y.count }} 篇</span></summary>
        <section v-for="m in y.months" :key="`${m.year}-${m.month}`" class="archive-month" :aria-label="`${m.year} 年 ${m.month} 月`">
          <h3>{{ m.month }} 月</h3>
          <ul>
            <li v-for="a in m.articles" :key="a.id">
              <time :datetime="isoDay(a.first_published_at)">{{ formatDay(a.first_published_at).replace(/^\d+年/, '') }}</time>
              <NuxtLink :to="a.canonical_path">{{ a.title }}</NuxtLink>
            </li>
          </ul>
        </section>
      </details>
      <p v-if="data?.meta.truncated" class="muted">文章较多，仅显示最近 2000 篇，请按年份查看更早的文章。</p>
    </div>
  </section>
</template>
