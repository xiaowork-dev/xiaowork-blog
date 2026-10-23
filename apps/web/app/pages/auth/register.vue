<script setup lang="ts">
import { charCount, checkPassword, fieldMessage, PASSWORD_MAX, PASSWORD_MIN } from '~/utils/authForms'

useHead({ title: '注册 · XiaoWork Blog' })
const api = useApi()

const form = reactive({ nickname: '', email: '', password: '', confirm: '' })
const fields = ref<Record<string, string[] | undefined>>({})
const error = ref('')
const pending = ref(false)
const done = ref<{ email: string, delivered: boolean } | null>(null)

function validate(): boolean {
  const f: Record<string, string[] | undefined> = {}
  const nick = charCount(form.nickname.trim())
  if (nick < 1 || nick > 40) f.nickname = ['INVALID_LENGTH']
  if (!form.email.includes('@')) f.email = ['INVALID']
  f.password = checkPassword(form.password)
  if (!f.password && form.password !== form.confirm) f.confirm = ['MISMATCH']
  fields.value = f
  return !Object.values(f).some(Boolean)
}

async function submit() {
  error.value = ''
  if (!validate()) return
  pending.value = true
  try {
    const res = await api.post<{ email_delivery: 'accepted' | 'unavailable' }>('/api/v1/auth/register', {
      email: form.email, nickname: form.nickname, password: form.password,
    })
    done.value = { email: form.email.trim(), delivered: res?.email_delivery === 'accepted' }
  }
  catch (err) {
    if (err instanceof ApiError) {
      fields.value = err.fields
      if (err.code === 'EMAIL_ALREADY_USED') fields.value = { email: ['已被注册，可直接登录或找回密码'] }
    }
    error.value = describeError(err)
  }
  finally {
    pending.value = false
  }
}
</script>

<template>
  <AuthCard title="注册" :subtitle="done ? undefined : '注册并验证邮箱后即可参与评论。'">
    <div v-if="done" class="stacked">
      <p v-if="done.delivered" class="alert alert-success" role="status">
        注册成功！验证邮件已发往 <strong>{{ done.email }}</strong>，请打开邮件中的链接并点击“确认验证”。
      </p>
      <p v-else class="alert alert-warning" role="status">
        账号已创建，但验证邮件<strong>暂时没有发送成功</strong>。请稍后使用下方按钮重新发送。
      </p>
      <ResendVerification :email="done.email" />
      <NuxtLink to="/auth/login" class="button button-block">前往登录</NuxtLink>
    </div>
    <form v-else novalidate @submit.prevent="submit">
      <FormField id="nickname" v-model="form.nickname" label="昵称" autocomplete="nickname" required :maxlength="40"
        :error="fieldMessage(fields.nickname)" hint="1–40 个字符，公开显示在评论中" />
      <FormField id="email" v-model="form.email" label="邮箱" type="email" autocomplete="email" required :maxlength="254"
        :error="fieldMessage(fields.email)" hint="仅用于登录和通知，不会公开" />
      <FormField id="password" v-model="form.password" label="密码" type="password" autocomplete="new-password" required
        :maxlength="128" :error="fieldMessage(fields.password)" :hint="`${PASSWORD_MIN}–${PASSWORD_MAX} 个字符，可使用密码管理器生成`" />
      <FormField id="confirm" v-model="form.confirm" label="确认密码" type="password" autocomplete="new-password" required
        :maxlength="128" :error="fieldMessage(fields.confirm)" />
      <p v-if="error" class="alert alert-error" role="alert">{{ error }}</p>
      <button type="submit" class="button button-block" :disabled="pending">{{ pending ? '提交中…' : '注册' }}</button>
    </form>
    <template #footer>
      已有账号？<NuxtLink to="/auth/login">登录</NuxtLink>
    </template>
  </AuthCard>
</template>
