<script setup lang="ts">
// Plain shell for account pages (/auth/*): the shared header without the profile column.
</script>

<template>
  <div class="site">
    <a href="#main" class="skip-link">跳到正文</a>
    <SiteHeader />
    <div id="main" class="shell">
      <slot />
    </div>
    <SiteFooter />
  </div>
</template>
