<script setup lang="ts">
defineProps<{ rows?: number }>()
</script>

<template>
  <div class="list-skeleton" aria-busy="true" aria-label="正在加载">
    <div v-for="i in rows ?? 3" :key="i" class="skeleton-card">
      <span class="skeleton-line w60" /><span class="skeleton-line w90" /><span class="skeleton-line w40" />
    </div>
  </div>
</template>
