<script setup lang="ts">
// Categories and tags (PRD ADMIN-03, API-DD §5.8): create, rename, describe (categories), reorder,
// enable / disable and delete when unused. Renames apply to public pages immediately; an article's own
// assignment changes only when it is published. Disabled terms are hidden from public lists.
import { ApiError, describeError, useApi } from '~/composables/useApi'
import { SLUG_PATTERN, type Term } from '~/utils/articles'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: '分类与标签 · XiaoWork Blog 管理后台' })

const api = useApi()
const kinds = [
  { key: 'categories', label: '分类', hint: '每篇文章最多一个分类；未选择时显示为“未分类”。停用后不出现在前台分类列表，已有文章不受影响。' },
  { key: 'tags', label: '标签', hint: '一篇文章可以有多个标签。停用后不出现在前台标签列表。' },
] as const
type Kind = typeof kinds[number]['key']
const lists = reactive<Record<Kind, Term[]>>({ categories: [], tags: [] })
const loaded = ref(false)
const drafts = reactive<Record<Kind, { name: string, slug: string }>>({ categories: { name: '', slug: '' }, tags: { name: '', slug: '' } })
const editing = ref<{ kind: Kind, id: string, name: string, slug: string, description: string } | null>(null)
const error = ref('')
const notice = ref('')
const busy = ref(false)
const confirmer = ref<{ ask: (q: { title: string, message?: string, confirmLabel?: string, danger?: boolean }) => Promise<boolean> } | null>(null)

async function load() {
  error.value = ''
  try {
    lists.categories = await api.get<Term[]>('/api/v1/admin/categories')
    lists.tags = await api.get<Term[]>('/api/v1/admin/tags')
    loaded.value = true
  }
  catch (err) {
    error.value = describeError(err)
  }
}
onMounted(load)

function suggestSlug(name: string) {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
}

function explain(err: unknown): string {
  if (err instanceof ApiError && err.code === 'SLUG_TAKEN') return '该 Slug 已被使用，请换一个。'
  if (err instanceof ApiError && err.code === 'RESOURCE_IN_USE') return '仍有草稿或已发布文章在使用，不能删除；可以先停用。'
  if (err instanceof ApiError && err.fields.slug) return 'Slug 只能包含小写字母、数字和单个连字符。'
  if (err instanceof ApiError && err.fields.name) return '名称需为 1–60 个字。'
  return describeError(err)
}

async function run(fn: () => Promise<unknown>, done: string) {
  busy.value = true
  error.value = ''
  notice.value = ''
  try {
    await fn()
    notice.value = done
    await load()
  }
  catch (err) {
    error.value = explain(err)
  }
  finally {
    busy.value = false
  }
}

async function create(kind: Kind) {
  const d = drafts[kind]
  const slug = d.slug.trim() || suggestSlug(d.name)
  if (!d.name.trim() || !SLUG_PATTERN.test(slug)) {
    error.value = '请填写名称；Slug 只能包含小写字母、数字和连字符（中文名称请手动填写 Slug）。'
    return
  }
  // New terms go to the end of the current order.
  const last = lists[kind].reduce((m, t) => Math.max(m, t.sort_order), 0)
  await run(() => api.post(`/api/v1/admin/${kind}`, { name: d.name.trim(), slug, sort_order: last + 10 }), '已添加')
  if (!error.value) drafts[kind] = { name: '', slug: '' }
}

async function saveEdit(kind: Kind, t: Term) {
  const e = editing.value
  if (!e) return
  const patch: Record<string, string> = { name: e.name, slug: e.slug }
  if (kind === 'categories') patch.description = e.description
  await run(() => api.patch(`/api/v1/admin/${kind}/${t.id}`, patch), '已保存')
  if (!error.value) editing.value = null
}

async function toggle(kind: Kind, t: Term) {
  await run(() => api.patch(`/api/v1/admin/${kind}/${t.id}`, { is_active: !t.is_active }), t.is_active ? '已停用' : '已启用')
}

// Reordering rewrites sort_order as 10, 20, 30… for the items whose position changed.
async function move(kind: Kind, i: number, delta: number) {
  const list = [...lists[kind]]
  const j = i + delta
  if (j < 0 || j >= list.length) return
  ;[list[i], list[j]] = [list[j]!, list[i]!]
  const changes = list.map((t, idx) => ({ t, order: (idx + 1) * 10 })).filter(({ t, order }) => t.sort_order !== order)
  await run(async () => {
    for (const { t, order } of changes) await api.patch(`/api/v1/admin/${kind}/${t.id}`, { sort_order: order })
  }, '顺序已更新')
}

async function remove(kind: Kind, t: Term) {
  const ok = await confirmer.value?.ask({
    title: `删除${kind === 'categories' ? '分类' : '标签'}“${t.name}”？`,
    message: '只能删除没有任何草稿或已发布文章使用的项；删除后不可恢复。',
    confirmLabel: '删除', danger: true,
  })
  if (!ok) return
  await run(() => api.del(`/api/v1/admin/${kind}/${t.id}`), '已删除')
}
</script>

<template>
  <div class="admin-page">
    <div class="admin-page-head">
      <h1>分类与标签</h1>
      <p class="muted">顺序即前台显示顺序。被草稿或已发布文章使用的分类/标签不能删除，可以先停用。</p>
    </div>
    <p v-if="notice" class="alert alert-success" role="status">{{ notice }}</p>
    <p v-if="error" class="alert alert-error" role="alert">{{ error }}</p>
    <ListSkeleton v-if="!loaded && !error" :rows="2" />
    <section v-for="k in kinds" v-else :key="k.key" class="term-section" :aria-labelledby="`h-${k.key}`">
      <h2 :id="`h-${k.key}`">{{ k.label }}</h2>
      <p class="muted">{{ k.hint }}</p>
      <form class="term-new" @submit.prevent="create(k.key)">
        <input v-model="drafts[k.key].name" :aria-label="`新${k.label}名称`" :placeholder="`${k.label}名称`" maxlength="60">
        <input v-model="drafts[k.key].slug" :aria-label="`新${k.label} Slug`" :placeholder="suggestSlug(drafts[k.key].name) || 'slug（小写字母/数字/-）'" maxlength="100">
        <button type="submit" class="button button-small" :disabled="busy">添加</button>
      </form>
      <div class="table-wrap">
        <table class="article-table">
          <thead><tr><th>顺序</th><th>名称</th><th>Slug</th><th>使用（草稿 / 已发布）</th><th>状态</th><th><span class="visually-hidden">操作</span></th></tr></thead>
          <tbody>
            <tr v-if="!lists[k.key].length"><td colspan="6" class="muted">暂无{{ k.label }}</td></tr>
            <tr v-for="(t, i) in lists[k.key]" :key="t.id" :class="{ 'is-disabled': !t.is_active }">
              <td class="nowrap">
                <button type="button" class="icon-button" :aria-label="`上移 ${t.name}`" :disabled="busy || i === 0" @click="move(k.key, i, -1)">↑</button>
                <button type="button" class="icon-button" :aria-label="`下移 ${t.name}`" :disabled="busy || i === lists[k.key].length - 1" @click="move(k.key, i, 1)">↓</button>
              </td>
              <template v-if="editing && editing.id === t.id">
                <td>
                  <input v-model="editing.name" :aria-label="`${k.label}名称`" maxlength="60">
                  <input v-if="k.key === 'categories'" v-model="editing.description" class="term-desc-input" :aria-label="`${k.label}说明`" placeholder="说明（可选）" maxlength="300">
                </td>
                <td><input v-model="editing.slug" :aria-label="`${k.label} Slug`" maxlength="100"></td>
              </template>
              <template v-else>
                <td>{{ t.name }}<span v-if="t.description" class="muted small block">{{ t.description }}</span></td>
                <td><code>{{ t.slug }}</code></td>
              </template>
              <td>{{ t.draft_count ?? 0 }} / {{ t.published_count ?? 0 }}</td>
              <td><span class="badge" :class="t.is_active ? 'badge-success' : 'badge-muted'">{{ t.is_active ? '启用' : '停用' }}</span></td>
              <td class="row-actions">
                <template v-if="editing && editing.id === t.id">
                  <button type="button" class="link-button" :disabled="busy" @click="saveEdit(k.key, t)">保存</button>
                  <button type="button" class="link-button" @click="editing = null">取消</button>
                </template>
                <template v-else>
                  <button type="button" class="link-button" @click="editing = { kind: k.key, id: t.id, name: t.name, slug: t.slug, description: t.description ?? '' }">编辑</button>
                  <button type="button" class="link-button" :disabled="busy" @click="toggle(k.key, t)">{{ t.is_active ? '停用' : '启用' }}</button>
                  <button type="button" class="link-button danger" :disabled="busy" @click="remove(k.key, t)">删除</button>
                </template>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
    <ConfirmDialog ref="confirmer" />
  </div>
</template>
