<script setup lang="ts">
// Bookmark ("收藏") toggle for one article (P8-B12). Guests are sent to the login page; the state is
// loaded in the browser only, so server-rendered pages never contain personal data.
const props = defineProps<{ articleId: string, title?: string, withLabel?: boolean }>()
const { isBookmarked, toggle, load, busy } = useBookmarks()
const { user } = useAuth()
const error = ref('')

onMounted(load)
watch(() => user.value?.id, () => load())

const on = computed(() => isBookmarked(props.articleId))
const label = computed(() => (on.value ? '取消收藏' : '收藏') + (props.title ? `：${props.title}` : ''))

async function onClick() {
  error.value = await toggle(props.articleId)
}
</script>

<template>
  <button
    type="button" class="bookmark-button" :class="{ 'with-label': withLabel }" :aria-pressed="on" :aria-label="label"
    :title="error || label" :disabled="busy.has(articleId)" @click="onClick"
  >
    <SiteIcon name="bookmark" :size="withLabel ? 17 : 18" />
    <span v-if="withLabel">{{ on ? '已收藏' : '收藏' }}</span>
  </button>
</template>
