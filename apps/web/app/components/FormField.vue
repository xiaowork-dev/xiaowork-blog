<script setup lang="ts">
// Text/e-mail/password input with a label and an error message linked through aria-describedby
// (UI/UX §6.1: errors are associated with the field, not shown by colour alone).
const props = defineProps<{
  id: string
  label: string
  type?: 'text' | 'email' | 'password'
  autocomplete?: string
  error?: string
  hint?: string
  required?: boolean
  maxlength?: number
}>()
const model = defineModel<string>({ required: true })
const reveal = ref(false)
const inputType = computed(() => (props.type === 'password' && reveal.value ? 'text' : props.type ?? 'text'))
const describedBy = computed(() => [props.error && `${props.id}-error`, props.hint && `${props.id}-hint`].filter(Boolean).join(' ') || undefined)
</script>

<template>
  <div class="field">
    <label :for="id">{{ label }}</label>
    <div class="field-control">
      <input
        :id="id"
        v-model="model"
        :type="inputType"
        :autocomplete="autocomplete"
        :required="required"
        :maxlength="maxlength"
        :aria-invalid="error ? 'true' : undefined"
        :aria-describedby="describedBy"
        spellcheck="false"
      >
      <button
        v-if="type === 'password'"
        type="button"
        class="reveal"
        :aria-pressed="reveal"
        :aria-label="reveal ? '隐藏密码' : '显示密码'"
        @click="reveal = !reveal"
      >
        {{ reveal ? '隐藏' : '显示' }}
      </button>
    </div>
    <p v-if="hint" :id="`${id}-hint`" class="field-hint">{{ hint }}</p>
    <p v-if="error" :id="`${id}-error`" class="field-error">⚠ {{ error }}</p>
  </div>
</template>
