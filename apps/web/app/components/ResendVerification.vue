<script setup lang="ts">
// Request a new verification e-mail. The API answers the same way whether or not the address exists.
const props = defineProps<{ email?: string }>()
const api = useApi()
const email = ref(props.email ?? '')
const state = ref<'idle' | 'sending' | 'sent'>('idle')
const error = ref('')

async function resend() {
  error.value = ''
  state.value = 'sending'
  try {
    await api.post('/api/v1/auth/resend-verification', { email: email.value })
    state.value = 'sent'
  }
  catch (err) {
    error.value = describeError(err)
    state.value = 'idle'
  }
}
</script>

<template>
  <form class="resend" @submit.prevent="resend">
    <FormField id="resend-email" v-model="email" label="注册邮箱" type="email" autocomplete="email" required />
    <p v-if="state === 'sent'" class="alert alert-success" role="status">
      如果该邮箱已注册且尚未验证，我们会发送新的验证链接（之前的链接将失效）。邮件可能有几分钟延迟。
    </p>
    <p v-if="error" class="alert alert-error" role="alert">{{ error }}</p>
    <button type="submit" class="button button-secondary" :disabled="state === 'sending' || !email">
      {{ state === 'sending' ? '发送中…' : '重新发送验证邮件' }}
    </button>
  </form>
</template>
