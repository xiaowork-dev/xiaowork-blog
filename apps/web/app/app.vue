<script setup lang="ts">
// The configured favicon (site settings, P7) applies to every page; the browser default is used otherwise.
const { site } = await useSite()
useHead(() => (site.value.favicon ? { link: [{ rel: 'icon', href: site.value.favicon.url, key: 'favicon' }] } : {}))
</script>

<template>
  <NuxtLayout>
    <NuxtPage />
  </NuxtLayout>
</template>
