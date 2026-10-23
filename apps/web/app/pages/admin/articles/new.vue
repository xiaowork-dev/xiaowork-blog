<script setup lang="ts">
// UI/UX §7.4 item 1: creating an article first creates an empty draft on the server, then opens the editor.
import { describeError, useApi } from '~/composables/useApi'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: '新建文章 · XiaoWork Blog 管理后台' })

const api = useApi()
const error = ref('')

async function create() {
  error.value = ''
  try {
    const res = await api.post<{ article_id: string }>('/api/v1/admin/articles')
    await navigateTo(`/admin/articles/${res!.article_id}/edit`, { replace: true })
  }
  catch (err) {
    error.value = describeError(err)
  }
}
onMounted(create)
</script>

<template>
  <div class="admin-page">
    <p v-if="!error" class="muted" role="status">正在创建草稿…</p>
    <div v-else class="alert alert-error" role="alert">
      创建失败：{{ error }} <button type="button" class="link-button" @click="create">重试</button>
    </div>
  </div>
</template>
