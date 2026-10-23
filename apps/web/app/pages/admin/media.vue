<script setup lang="ts">
import type { MediaItem } from '~/utils/media'

definePageMeta({ layout: 'admin', middleware: 'admin' })
useHead({ title: '媒体库 · XiaoWork Blog 管理后台' })

const browser = ref<{ reload: () => void, prepend: (m: MediaItem) => void } | null>(null)
</script>

<template>
  <div class="admin-page">
    <div class="admin-page-head">
      <h1>媒体库</h1>
      <p class="muted">
        新上传的图片默认不公开，只有被已发布文章、站点设置等引用后游客才能访问。被引用的图片无法删除。
      </p>
    </div>
    <MediaUploader @uploaded="m => browser?.prepend(m)" />
    <MediaBrowser ref="browser" />
  </div>
</template>
