<script setup lang="ts">
// /admin control panel (UI/UX §7.2, PRD ADMIN-01): real counts from the database, recent article changes
// and admin activity. No charts, no review-queue numbers (comments are public at once).
import { describeError } from '~/composables/useApi'
import { ARTICLE_STATUS_TEXT, eventText, type Overview } from '~/utils/admin'
import { formatDateTime } from '~/utils/public'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: '控制台 · XiaoWork Blog 管理后台' })

const api = useApi()
const failure = ref('')
const { data, status, refresh } = await useAsyncData('admin-overview', async () => {
  failure.value = ''
  try {
    return await api.get<Overview>('/api/v1/admin/overview')
  }
  catch (err) {
    failure.value = describeError(err)
    return null
  }
}, { server: false })

const cards = computed(() => {
  const o = data.value
  if (!o) return []
  return [
    { label: '文章总数', value: o.article_count, hint: `已发布 ${o.published_count} · 草稿 ${o.draft_count} · 已撤回 ${o.withdrawn_count}`, to: '/admin/articles' },
    { label: '公开文章', value: o.published_count, hint: o.changed_count ? `${o.changed_count} 篇有未发布修改` : '没有未发布的修改', to: '/admin/articles?status=PUBLISHED' },
    { label: '注册用户', value: o.user_count, hint: `近 7 天新增 ${o.new_user_count} · 已禁用 ${o.disabled_user_count}`, to: '/admin/users' },
    { label: '公开评论', value: o.comment_count, hint: `近 7 天 ${o.new_comment_count} 条 · 已隐藏 ${o.hidden_comment_count}`, to: '/admin/comments' },
    { label: '媒体文件', value: o.media_count, hint: `回收站文章 ${o.trashed_count} 篇`, to: '/admin/media' },
  ]
})

const NOTICE_TEXT: Record<string, (n: number) => string> = {
  MEDIA_PENDING_DELETE: n => `有 ${n} 个图片的删除没有完成，请在服务器上运行 blog-admin media-check 修复。`,
  SINGLE_ADMIN: () => '当前只有一个可用的管理员账号，请妥善保管其密码（管理员只能通过 blog-admin 命令创建）。',
}
</script>

<template>
  <div class="admin-page">
    <div class="admin-page-head with-action">
      <div>
        <h1>控制台</h1>
        <p class="muted">站点内容与账号的实时概况。</p>
      </div>
      <NuxtLink to="/admin/articles/new" class="button">新建文章</NuxtLink>
    </div>

    <ListSkeleton v-if="!data && !failure" :rows="2" />
    <StateBlock v-else-if="failure" kind="error" title="概况加载失败" :message="failure" :retrying="status === 'pending'" @retry="refresh()" />
    <template v-else-if="data">
      <ul class="stat-grid">
        <li v-for="c in cards" :key="c.label">
          <NuxtLink :to="c.to" class="stat-card">
            <span class="stat-label">{{ c.label }}</span>
            <span class="stat-value">{{ c.value }}</span>
            <span class="stat-hint">{{ c.hint }}</span>
          </NuxtLink>
        </li>
      </ul>

      <div v-if="data.notices.length" class="notice-list" role="status">
        <p v-for="n in data.notices" :key="n.code" class="alert alert-warning">{{ NOTICE_TEXT[n.code]?.(n.count) ?? n.code }}</p>
      </div>

      <div class="overview-columns">
        <section class="account-card" aria-labelledby="recent-articles">
          <h2 id="recent-articles">最近更新的文章</h2>
          <p v-if="!data.recent_articles.length" class="muted">还没有文章。<NuxtLink to="/admin/articles/new">写第一篇</NuxtLink></p>
          <ul v-else class="overview-list">
            <li v-for="a in data.recent_articles" :key="a.id">
              <NuxtLink :to="`/admin/articles/${a.id}/edit`" class="overview-title">{{ a.title || a.public_title || '（无标题）' }}</NuxtLink>
              <span class="overview-meta">
                <span class="badge" :class="a.status === 'PUBLISHED' ? 'badge-success' : a.status === 'TRASHED' ? 'badge-muted' : 'badge-warning'">{{ ARTICLE_STATUS_TEXT[a.status] }}</span>
                <span v-if="a.has_unpublished_changes" class="badge badge-accent">有未发布修改</span>
                <time :datetime="a.updated_at">{{ formatDateTime(a.updated_at) }}</time>
                <a v-if="a.canonical_path" :href="a.canonical_path" target="_blank" rel="noopener">查看 ↗</a>
              </span>
            </li>
          </ul>
        </section>
        <section class="account-card" aria-labelledby="recent-events">
          <h2 id="recent-events">管理动态</h2>
          <p v-if="!data.recent_events.length" class="muted">暂无管理操作记录。</p>
          <ol v-else class="overview-list">
            <li v-for="e in data.recent_events" :key="e.id">
              <span class="overview-title">{{ e.actor_nickname }} · {{ eventText(e.action) }}</span>
              <span class="overview-meta"><time :datetime="e.created_at">{{ formatDateTime(e.created_at) }}</time></span>
            </li>
          </ol>
        </section>
      </div>
    </template>
  </div>
</template>
