<script setup lang="ts">
defineProps<{ title: string, subtitle?: string }>()
</script>

<template>
  <main class="auth-page">
    <section class="auth-card" :aria-labelledby="'auth-title'">
      <h1 id="auth-title">{{ title }}</h1>
      <p v-if="subtitle" class="auth-subtitle">{{ subtitle }}</p>
      <slot />
    </section>
    <p v-if="$slots.footer" class="auth-footer">
      <slot name="footer" />
    </p>
  </main>
</template>
