<script setup lang="ts">
// Article page shell (P8-B12, approved mock-up): the reading column with its own right sidebar (table of
// contents, related articles) instead of the profile column, so long articles get the full width.
</script>

<template>
  <div class="site">
    <a href="#main" class="skip-link">跳到正文</a>
    <SiteHeader />
    <main id="main" class="post-shell" tabindex="-1">
      <slot />
    </main>
    <SiteFooter />
  </div>
</template>
