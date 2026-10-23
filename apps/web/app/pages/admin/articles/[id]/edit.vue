<script setup lang="ts">
// Markdown editor workspace (UI/UX §7.4). Every change only edits the draft; going public is always the
// explicit "发布 / 发布更新" action with a confirmation (PRD POST-05/06).
import { ApiError, describeError, useApi } from '~/composables/useApi'
import { useDraftEditor } from '~/composables/useDraftEditor'
import { useLiveMarkdown } from '~/composables/useMarkdown'
import { SLUG_PATTERN, statusBadges, type PublishResult, type Term } from '~/utils/articles'
import { formatDate, type MediaItem } from '~/utils/media'

definePageMeta({ layout: 'editor', middleware: 'admin' })

const route = useRoute()
const id = String(route.params.id)
const api = useApi()
const ed = useDraftEditor(id)
const { form, detail, state } = ed
const render = useLiveMarkdown()
const preview = computed(() => render.value(form.value.body_markdown))

type Mode = 'source' | 'split' | 'preview'
const mode = ref<Mode>(route.query.mode === 'preview' ? 'preview' : 'split')
const showSettings = ref(false)
const loadError = ref('')
const notice = ref<{ text: string, tone: 'error' | 'success' } | null>(null)
const categories = ref<Term[]>([])
const tags = ref<Term[]>([])
const newTag = ref('')
const coverUrl = ref<string | null>(null)

const editor = ref<{ wrap: (b: string, a?: string, s?: string) => void, prefixLines: (p: string | ((i: number) => string)) => void, insertBlock: (t: string) => void, insertText: (t: string) => void } | null>(null)
const imagePicker = ref<{ open: () => void } | null>(null)
const coverPicker = ref<{ open: () => void } | null>(null)
const publishDialog = ref<HTMLDialogElement | null>(null)
const importInput = ref<HTMLInputElement | null>(null)
const publishing = ref(false)
const publishError = ref('')
const published = ref<PublishResult | null>(null)

useHead({ title: computed(() => `${form.value.title || '无标题'} · 编辑文章`) })

onMounted(async () => {
  try {
    await ed.load()
    coverUrl.value = detail.value?.draft.cover?.url ?? null
    ;[categories.value, tags.value] = await Promise.all([
      api.get<Term[]>('/api/v1/admin/categories'), api.get<Term[]>('/api/v1/admin/tags'),
    ])
  }
  catch (err) {
    loadError.value = describeError(err)
  }
})

function flash(text: string, tone: 'error' | 'success' = 'success') {
  notice.value = { text, tone }
  setTimeout(() => { if (notice.value?.text === text) notice.value = null }, 4000)
}

// ---- leaving with unsaved work (POST-03, UI/UX §7.4 item 9) ----
const unsafeToLeave = computed(() => ed.dirty.value || state.value === 'saving' || state.value === 'conflict')
function beforeUnload(e: BeforeUnloadEvent) {
  if (unsafeToLeave.value) {
    e.preventDefault()
    e.returnValue = ''
  }
}
onMounted(() => window.addEventListener('beforeunload', beforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
onBeforeRouteLeave(async () => {
  if (!unsafeToLeave.value) return true
  if (state.value !== 'conflict' && await ed.flush()) return true
  return window.confirm('还有未保存到服务器的修改（本地已备份）。确定离开吗？')
})

// ---- toolbar ----
const tools: { label: string, title: string, run: () => void }[] = [
  { label: 'H2', title: '二级标题', run: () => editor.value?.prefixLines('## ') },
  { label: 'H3', title: '三级标题', run: () => editor.value?.prefixLines('### ') },
  { label: 'B', title: '加粗 (Ctrl+B)', run: () => editor.value?.wrap('**', '**', '加粗文字') },
  { label: 'I', title: '斜体 (Ctrl+I)', run: () => editor.value?.wrap('*', '*', '斜体文字') },
  { label: 'S', title: '删除线', run: () => editor.value?.wrap('~~', '~~', '删除文字') },
  { label: '“”', title: '引用', run: () => editor.value?.prefixLines('> ') },
  { label: '•', title: '无序列表', run: () => editor.value?.prefixLines('- ') },
  { label: '1.', title: '有序列表', run: () => editor.value?.prefixLines(i => `${i + 1}. `) },
  { label: '☐', title: '任务列表', run: () => editor.value?.prefixLines('- [ ] ') },
  { label: '`', title: '行内代码 (Ctrl+E)', run: () => editor.value?.wrap('`', '`', 'code') },
  { label: '{ }', title: '代码块', run: () => editor.value?.insertBlock('```go\n// 代码\n```') },
  { label: '链接', title: '链接 (Ctrl+K)', run: () => editor.value?.wrap('[', '](https://)', '链接文字') },
  { label: '表格', title: '表格', run: () => editor.value?.insertBlock('| 列 1 | 列 2 |\n| --- | --- |\n| 内容 | 内容 |') },
  { label: '—', title: '分隔线', run: () => editor.value?.insertBlock('---') },
]

function insertImage(m: MediaItem) {
  editor.value?.insertText(`![${m.original_filename.replace(/[[\]]/g, '')}](${m.url})`)
}

function chooseCover(m: MediaItem) {
  form.value.cover_media_id = m.id
  coverUrl.value = m.thumbnail_url
}

// ---- import / export .md (POST-11) ----
async function onImport(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  ;(e.target as HTMLInputElement).value = ''
  if (!file) return
  if (file.size > 2 * 1024 * 1024) return flash('Markdown 文件超过 2 MB 上限', 'error')
  const content = await file.text()
  if (form.value.body_markdown.trim() && !window.confirm('导入会替换当前正文（可在保存前撤销：Ctrl+Z）。继续吗？')) return
  form.value.body_markdown = content
  const h1 = /^#\s+(.+)$/m.exec(content)
  if (!form.value.title.trim() && h1) form.value.title = h1[1]!.trim().slice(0, 200)
  flash(`已导入 ${file.name}`)
}

function exportMarkdown() {
  const blob = new Blob([form.value.body_markdown], { type: 'text/markdown;charset=utf-8' })
  const a = document.createElement('a')
  a.href = URL.createObjectURL(blob)
  a.download = `${form.value.candidate_slug || detail.value?.current_publication?.slug || 'article'}.md`
  a.click()
  URL.revokeObjectURL(a.href)
}

// ---- tags ----
function toggleTag(tagId: string) {
  const list = form.value.tag_ids
  form.value.tag_ids = list.includes(tagId) ? list.filter(t => t !== tagId) : [...list, tagId]
}

async function createTag() {
  const name = newTag.value.trim()
  if (!name) return
  const slug = name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
  if (!slug) return flash('新标签需要英文/数字名称，或到“分类标签”页面设置 Slug', 'error')
  try {
    const t = await api.post<Term>('/api/v1/admin/tags', { name, slug })
    if (t) {
      tags.value.push(t)
      form.value.tag_ids = [...form.value.tag_ids, t.id]
    }
    newTag.value = ''
  }
  catch (err) {
    flash(describeError(err), 'error')
  }
}

// ---- publish (UI/UX §7.4 item 8) ----
const isPublic = computed(() => detail.value?.status === 'PUBLISHED')
const publishLabel = computed(() => (detail.value?.current_publication ? '发布更新' : '发布'))
const slugInvalid = computed(() => Boolean(form.value.candidate_slug && !SLUG_PATTERN.test(form.value.candidate_slug.trim())))
const targetSlug = computed(() => form.value.candidate_slug?.trim() || detail.value?.current_publication?.slug || '（发布时根据标题自动生成）')
const publishProblems = computed(() => [
  ...(!form.value.title.trim() ? ['标题不能为空'] : []),
  ...(!form.value.body_markdown.trim() ? ['正文不能为空'] : []),
  ...(slugInvalid.value ? ['Slug 只能包含小写字母、数字和连字符'] : []),
])
const categoryName = computed(() => categories.value.find(c => c.id === form.value.category_id)?.name ?? '未分类')
const tagNames = computed(() => tags.value.filter(t => form.value.tag_ids.includes(t.id)).map(t => t.name))

function openPublish() {
  publishError.value = ''
  published.value = null
  publishDialog.value?.showModal()
}

async function confirmPublish() {
  publishing.value = true
  publishError.value = ''
  try {
    published.value = await ed.publish()
  }
  catch (err) {
    publishError.value = err instanceof ApiError && err.code === 'SLUG_TAKEN'
      ? '该 Slug 已被其他文章使用（含历史地址），请在设置中修改后再发布。'
      : describeError(err)
  }
  finally {
    publishing.value = false
  }
}

async function runLifecycle(action: 'withdraw' | 'trash' | 'restore', question?: string) {
  if (question && !window.confirm(question)) return
  try {
    if (action !== 'restore') await ed.flush()
    await ed.lifecycle(action)
    flash({ withdraw: '已撤回，公开页面不再可访问', trash: '已移至回收站', restore: '已恢复为非公开状态' }[action])
  }
  catch (err) {
    flash(describeError(err), 'error')
  }
}

const saveText = computed(() => {
  switch (state.value) {
    case 'saving': return '正在保存…'
    case 'dirty': return '有未保存修改'
    case 'error': return '保存失败'
    case 'conflict': return '版本冲突'
    default: return ed.savedAt.value ? `已保存于 ${new Date(ed.savedAt.value).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}` : '已保存'
  }
})
const trashed = computed(() => detail.value?.status === 'TRASHED')
</script>

<template>
  <div class="workspace-page">
    <header class="ws-head">
      <NuxtLink to="/admin/articles" class="ws-back">← 文章管理</NuxtLink>
      <label class="visually-hidden" for="ws-title">标题</label>
      <input id="ws-title" v-model="form.title" class="ws-title" placeholder="文章标题" maxlength="200" :readonly="trashed">
      <span v-for="b in detail ? statusBadges(detail.status, detail.has_unpublished_changes) : []" :key="b.label" class="badge" :class="`badge-${b.tone}`">{{ b.label }}</span>
      <span class="ws-save" :class="`save-${state}`" role="status" aria-live="polite">{{ saveText }}</span>
      <div class="ws-modes" role="group" aria-label="编辑模式">
        <button v-for="m in (['source', 'split', 'preview'] as const)" :key="m" type="button" :aria-pressed="mode === m" @click="mode = m">
          {{ { source: '源码', split: '分栏', preview: '预览' }[m] }}
        </button>
      </div>
      <button type="button" class="button button-secondary button-small" :aria-expanded="showSettings" @click="showSettings = !showSettings">文章设置</button>
      <a v-if="isPublic && detail?.current_publication" :href="detail.current_publication.canonical_path" target="_blank" rel="noopener" class="button button-secondary button-small">查看公开文章</a>
      <button type="button" class="button button-small" :disabled="!ed.loaded.value || trashed || state === 'conflict'" @click="openPublish">{{ publishLabel }}</button>
    </header>

    <p v-if="loadError" class="alert alert-error ws-banner" role="alert">{{ loadError }}</p>
    <p v-if="notice" class="alert ws-banner" :class="notice.tone === 'error' ? 'alert-error' : 'alert-success'" role="status">{{ notice.text }}</p>
    <div v-if="trashed" class="alert alert-warning ws-banner">
      文章在回收站中，内容为只读。<button type="button" class="link-button" @click="runLifecycle('restore')">恢复文章</button>（恢复后不会自动公开）
    </div>
    <div v-if="ed.recovered.value" class="alert alert-info ws-banner">
      发现 {{ new Date(ed.recovered.value.at).toLocaleString('zh-CN') }} 未保存到服务器的本地内容
      <template v-if="ed.recovered.value.revision !== ed.revision.value">（基于旧版本 r{{ ed.recovered.value.revision }}，服务器当前为 r{{ ed.revision.value }}，恢复后保存将覆盖服务器草稿）</template>。
      <button type="button" class="link-button" @click="ed.restoreBackup()">恢复本地内容</button>
      <button type="button" class="link-button" @click="ed.dismissBackup()">丢弃</button>
    </div>
    <div v-if="state === 'error'" class="alert alert-error ws-banner" role="alert">
      {{ ed.error.value }}（内容已保存在本地，不会丢失）
      <button type="button" class="link-button" @click="ed.save()">立即重试</button>
    </div>
    <section v-if="ed.conflict.value" class="alert alert-warning ws-banner conflict" role="alert" aria-label="版本冲突">
      <p><strong>草稿已在其他窗口或设备更新</strong>（服务器版本 r{{ ed.conflict.value.serverRevision }}，你的编辑基于 r{{ ed.revision.value }}）。你的本地内容已保留，请选择如何处理：</p>
      <div v-if="ed.conflict.value.server" class="conflict-diff">
        <div>
          <h3>服务器版本</h3>
          <p class="muted">标题：{{ ed.conflict.value.server.draft.title || '（无）' }}</p>
          <pre>{{ ed.conflict.value.server.draft.body_markdown }}</pre>
        </div>
        <div>
          <h3>我的本地内容</h3>
          <p class="muted">标题：{{ form.title || '（无）' }}</p>
          <pre>{{ form.body_markdown }}</pre>
        </div>
      </div>
      <div class="dialog-actions">
        <button type="button" class="button button-secondary" @click="ed.useServerVersion()">载入服务器版本</button>
        <button type="button" class="button" @click="ed.keepMine()">用我的内容覆盖</button>
      </div>
    </section>
    <div v-if="ed.discarded.value" class="alert alert-info ws-banner">
      已载入服务器版本。被替换的本地正文仍可复制：
      <details><summary>查看被替换的内容</summary><pre class="discarded">{{ ed.discarded.value.body_markdown }}</pre></details>
    </div>

    <div class="ws-body" :class="{ 'with-settings': showSettings }">
      <div class="ws-panes" :class="`mode-${mode}`">
        <section v-show="mode !== 'preview'" class="ws-source" aria-label="Markdown 原文">
          <div class="md-toolbar" role="toolbar" aria-label="Markdown 工具栏">
            <button v-for="t in tools" :key="t.title" type="button" :title="t.title" :aria-label="t.title" :disabled="trashed" @click="t.run">{{ t.label }}</button>
            <button type="button" title="插入图片（也可直接粘贴或拖入图片）" :disabled="trashed" @click="imagePicker?.open()">图片</button>
            <span class="md-toolbar-sep" />
            <button type="button" title="导入 .md 文件" :disabled="trashed" @click="importInput?.click()">导入</button>
            <button type="button" title="导出为 .md 文件" @click="exportMarkdown">导出</button>
            <input ref="importInput" type="file" accept=".md,.markdown,text/markdown,text/plain" hidden @change="onImport">
          </div>
          <ClientOnly>
            <MarkdownEditor v-if="ed.loaded.value" :key="trashed ? 'ro' : 'rw'" ref="editor" v-model="form.body_markdown" :readonly="trashed"
              @save="ed.flush()" @notice="(t, tone) => flash(t, tone)" />
            <template #fallback><div class="md-editor md-editor-loading">编辑器加载中…</div></template>
          </ClientOnly>
        </section>
        <article v-show="mode !== 'source'" class="ws-preview md-body" aria-label="实时预览">
          <h1 class="preview-title">{{ form.title || '无标题' }}</h1>
          <div v-html="preview" />
        </article>
      </div>

      <aside v-show="showSettings" class="ws-settings" aria-label="文章设置">
        <div class="ws-settings-head">
          <h2>文章设置</h2>
          <!-- On narrow screens the drawer overlays the header; it must always be closable. -->
          <button type="button" class="button button-secondary button-small" @click="showSettings = false">完成</button>
        </div>
        <p class="muted">设置随草稿自动保存；只有点击“{{ publishLabel }}”后才会公开生效。</p>
        <div class="field">
          <label for="s-slug">Slug（文章地址）</label>
          <input id="s-slug" v-model="form.candidate_slug" placeholder="留空则按标题自动生成" maxlength="160" :aria-invalid="slugInvalid || undefined" :readonly="trashed">
          <p class="field-hint">地址为 /posts/{{ targetSlug }}；已发布文章修改后，旧地址会自动跳转。</p>
          <p v-if="slugInvalid || ed.fieldErrors.value.candidate_slug" class="field-error">⚠ 只能包含小写字母、数字和单个连字符</p>
        </div>
        <div class="field">
          <label for="s-summary">摘要</label>
          <textarea id="s-summary" v-model="form.summary" rows="3" maxlength="500" placeholder="留空则自动截取正文开头" :readonly="trashed" />
        </div>
        <div class="field">
          <label for="s-category">分类</label>
          <select id="s-category" v-model="form.category_id" :disabled="trashed">
            <option :value="null">未分类</option>
            <option v-for="c in categories" :key="c.id" :value="c.id">{{ c.name }}{{ c.is_active ? '' : '（已停用）' }}</option>
          </select>
          <p class="field-hint"><NuxtLink to="/admin/taxonomy" target="_blank">管理分类与标签</NuxtLink></p>
        </div>
        <fieldset class="field tag-field" :disabled="trashed">
          <legend>标签</legend>
          <div class="tag-chips">
            <button v-for="t in tags" :key="t.id" type="button" class="chip" :aria-pressed="form.tag_ids.includes(t.id)" @click="toggleTag(t.id)">{{ t.name }}</button>
            <span v-if="!tags.length" class="muted">暂无标签</span>
          </div>
          <div class="tag-new">
            <input v-model="newTag" placeholder="新标签（英文名）" maxlength="60" @keydown.enter.prevent="createTag">
            <button type="button" class="link-button" @click="createTag">添加</button>
          </div>
        </fieldset>
        <div class="field">
          <span class="label">封面</span>
          <div class="cover-box">
            <img v-if="coverUrl && form.cover_media_id" :src="coverUrl" alt="封面预览">
            <span v-else class="muted">未设置封面</span>
          </div>
          <div class="cover-actions">
            <button type="button" class="link-button" :disabled="trashed" @click="coverPicker?.open()">选择封面</button>
            <button v-if="form.cover_media_id" type="button" class="link-button danger" :disabled="trashed" @click="form.cover_media_id = null">移除</button>
          </div>
        </div>
        <div class="field">
          <label for="s-seo-title">SEO 标题</label>
          <input id="s-seo-title" v-model="form.seo_title" maxlength="200" placeholder="默认使用文章标题" :readonly="trashed">
        </div>
        <div class="field">
          <label for="s-seo-desc">SEO 描述</label>
          <textarea id="s-seo-desc" v-model="form.seo_description" rows="2" maxlength="320" placeholder="默认使用摘要" :readonly="trashed" />
        </div>
        <label class="switch"><input v-model="form.comments_enabled" type="checkbox" :disabled="trashed"> 允许评论（关闭后保留历史评论）</label>
        <hr>
        <div class="danger-zone">
          <button v-if="isPublic" type="button" class="link-button" @click="runLifecycle('withdraw', '撤回后游客将无法访问这篇文章（评论与图片保留）。确定撤回吗？')">撤回发布</button>
          <button v-if="!trashed" type="button" class="link-button danger" @click="runLifecycle('trash', '移至回收站后文章不再公开；评论与图片不会被删除，可从回收站恢复。确定吗？')">移至回收站</button>
        </div>
        <p v-if="detail" class="muted small">
          草稿版本 r{{ detail.draft.revision }}<template v-if="detail.current_publication"> · 公开版本 #{{ detail.current_publication.publication_revision }}，发布于 {{ formatDate(detail.current_publication.published_at) }}</template>
        </p>
      </aside>
    </div>

    <MediaPicker ref="imagePicker" @select="insertImage" />
    <MediaPicker ref="coverPicker" @select="chooseCover" />

    <dialog ref="publishDialog" class="media-dialog media-dialog-small" aria-labelledby="pub-title">
      <template v-if="published">
        <h2 id="pub-title">{{ published.published_changed ? '发布成功' : '公开版本已是最新' }}</h2>
        <p>{{ published.published_changed ? `公开版本已更新为 #${published.publication_revision}。` : '该草稿版本已发布过，公开内容没有变化。' }}</p>
        <div class="dialog-actions">
          <a :href="published.canonical_path" target="_blank" rel="noopener" class="button button-secondary">查看公开文章</a>
          <button type="button" class="button" @click="publishDialog?.close()">继续编辑</button>
        </div>
      </template>
      <template v-else>
        <h2 id="pub-title">确认{{ publishLabel }}？</h2>
        <p class="muted">{{ detail?.current_publication ? '将用当前草稿一次性替换线上版本。' : '文章将首次对外公开。' }}</p>
        <dl class="publish-summary">
          <dt>标题</dt><dd>{{ form.title || '（空）' }}</dd>
          <dt>地址</dt><dd>/posts/{{ targetSlug }}</dd>
          <dt>分类</dt><dd>{{ categoryName }}</dd>
          <dt>标签</dt><dd>{{ tagNames.join('、') || '无' }}</dd>
          <dt>封面</dt><dd>{{ form.cover_media_id ? '已设置' : '无' }}</dd>
          <dt>评论</dt><dd>{{ form.comments_enabled ? '允许' : '关闭' }}</dd>
          <dt>SEO 标题</dt><dd>{{ form.seo_title || '使用文章标题' }}</dd>
        </dl>
        <ul v-if="publishProblems.length" class="alert alert-error" role="alert">
          <li v-for="p in publishProblems" :key="p">{{ p }}</li>
        </ul>
        <p v-if="publishError" class="alert alert-error" role="alert">{{ publishError }}</p>
        <div class="dialog-actions">
          <button type="button" class="button button-secondary" @click="publishDialog?.close()">取消</button>
          <button type="button" class="button" :disabled="publishing || publishProblems.length > 0" @click="confirmPublish">
            {{ publishing ? '发布中…' : `确认${publishLabel}` }}
          </button>
        </div>
      </template>
    </dialog>
  </div>
</template>
