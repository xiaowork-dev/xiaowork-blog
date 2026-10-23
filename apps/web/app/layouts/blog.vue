<script setup lang="ts">
// Public blog shell (UI/UX §3): left profile column (sticky on desktop), right content; single column
// below 1024 px with the profile card first.
</script>

<template>
  <div class="site">
    <a href="#main" class="skip-link">跳到正文</a>
    <SiteHeader />
    <div class="blog-layout">
      <div class="blog-side">
        <ProfileCard />
      </div>
      <main id="main" class="blog-main" tabindex="-1">
        <slot />
      </main>
    </div>
    <SiteFooter />
  </div>
</template>
