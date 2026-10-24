<script setup lang="ts">
// Functional admin frame (UI/UX §7.1): fixed left navigation, page content on the right, current admin and
// a way back to the blog on top. Below 768 px the navigation becomes a toggled panel.
const { user } = useAuth()
const route = useRoute()
const menuOpen = ref(false)

const NAV = [
  { to: '/admin', label: '控制台', exact: true },
  { to: '/admin/articles', label: '文章管理' },
  { to: '/admin/taxonomy', label: '分类与标签' },
  { to: '/admin/comments', label: '评论管理' },
  { to: '/admin/users', label: '用户管理' },
  { to: '/admin/media', label: '媒体库' },
  { to: '/admin/settings', label: '网站设置' },
  { to: '/admin/about', label: '关于页' },
]

function active(item: { to: string, exact?: boolean }) {
  return item.exact ? route.path === item.to : route.path === item.to || route.path.startsWith(item.to + '/')
}

watch(() => route.fullPath, () => { menuOpen.value = false })
</script>

<template>
  <div class="admin-shell">
    <a href="#admin-main" class="skip-link">跳到正文</a>
    <header class="admin-top">
      <button
        type="button" class="icon-button admin-menu-button" :aria-expanded="menuOpen" aria-controls="admin-nav"
        aria-label="打开后台导航" @click="menuOpen = !menuOpen"
      >
        <SiteIcon :name="menuOpen ? 'close' : 'menu'" />
      </button>
      <NuxtLink to="/admin" class="brand">XiaoWork Blog</NuxtLink>
      <span class="flag">管理后台</span>
      <span class="who admin-who" :title="user?.email">{{ user?.nickname }}</span>
      <NuxtLink to="/" class="admin-back">返回博客</NuxtLink>
    </header>
    <div class="admin-body">
      <nav id="admin-nav" class="admin-nav" :class="{ open: menuOpen }" aria-label="后台导航">
        <NuxtLink v-for="item in NAV" :key="item.to" :to="item.to" :class="{ 'is-active': active(item) }" :aria-current="active(item) ? 'page' : undefined">
          {{ item.label }}
        </NuxtLink>
      </nav>
      <main id="admin-main" class="admin-main" tabindex="-1">
        <slot />
      </main>
    </div>
  </div>
</template>
