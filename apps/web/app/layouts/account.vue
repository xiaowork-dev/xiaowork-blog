<script setup lang="ts">
// User centre shell (UI/UX §6.3): shared header, a small section menu and the page content.
const { user } = useAuth()
</script>

<template>
  <div class="site">
    <a href="#main" class="skip-link">跳到正文</a>
    <SiteHeader />
    <div class="account-layout">
      <nav class="account-nav" aria-label="个人中心">
        <p class="account-who">{{ user?.nickname }}</p>
        <NuxtLink to="/account/profile">个人资料</NuxtLink>
        <NuxtLink to="/account/security">账号安全</NuxtLink>
        <NuxtLink to="/account/comments">我的评论</NuxtLink>
        <NuxtLink v-if="user?.role === 'ADMIN'" to="/admin/articles">管理后台</NuxtLink>
      </nav>
      <main id="main" class="account-main" tabindex="-1">
        <slot />
      </main>
    </div>
    <SiteFooter />
  </div>
</template>
