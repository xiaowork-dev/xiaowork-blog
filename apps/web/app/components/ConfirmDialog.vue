<script setup lang="ts">
// Shared confirmation dialog for destructive admin actions (UI/UX §7.1, PRD ADMIN-08 "危险操作须二次确认").
// Usage: const ok = await confirmRef.value.ask({ title, message, confirmLabel, danger })
interface Ask {
  title: string
  message?: string
  quote?: string
  confirmLabel?: string
  danger?: boolean
}

const dialog = ref<HTMLDialogElement | null>(null)
const state = ref<Ask>({ title: '' })
let resolve: ((ok: boolean) => void) | null = null
const titleId = useId()

function ask(input: Ask): Promise<boolean> {
  resolve?.(false)
  state.value = input
  dialog.value?.showModal()
  return new Promise((r) => { resolve = r })
}

function finish(ok: boolean) {
  const r = resolve
  resolve = null
  dialog.value?.close()
  r?.(ok)
}

defineExpose({ ask })
</script>

<template>
  <dialog ref="dialog" class="media-dialog media-dialog-small" :aria-labelledby="titleId" @close="finish(false)">
    <h2 :id="titleId">{{ state.title }}</h2>
    <p v-if="state.message">{{ state.message }}</p>
    <blockquote v-if="state.quote" class="comment-quote">{{ state.quote }}</blockquote>
    <div class="dialog-actions">
      <button type="button" class="button button-secondary" @click="finish(false)">取消</button>
      <button type="button" class="button" :class="{ 'button-danger': state.danger }" @click="finish(true)">{{ state.confirmLabel ?? '确定' }}</button>
    </div>
  </dialog>
</template>
