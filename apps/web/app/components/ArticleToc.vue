<script setup lang="ts">
import type { TocEntry } from '~/utils/markdown'

defineProps<{ toc: TocEntry[], activeId?: string }>()
</script>

<template>
  <ol class="toc-list">
    <li v-for="t in toc" :key="t.id" :class="[`toc-l${t.level}`, { active: t.id === activeId }]">
      <a :href="`#${t.id}`" :aria-current="t.id === activeId ? 'location' : undefined">{{ t.text }}</a>
    </li>
  </ol>
</template>
