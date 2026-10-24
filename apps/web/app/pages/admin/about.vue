<script setup lang="ts">
// /admin/about (UI/UX §7.8, API-DD §5.8): the about page is the exception to drafts — saving publishes it
// at once, and the page says so plainly. The preview uses the same safe renderer as the public page.
import { ApiError, describeError } from '~/composables/useApi'
import { createDocumentRenderer, loadHighlighter } from '~/utils/markdown'
import type { MediaItem } from '~/utils/media'
import { formatDateTime } from '~/utils/public'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: '关于页 · XiaoWork Blog 管理后台' })

interface AboutAdmin {
  exists: boolean
  title: string
  body_markdown: string
  updated_at: string | null
  updated_by: string | null
}

const api = useApi()
const title = ref('')
const body = ref('')
const saved = ref<AboutAdmin | null>(null)
const failure = ref('')
const saving = ref(false)
const notice = ref('')
const saveError = ref('')
const fieldErrors = ref<Record<string, string>>({})
const mode = ref<'split' | 'source' | 'preview'>('split')
const dirty = computed(() => !!saved.value && (title.value !== saved.value.title || body.value !== saved.value.body_markdown))

async function load() {
  failure.value = ''
  try {
    const a = await api.get<AboutAdmin>('/api/v1/admin/pages/about')
    saved.value = a
    title.value = a.title
    body.value = a.body_markdown
  }
  catch (err) {
    failure.value = describeError(err)
  }
}

// Preview: renders at once without highlighting, then upgrades when Shiki has loaded.
const renderDoc = shallowRef(createDocumentRenderer(null))
const preview = computed(() => renderDoc.value(body.value).html)
onMounted(async () => {
  await load()
  try {
    renderDoc.value = createDocumentRenderer(await loadHighlighter())
  }
  catch { /* keep the plain renderer */ }
})

const confirmer = ref<{ ask: (q: { title: string, message?: string, confirmLabel?: string }) => Promise<boolean> } | null>(null)

async function save() {
  fieldErrors.value = {}
  saveError.value = ''
  notice.value = ''
  if (!title.value.trim()) {
    fieldErrors.value.title = '请填写标题'
    return
  }
  const ok = await confirmer.value?.ask({
    title: '保存并立即公开？', message: '关于页没有草稿：保存后访客马上看到新内容。', confirmLabel: '保存并公开',
  })
  if (!ok) return
  saving.value = true
  try {
    const a = await api.put<AboutAdmin>('/api/v1/admin/pages/about', { title: title.value, body_markdown: body.value })
    if (a) {
      saved.value = a
      title.value = a.title
      body.value = a.body_markdown
    }
    notice.value = '已保存，关于页已更新。'
  }
  catch (err) {
    if (err instanceof ApiError && err.code === 'MEDIA_NOT_READY') saveError.value = '正文引用的图片不存在、已删除或不能用于页面，请检查图片链接。'
    else if (err instanceof ApiError && Object.keys(err.fields).length) {
      if (err.fields.title) fieldErrors.value.title = err.fields.title.includes('TOO_LONG') ? '标题最多 160 个字' : '标题不符合要求'
      if (err.fields.body_markdown) fieldErrors.value.body = err.fields.body_markdown.includes('TOO_LONG') ? '正文过长（最多 1 MiB）' : '正文包含不允许的字符'
      saveError.value = '请检查标出的内容。'
    }
    else saveError.value = describeError(err)
  }
  finally {
    saving.value = false
  }
}

// ---- images and leave guard ----
const editor = ref<{ insertText: (s: string) => void } | null>(null)
const picker = ref<{ open: () => void } | null>(null)
function insertImage(m: MediaItem) {
  const alt = m.original_filename.replace(/\.[a-z0-9]+$/i, '').replace(/[[\]]/g, '')
  editor.value?.insertText(`\n![${alt}](${m.url})\n`)
}
function onNotice(message: string) {
  saveError.value = message
}

function beforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value) e.preventDefault()
}
onMounted(() => window.addEventListener('beforeunload', beforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
onBeforeRouteLeave(async () => {
  if (!dirty.value) return true
  return await confirmer.value?.ask({ title: '离开此页？', message: '有尚未保存的修改，离开后会丢失。', confirmLabel: '离开' }) ?? false
})
</script>

<template>
  <div class="admin-page about-admin">
    <div class="admin-page-head with-action">
      <div>
        <h1>关于页</h1>
        <p class="muted">
          <template v-if="saved?.exists && saved.updated_at">最近由 {{ saved.updated_by }} 保存于 {{ formatDateTime(saved.updated_at) }}。</template>
          <template v-else-if="saved">尚未保存过：前台关于页显示个人栏简介。</template>
          <NuxtLink to="/about" target="_blank"> 查看前台 ↗</NuxtLink>
        </p>
      </div>
      <button type="button" class="button" :disabled="saving || !saved" @click="save">{{ saving ? '保存中…' : '保存并公开' }}</button>
    </div>
    <p class="alert alert-warning" role="note">关于页<strong>没有草稿</strong>：点击“保存并公开”后，访客立即看到新内容。</p>
    <p v-if="dirty" class="muted" role="status">有未保存的修改。</p>
    <p v-if="notice" class="alert alert-success" role="status">{{ notice }}</p>
    <p v-if="saveError" class="alert alert-error" role="alert">{{ saveError }}</p>

    <StateBlock v-if="failure" kind="error" title="关于页加载失败" :message="failure" @retry="load" />
    <ListSkeleton v-else-if="!saved" :rows="2" />
    <template v-else>
      <FormField id="about-title" v-model="title" label="标题" required :maxlength="160" :error="fieldErrors.title" />
      <div class="about-toolbar">
        <div class="ws-modes" role="group" aria-label="编辑模式">
          <button type="button" :aria-pressed="mode === 'source'" @click="mode = 'source'">源码</button>
          <button type="button" :aria-pressed="mode === 'split'" @click="mode = 'split'">分栏</button>
          <button type="button" :aria-pressed="mode === 'preview'" @click="mode = 'preview'">预览</button>
        </div>
        <button type="button" class="button button-small button-secondary" @click="picker?.open()">插入图片</button>
      </div>
      <p v-if="fieldErrors.body" class="field-error">⚠ {{ fieldErrors.body }}</p>
      <div class="about-panes" :class="`mode-${mode}`">
        <div v-show="mode !== 'preview'" class="about-source">
          <ClientOnly>
            <MarkdownEditor ref="editor" v-model="body" @save="save" @notice="onNotice" />
            <template #fallback><p class="md-editor-loading">正在加载编辑器…</p></template>
          </ClientOnly>
        </div>
        <div v-show="mode !== 'source'" class="about-preview">
          <h2 class="preview-title">{{ title || '（无标题）' }}</h2>
          <div class="md-body" v-html="preview" />
        </div>
      </div>
    </template>
    <MediaPicker ref="picker" usage-kind="PAGE" title="插入图片" @select="insertImage" />
    <ConfirmDialog ref="confirmer" />
  </div>
</template>
