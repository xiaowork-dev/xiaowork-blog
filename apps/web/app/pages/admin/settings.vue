<script setup lang="ts">
// /admin/settings (UI/UX §7.8, PRD ADMIN-07): site name, description, logo / favicon, the left profile
// column (avatar, name, bio, link icons), top navigation and default SEO. Saving takes effect on the
// public site immediately. Deployment secrets (SMTP, database, keys) are environment variables and are
// deliberately not part of this page.
import { ApiError, describeError } from '~/composables/useApi'
import { ICON_LABELS, linkProblem, type AdminSite, type EditableLink } from '~/utils/admin'
import type { MediaItem } from '~/utils/media'
import type { MediaRef } from '~/utils/public'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: '网站设置 · XiaoWork Blog 管理后台' })

const api = useApi()
const failure = ref('')
const loaded = ref(false)

const form = reactive({
  site_title: '', site_description: '', contact_email: '', profile_name: '', profile_bio: '',
  default_seo_title: '', default_seo_description: '',
})
const images = reactive<Record<'profile_avatar' | 'logo' | 'favicon', MediaRef | null>>({ profile_avatar: null, logo: null, favicon: null })
const social = ref<EditableLink[]>([])
const nav = ref<EditableLink[]>([])
const iconKeys = ref<string[]>(['github', 'mail', 'rss', 'link'])

function fill(s: AdminSite) {
  Object.assign(form, {
    site_title: s.site_title, site_description: s.site_description, contact_email: s.contact_email ?? '',
    profile_name: s.profile_name, profile_bio: s.profile_bio,
    default_seo_title: s.default_seo_title ?? '', default_seo_description: s.default_seo_description ?? '',
  })
  images.profile_avatar = s.profile_avatar
  images.logo = s.logo
  images.favicon = s.favicon
  social.value = s.social_links.map(l => ({ ...l }))
  nav.value = s.navigation_links.map(l => ({ ...l }))
  iconKeys.value = s.icon_keys
}

async function load() {
  failure.value = ''
  try {
    fill(await api.get<AdminSite>('/api/v1/admin/site'))
    loaded.value = true
  }
  catch (err) {
    failure.value = describeError(err)
  }
}
onMounted(load)

// ---- basic settings ----
const FIELD_TEXT: Record<string, string> = {
  REQUIRED: '必填', TOO_LONG: '内容过长', INVALID_CHARACTERS: '包含不允许的不可见字符', INVALID_EMAIL: '邮箱格式不正确', INVALID: '格式不正确',
}
const fieldErrors = ref<Record<string, string>>({})
const saving = ref(false)
const saved = ref('')
const saveError = ref('')

async function saveSettings() {
  saving.value = true
  saved.value = ''
  saveError.value = ''
  fieldErrors.value = {}
  try {
    const res = await api.patch<AdminSite>('/api/v1/admin/site', {
      ...form,
      contact_email: form.contact_email.trim() || null,
      default_seo_title: form.default_seo_title.trim() || null,
      default_seo_description: form.default_seo_description.trim() || null,
      profile_avatar_media_id: images.profile_avatar?.id ?? null,
      logo_media_id: images.logo?.id ?? null,
      favicon_media_id: images.favicon?.id ?? null,
    })
    if (res) {
      form.site_title = res.site_title
      form.profile_name = res.profile_name
    }
    clearNuxtData('site') // the public shell reloads /site on the next navigation
    saved.value = '已保存，前台立即生效。'
  }
  catch (err) {
    if (err instanceof ApiError && Object.keys(err.fields).length) {
      for (const [k, codes] of Object.entries(err.fields)) fieldErrors.value[k] = FIELD_TEXT[codes[0] ?? ''] ?? '内容不符合要求'
      saveError.value = '请检查标出的字段。'
    }
    else if (err instanceof ApiError && err.code === 'MEDIA_NOT_READY') saveError.value = '所选图片不可用（可能已被删除或是用户头像），请重新选择。'
    else saveError.value = describeError(err)
  }
  finally {
    saving.value = false
  }
}

// ---- images from the P3 media library ----
const picker = ref<{ open: () => void } | null>(null)
const picking = ref<keyof typeof images | null>(null)
const IMAGE_LABEL = { profile_avatar: '个人栏头像', logo: '网站 Logo', favicon: '网站图标（Favicon）' }

function pick(slot: keyof typeof images) {
  picking.value = slot
  picker.value?.open()
}
function onPicked(m: MediaItem) {
  if (picking.value) images[picking.value] = { id: m.id, url: m.url }
}

// ---- link lists ----
type ListKind = 'social' | 'nav'
const listState = reactive<Record<ListKind, { saving: boolean, saved: string, error: string, fields: Record<string, string> }>>({
  social: { saving: false, saved: '', error: '', fields: {} },
  nav: { saving: false, saved: '', error: '', fields: {} },
})
const LIST_FIELD_TEXT: Record<string, string> = {
  UNSAFE_URL: '只支持安全的 https:// 或 mailto: 链接', INVALID_PATH: '站内链接需以 / 开头，且不能指向后台、账号或接口',
  REQUIRED: '请填写名称', TOO_LONG: '内容过长', INVALID: '格式不正确', TOO_MANY: '数量超过上限', INVALID_CHARACTERS: '包含不可见字符',
}

function listOf(kind: ListKind) {
  return kind === 'social' ? social : nav
}
function move(kind: ListKind, i: number, delta: number) {
  const list = listOf(kind).value
  const j = i + delta
  if (j < 0 || j >= list.length) return
  ;[list[i], list[j]] = [list[j]!, list[i]!]
}
function removeLink(kind: ListKind, i: number) {
  listOf(kind).value.splice(i, 1)
}
function addLink(kind: ListKind) {
  if (kind === 'social') social.value.push({ icon_key: 'link', label: '', url: 'https://', is_enabled: true })
  else nav.value.push({ label: '', link_type: 'INTERNAL', url: '/', is_enabled: true })
}

async function saveLinks(kind: ListKind) {
  const st = listState[kind]
  st.saved = ''
  st.error = ''
  st.fields = {}
  const list = listOf(kind).value
  list.forEach((l, i) => {
    const p = linkProblem(l, kind)
    if (p) st.fields[`links[${i}]`] = p
  })
  if (Object.keys(st.fields).length) {
    st.error = '请修正标出的链接后再保存。'
    return
  }
  st.saving = true
  try {
    const body = list.map(l => kind === 'social'
      ? { id: l.id, icon_key: l.icon_key, label: l.label.trim(), url: l.url.trim(), is_enabled: l.is_enabled }
      : { id: l.id, label: l.label.trim(), link_type: l.link_type, url: l.url.trim(), is_enabled: l.is_enabled })
    const res = await api.put<AdminSite>(`/api/v1/admin/site/${kind === 'social' ? 'social-links' : 'navigation-links'}`, { links: body })
    // Only the saved list is refreshed (its new ids); unsaved edits in the other sections stay as they are.
    if (res && kind === 'social') social.value = res.social_links.map(l => ({ ...l }))
    if (res && kind === 'nav') nav.value = res.navigation_links.map(l => ({ ...l }))
    clearNuxtData('site')
    st.saved = kind === 'nav' && !list.length ? '已保存：前台使用默认导航（首页 / 分类 / 标签 / 归档 / 关于）。' : '已保存，前台立即生效。'
  }
  catch (err) {
    if (err instanceof ApiError && Object.keys(err.fields).length) {
      for (const [k, codes] of Object.entries(err.fields)) {
        const row = /^links\[\d+\]/.exec(k)?.[0] ?? k
        st.fields[row] = LIST_FIELD_TEXT[codes[0] ?? ''] ?? '内容不符合要求'
      }
      st.error = '请修正标出的链接后再保存。'
    }
    else st.error = describeError(err)
  }
  finally {
    st.saving = false
  }
}
</script>

<template>
  <div class="admin-page settings-page">
    <div class="admin-page-head">
      <h1>网站设置</h1>
      <p class="muted">保存后前台立即生效。SMTP、数据库等部署配置通过服务器环境变量管理，不在此处显示或修改。</p>
    </div>
    <ListSkeleton v-if="!loaded && !failure" :rows="2" />
    <StateBlock v-else-if="failure" kind="error" title="设置加载失败" :message="failure" @retry="load" />
    <template v-else>
      <form class="settings-form" novalidate @submit.prevent="saveSettings">
        <section class="account-card" aria-labelledby="s-basic">
          <h2 id="s-basic">站点信息</h2>
          <FormField id="site_title" v-model="form.site_title" label="网站名称" required :maxlength="120" :error="fieldErrors.site_title" />
          <div class="field">
            <label for="site_description">网站描述</label>
            <textarea id="site_description" v-model="form.site_description" rows="2" maxlength="320" />
            <p v-if="fieldErrors.site_description" class="field-error">⚠ {{ fieldErrors.site_description }}</p>
          </div>
          <FormField id="contact_email" v-model="form.contact_email" label="公开联系邮箱（可选）" type="email" :error="fieldErrors.contact_email" hint="会出现在 RSS 等公开位置，请勿填写个人登录邮箱。" />
          <div class="image-slots">
            <div v-for="slot in (['logo', 'favicon'] as const)" :key="slot" class="image-slot">
              <span class="label">{{ IMAGE_LABEL[slot] }}</span>
              <img v-if="images[slot]" :src="images[slot]!.url" :alt="IMAGE_LABEL[slot]" class="slot-preview">
              <span v-else class="slot-empty">未设置</span>
              <div class="slot-actions">
                <button type="button" class="link-button" @click="pick(slot)">{{ images[slot] ? '更换' : '选择图片' }}</button>
                <button v-if="images[slot]" type="button" class="link-button danger" @click="images[slot] = null">移除</button>
              </div>
            </div>
          </div>
        </section>

        <section class="account-card" aria-labelledby="s-profile">
          <h2 id="s-profile">左侧个人栏</h2>
          <div class="image-slot image-slot-row">
            <img v-if="images.profile_avatar" :src="images.profile_avatar.url" alt="个人栏头像" class="account-avatar">
            <span v-else class="account-avatar account-avatar-text" aria-hidden="true">{{ Array.from(form.profile_name || 'X')[0] }}</span>
            <div class="slot-actions">
              <button type="button" class="link-button" @click="pick('profile_avatar')">{{ images.profile_avatar ? '更换头像' : '选择头像' }}</button>
              <button v-if="images.profile_avatar" type="button" class="link-button danger" @click="images.profile_avatar = null">移除</button>
            </div>
          </div>
          <FormField id="profile_name" v-model="form.profile_name" label="昵称" required :maxlength="80" :error="fieldErrors.profile_name" />
          <div class="field">
            <label for="profile_bio">简介</label>
            <textarea id="profile_bio" v-model="form.profile_bio" rows="2" maxlength="240" />
            <p v-if="fieldErrors.profile_bio" class="field-error">⚠ {{ fieldErrors.profile_bio }}</p>
          </div>
        </section>

        <section class="account-card" aria-labelledby="s-seo">
          <h2 id="s-seo">默认 SEO</h2>
          <p class="muted">用于首页等没有单独 SEO 设置的页面；文章页使用文章自己的 SEO 字段。</p>
          <FormField id="default_seo_title" v-model="form.default_seo_title" label="默认标题（可选）" :maxlength="200" :error="fieldErrors.default_seo_title" />
          <div class="field">
            <label for="default_seo_description">默认描述（可选）</label>
            <textarea id="default_seo_description" v-model="form.default_seo_description" rows="2" maxlength="320" />
            <p v-if="fieldErrors.default_seo_description" class="field-error">⚠ {{ fieldErrors.default_seo_description }}</p>
          </div>
        </section>

        <div class="sticky-save">
          <p v-if="saveError" class="alert alert-error" role="alert">{{ saveError }}</p>
          <p v-if="saved" class="alert alert-success" role="status">{{ saved }}</p>
          <button type="submit" class="button" :disabled="saving">{{ saving ? '保存中…' : '保存站点与个人栏设置' }}</button>
        </div>
      </form>

      <section v-for="kind in (['social', 'nav'] as const)" :key="kind" class="account-card" :aria-labelledby="`s-${kind}`">
        <h2 :id="`s-${kind}`">{{ kind === 'social' ? '个人栏外链图标' : '顶部导航' }}</h2>
        <p class="muted">
          <template v-if="kind === 'social'">显示在个人栏的图标链接，只支持 https:// 与 mailto:。顺序即前台显示顺序，停用的链接不显示。</template>
          <template v-else>为空时前台使用默认导航（首页 / 分类 / 标签 / 归档 / 关于）。站内链接以 / 开头，外部链接必须是 https://。</template>
        </p>
        <p v-if="!listOf(kind).value.length" class="muted">（暂无{{ kind === 'social' ? '外链' : '自定义导航' }}）</p>
        <ol class="link-editor">
          <li v-for="(l, i) in listOf(kind).value" :key="l.id ?? `new-${i}`" :class="{ 'is-disabled': !l.is_enabled }">
            <select v-if="kind === 'social'" v-model="l.icon_key" :aria-label="`第 ${i + 1} 个链接的图标`">
              <option v-for="k in iconKeys" :key="k" :value="k">{{ ICON_LABELS[k] ?? k }}</option>
            </select>
            <select v-else v-model="l.link_type" :aria-label="`第 ${i + 1} 个导航的类型`">
              <option value="INTERNAL">站内</option>
              <option value="EXTERNAL">外部</option>
            </select>
            <input v-model="l.label" :aria-label="`第 ${i + 1} 个${kind === 'social' ? '链接' : '导航'}的名称`" placeholder="名称" :maxlength="kind === 'social' ? 80 : 60">
            <input v-model="l.url" class="link-url" :aria-label="`第 ${i + 1} 个${kind === 'social' ? '链接' : '导航'}的地址`" :placeholder="kind === 'social' ? 'https:// 或 mailto:' : '/about 或 https://'" :aria-invalid="listState[kind].fields[`links[${i}]`] ? 'true' : undefined">
            <label class="switch"><input v-model="l.is_enabled" type="checkbox"> 显示</label>
            <span class="link-tools">
              <button type="button" class="icon-button" :aria-label="`上移第 ${i + 1} 项`" :disabled="i === 0" @click="move(kind, i, -1)">↑</button>
              <button type="button" class="icon-button" :aria-label="`下移第 ${i + 1} 项`" :disabled="i === listOf(kind).value.length - 1" @click="move(kind, i, 1)">↓</button>
              <button type="button" class="icon-button danger" :aria-label="`删除第 ${i + 1} 项`" @click="removeLink(kind, i)"><SiteIcon name="close" :size="18" /></button>
            </span>
            <p v-if="listState[kind].fields[`links[${i}]`]" class="field-error link-error">⚠ {{ listState[kind].fields[`links[${i}]`] }}</p>
          </li>
        </ol>
        <div class="link-editor-actions">
          <button type="button" class="button button-small button-secondary" :disabled="listOf(kind).value.length >= (kind === 'social' ? 12 : 10)" @click="addLink(kind)">添加</button>
          <button type="button" class="button button-small" :disabled="listState[kind].saving" @click="saveLinks(kind)">{{ listState[kind].saving ? '保存中…' : '保存' }}</button>
        </div>
        <p v-if="listState[kind].error" class="alert alert-error" role="alert">{{ listState[kind].error }}</p>
        <p v-if="listState[kind].saved" class="alert alert-success" role="status">{{ listState[kind].saved }}</p>
      </section>
    </template>
    <MediaPicker ref="picker" usage-kind="SITE" :title="picking ? `选择${IMAGE_LABEL[picking]}` : '选择图片'" @select="onPicked" />
  </div>
</template>
