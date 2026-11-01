<script setup lang="ts">
// /account/bookmarks (P8-B12): the reader's bookmarked public articles, newest bookmark first. Withdrawn
// articles are hidden here until they are public again; personal data, so never rendered on the server.
import { describeError } from '~/composables/useApi'
import { parsePage, type ArticleSummary, type ListMeta } from '~/utils/public'

definePageMeta({ layout: 'account', middleware: 'auth' })
useHead({ title: '我的收藏 · XiaoWork Blog', meta: [{ name: 'robots', content: 'noindex, nofollow' }] })

const api = useApi()
const route = useRoute()
const page = computed(() => parsePage(route.query.page) ?? 1)
const failure = ref('')
const { data, status, refresh } = await useAsyncData(
  () => `my-bookmarks:${page.value}`,
  async () => {
    failure.value = ''
    try {
      return await api.getPage<ArticleSummary>('/api/v1/me/bookmarks', { page: page.value }) as { data: ArticleSummary[], meta: ListMeta }
    }
    catch (err) {
      failure.value = describeError(err)
      return null
    }
  },
  { server: false, watch: [page] },
)
</script>

<template>
  <section class="account-page">
    <h1 class="section-title">我的收藏</h1>
    <p class="section-desc">在文章卡片或文章页点击书签图标即可收藏；文章撤回后会暂时从这里隐藏。</p>
    <ArticleList
      :articles="data?.data" :page="page" :total-pages="data?.meta.total_pages ?? 1" :pending="status === 'pending' || status === 'idle'"
      :error="!!failure" empty-title="还没有收藏文章" @retry="refresh()"
    >
      <template #empty><NuxtLink to="/" class="button button-small button-secondary">去看看文章</NuxtLink></template>
    </ArticleList>
  </section>
</template>
