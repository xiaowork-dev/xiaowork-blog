<script setup lang="ts">
// /account/profile (UI/UX §6.3): avatar (P3 POST /me/avatar), nickname, bio, e-mail and its state.
import { ApiError, describeError } from '~/composables/useApi'

definePageMeta({ layout: 'account', middleware: 'auth' })
useHead({ title: '个人资料 · XiaoWork Blog', meta: [{ name: 'robots', content: 'noindex, nofollow' }] })

const MAX_BIO = 200
const api = useApi()
const { user, refresh } = useAuth()

const nickname = ref(user.value?.nickname ?? '')
const bio = ref(user.value?.bio ?? '')
const saving = ref(false)
const saved = ref(false)
const formError = ref('')
const fieldErrors = ref<Record<string, string>>({})
const bioLength = computed(() => Array.from(bio.value.trim()).length)

const FIELD_TEXT: Record<string, string> = {
  INVALID_LENGTH: '昵称需为 1–40 个字符，且不能包含控制字符。',
  TOO_LONG: `简介不能超过 ${MAX_BIO} 个字。`,
  INVALID_CHARACTERS: '简介包含不允许的不可见字符。',
}

async function saveProfile() {
  saving.value = true
  saved.value = false
  formError.value = ''
  fieldErrors.value = {}
  try {
    await api.patch('/api/v1/me/profile', { nickname: nickname.value, bio: bio.value })
    await refresh()
    nickname.value = user.value?.nickname ?? nickname.value
    bio.value = user.value?.bio ?? bio.value
    saved.value = true
  }
  catch (err) {
    if (err instanceof ApiError && Object.keys(err.fields).length) {
      for (const [k, codes] of Object.entries(err.fields)) fieldErrors.value[k] = FIELD_TEXT[codes[0] ?? ''] ?? '内容不符合要求。'
    }
    else formError.value = describeError(err)
  }
  finally {
    saving.value = false
  }
}

// ---- avatar ----
const fileInput = ref<HTMLInputElement | null>(null)
const preview = ref<string | null>(null)
const uploading = ref(false)
const progress = ref(0)
const avatarError = ref('')
const avatarOk = ref(false)
const shownAvatar = computed(() => preview.value ?? user.value?.avatar_url ?? null)
const avatarBroken = ref(false)
watch(shownAvatar, () => { avatarBroken.value = false })

const AVATAR_ERRORS: Record<string, string> = {
  PAYLOAD_TOO_LARGE: '图片太大，请选择更小的文件。',
  UNSUPPORTED_MEDIA_TYPE: '只支持 JPEG、PNG 或 WebP 图片。',
}

async function onAvatarPicked(e: Event) {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  avatarError.value = ''
  avatarOk.value = false
  if (!['image/jpeg', 'image/png', 'image/webp'].includes(file.type)) {
    avatarError.value = AVATAR_ERRORS.UNSUPPORTED_MEDIA_TYPE!
    return
  }
  const local = URL.createObjectURL(file)
  preview.value = local
  uploading.value = true
  progress.value = 0
  try {
    const form = new FormData()
    form.append('file', file)
    await api.upload('/api/v1/me/avatar', form, (f) => { progress.value = f })
    await refresh()
    avatarOk.value = true
  }
  catch (err) {
    avatarError.value = err instanceof ApiError && AVATAR_ERRORS[err.code] ? AVATAR_ERRORS[err.code]! : describeError(err)
  }
  finally {
    uploading.value = false
    preview.value = null // fall back to the saved avatar (new one on success, old one on failure)
    URL.revokeObjectURL(local)
    if (fileInput.value) fileInput.value.value = ''
  }
}
</script>

<template>
  <div v-if="user" class="account-page">
    <h1 class="section-title">个人资料</h1>

    <section class="account-card" aria-labelledby="avatar-title">
      <h2 id="avatar-title">头像</h2>
      <div class="avatar-row">
        <img v-if="shownAvatar && !avatarBroken" :src="shownAvatar" alt="当前头像" class="account-avatar" width="88" height="88" @error="avatarBroken = true">
        <span v-else class="account-avatar account-avatar-text" aria-hidden="true">{{ Array.from(user.nickname)[0] }}</span>
        <div class="avatar-actions">
          <input ref="fileInput" type="file" accept="image/jpeg,image/png,image/webp" class="visually-hidden" id="avatar-file" @change="onAvatarPicked">
          <label for="avatar-file" class="button button-small" :aria-disabled="uploading">{{ uploading ? '上传中…' : '更换头像' }}</label>
          <p class="muted">支持 JPEG、PNG、WebP；建议使用正方形图片。</p>
          <progress v-if="uploading" :value="progress" max="1" aria-label="上传进度" />
          <p v-if="avatarOk" class="alert alert-success" role="status">头像已更新。</p>
          <p v-if="avatarError" class="alert alert-error" role="alert">{{ avatarError }}</p>
        </div>
      </div>
    </section>

    <section class="account-card" aria-labelledby="email-title">
      <h2 id="email-title">登录邮箱</h2>
      <p class="email-line">
        <span>{{ user.email }}</span>
        <span class="badge" :class="user.email_verified ? 'badge-success' : 'badge-warning'">{{ user.email_verified ? '已验证' : '未验证' }}</span>
      </p>
      <p class="muted">邮箱只用于登录和找回密码，不会在评论中公开。更换邮箱请前往 <NuxtLink to="/account/security">账号安全</NuxtLink>。</p>
      <ResendVerification v-if="!user.email_verified" :email="user.email" />
    </section>

    <section class="account-card" aria-labelledby="info-title">
      <h2 id="info-title">基本信息</h2>
      <form class="stacked" novalidate @submit.prevent="saveProfile">
        <FormField id="nickname" v-model="nickname" label="昵称" autocomplete="nickname" :maxlength="40" required :error="fieldErrors.nickname" hint="1–40 个字符，会显示在你的评论上。" />
        <div class="field">
          <label for="bio">个人简介</label>
          <textarea id="bio" v-model="bio" rows="3" :aria-invalid="fieldErrors.bio ? 'true' : undefined" aria-describedby="bio-hint" />
          <p id="bio-hint" class="field-hint" :class="{ 'field-error': bioLength > MAX_BIO }">{{ bioLength }} / {{ MAX_BIO }}</p>
          <p v-if="fieldErrors.bio" class="field-error">⚠ {{ fieldErrors.bio }}</p>
        </div>
        <p v-if="formError" class="alert alert-error" role="alert">{{ formError }}</p>
        <p v-if="saved" class="alert alert-success" role="status">资料已保存。</p>
        <div><button type="submit" class="button" :disabled="saving">{{ saving ? '保存中…' : '保存资料' }}</button></div>
      </form>
    </section>
  </div>
</template>
