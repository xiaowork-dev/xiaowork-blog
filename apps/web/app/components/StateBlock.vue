<script setup lang="ts">
// Empty / error states with a next step (UI/UX §8). Errors offer a retry and say what happened.
defineProps<{ kind: 'empty' | 'error', title: string, message?: string, retrying?: boolean }>()
defineEmits<{ retry: [] }>()
</script>

<template>
  <div class="state-block" :class="`state-${kind}`" :role="kind === 'error' ? 'alert' : undefined">
    <p class="state-title">{{ title }}</p>
    <p v-if="message" class="state-message">{{ message }}</p>
    <div class="state-actions">
      <button v-if="kind === 'error'" type="button" class="button button-small" :disabled="retrying" @click="$emit('retry')">
        {{ retrying ? '正在重试…' : '重试' }}
      </button>
      <slot />
    </div>
  </div>
</template>
