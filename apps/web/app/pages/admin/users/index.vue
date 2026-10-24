<script setup lang="ts">
// /admin/users (UI/UX §7.6, PRD ADMIN-05): search by nickname / e-mail, filter by status and verification,
// open a user to see details and comments, disable / enable ordinary accounts.
import { describeError } from '~/composables/useApi'
import type { AdminUser } from '~/utils/admin'
import { formatDay, parsePage, type ListMeta } from '~/utils/public'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: '用户管理 · XiaoWork Blog 管理后台' })

const api = useApi()
const route = useRoute()
const str = (v: unknown) => (typeof v === 'string' ? v : '')
const filters = computed(() => ({
  q: str(route.query.q), status: str(route.query.status), verified: str(route.query.verified), page: parsePage(route.query.page) ?? 1,
}))
const qInput = ref(filters.value.q)

const failure = ref('')
const { data, status, refresh } = await useAsyncData(
  () => `admin-users:${JSON.stringify(filters.value)}`,
  async () => {
    failure.value = ''
    const f = filters.value
    try {
      return await api.getPage<AdminUser>('/api/v1/admin/users', {
        page: f.page, q: f.q || undefined, status: f.status || undefined, verified: f.verified || undefined,
      }) as { data: AdminUser[], meta: ListMeta }
    }
    catch (err) {
      failure.value = describeError(err)
      return null
    }
  },
  { server: false, watch: [filters] },
)

function apply(patch: Record<string, string>) {
  const query: Record<string, string> = {}
  for (const [k, v] of Object.entries({ q: filters.value.q, status: filters.value.status, verified: filters.value.verified, ...patch })) {
    if (v) query[k] = v
  }
  navigateTo({ path: '/admin/users', query })
}
const pagerQuery = computed(() => Object.fromEntries(Object.entries({ q: filters.value.q, status: filters.value.status, verified: filters.value.verified }).filter(([, v]) => v)) as Record<string, string>)
</script>

<template>
  <div class="admin-page">
    <div class="admin-page-head">
      <h1>用户管理</h1>
      <p class="muted">可以禁用违规的普通用户：禁用后其所有登录立即失效，无法再登录或评论；已有评论保留，可在评论管理中处理。管理员账号不能在这里修改。</p>
    </div>

    <form class="media-toolbar" role="search" @submit.prevent="apply({ q: qInput.trim() })">
      <label class="visually-hidden" for="user-q">搜索用户</label>
      <input id="user-q" v-model="qInput" type="search" placeholder="搜索昵称或邮箱" maxlength="100">
      <label class="visually-hidden" for="user-status">账号状态</label>
      <select id="user-status" :value="filters.status" @change="apply({ status: ($event.target as HTMLSelectElement).value })">
        <option value="">全部状态</option>
        <option value="ACTIVE">正常</option>
        <option value="DISABLED">已禁用</option>
      </select>
      <label class="visually-hidden" for="user-verified">邮箱验证</label>
      <select id="user-verified" :value="filters.verified" @change="apply({ verified: ($event.target as HTMLSelectElement).value })">
        <option value="">全部邮箱</option>
        <option value="true">已验证</option>
        <option value="false">未验证</option>
      </select>
      <button type="submit" class="button button-small">搜索</button>
    </form>

    <ListSkeleton v-if="!data && !failure" :rows="3" />
    <StateBlock v-else-if="failure" kind="error" title="用户加载失败" :message="failure" :retrying="status === 'pending'" @retry="refresh()" />
    <StateBlock v-else-if="data && !data.data.length" kind="empty" title="没有符合条件的用户" />
    <template v-else-if="data">
      <p class="muted">共 {{ data.meta.total }} 个账号</p>
      <div class="table-wrap">
        <table class="article-table">
          <thead><tr><th>用户</th><th>邮箱</th><th>状态</th><th>评论</th><th>注册时间</th></tr></thead>
          <tbody>
            <tr v-for="u in data.data" :key="u.id">
              <td>
                <NuxtLink :to="`/admin/users/${u.id}`" class="user-cell">
                  <img v-if="u.avatar_url" :src="u.avatar_url" alt="" width="28" height="28" loading="lazy">
                  <span v-else class="user-initial" aria-hidden="true">{{ Array.from(u.nickname)[0] }}</span>
                  <span>{{ u.nickname }}</span>
                </NuxtLink>
                <span v-if="u.role === 'ADMIN'" class="owner-badge">管理员</span>
              </td>
              <td class="nowrap">{{ u.email }} <span class="badge" :class="u.email_verified ? 'badge-success' : 'badge-warning'">{{ u.email_verified ? '已验证' : '未验证' }}</span></td>
              <td><span class="badge" :class="u.status === 'ACTIVE' ? 'badge-success' : 'badge-danger'">{{ u.status === 'ACTIVE' ? '正常' : '已禁用' }}</span></td>
              <td>{{ u.comment_count }}</td>
              <td class="nowrap">{{ formatDay(u.created_at) }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <PagerNav :page="filters.page" :total-pages="data.meta.total_pages" :query="pagerQuery" />
    </template>
  </div>
</template>
