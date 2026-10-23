<script setup lang="ts">
useHead({ title: '登录 · XiaoWork Blog' })
const route = useRoute()
const api = useApi()
const { user, refresh } = useAuth()

const email = ref('')
const password = ref('')
const pending = ref(false)
const error = ref('')
const unverified = ref(false)

async function submit() {
  error.value = ''
  unverified.value = false
  pending.value = true
  try {
    await api.post('/api/v1/auth/login', { email: email.value, password: password.value })
    api.resetCsrf() // the CSRF context is now the new session
    await refresh()
    await navigateTo(safeRedirect(route.query.redirect))
  }
  catch (err) {
    if (err instanceof ApiError && err.code === 'EMAIL_NOT_VERIFIED') unverified.value = true
    error.value = describeError(err)
  }
  finally {
    pending.value = false
    password.value = ''
  }
}
</script>

<template>
  <AuthCard title="登录" subtitle="使用注册邮箱和密码登录。">
    <div v-if="user" class="alert alert-info" role="status">
      你已登录为 <strong>{{ user.nickname }}</strong>。<NuxtLink to="/">返回首页</NuxtLink>
    </div>
    <form v-else novalidate @submit.prevent="submit">
      <FormField id="email" v-model="email" label="邮箱" type="email" autocomplete="email" required :maxlength="254" />
      <FormField id="password" v-model="password" label="密码" type="password" autocomplete="current-password" required :maxlength="128" />
      <p v-if="error" class="alert alert-error" role="alert">{{ error }}</p>
      <button type="submit" class="button button-block" :disabled="pending || !email || !password">
        {{ pending ? '登录中…' : '登录' }}
      </button>
      <p class="auth-links">
        <NuxtLink to="/auth/forgot-password">忘记密码？</NuxtLink>
      </p>
    </form>
    <div v-if="unverified" class="stacked">
      <p class="muted">没有收到验证邮件？可以重新发送：</p>
      <ResendVerification :email="email" />
    </div>
    <template #footer>
      还没有账号？<NuxtLink to="/auth/register">注册</NuxtLink>
    </template>
  </AuthCard>
</template>
