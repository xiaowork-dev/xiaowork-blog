<script setup lang="ts">
// About page (UI/UX §4.4): the administrator's saved Markdown (edited in P7); a quiet placeholder until then.
import { useDocumentRenderer } from '~/composables/useMarkdown'
import { failureStatus } from '~/composables/usePublicData'

definePageMeta({ layout: 'blog' })

interface AboutPage {
  title: string
  body_markdown: string
  updated_at: string
}

const { site } = await useSite()
const requestFetch = useRequestFetch()
const { data, status, error, refresh } = await useAsyncData('about', async () => {
  try {
    return await requestFetch<{ data: AboutPage }>('/api/v1/pages/about').then(r => r.data)
  }
  catch (err) {
    if (failureStatus(err) === 404) return null // not written yet: placeholder, not an error
    throw err
  }
})
if (import.meta.server && error.value) {
  const event = useRequestEvent()
  if (event) setResponseStatus(event, 503)
}
const renderDoc = await useDocumentRenderer()
const html = computed(() => (data.value ? renderDoc(data.value.body_markdown).html : ''))

const body = ref<HTMLElement | null>(null)
const lightbox = ref<{ open: (img: HTMLImageElement) => void } | null>(null)
useReadingEnhancements(body, img => lightbox.value?.open(img))

usePageSeo(() => ({
  title: `${data.value?.title || '关于'} · ${site.value.site_title}`,
  description: site.value.profile.bio || site.value.site_description,
  path: '/about',
  siteName: site.value.site_title,
}))
</script>

<template>
  <section class="about-page" aria-labelledby="page-heading">
    <h1 id="page-heading" class="section-title">{{ data?.title || '关于' }}</h1>
    <StateBlock v-if="error" kind="error" title="关于页加载失败" :retrying="status === 'pending'" @retry="refresh()" />
    <div v-else-if="data" ref="body" class="md-body about-body" v-html="html" />
    <div v-else class="about-empty">
      <p>{{ site.profile.bio || '这里还没有介绍内容。' }}</p>
    </div>
    <ImageLightbox ref="lightbox" />
  </section>
</template>
