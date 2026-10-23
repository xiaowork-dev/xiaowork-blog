<script setup lang="ts">
import { checkPassword, fieldMessage, PASSWORD_MAX, PASSWORD_MIN, takeTokenFromUrl } from '~/utils/authForms'

useHead({ title: '重置密码 · XiaoWork Blog' })
const api = useApi()
const { user } = useAuth()
const token = ref('')
const form = reactive({ password: '', confirm: '' })
const fields = ref<Record<string, string[] | undefined>>({})
const state = ref<'form' | 'missing' | 'pending' | 'done' | 'invalid'>('form')
const error = ref('')

onMounted(() => {
  token.value = takeTokenFromUrl()
  if (!token.value) state.value = 'missing'
})

async function submit() {
  error.value = ''
  const problem = checkPassword(form.password, form.confirm)
  fields.value = problem?.[0] === 'MISMATCH' ? { confirm: problem } : { password: problem }
  if (problem) return
  state.value = 'pending'
  try {
    await api.post('/api/v1/auth/reset-password', { token: token.value, new_password: form.password })
    user.value = null // every session was revoked by the API
    state.value = 'done'
  }
  catch (err) {
    if (err instanceof ApiError && err.code === 'TOKEN_INVALID_OR_EXPIRED') {
      state.value = 'invalid'
      return
    }
    if (err instanceof ApiError && err.fields.new_password) fields.value = { password: err.fields.new_password }
    error.value = describeError(err)
    state.value = 'form'
  }
  finally {
    form.password = ''
    form.confirm = ''
  }
}
</script>

<template>
  <AuthCard title="重置密码">
    <form v-if="state === 'form' || state === 'pending'" novalidate @submit.prevent="submit">
      <FormField id="password" v-model="form.password" label="新密码" type="password" autocomplete="new-password" required
        :maxlength="128" :error="fieldMessage(fields.password)" :hint="`${PASSWORD_MIN}–${PASSWORD_MAX} 个字符`" />
      <FormField id="confirm" v-model="form.confirm" label="确认新密码" type="password" autocomplete="new-password" required
        :maxlength="128" :error="fieldMessage(fields.confirm)" />
      <p class="muted">重置成功后，所有设备上的登录都会失效，需要用新密码重新登录。</p>
      <p v-if="error" class="alert alert-error" role="alert">{{ error }}</p>
      <button type="submit" class="button button-block" :disabled="state === 'pending'">
        {{ state === 'pending' ? '提交中…' : '设置新密码' }}
      </button>
    </form>
    <div v-else-if="state === 'done'" class="stacked">
      <p class="alert alert-success" role="status">密码已重置。请使用新密码登录。</p>
      <NuxtLink to="/auth/login" class="button button-block">前往登录</NuxtLink>
    </div>
    <div v-else class="stacked">
      <p class="alert alert-error" role="alert">
        {{ state === 'missing' ? '链接不完整，请从邮件中重新打开。' : '重置链接无效、已使用或已过期。' }}
      </p>
      <NuxtLink to="/auth/forgot-password" class="button button-block">重新申请重置邮件</NuxtLink>
    </div>
  </AuthCard>
</template>
