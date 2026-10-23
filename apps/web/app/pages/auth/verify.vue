<script setup lang="ts">
// The e-mail link opens this page; the token is only consumed after the user clicks the button,
// so mail scanners that prefetch links cannot verify by accident (API-DD §3.4 item 5, UI/UX §6.2).
useHead({ title: '验证邮箱 · XiaoWork Blog' })
const api = useApi()
const token = ref('')
const state = ref<'ready' | 'missing' | 'pending' | 'done' | 'invalid'>('ready')
const error = ref('')

onMounted(() => {
  token.value = takeTokenFromUrl()
  if (!token.value) state.value = 'missing'
})

async function confirm() {
  error.value = ''
  state.value = 'pending'
  try {
    await api.post('/api/v1/auth/verify-email', { token: token.value })
    state.value = 'done'
  }
  catch (err) {
    if (err instanceof ApiError && err.code === 'TOKEN_INVALID_OR_EXPIRED') {
      state.value = 'invalid'
      return
    }
    error.value = describeError(err)
    state.value = 'ready'
  }
}
</script>

<template>
  <AuthCard title="验证邮箱">
    <div v-if="state === 'ready' || state === 'pending'" class="stacked">
      <p>点击下方按钮完成邮箱验证。验证后即可登录并参与评论。</p>
      <p v-if="error" class="alert alert-error" role="alert">{{ error }}</p>
      <button type="button" class="button button-block" :disabled="state === 'pending'" @click="confirm">
        {{ state === 'pending' ? '验证中…' : '确认验证' }}
      </button>
    </div>
    <div v-else-if="state === 'done'" class="stacked">
      <p class="alert alert-success" role="status">邮箱验证成功！现在可以登录了。</p>
      <NuxtLink to="/auth/login" class="button button-block">前往登录</NuxtLink>
    </div>
    <div v-else class="stacked">
      <p class="alert alert-error" role="alert">
        {{ state === 'missing' ? '链接不完整，请从邮件中重新打开。' : '验证链接无效、已使用或已过期。' }}
      </p>
      <p class="muted">如果尚未完成验证，可以申请新的验证邮件：</p>
      <ResendVerification />
    </div>
  </AuthCard>
</template>
