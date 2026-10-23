<script setup lang="ts">
// UI/UX §1.2 names this route; API-DD §5.6 (higher priority) sends mail links to /auth/verify.
// Keep both working by redirecting, preserving the query (ADR-0002).
definePageMeta({
  redirect: to => ({ path: '/auth/verify', query: to.query }),
})
</script>

<template>
  <div />
</template>
