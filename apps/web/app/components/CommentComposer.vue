<script setup lang="ts">
// Comment / reply form. The text stays in the box until the server accepts it, so a failed request
// (network, 429, closed comments) never loses what the reader wrote (UI/UX §5.2, §8).
import { ApiError, describeError } from '~/composables/useApi'
import { commentLength, MAX_COMMENT_LENGTH, type PublicComment } from '~/utils/comments'

const props = defineProps<{
  articleId: string
  /** Reply target; absent for a new top-level comment. */
  parent?: { id: string, nickname: string | null }
  autofocus?: boolean
}>()
const emit = defineEmits<{ posted: [comment: PublicComment], cancel: [] }>()

const api = useApi()
const text = ref('')
const sending = ref(false)
const error = ref('')
const textarea = ref<HTMLTextAreaElement | null>(null)
const inputId = useId()
const length = computed(() => commentLength(text.value))
const tooLong = computed(() => length.value > MAX_COMMENT_LENGTH)

onMounted(() => {
  if (props.autofocus) textarea.value?.focus()
})

async function submit() {
  if (sending.value || !length.value || tooLong.value) return
  error.value = ''
  sending.value = true
  try {
    const path = props.parent ? `/api/v1/comments/${props.parent.id}/replies` : `/api/v1/articles/${props.articleId}/comments`
    const created = await api.post<PublicComment>(path, { body_text: text.value })
    if (created) {
      text.value = ''
      emit('posted', created)
    }
  }
  catch (err) {
    if (err instanceof ApiError && err.fields.body_text?.includes('TOO_LONG')) error.value = `评论不能超过 ${MAX_COMMENT_LENGTH} 字。`
    else if (err instanceof ApiError && err.fields.body_text) error.value = '请输入评论内容。'
    else if (err instanceof ApiError && err.code === 'SESSION_EXPIRED') error.value = '登录已过期，请重新登录后再提交（内容已保留）。'
    else error.value = describeError(err)
  }
  finally {
    sending.value = false
  }
}

function onKey(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
    e.preventDefault()
    submit()
  }
  else if (e.key === 'Escape' && props.parent) {
    e.preventDefault()
    emit('cancel')
  }
}
</script>

<template>
  <form class="comment-composer" @submit.prevent="submit">
    <label :for="inputId" :class="parent ? 'composer-target' : 'visually-hidden'">
      {{ parent ? `回复 @${parent.nickname ?? '已删除的评论'}` : '发表评论' }}
    </label>
    <textarea
      :id="inputId" ref="textarea" v-model="text" rows="3" :placeholder="parent ? '写下你的回复…' : '写下你的评论，支持换行、Emoji 和链接'"
      :aria-invalid="tooLong || error ? 'true' : undefined" :aria-describedby="`${inputId}-hint`" @keydown="onKey"
    />
    <div class="composer-bar">
      <span :id="`${inputId}-hint`" class="composer-count" :class="{ over: tooLong }">{{ length }} / {{ MAX_COMMENT_LENGTH }}<span class="visually-hidden"> 字，Ctrl+Enter 提交</span></span>
      <div class="composer-actions">
        <button v-if="parent" type="button" class="button button-small button-secondary" @click="emit('cancel')">取消</button>
        <button type="submit" class="button button-small" :disabled="sending || !length || tooLong">
          {{ sending ? '提交中…' : parent ? '回复' : '发表评论' }}
        </button>
      </div>
    </div>
    <p v-if="error" class="alert alert-error composer-error" role="alert">{{ error }}</p>
  </form>
</template>
