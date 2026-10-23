<script setup lang="ts">
const { site } = await useSite()
const year = new Date().getFullYear()
</script>

<template>
  <footer class="site-footer">
    <span>© {{ year }} {{ site.site_title }}</span>
    <a href="/rss.xml" class="footer-rss"><SiteIcon name="rss" :size="16" /> RSS</a>
  </footer>
</template>
