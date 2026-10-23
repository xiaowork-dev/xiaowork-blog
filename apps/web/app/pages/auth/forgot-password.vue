<script setup lang="ts">
useHead({ title: '忘记密码 · XiaoWork Blog' })
const api = useApi()
const email = ref('')
const pending = ref(false)
const sent = ref(false)
const error = ref('')

async function submit() {
  error.value = ''
  pending.value = true
  try {
    await api.post('/api/v1/auth/forgot-password', { email: email.value })
    sent.value = true
  }
  catch (err) {
    error.value = describeError(err)
  }
  finally {
    pending.value = false
  }
}
</script>

<template>
  <AuthCard title="忘记密码" subtitle="输入注册邮箱，我们会发送一次性的重置链接。">
    <p v-if="sent" class="alert alert-success" role="status">
      如果该邮箱对应一个可用账号，我们将向它发送重置链接（30 分钟内有效）。请留意收件箱和垃圾邮件文件夹。
    </p>
    <form v-else novalidate @submit.prevent="submit">
      <FormField id="email" v-model="email" label="邮箱" type="email" autocomplete="email" required :maxlength="254" />
      <p v-if="error" class="alert alert-error" role="alert">{{ error }}</p>
      <button type="submit" class="button button-block" :disabled="pending || !email">
        {{ pending ? '提交中…' : '发送重置链接' }}
      </button>
    </form>
    <template #footer>
      想起来了？<NuxtLink to="/auth/login">返回登录</NuxtLink>
    </template>
  </AuthCard>
</template>
