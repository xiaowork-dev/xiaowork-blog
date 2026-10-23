<script setup lang="ts">
// Media library list (UI/UX §7.7). mode="manage": preview, copy URL / Markdown, delete with reference
// protection. mode="select": click a card to choose it (used by MediaPicker for the P4 editor).
import { ApiError, describeError, useApi } from '~/composables/useApi'
import { copyText, describeReferences, formatBytes, formatDate, type MediaItem } from '~/utils/media'

const props = withDefaults(defineProps<{ mode?: 'manage' | 'select', usageKind?: string }>(), { mode: 'manage', usageKind: '' })
const emit = defineEmits<{ select: [media: MediaItem] }>()

const api = useApi()
const items = ref<MediaItem[]>([])
const page = ref(1)
const totalPages = ref(1)
const total = ref(0)
const query = ref('')
const kind = ref(props.usageKind)
const loading = ref(false)
const error = ref('')
const notice = ref('')
const preview = ref<MediaItem | null>(null)
const pendingDelete = ref<MediaItem | null>(null)
const deleteError = ref('')
const deleting = ref(false)
const previewDialog = ref<HTMLDialogElement | null>(null)
const deleteDialog = ref<HTMLDialogElement | null>(null)

async function load(reset = true) {
  loading.value = true
  error.value = ''
  try {
    const next = reset ? 1 : page.value + 1
    const res = await api.getPage<MediaItem>('/api/v1/admin/media', {
      page: next, page_size: 24, q: query.value.trim() || undefined, usage_kind: kind.value || undefined,
    })
    items.value = reset ? res.data : [...items.value, ...res.data]
    page.value = next
    total.value = res.meta.total ?? items.value.length
    totalPages.value = res.meta.total_pages ?? next
  }
  catch (err) {
    error.value = describeError(err)
  }
  finally {
    loading.value = false
  }
}

let timer: ReturnType<typeof setTimeout> | undefined
watch([query, kind], () => {
  clearTimeout(timer)
  timer = setTimeout(() => load(true), 300)
})
onMounted(() => load(true))

function flash(msg: string) {
  notice.value = msg
  setTimeout(() => { if (notice.value === msg) notice.value = '' }, 2500)
}

async function copy(item: MediaItem, as: 'url' | 'markdown') {
  const url = item.url // site-relative /media/{id}/file, as API-DD §5.7 requires in Markdown
  const text = as === 'url' ? new URL(url, window.location.origin).toString() : `![${item.original_filename}](${url})`
  flash((await copyText(text)) ? (as === 'url' ? '图片地址已复制' : 'Markdown 已复制') : '复制失败，请手动复制')
}

function openPreview(item: MediaItem) {
  preview.value = item
  nextTick(() => previewDialog.value?.showModal())
}

function askDelete(item: MediaItem) {
  pendingDelete.value = item
  deleteError.value = ''
  nextTick(() => deleteDialog.value?.showModal())
}

async function confirmDelete() {
  if (!pendingDelete.value) return
  deleting.value = true
  deleteError.value = ''
  try {
    await api.del(`/api/v1/admin/media/${pendingDelete.value.id}`)
    items.value = items.value.filter(i => i.id !== pendingDelete.value?.id)
    total.value = Math.max(0, total.value - 1)
    deleteDialog.value?.close()
    flash('图片已删除')
  }
  catch (err) {
    deleteError.value = err instanceof ApiError && err.code === 'RESOURCE_IN_USE' ? describeReferences(err.details) : describeError(err)
  }
  finally {
    deleting.value = false
  }
}

function prepend(media: MediaItem) {
  items.value = [{ ...media, reference_count: 0 }, ...items.value.filter(i => i.id !== media.id)]
  total.value++
}

defineExpose({ reload: () => load(true), prepend })
</script>

<template>
  <section class="media-browser" aria-label="媒体列表">
    <div class="media-toolbar">
      <label class="visually-hidden" for="media-search">按文件名搜索</label>
      <input id="media-search" v-model="query" type="search" placeholder="按文件名搜索" maxlength="100">
      <label class="visually-hidden" for="media-kind">用途</label>
      <select v-if="mode === 'manage'" id="media-kind" v-model="kind">
        <option value="">全部用途</option>
        <option value="ARTICLE">文章图片</option>
        <option value="SITE">站点图片</option>
        <option value="PAGE">关于页</option>
        <option value="AVATAR">用户头像</option>
      </select>
      <span class="muted">共 {{ total }} 张</span>
    </div>

    <p v-if="notice" class="alert alert-success" role="status">{{ notice }}</p>
    <div v-if="error" class="alert alert-error" role="alert">
      {{ error }} <button type="button" class="link-button" @click="load(true)">重试</button>
    </div>

    <ul v-if="loading && !items.length" class="media-grid" aria-busy="true" aria-label="加载中">
      <li v-for="n in 8" :key="n" class="media-card skeleton" />
    </ul>
    <div v-else-if="!items.length && !error" class="media-empty">
      <strong>{{ query || kind ? '没有符合条件的图片' : '还没有上传图片' }}</strong>
      <p class="muted">{{ query || kind ? '换个关键词或用途试试。' : '使用上方区域上传第一张图片。' }}</p>
    </div>
    <ul v-else class="media-grid">
      <li v-for="item in items" :key="item.id" class="media-card">
        <button type="button" class="media-thumb" :aria-label="mode === 'select' ? `选择 ${item.original_filename}` : `预览 ${item.original_filename}`"
          @click="mode === 'select' ? emit('select', item) : openPreview(item)">
          <img :src="item.thumbnail_url" :alt="item.original_filename" loading="lazy" decoding="async">
        </button>
        <div class="media-meta">
          <span class="media-name" :title="item.original_filename">{{ item.original_filename }}</span>
          <span class="muted">{{ item.width }}×{{ item.height }} · {{ formatBytes(item.size_bytes) }}</span>
          <span class="muted">{{ formatDate(item.created_at) }}</span>
          <span v-if="item.reference_count" class="badge">已被引用 {{ item.reference_count }} 处</span>
          <span v-else class="badge badge-muted">未被引用（不公开）</span>
        </div>
        <div v-if="mode === 'manage'" class="media-actions">
          <button type="button" class="link-button" @click="copy(item, 'url')">复制地址</button>
          <button type="button" class="link-button" @click="copy(item, 'markdown')">复制 Markdown</button>
          <button type="button" class="link-button danger" @click="askDelete(item)">删除</button>
        </div>
      </li>
    </ul>
    <div v-if="page < totalPages" class="media-more">
      <button type="button" class="button button-secondary" :disabled="loading" @click="load(false)">
        {{ loading ? '加载中…' : '加载更多' }}
      </button>
    </div>

    <dialog ref="previewDialog" class="media-dialog" aria-label="图片预览" @close="preview = null">
      <template v-if="preview">
        <img :src="preview.url" :alt="preview.original_filename" class="media-full">
        <p><strong>{{ preview.original_filename }}</strong></p>
        <p class="muted">
          {{ preview.mime_type }} · {{ preview.width }}×{{ preview.height }} · {{ formatBytes(preview.size_bytes) }}
          · 上传于 {{ formatDate(preview.created_at) }}<template v-if="preview.uploaded_by"> · {{ preview.uploaded_by.nickname }}</template>
        </p>
        <code class="media-url">{{ preview.url }}</code>
        <div class="dialog-actions">
          <button type="button" class="button button-secondary" @click="copy(preview, 'url')">复制地址</button>
          <button type="button" class="button" @click="previewDialog?.close()">关闭</button>
        </div>
      </template>
    </dialog>

    <dialog ref="deleteDialog" class="media-dialog media-dialog-small" aria-labelledby="delete-title">
      <template v-if="pendingDelete">
        <h2 id="delete-title">删除图片？</h2>
        <p>将永久删除 <strong>{{ pendingDelete.original_filename }}</strong>，此操作无法撤销。被文章、头像或站点设置引用的图片不能删除。</p>
        <p v-if="deleteError" class="alert alert-error" role="alert">{{ deleteError }}</p>
        <div class="dialog-actions">
          <button type="button" class="button button-secondary" @click="deleteDialog?.close()">取消</button>
          <button type="button" class="button button-danger" :disabled="deleting" @click="confirmDelete">
            {{ deleting ? '删除中…' : '确认删除' }}
          </button>
        </div>
      </template>
    </dialog>
  </section>
</template>
