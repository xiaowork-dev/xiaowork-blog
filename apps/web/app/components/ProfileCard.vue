<script setup lang="ts">
// Left profile column (UI/UX §0.1 #2): avatar, name, short bio and configured link icons.
// P8-B12 (approved mock-up): followed by the site's internal navigation — a vertical menu on desktop and a
// row of tabs on narrow screens. It reuses the navigation configured for the header, nothing new.
import { safeExternalUrl, safeInternalPath } from '~/utils/public'

const { site, navigation } = await useSite()
const route = useRoute()
const profile = computed(() => site.value.profile)
const links = computed(() => site.value.social_links.filter(l => safeExternalUrl(l.url)))
const navLinks = computed(() => navigation.value.filter(l => l.link_type === 'INTERNAL' && safeInternalPath(l.url)))
const initial = computed(() => Array.from(profile.value.name.trim())[0]?.toUpperCase() ?? 'X')
const knownIcons = ['github', 'mail', 'rss', 'link']
const NAV_ICONS: Record<string, string> = { '/': 'home', '/archives': 'archive', '/about': 'info', '/categories': 'folder', '/tags': 'tag', '/search': 'search' }
const navIcon = (url: string) => NAV_ICONS[url] ?? 'link'
function isActive(url: string): boolean {
  if (url === '/') return route.path === '/'
  return route.path === url || route.path.startsWith(url + '/')
}
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
    <nav v-if="navLinks.length" class="profile-nav" aria-label="博客导航">
      <NuxtLink
        v-for="l in navLinks" :key="l.id" :to="l.url" :class="{ active: isActive(l.url) }"
        :aria-current="isActive(l.url) ? 'page' : undefined"
      >
        <SiteIcon :name="navIcon(l.url)" :size="17" />{{ l.label }}
      </NuxtLink>
    </nav>
  </aside>
</template>
