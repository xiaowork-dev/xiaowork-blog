<script setup lang="ts">
// Top bar shared by every public page (UI/UX §3.1): site name, main navigation, search, theme, account.
// Below 768 px the navigation moves into a drawer with focus management and Esc to close.
import { safeExternalUrl, safeInternalPath } from '~/utils/public'
import { THEME_LABEL } from '~/composables/useTheme'

const { site, navigation } = await useSite()
const { user, logout } = useAuth()
const { choice, cycle } = useTheme()
const route = useRoute()

const menuOpen = ref(false)
const menuButton = ref<HTMLButtonElement | null>(null)
const drawer = ref<HTMLElement | null>(null)
const loggingOut = ref(false)

const links = computed(() => navigation.value.filter(l =>
  l.link_type === 'INTERNAL' ? safeInternalPath(l.url) : safeExternalUrl(l.url)))

const loginLink = computed(() => {
  const here = route.fullPath
  return here.startsWith('/auth/') ? '/auth/login' : { path: '/auth/login', query: { redirect: here } }
})

function isActive(url: string): boolean {
  if (url === '/') return route.path === '/'
  return route.path === url || route.path.startsWith(url + '/')
}

const themeIcon = computed(() => (choice.value === 'light' ? 'sun' : choice.value === 'dark' ? 'moon' : 'monitor'))

async function openMenu() {
  menuOpen.value = true
  await nextTick()
  drawer.value?.querySelector<HTMLElement>('a, button')?.focus()
}

function closeMenu(restoreFocus = true) {
  if (!menuOpen.value) return
  menuOpen.value = false
  if (restoreFocus) menuButton.value?.focus()
}

function onDrawerKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault()
    closeMenu()
    return
  }
  if (e.key !== 'Tab' || !drawer.value) return
  const items = [...drawer.value.querySelectorAll<HTMLElement>('a, button')]
  if (!items.length) return
  const first = items[0]!
  const last = items[items.length - 1]!
  if (e.shiftKey && document.activeElement === first) {
    e.preventDefault()
    last.focus()
  }
  else if (!e.shiftKey && document.activeElement === last) {
    e.preventDefault()
    first.focus()
  }
}

watch(() => route.fullPath, () => closeMenu(false))

async function onLogout() {
  loggingOut.value = true
  try {
    await logout()
    closeMenu(false)
    await navigateTo('/')
  }
  finally {
    loggingOut.value = false
  }
}
</script>

<template>
  <header class="site-header">
    <div class="site-header-inner">
      <NuxtLink to="/" class="brand">{{ site.site_title }}</NuxtLink>
      <nav class="main-nav" aria-label="主导航">
        <template v-for="l in links" :key="l.id">
          <NuxtLink
            v-if="l.link_type === 'INTERNAL'" :to="l.url" :class="{ active: isActive(l.url) }"
            :aria-current="isActive(l.url) ? 'page' : undefined"
          >{{ l.label }}</NuxtLink>
          <a v-else :href="l.url" target="_blank" rel="noopener noreferrer">{{ l.label }}</a>
        </template>
      </nav>
      <div class="header-tools">
        <NuxtLink to="/search" class="icon-button" aria-label="搜索文章" title="搜索"><SiteIcon name="search" /></NuxtLink>
        <button
          type="button" class="icon-button" :aria-label="`主题：${THEME_LABEL[choice]}（点击切换）`"
          :title="`主题：${THEME_LABEL[choice]}`" @click="cycle"
        >
          <ClientOnly><SiteIcon :name="themeIcon" /><template #fallback><SiteIcon name="monitor" /></template></ClientOnly>
        </button>
        <div class="account-links">
          <template v-if="user">
            <NuxtLink v-if="user.role === 'ADMIN'" to="/admin/articles">后台</NuxtLink>
            <NuxtLink to="/account/profile" class="who" :title="`个人中心：${user.nickname}`">{{ user.nickname }}</NuxtLink>
            <button type="button" class="link-button" :disabled="loggingOut" @click="onLogout">退出</button>
          </template>
          <template v-else>
            <NuxtLink :to="loginLink" class="button button-small">登录</NuxtLink>
          </template>
        </div>
        <button
          ref="menuButton" type="button" class="icon-button menu-button" aria-label="打开导航菜单"
          :aria-expanded="menuOpen" aria-controls="mobile-nav" @click="openMenu"
        >
          <SiteIcon name="menu" />
        </button>
      </div>
    </div>
    <!-- Teleported: the header's backdrop-filter would otherwise become the containing block of position: fixed. -->
    <Teleport to="body">
      <div v-if="menuOpen" class="drawer-backdrop" @click="closeMenu()" />
      <div
        v-if="menuOpen" id="mobile-nav" ref="drawer" class="mobile-drawer" role="dialog" aria-modal="true"
        aria-label="导航菜单" @keydown="onDrawerKey"
      >
        <div class="drawer-head">
          <span class="brand">{{ site.site_title }}</span>
          <button type="button" class="icon-button" aria-label="关闭导航菜单" @click="closeMenu()"><SiteIcon name="close" /></button>
        </div>
        <nav aria-label="主导航（移动端）">
          <template v-for="l in links" :key="l.id">
            <NuxtLink v-if="l.link_type === 'INTERNAL'" :to="l.url" :class="{ active: isActive(l.url) }">{{ l.label }}</NuxtLink>
            <a v-else :href="l.url" target="_blank" rel="noopener noreferrer">{{ l.label }}</a>
          </template>
          <NuxtLink to="/search">搜索</NuxtLink>
        </nav>
        <div class="drawer-account">
          <template v-if="user">
            <span class="muted">已登录：{{ user.nickname }}</span>
            <NuxtLink to="/account/profile">个人中心</NuxtLink>
            <NuxtLink to="/account/comments">我的评论</NuxtLink>
            <NuxtLink v-if="user.role === 'ADMIN'" to="/admin/articles">后台管理</NuxtLink>
            <button type="button" class="link-button" :disabled="loggingOut" @click="onLogout">退出登录</button>
          </template>
          <template v-else>
            <NuxtLink :to="loginLink">登录</NuxtLink>
            <NuxtLink to="/auth/register">注册</NuxtLink>
          </template>
        </div>
      </div>
    </Teleport>
  </header>
</template>
