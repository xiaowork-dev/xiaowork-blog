<script setup lang="ts">
// Categories and tags needed by articles (API-DD §5.8). Renames apply to public pages immediately; an
// article's own assignment changes only when it is published.
import { describeError, useApi } from '~/composables/useApi'
import { SLUG_PATTERN, type Term } from '~/utils/articles'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: '分类与标签 · XiaoWork Blog 管理后台' })

const api = useApi()
const kinds = [
  { key: 'categories', label: '分类', hint: '每篇文章最多一个分类；未选择时显示为“未分类”。' },
  { key: 'tags', label: '标签', hint: '一篇文章可以有多个标签。' },
] as const
type Kind = typeof kinds[number]['key']
const lists = reactive<Record<Kind, Term[]>>({ categories: [], tags: [] })
const drafts = reactive<Record<Kind, { name: string, slug: string }>>({ categories: { name: '', slug: '' }, tags: { name: '', slug: '' } })
const editing = ref<{ kind: Kind, id: string, name: string, slug: string } | null>(null)
const error = ref('')
const notice = ref('')

async function load() {
  error.value = ''
  try {
    lists.categories = await api.get<Term[]>('/api/v1/admin/categories')
    lists.tags = await api.get<Term[]>('/api/v1/admin/tags')
  }
  catch (err) {
    error.value = describeError(err)
  }
}
onMounted(load)

function suggestSlug(name: string) {
  return name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
}

async function create(kind: Kind) {
  const d = drafts[kind]
  const slug = d.slug.trim() || suggestSlug(d.name)
  if (!d.name.trim() || !SLUG_PATTERN.test(slug)) {
    error.value = '请填写名称，Slug 只能包含小写字母、数字和连字符（中文名称请手动填写 Slug）。'
    return
  }
  try {
    await api.post(`/api/v1/admin/${kind}`, { name: d.name.trim(), slug })
    drafts[kind] = { name: '', slug: '' }
    notice.value = '已添加'
    await load()
  }
  catch (err) {
    error.value = describeError(err)
  }
}

async function update(kind: Kind, t: Term, patch: Partial<Term>) {
  try {
    await api.patch(`/api/v1/admin/${kind}/${t.id}`, patch)
    editing.value = null
    notice.value = '已保存'
    await load()
  }
  catch (err) {
    error.value = describeError(err)
  }
}

async function remove(kind: Kind, t: Term) {
  if (!window.confirm(`删除“${t.name}”？`)) return
  try {
    await api.del(`/api/v1/admin/${kind}/${t.id}`)
    notice.value = '已删除'
    await load()
  }
  catch (err) {
    error.value = describeError(err)
  }
}
</script>

<template>
  <div class="admin-page">
    <div class="admin-page-head">
      <h1>分类与标签</h1>
      <p class="muted">被草稿或已发布文章使用的分类/标签不能删除，可以先停用。</p>
    </div>
    <p v-if="notice" class="alert alert-success" role="status">{{ notice }}</p>
    <p v-if="error" class="alert alert-error" role="alert">{{ error }}</p>
    <section v-for="k in kinds" :key="k.key" class="term-section" :aria-labelledby="`h-${k.key}`">
      <h2 :id="`h-${k.key}`">{{ k.label }}</h2>
      <p class="muted">{{ k.hint }}</p>
      <form class="term-new" @submit.prevent="create(k.key)">
        <input v-model="drafts[k.key].name" :aria-label="`新${k.label}名称`" :placeholder="`${k.label}名称`" maxlength="60">
        <input v-model="drafts[k.key].slug" :aria-label="`新${k.label} Slug`" :placeholder="suggestSlug(drafts[k.key].name) || 'slug（小写字母/数字/-）'" maxlength="100">
        <button type="submit" class="button button-small">添加</button>
      </form>
      <div class="table-wrap">
        <table class="article-table">
          <thead><tr><th>名称</th><th>Slug</th><th>使用（草稿 / 已发布）</th><th>状态</th><th><span class="visually-hidden">操作</span></th></tr></thead>
          <tbody>
            <tr v-if="!lists[k.key].length"><td colspan="5" class="muted">暂无{{ k.label }}</td></tr>
            <tr v-for="t in lists[k.key]" :key="t.id">
              <template v-if="editing && editing.id === t.id">
                <td><input v-model="editing.name" :aria-label="`${k.label}名称`" maxlength="60"></td>
                <td><input v-model="editing.slug" :aria-label="`${k.label} Slug`" maxlength="100"></td>
              </template>
              <template v-else>
                <td>{{ t.name }}</td>
                <td><code>{{ t.slug }}</code></td>
              </template>
              <td>{{ t.draft_count ?? 0 }} / {{ t.published_count ?? 0 }}</td>
              <td><span class="badge" :class="t.is_active ? 'badge-success' : 'badge-muted'">{{ t.is_active ? '启用' : '停用' }}</span></td>
              <td class="row-actions">
                <template v-if="editing && editing.id === t.id">
                  <button type="button" class="link-button" @click="update(k.key, t, { name: editing.name, slug: editing.slug })">保存</button>
                  <button type="button" class="link-button" @click="editing = null">取消</button>
                </template>
                <template v-else>
                  <button type="button" class="link-button" @click="editing = { kind: k.key, id: t.id, name: t.name, slug: t.slug }">编辑</button>
                  <button type="button" class="link-button" @click="update(k.key, t, { is_active: !t.is_active })">{{ t.is_active ? '停用' : '启用' }}</button>
                  <button type="button" class="link-button danger" @click="remove(k.key, t)">删除</button>
                </template>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>
