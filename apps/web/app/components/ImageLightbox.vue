<script setup lang="ts">
// Full-size image viewer for article images (PRD 4.2 "图片放大浏览"). Native <dialog>: Esc closes and
// focus returns to the image that opened it.
const dialog = ref<HTMLDialogElement | null>(null)
const src = ref('')
const alt = ref('')
let opener: HTMLElement | null = null

function open(img: HTMLImageElement) {
  opener = img
  src.value = img.currentSrc || img.src
  alt.value = img.alt
  dialog.value?.showModal()
}

function close() {
  dialog.value?.close()
}

function onClose() {
  opener?.focus()
  opener = null
}

defineExpose({ open })
</script>

<template>
  <dialog ref="dialog" class="lightbox" aria-label="图片预览" @close="onClose" @click.self="close">
    <figure>
      <img :src="src" :alt="alt">
      <figcaption v-if="alt">{{ alt }}</figcaption>
    </figure>
    <button type="button" class="icon-button lightbox-close" aria-label="关闭图片预览" @click="close"><SiteIcon name="close" /></button>
  </dialog>
</template>
