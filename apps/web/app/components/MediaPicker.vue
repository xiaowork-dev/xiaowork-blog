<script setup lang="ts">
// Reusable image dialog (P3 media library): upload new images or pick existing ones. Used by the article
// editor (ARTICLE), the site settings (SITE) and the about page (PAGE).
// Usage: <MediaPicker ref="picker" usage-kind="SITE" @select="use" /> … picker.value.open()
import type { MediaItem } from '~/utils/media'

const props = withDefaults(defineProps<{ usageKind?: 'ARTICLE' | 'SITE' | 'PAGE', title?: string }>(), { usageKind: 'ARTICLE', title: '选择图片' })
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
        <h2 id="picker-title">{{ props.title }}</h2>
        <button type="button" class="link-button" @click="dialog?.close()">关闭</button>
      </div>
      <MediaUploader :usage-kind="usageKind" @uploaded="m => browser?.prepend(m)" />
      <MediaBrowser ref="browser" mode="select" usage-kind="ARTICLE,SITE,PAGE" @select="choose" />
    </template>
  </dialog>
</template>
