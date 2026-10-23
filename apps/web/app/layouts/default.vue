<script setup lang="ts">
const { user, logout } = useAuth()
const route = useRoute()
const loggingOut = ref(false)

const loginLink = computed(() => {
  const here = route.fullPath
  return here.startsWith('/auth/') ? '/auth/login' : { path: '/auth/login', query: { redirect: here } }
})

async function onLogout() {
  loggingOut.value = true
  try {
    await logout()
    await navigateTo('/')
  }
  finally {
    loggingOut.value = false
  }
}
</script>

<template>
  <div class="shell">
    <header class="topbar">
      <NuxtLink to="/" class="brand">XiaoWork Blog</NuxtLink>
      <nav class="account" aria-label="账号">
        <template v-if="user">
          <NuxtLink v-if="user.role === 'ADMIN'" to="/admin/articles">后台</NuxtLink>
          <span class="who" :title="user.email">{{ user.nickname }}</span>
          <button type="button" class="link-button" :disabled="loggingOut" @click="onLogout">退出</button>
        </template>
        <template v-else>
          <NuxtLink :to="loginLink">登录</NuxtLink>
          <NuxtLink to="/auth/register" class="button button-small">注册</NuxtLink>
        </template>
      </nav>
    </header>
    <slot />
  </div>
</template>
