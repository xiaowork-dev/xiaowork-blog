<script setup lang="ts">
// /account/security (UI/UX §6.3): password change, new-email verification and signing out — all on the
// P2 APIs. Changing the password signs out every device (API-DD §5.6: re-login required).
import { ApiError, describeError } from '~/composables/useApi'
import { checkPassword, fieldMessage } from '~/utils/authForms'

definePageMeta({ layout: 'account', middleware: 'auth' })
useHead({ title: '账号安全 · XiaoWork Blog', meta: [{ name: 'robots', content: 'noindex, nofollow' }] })

const api = useApi()
const { user, refresh, logout } = useAuth()

// ---- password ----
const current = ref('')
const next = ref('')
const confirmNext = ref('')
const pwBusy = ref(false)
const pwError = ref('')
const pwFields = ref<Record<string, string>>({})
const pwDone = ref(false)

async function changePassword() {
  pwError.value = ''
  pwFields.value = {}
  const problem = checkPassword(next.value)
  if (problem) pwFields.value.new_password = fieldMessage(problem)
  if (next.value !== confirmNext.value) pwFields.value.confirm = fieldMessage(['MISMATCH'])
  if (!current.value) pwFields.value.current_password = '请输入当前密码。'
  if (Object.keys(pwFields.value).length) return
  pwBusy.value = true
  try {
    await api.post('/api/v1/me/change-password', { current_password: current.value, new_password: next.value })
    pwDone.value = true
    current.value = next.value = confirmNext.value = ''
    api.resetCsrf()
    await refresh() // every session was revoked, this one included
  }
  catch (err) {
    if (err instanceof ApiError && err.fields.current_password) pwFields.value.current_password = '当前密码不正确。'
    else if (err instanceof ApiError && err.fields.new_password?.includes('SAME_AS_CURRENT')) pwFields.value.new_password = '新密码不能与当前密码相同。'
    else if (err instanceof ApiError && err.fields.new_password) pwFields.value.new_password = fieldMessage(err.fields.new_password)
    else pwError.value = describeError(err)
  }
  finally {
    pwBusy.value = false
  }
}

// ---- e-mail change ----
const newEmail = ref('')
const emailBusy = ref(false)
const emailError = ref('')
const emailSentTo = ref('')

async function requestEmailChange() {
  emailError.value = ''
  emailSentTo.value = ''
  emailBusy.value = true
  try {
    await api.post('/api/v1/me/change-email/request', { new_email: newEmail.value })
    emailSentTo.value = newEmail.value.trim()
    newEmail.value = ''
  }
  catch (err) {
    if (err instanceof ApiError && err.code === 'EMAIL_ALREADY_USED') emailError.value = '该邮箱已被其他账号使用。'
    else if (err instanceof ApiError && err.fields.new_email?.includes('SAME_AS_CURRENT')) emailError.value = '新邮箱与当前邮箱相同。'
    else if (err instanceof ApiError && err.fields.new_email) emailError.value = '请输入有效的邮箱地址。'
    else if (err instanceof ApiError && err.code === 'EMAIL_NOT_VERIFIED') emailError.value = '请先验证当前邮箱，再申请更换。'
    else emailError.value = describeError(err)
  }
  finally {
    emailBusy.value = false
  }
}

// ---- sessions ----
const dialog = ref<HTMLDialogElement | null>(null)
const sessionBusy = ref(false)
const sessionError = ref('')

async function logoutHere() {
  sessionBusy.value = true
  try {
    await logout()
    await navigateTo('/')
  }
  finally {
    sessionBusy.value = false
  }
}

async function logoutEverywhere() {
  sessionBusy.value = true
  sessionError.value = ''
  try {
    await api.post('/api/v1/auth/logout-all')
    api.resetCsrf()
    dialog.value?.close()
    await refresh()
    await navigateTo({ path: '/auth/login', query: { redirect: '/account/security' } })
  }
  catch (err) {
    sessionError.value = describeError(err)
  }
  finally {
    sessionBusy.value = false
  }
}
</script>

<template>
  <div class="account-page">
    <h1 class="section-title">账号安全</h1>

    <section class="account-card" aria-labelledby="pw-title">
      <h2 id="pw-title">修改密码</h2>
      <div v-if="pwDone" class="stacked" role="status">
        <p class="alert alert-success">密码已修改。为了安全，所有设备（包括当前设备）都已退出，请使用新密码重新登录。</p>
        <div><NuxtLink :to="{ path: '/auth/login', query: { redirect: '/account/security' } }" class="button">重新登录</NuxtLink></div>
      </div>
      <form v-else class="stacked" novalidate @submit.prevent="changePassword">
        <FormField id="current-password" v-model="current" label="当前密码" type="password" autocomplete="current-password" required :error="pwFields.current_password" />
        <FormField id="new-password" v-model="next" label="新密码" type="password" autocomplete="new-password" required :error="pwFields.new_password" hint="12–128 个字符，可使用密码管理器生成。" />
        <FormField id="confirm-password" v-model="confirmNext" label="确认新密码" type="password" autocomplete="new-password" required :error="pwFields.confirm" />
        <p v-if="pwError" class="alert alert-error" role="alert">{{ pwError }}</p>
        <p class="muted">修改成功后，所有设备都需要重新登录。</p>
        <div><button type="submit" class="button" :disabled="pwBusy">{{ pwBusy ? '提交中…' : '修改密码' }}</button></div>
      </form>
    </section>

    <section v-if="user" class="account-card" aria-labelledby="email-title">
      <h2 id="email-title">更换登录邮箱</h2>
      <p class="muted">当前邮箱：{{ user.email }}。我们会向新邮箱发送确认链接，<strong>需在登录本账号的状态下打开链接</strong>才会生效。</p>
      <p v-if="emailSentTo" class="alert alert-success" role="status">
        确认邮件已发送至 <strong>{{ emailSentTo }}</strong>。邮件可能有几分钟延迟；确认前，登录邮箱保持不变。
      </p>
      <form class="stacked" novalidate @submit.prevent="requestEmailChange">
        <FormField id="new-email" v-model="newEmail" label="新邮箱" type="email" autocomplete="email" required :error="emailError" />
        <div><button type="submit" class="button" :disabled="emailBusy || !newEmail.trim() || !user.email_verified">{{ emailBusy ? '发送中…' : '发送确认邮件' }}</button></div>
        <p v-if="!user.email_verified" class="muted">请先验证当前邮箱，才能更换邮箱。</p>
      </form>
    </section>

    <section v-if="user" class="account-card" aria-labelledby="session-title">
      <h2 id="session-title">登录设备</h2>
      <p class="muted">如果怀疑账号在其他地方登录，可以退出所有设备。</p>
      <div class="session-actions">
        <button type="button" class="button button-secondary" :disabled="sessionBusy" @click="logoutHere">退出当前设备</button>
        <button type="button" class="button button-danger" :disabled="sessionBusy" @click="dialog?.showModal()">退出所有设备</button>
      </div>
    </section>

    <dialog ref="dialog" class="media-dialog media-dialog-small" aria-labelledby="logout-all-title">
      <h2 id="logout-all-title">退出所有设备？</h2>
      <p>所有已登录的浏览器和设备（包括当前设备）都会立即退出，需要重新登录。</p>
      <p v-if="sessionError" class="alert alert-error" role="alert">{{ sessionError }}</p>
      <div class="dialog-actions">
        <button type="button" class="button button-secondary" @click="dialog?.close()">取消</button>
        <button type="button" class="button button-danger" :disabled="sessionBusy" @click="logoutEverywhere">{{ sessionBusy ? '处理中…' : '全部退出' }}</button>
      </div>
    </dialog>
  </div>
</template>
