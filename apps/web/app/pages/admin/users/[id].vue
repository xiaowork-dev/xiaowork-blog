<script setup lang="ts">
// /admin/users/{id}: account details, comment summary and disable / enable (UI/UX §7.6). Admin accounts
// and the current account cannot be disabled; there is no UI for changing roles.
import { describeError } from '~/composables/useApi'
import { failureStatus } from '~/composables/usePublicData'
import type { AdminUserDetail } from '~/utils/admin'
import { placeholderText, STATUS_TEXT, type CommentStatus } from '~/utils/comments'
import { formatDateTime } from '~/utils/public'

definePageMeta({ layout: 'admin', middleware: 'admin' })

const api = useApi()
const route = useRoute()
const id = computed(() => String(route.params.id))
const failure = ref('')
const missing = ref(false)
const { data, status, refresh } = await useAsyncData(() => `admin-user:${id.value}`, async () => {
  failure.value = ''
  missing.value = false
  try {
    return await api.get<AdminUserDetail>(`/api/v1/admin/users/${id.value}`)
  }
  catch (err) {
    if (failureStatus(err) === 404) missing.value = true
    else failure.value = describeError(err)
    return null
  }
}, { server: false, watch: [id] })
useHead(() => ({ title: `${data.value?.nickname ?? '用户详情'} · XiaoWork Blog 管理后台` }))

const confirmer = ref<{ ask: (q: { title: string, message?: string, confirmLabel?: string, danger?: boolean }) => Promise<boolean> } | null>(null)
const busy = ref(false)
const notice = ref('')
const actionError = ref('')

async function setStatus(disable: boolean) {
  const u = data.value
  if (!u) return
  const ok = await confirmer.value?.ask(disable
    ? { title: `禁用“${u.nickname}”？`, message: `该账号的 ${u.active_session_count} 个登录会立即失效，之后无法登录、评论或找回密码；已发表的评论保留。可以随时恢复。`, confirmLabel: '禁用账号', danger: true }
    : { title: `恢复“${u.nickname}”？`, message: '恢复后该用户可以重新登录并发表评论（需要重新登录）。', confirmLabel: '恢复账号' })
  if (!ok) return
  busy.value = true
  actionError.value = ''
  try {
    const res = await api.post<{ revoked_sessions: number }>(`/api/v1/admin/users/${u.id}/${disable ? 'disable' : 'enable'}`)
    notice.value = disable ? `已禁用，撤销了 ${res?.revoked_sessions ?? 0} 个登录。` : '已恢复。'
    await refresh()
  }
  catch (err) {
    actionError.value = describeError(err)
  }
  finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="admin-page">
    <nav class="breadcrumb" aria-label="面包屑"><NuxtLink to="/admin/users">用户管理</NuxtLink><span aria-hidden="true">/</span><span>详情</span></nav>
    <ListSkeleton v-if="!data && !failure && !missing" :rows="2" />
    <StateBlock v-else-if="missing" kind="empty" title="用户不存在">
      <NuxtLink to="/admin/users" class="button button-small button-secondary">返回用户列表</NuxtLink>
    </StateBlock>
    <StateBlock v-else-if="failure" kind="error" title="用户加载失败" :message="failure" :retrying="status === 'pending'" @retry="refresh()" />
    <template v-else-if="data">
      <section class="account-card user-head" aria-labelledby="user-title">
        <img v-if="data.avatar_url" :src="data.avatar_url" alt="" class="account-avatar" width="72" height="72">
        <span v-else class="account-avatar account-avatar-text" aria-hidden="true">{{ Array.from(data.nickname)[0] }}</span>
        <div class="user-head-main">
          <h1 id="user-title">{{ data.nickname }} <span v-if="data.role === 'ADMIN'" class="owner-badge">管理员</span></h1>
          <p class="muted">{{ data.email }}</p>
          <p class="user-badges">
            <span class="badge" :class="data.status === 'ACTIVE' ? 'badge-success' : 'badge-danger'">{{ data.status === 'ACTIVE' ? '账号正常' : '已禁用' }}</span>
            <span class="badge" :class="data.email_verified ? 'badge-success' : 'badge-warning'">{{ data.email_verified ? '邮箱已验证' : '邮箱未验证' }}</span>
          </p>
        </div>
        <div class="user-actions">
          <button v-if="data.can_disable" type="button" class="button button-danger" :disabled="busy" @click="setStatus(true)">禁用账号</button>
          <button v-if="data.can_enable" type="button" class="button" :disabled="busy" @click="setStatus(false)">恢复账号</button>
          <p v-if="data.is_self" class="muted">这是你当前登录的账号。</p>
          <p v-else-if="data.role === 'ADMIN'" class="muted">管理员账号不能在后台禁用。</p>
        </div>
      </section>
      <p v-if="notice" class="alert alert-success" role="status">{{ notice }}</p>
      <p v-if="actionError" class="alert alert-error" role="alert">{{ actionError }}</p>

      <section class="account-card" aria-labelledby="user-info">
        <h2 id="user-info">账号信息</h2>
        <dl class="publish-summary">
          <dt>注册时间</dt><dd>{{ formatDateTime(data.created_at) }}</dd>
          <dt>邮箱验证</dt><dd>{{ data.email_verified_at ? formatDateTime(data.email_verified_at) : '未验证' }}</dd>
          <dt>有效登录</dt><dd>{{ data.active_session_count }} 个</dd>
          <dt>评论</dt><dd>共 {{ data.comment_count }} 条（公开 {{ data.visible_comment_count }}，已隐藏 {{ data.hidden_comment_count }}）</dd>
          <dt>简介</dt><dd>{{ data.bio || '—' }}</dd>
        </dl>
      </section>

      <section class="account-card" aria-labelledby="user-comments">
        <div class="section-head">
          <h2 id="user-comments">最近评论</h2>
          <NuxtLink v-if="data.comment_count" :to="{ path: '/admin/comments', query: { author_id: data.id } }">在评论管理中查看全部 →</NuxtLink>
        </div>
        <p v-if="!data.recent_comments.length" class="muted">还没有评论。</p>
        <ol v-else class="my-comments">
          <li v-for="c in data.recent_comments" :key="c.id" class="my-comment">
            <header class="my-comment-head">
              <a v-if="c.article.canonical_path" :href="`${c.article.canonical_path}#comments`" target="_blank" rel="noopener" class="my-comment-article">{{ c.article.title || '（无标题）' }}</a>
              <span v-else class="muted-inline">{{ c.article.title || '（无标题）' }}（未公开）</span>
              <span class="badge" :class="c.status === 'VISIBLE' ? 'badge-success' : c.status === 'HIDDEN' ? 'badge-warning' : 'badge-muted'">{{ STATUS_TEXT[c.status as CommentStatus] }}</span>
            </header>
            <CommentText v-if="c.body_text" :text="c.body_text" />
            <p v-else class="comment-text comment-gone">{{ placeholderText(c.status as CommentStatus) }}</p>
            <footer class="my-comment-foot"><time :datetime="c.created_at">{{ formatDateTime(c.created_at) }}</time></footer>
          </li>
        </ol>
      </section>
    </template>
    <ConfirmDialog ref="confirmer" />
  </div>
</template>
