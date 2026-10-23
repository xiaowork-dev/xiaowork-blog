<script setup lang="ts">
// Reusable image uploader (UI/UX §7.7): choose, drag & drop or paste; per-file status
// (校验中 → 上传中 → 成功/失败) with progress and retry. Used by the media library and, from P4, the editor.
import { describeError, useApi } from '~/composables/useApi'
import { MEDIA_ACCEPT, MEDIA_MAX_BYTES, formatBytes, namePastedFile, precheckImage, type MediaItem } from '~/utils/media'

const props = withDefaults(defineProps<{
  endpoint?: string
  usageKind?: 'ARTICLE' | 'SITE' | 'PAGE'
  maxBytes?: number
  multiple?: boolean
}>(), { endpoint: '/api/v1/admin/media', usageKind: 'ARTICLE', maxBytes: MEDIA_MAX_BYTES, multiple: true })

const emit = defineEmits<{ uploaded: [media: MediaItem] }>()

interface QueueItem {
  key: number
  file: File
  status: 'checking' | 'uploading' | 'done' | 'error'
  progress: number
  error: string
}

const api = useApi()
const queue = ref<QueueItem[]>([])
const dragging = ref(false)
const input = ref<HTMLInputElement | null>(null)
let seq = 0

async function start(item: QueueItem) {
  item.error = ''
  item.status = 'checking'
  const problem = precheckImage(item.file, props.maxBytes)
  if (problem) {
    item.status = 'error'
    item.error = problem
    return
  }
  item.status = 'uploading'
  item.progress = 0
  const form = new FormData()
  if (props.endpoint.includes('/admin/')) form.append('usage_kind', props.usageKind)
  form.append('file', item.file, item.file.name)
  try {
    const media = await api.upload<MediaItem>(props.endpoint, form, (p) => { item.progress = p })
    item.status = 'done'
    if (media) emit('uploaded', media)
  }
  catch (err) {
    item.status = 'error'
    item.error = describeError(err)
  }
}

function add(files: Iterable<File>) {
  for (const raw of files) {
    const item = reactive<QueueItem>({ key: ++seq, file: namePastedFile(raw), status: 'checking', progress: 0, error: '' })
    queue.value.unshift(item)
    void start(item)
    if (!props.multiple) break
  }
}

function onPick(e: Event) {
  const el = e.target as HTMLInputElement
  if (el.files) add(Array.from(el.files))
  el.value = ''
}

function onDrop(e: DragEvent) {
  dragging.value = false
  if (e.dataTransfer?.files) add(Array.from(e.dataTransfer.files))
}

function onPaste(e: ClipboardEvent) {
  const files = Array.from(e.clipboardData?.files ?? []).filter(f => f.type.startsWith('image/'))
  if (files.length) {
    e.preventDefault()
    add(files)
  }
}

function clearFinished() {
  queue.value = queue.value.filter(i => i.status !== 'done')
}

const statusText: Record<QueueItem['status'], string> = { checking: '校验中', uploading: '上传中', done: '已上传', error: '失败' }
</script>

<template>
  <div class="uploader">
    <div
      class="dropzone"
      :class="{ dragging }"
      tabindex="0"
      role="button"
      aria-label="选择、拖拽或粘贴图片上传"
      @click="input?.click()"
      @keydown.enter.prevent="input?.click()"
      @keydown.space.prevent="input?.click()"
      @dragover.prevent="dragging = true"
      @dragleave="dragging = false"
      @drop.prevent="onDrop"
      @paste="onPaste"
    >
      <strong>点击选择、拖拽或粘贴图片</strong>
      <span class="muted">JPG / PNG / WebP / GIF，单个不超过 {{ formatBytes(maxBytes) }}</span>
      <input ref="input" type="file" :accept="MEDIA_ACCEPT" :multiple="multiple" hidden @change="onPick">
    </div>

    <ul v-if="queue.length" class="upload-queue" aria-live="polite">
      <li v-for="item in queue" :key="item.key" :class="`upload-${item.status}`">
        <span class="upload-name" :title="item.file.name">{{ item.file.name }}</span>
        <span class="upload-size">{{ formatBytes(item.file.size) }}</span>
        <span class="upload-status">
          {{ statusText[item.status] }}<template v-if="item.status === 'uploading'"> {{ Math.round(item.progress * 100) }}%</template>
        </span>
        <progress v-if="item.status === 'uploading'" :value="item.progress" max="1" :aria-label="`${item.file.name} 上传进度`" />
        <span v-if="item.error" class="field-error upload-error">⚠ {{ item.error }}</span>
        <button v-if="item.status === 'error'" type="button" class="link-button" @click="start(item)">重试</button>
      </li>
    </ul>
    <button v-if="queue.some(i => i.status === 'done')" type="button" class="link-button" @click="clearFinished">清除已完成</button>
  </div>
</template>
