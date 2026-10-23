<script setup lang="ts">
// Functional admin frame (UI/UX §7.1). Only the media library exists so far; other sections arrive in P4–P7.
const { user } = useAuth()
</script>

<template>
  <div class="admin-shell">
    <header class="admin-top">
      <NuxtLink to="/" class="brand">XiaoWork Blog</NuxtLink>
      <span class="flag">管理后台</span>
      <span class="who admin-who">{{ user?.nickname }}</span>
    </header>
    <div class="admin-body">
      <nav class="admin-nav" aria-label="后台导航">
        <NuxtLink to="/admin/media">媒体库</NuxtLink>
        <NuxtLink to="/">返回站点</NuxtLink>
      </nav>
      <main class="admin-main">
        <slot />
      </main>
    </div>
  </div>
</template>
