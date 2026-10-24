<script setup lang="ts">
// Article list (UI/UX §7.3): status tabs incl. "有未发布修改" and the recycle bin, search, row actions.
import { describeError, useApi } from '~/composables/useApi'
import { statusBadges, type ArticleListItem } from '~/utils/articles'
import { formatDate } from '~/utils/media'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: '文章管理 · XiaoWork Blog 管理后台' })

const api = useApi()
const tabs = [
  { key: '', label: '全部' }, { key: 'DRAFT', label: '草稿' }, { key: 'CHANGED', label: '有未发布修改' },
  { key: 'PUBLISHED', label: '已发布' }, { key: 'WITHDRAWN', label: '已撤回' }, { key: 'TRASHED', label: '回收站' },
] as const
const route = useRoute()
const status = ref<string>(typeof route.query.status === 'string' ? route.query.status : '')
const query = ref('')
const page = ref(1)
const totalPages = ref(1)
const total = ref(0)
const items = ref<ArticleListItem[]>([])
const loading = ref(false)
const error = ref('')
const notice = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await api.getPage<ArticleListItem>('/api/v1/admin/articles', {
      status: status.value || undefined, q: query.value.trim() || undefined, page: page.value, page_size: 20,
    })
    items.value = res.data
    total.value = res.meta.total ?? res.data.length
    totalPages.value = Math.max(1, res.meta.total_pages ?? 1)
  }
  catch (err) {
    error.value = describeError(err)
  }
  finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch(query, () => {
  clearTimeout(timer)
  timer = setTimeout(() => { page.value = 1; void load() }, 300)
})
watch([status, page], () => void load())
onMounted(load)

const confirmer = ref<{ ask: (q: { title: string, message?: string, confirmLabel?: string, danger?: boolean }) => Promise<boolean> } | null>(null)

async function act(item: ArticleListItem, action: 'withdraw' | 'trash' | 'restore') {
  const name = item.title || '无标题'
  const questions = {
    withdraw: { title: `撤回《${name}》？`, message: '游客将无法访问这篇文章，评论与图片保留，之后可以重新发布。', confirmLabel: '撤回', danger: true },
    trash: { title: `将《${name}》移至回收站？`, message: '文章不再公开；移入回收站不会清理图片和评论，可随时恢复。', confirmLabel: '移至回收站', danger: true },
    restore: null,
  }
  const q = questions[action]
  if (q && !(await confirmer.value?.ask(q))) return
  try {
    if (action === 'trash') await api.del(`/api/v1/admin/articles/${item.id}`)
    else await api.post(`/api/v1/admin/articles/${item.id}/${action}`)
    notice.value = { withdraw: '已撤回', trash: '已移至回收站', restore: '已恢复（未公开，需要时请重新发布）' }[action]
    await load()
  }
  catch (err) {
    error.value = describeError(err)
  }
}
</script>

<template>
  <div class="admin-page">
    <div class="admin-page-head with-action">
      <div>
        <h1>文章管理</h1>
        <p class="muted">草稿修改不会影响已发布内容，只有在编辑器中点击“发布更新”才会替换线上版本。</p>
      </div>
      <NuxtLink to="/admin/articles/new" class="button">新建文章</NuxtLink>
    </div>

    <div class="tabs" role="tablist" aria-label="按状态筛选">
      <button v-for="t in tabs" :key="t.key" type="button" role="tab" :aria-selected="status === t.key" @click="status = t.key; page = 1">{{ t.label }}</button>
    </div>
    <div class="media-toolbar">
      <label class="visually-hidden" for="article-search">按标题搜索</label>
      <input id="article-search" v-model="query" type="search" placeholder="按标题搜索" maxlength="100">
      <span class="muted">共 {{ total }} 篇</span>
    </div>
    <p v-if="notice" class="alert alert-success" role="status">{{ notice }}</p>
    <div v-if="error" class="alert alert-error" role="alert">{{ error }} <button type="button" class="link-button" @click="load">重试</button></div>

    <div v-if="loading && !items.length" class="article-table skeleton" aria-busy="true" style="min-height: 200px" />
    <div v-else-if="!items.length && !error" class="media-empty">
      <strong>{{ query ? '没有符合条件的文章' : status === 'TRASHED' ? '回收站是空的' : '还没有文章' }}</strong>
      <p class="muted"><NuxtLink to="/admin/articles/new">写第一篇文章</NuxtLink></p>
    </div>
    <div v-else class="table-wrap">
      <table class="article-table">
        <thead>
          <tr><th>标题</th><th>状态</th><th>分类</th><th>草稿保存</th><th>公开更新</th><th><span class="visually-hidden">操作</span></th></tr>
        </thead>
        <tbody>
          <tr v-for="a in items" :key="a.id">
            <td class="title-cell">
              <NuxtLink :to="`/admin/articles/${a.id}/edit`">{{ a.title || '无标题' }}</NuxtLink>
              <span v-if="a.slug" class="muted small">/posts/{{ a.slug }}</span>
            </td>
            <td><span v-for="b in statusBadges(a.status, a.has_unpublished_changes)" :key="b.label" class="badge" :class="`badge-${b.tone}`">{{ b.label }}</span></td>
            <td>{{ a.category_name || '未分类' }}</td>
            <td class="nowrap">{{ formatDate(a.saved_at) }}</td>
            <td class="nowrap">{{ a.public_updated_at ? formatDate(a.public_updated_at) : '—' }}</td>
            <td class="row-actions">
              <template v-if="a.status !== 'TRASHED'">
                <NuxtLink :to="`/admin/articles/${a.id}/edit`">编辑</NuxtLink>
                <NuxtLink :to="`/admin/articles/${a.id}/edit?mode=preview`">预览</NuxtLink>
                <a v-if="a.status === 'PUBLISHED' && a.slug" :href="`/posts/${a.slug}`" target="_blank" rel="noopener">公开页</a>
                <button v-if="a.status === 'PUBLISHED'" type="button" class="link-button" @click="act(a, 'withdraw')">撤回</button>
                <button type="button" class="link-button danger" @click="act(a, 'trash')">移至回收站</button>
              </template>
              <button v-else type="button" class="link-button" @click="act(a, 'restore')">恢复</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <nav v-if="totalPages > 1" class="pager" aria-label="分页">
      <button type="button" class="button button-secondary button-small" :disabled="page <= 1" @click="page--">上一页</button>
      <span>{{ page }} / {{ totalPages }}</span>
      <button type="button" class="button button-secondary button-small" :disabled="page >= totalPages" @click="page++">下一页</button>
    </nav>
    <ConfirmDialog ref="confirmer" />
  </div>
</template>
