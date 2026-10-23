<script setup lang="ts">
// Confirms an e-mail change. The API requires the signed-in session of the same account, so a leaked
// link alone cannot take over an account (API-DD §5.6). The request form itself lives in the user
// centre (/account/security, later phase).
useHead({ title: '确认更换邮箱 · XiaoWork Blog' })
const api = useApi()
const route = useRoute()
const { user, refresh } = useAuth()
const token = ref('')
const state = ref<'ready' | 'missing' | 'pending' | 'done' | 'invalid'>('ready')
const error = ref('')
const newEmail = ref('')

// Keep the link working through the login redirect without exposing the token in the address bar later.
const loginTarget = computed(() => ({ path: '/auth/login', query: { redirect: route.fullPath } }))

onMounted(() => {
  if (!user.value) return // keep the token in the URL so the login redirect can bring it back
  token.value = takeTokenFromUrl()
  if (!token.value) state.value = 'missing'
})

async function confirm() {
  error.value = ''
  state.value = 'pending'
  try {
    const res = await api.post<{ email: string }>('/api/v1/me/change-email/confirm', { token: token.value })
    newEmail.value = res?.email ?? ''
    await refresh()
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
  <AuthCard title="确认更换邮箱">
    <div v-if="!user" class="stacked">
      <p class="alert alert-info" role="status">请先登录<strong>原账号</strong>，再确认更换登录邮箱。</p>
      <NuxtLink :to="loginTarget" class="button button-block">登录后继续</NuxtLink>
    </div>
    <div v-else-if="state === 'ready' || state === 'pending'" class="stacked">
      <p>将账号 <strong>{{ user.nickname }}</strong> 的登录邮箱更换为邮件中的新地址。确认后，其他设备上的登录会失效。</p>
      <p v-if="error" class="alert alert-error" role="alert">{{ error }}</p>
      <button type="button" class="button button-block" :disabled="state === 'pending'" @click="confirm">
        {{ state === 'pending' ? '处理中…' : '确认更换' }}
      </button>
    </div>
    <p v-else-if="state === 'done'" class="alert alert-success" role="status">
      登录邮箱已更换为 <strong>{{ newEmail }}</strong>。
    </p>
    <p v-else class="alert alert-error" role="alert">
      {{ state === 'missing' ? '链接不完整，请从邮件中重新打开。' : '确认链接无效、已过期，或不属于当前登录账号。' }}
    </p>
  </AuthCard>
</template>
