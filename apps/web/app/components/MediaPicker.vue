<script setup lang="ts">
// Reusable "insert image" dialog for the P4 Markdown editor: upload new images or pick existing ones.
// Usage: <MediaPicker ref="picker" @select="insert" /> … picker.value.open()
import type { MediaItem } from '~/utils/media'

const emit = defineEmits<{ select: [media: MediaItem] }>()
const dialog = ref<HTMLDialogElement | null>(null)
const browser = ref<{ reload: () => void, prepend: (m: MediaItem) => void } | null>(null)
const opened = ref(false)

function open() {
  opened.value = true
  nextTick(() => dialog.value?.showModal())
}

function choose(media: MediaItem) {
  emit('select', media)
  dialog.value?.close()
}

defineExpose({ open })
</script>

<template>
  <dialog ref="dialog" class="media-dialog media-picker" aria-labelledby="picker-title">
    <template v-if="opened">
      <div class="picker-head">
        <h2 id="picker-title">选择图片</h2>
        <button type="button" class="link-button" @click="dialog?.close()">关闭</button>
      </div>
      <MediaUploader @uploaded="m => browser?.prepend(m)" />
      <MediaBrowser ref="browser" mode="select" usage-kind="ARTICLE" @select="choose" />
    </template>
  </dialog>
</template>
