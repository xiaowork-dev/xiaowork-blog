<script setup lang="ts">
// Plain-text comment body: interpolated text (escaped by Vue), http(s) links with ugc/nofollow, styled
// @mentions. No v-html anywhere (API-DD §5.5, UI/UX §5.2).
import { commentSegments } from '~/utils/comments'

const props = defineProps<{ text: string }>()
const segments = computed(() => commentSegments(props.text))
</script>

<template>
  <p class="comment-text"><template v-for="(s, i) in segments" :key="i"><a
    v-if="s.kind === 'link'" :href="s.href" target="_blank" rel="nofollow ugc noopener noreferrer"
  >{{ s.text }}</a><span v-else-if="s.kind === 'mention'" class="mention">{{ s.text }}</span><template v-else>{{ s.text }}</template></template></p>
</template>
