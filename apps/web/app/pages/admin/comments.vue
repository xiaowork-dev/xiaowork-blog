<script setup lang="ts">
// /admin/comments (UI/UX §7.5): after-the-fact moderation only — filter by article, user, status and time;
// hide / unhide / delete with confirmation. There is deliberately no review queue or "approve" action.
import { describeError } from '~/composables/useApi'
import { STATUS_TEXT, type AdminComment } from '~/utils/comments'
import { formatDateTime, parsePage, type ListMeta } from '~/utils/public'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: '评论管理 · XiaoWork Blog' })

const api = useApi()
const route = useRoute()

const STATUSES = [
  { value: '', label: '全部' },
  { value: 'VISIBLE', label: '公开' },
  { value: 'HIDDEN', label: '已隐藏' },
  { value: 'DELETED', label: '已删除' },
]

const str = (v: unknown) => (typeof v === 'string' ? v : '')
const filters = computed(() => ({
  status: str(route.query.status),
  q: str(route.query.q),
  article_id: str(route.query.article_id),
  author_id: str(route.query.author_id),
  from: str(route.query.from),
  to: str(route.query.to),
  page: parsePage(route.query.page) ?? 1,
}))
const qInput = ref(filters.value.q)
const fromInput = ref(filters.value.from)
const toInput = ref(filters.value.to)

// Dates are picked as calendar days in the site time zone (UTC+8, no DST) and sent as instants.
function dayStart(day: string, plusDays = 0): string | undefined {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) return undefined
  const d = new Date(`${day}T00:00:00+08:00`)
  d.setUTCDate(d.getUTCDate() + plusDays)
  return d.toISOString().replace(/\.\d{3}Z$/, 'Z')
}

const failure = ref('')
const { data, status, refresh } = await useAsyncData(
  () => `admin-comments:${JSON.stringify(filters.value)}`,
  async () => {
    failure.value = ''
    const f = filters.value
    try {
      return await api.getPage<AdminComment>('/api/v1/admin/comments', {
        page: f.page, status: f.status || undefined, q: f.q || undefined, article_id: f.article_id || undefined,
        author_id: f.author_id || undefined, from: dayStart(f.from), to: dayStart(f.to, 1),
      }) as { data: AdminComment[], meta: ListMeta }
    }
    catch (err) {
      failure.value = describeError(err)
      return null
    }
  },
  { server: false, watch: [filters] },
)

function apply(patch: Record<string, string | undefined>) {
  const query: Record<string, string> = {}
  for (const [k, v] of Object.entries({ ...filters.value, page: undefined, ...patch })) {
    if (v) query[k] = String(v)
  }
  navigateTo({ path: '/admin/comments', query })
}

function submitSearch() {
  apply({ q: qInput.value.trim(), from: fromInput.value, to: toInput.value })
}

const pagerQuery = computed(() => {
  const { page: _page, ...rest } = filters.value
  return Object.fromEntries(Object.entries(rest).filter(([, v]) => v)) as Record<string, string>
})

const scope = computed(() => {
  const first = data.value?.data[0]
  return {
    article: filters.value.article_id ? (first?.article.title || filters.value.article_id) : '',
    author: filters.value.author_id ? (first?.author.nickname || filters.value.author_id) : '',
  }
})

// ---- actions ----
type Action = 'hide' | 'unhide' | 'delete'
const ACTION_TEXT: Record<Action, { title: string, body: string, button: string }> = {
  hide: { title: '隐藏这条评论？', body: '隐藏后正文不再对读者显示，回复结构保留；之后可以恢复。', button: '隐藏' },
  unhide: { title: '恢复这条评论？', body: '恢复后正文会重新公开显示。', button: '恢复' },
  delete: { title: '删除这条评论？', body: '删除后正文永久清除且不可恢复；已有的回复会保留在“该评论已删除”的占位下。', button: '删除' },
}
const dialog = ref<HTMLDialogElement | null>(null)
const pending = ref<{ action: Action, comment: AdminComment } | null>(null)
const busy = ref(false)
const actionError = ref('')
const notice = ref('')

function ask(action: Action, comment: AdminComment) {
  pending.value = { action, comment }
  actionError.value = ''
  dialog.value?.showModal()
}

async function run() {
  const p = pending.value
  if (!p) return
  busy.value = true
  actionError.value = ''
  try {
    if (p.action === 'delete') {
      await api.del(`/api/v1/admin/comments/${p.comment.id}`)
      Object.assign(p.comment, { status: 'DELETED_ADMIN', body_text: null })
    }
    else {
      const res = await api.post<{ status: AdminComment['status'] }>(`/api/v1/admin/comments/${p.comment.id}/${p.action}`)
      if (res) p.comment.status = res.status
    }
    notice.value = `已${ACTION_TEXT[p.action].button}。`
    dialog.value?.close()
  }
  catch (err) {
    actionError.value = describeError(err)
  }
  finally {
    busy.value = false
  }
}

function badge(s: AdminComment['status']) {
  return s === 'VISIBLE' ? 'badge-success' : s === 'HIDDEN' ? 'badge-warning' : 'badge-muted'
}
</script>

<template>
  <div class="admin-page">
    <div class="admin-page-head">
      <h1>评论管理</h1>
      <p class="muted">评论发表后立即公开；这里只做事后治理：隐藏、恢复或删除违规评论。</p>
    </div>

    <div class="tabs" role="tablist" aria-label="评论状态">
      <button
        v-for="s in STATUSES" :key="s.value" type="button" role="tab" :aria-selected="filters.status === s.value"
        @click="apply({ status: s.value })"
      >{{ s.label }}</button>
    </div>

    <form class="media-toolbar comment-filters" role="search" @submit.prevent="submitSearch">
      <label class="visually-hidden" for="comment-q">搜索评论</label>
      <input id="comment-q" v-model="qInput" type="search" placeholder="搜索正文、昵称或邮箱" maxlength="100">
      <label for="comment-from">从</label>
      <input id="comment-from" v-model="fromInput" type="date">
      <label for="comment-to">到</label>
      <input id="comment-to" v-model="toInput" type="date">
      <button type="submit" class="button button-small">筛选</button>
    </form>
    <p v-if="scope.article || scope.author" class="filter-scope">
      <template v-if="scope.article">文章：<strong>{{ scope.article }}</strong> <button type="button" class="link-button" @click="apply({ article_id: '' })">清除</button></template>
      <template v-if="scope.author">用户：<strong>{{ scope.author }}</strong> <button type="button" class="link-button" @click="apply({ author_id: '' })">清除</button></template>
    </p>
    <p class="visually-hidden" role="status" aria-live="polite">{{ notice }}</p>

    <ListSkeleton v-if="!data && !failure" :rows="3" />
    <StateBlock v-else-if="failure" kind="error" title="评论加载失败" :message="failure" :retrying="status === 'pending'" @retry="refresh()" />
    <StateBlock v-else-if="data && !data.data.length" kind="empty" title="没有符合条件的评论" />
    <template v-else-if="data">
      <p class="muted">共 {{ data.meta.total }} 条</p>
      <div class="table-wrap">
        <table class="article-table comment-table">
          <thead>
            <tr><th>评论</th><th>作者</th><th>文章</th><th>状态</th><th>操作</th></tr>
          </thead>
          <tbody>
            <tr v-for="c in data.data" :key="c.id">
              <td class="comment-cell">
                <p v-if="c.body_text" class="comment-snippet">{{ c.body_text }}</p>
                <p v-else class="muted">（正文已清除）</p>
                <span class="muted small">{{ formatDateTime(c.created_at) }}<template v-if="c.parent_id"> · 回复</template><template v-if="c.reply_count"> · {{ c.reply_count }} 条回复</template></span>
              </td>
              <td>
                <button type="button" class="link-button" :title="`只看 ${c.author.nickname} 的评论`" @click="apply({ author_id: c.author.id })">{{ c.author.nickname }}</button>
                <span v-if="c.author.role === 'ADMIN'" class="owner-badge">博主</span>
                <span class="muted small block">{{ c.author.email }}</span>
                <NuxtLink :to="`/admin/users/${c.author.id}`" class="small block">用户详情 →</NuxtLink>
                <span v-if="c.author.status !== 'ACTIVE'" class="badge badge-danger">已禁用</span>
              </td>
              <td>
                <button type="button" class="link-button" :title="`只看《${c.article.title}》的评论`" @click="apply({ article_id: c.article.id })">{{ c.article.title || '（无标题）' }}</button>
                <a v-if="c.article.canonical_path" :href="`${c.article.canonical_path}#comments`" target="_blank" rel="noopener" class="small block">查看文章 ↗</a>
                <span v-else class="muted small block">文章未公开</span>
              </td>
              <td><span class="badge" :class="badge(c.status)">{{ STATUS_TEXT[c.status] }}</span></td>
              <td class="row-actions">
                <button v-if="c.status === 'VISIBLE'" type="button" class="link-button" @click="ask('hide', c)">隐藏</button>
                <button v-if="c.status === 'HIDDEN'" type="button" class="link-button" @click="ask('unhide', c)">恢复</button>
                <button v-if="c.status === 'VISIBLE' || c.status === 'HIDDEN'" type="button" class="link-button danger" @click="ask('delete', c)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <PagerNav :page="filters.page" :total-pages="data.meta.total_pages" :query="pagerQuery" />
    </template>

    <dialog ref="dialog" class="media-dialog media-dialog-small" aria-labelledby="moderate-title">
      <template v-if="pending">
        <h2 id="moderate-title">{{ ACTION_TEXT[pending.action].title }}</h2>
        <p>{{ ACTION_TEXT[pending.action].body }}</p>
        <blockquote v-if="pending.comment.body_text" class="comment-quote">{{ pending.comment.body_text }}</blockquote>
        <p v-if="actionError" class="alert alert-error" role="alert">{{ actionError }}</p>
        <div class="dialog-actions">
          <button type="button" class="button button-secondary" @click="dialog?.close()">取消</button>
          <button type="button" class="button" :class="{ 'button-danger': pending.action !== 'unhide' }" :disabled="busy" @click="run">
            {{ busy ? '处理中…' : ACTION_TEXT[pending.action].button }}
          </button>
        </div>
      </template>
    </dialog>
  </div>
</template>
