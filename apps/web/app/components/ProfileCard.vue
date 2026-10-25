<script setup lang="ts">
// Left profile column (UI/UX §0.1 #2): avatar, name, short bio and configured link icons — nothing else.
import { safeExternalUrl } from '~/utils/public'

const { site } = await useSite()
const profile = computed(() => site.value.profile)
const links = computed(() => site.value.social_links.filter(l => safeExternalUrl(l.url)))
const initial = computed(() => Array.from(profile.value.name.trim())[0]?.toUpperCase() ?? 'X')
const knownIcons = ['github', 'mail', 'rss', 'link']
// A missing avatar file falls back to the initial (also when it failed before hydration).
const avatarFailed = ref(false)
const avatarImg = ref<HTMLImageElement | null>(null)
onMounted(() => {
  const img = avatarImg.value
  if (img?.complete && img.naturalWidth === 0) avatarFailed.value = true
})
</script>

<template>
  <aside class="profile-card" aria-label="博主信息">
    <img v-if="profile.avatar && !avatarFailed" ref="avatarImg" :src="profile.avatar.url" :alt="`${profile.name} 的头像`" class="profile-avatar" width="92" height="92" @error="avatarFailed = true">
    <span v-else class="profile-avatar profile-avatar-text" aria-hidden="true">{{ initial }}</span>
    <h2 class="profile-name">{{ profile.name }}</h2>
    <p v-if="profile.bio" class="profile-bio">{{ profile.bio }}</p>
    <ul v-if="links.length" class="profile-links">
      <li v-for="l in links" :key="l.id">
        <a
          :href="l.url" :aria-label="l.label" :title="l.label"
          v-bind="l.url.startsWith('https:') ? { target: '_blank', rel: 'noopener noreferrer me' } : {}"
        >
          <SiteIcon :name="knownIcons.includes(l.icon_key) ? l.icon_key : 'link'" />
        </a>
      </li>
    </ul>
  </aside>
</template>
