import { absoluteUrl } from '~/utils/public'

export interface PageSeo {
  title: string
  description?: string
  /** Site-relative canonical path; omitted for pages that must not be indexed. */
  path?: string
  image?: string | null
  type?: 'website' | 'article'
  noindex?: boolean
  publishedTime?: string
  modifiedTime?: string
  tags?: string[]
  siteName?: string
}

/** Title, description, canonical, Open Graph and robots for a public page, rendered by SSR (DISCOVER-04). */
export function usePageSeo(input: () => PageSeo) {
  const config = useRuntimeConfig()
  const siteUrl = String(config.public.siteUrl)
  const seo = computed(input)
  const url = computed(() => (seo.value.path ? absoluteUrl(siteUrl, seo.value.path) : undefined))
  const image = computed(() => (seo.value.image ? absoluteUrl(siteUrl, seo.value.image) : undefined))
  useHead(() => ({
    title: seo.value.title,
    link: url.value && !seo.value.noindex ? [{ rel: 'canonical', href: url.value }] : [],
  }))
  useSeoMeta({
    description: () => seo.value.description || undefined,
    robots: () => (seo.value.noindex ? 'noindex, follow' : 'index, follow'),
    ogTitle: () => seo.value.title,
    ogDescription: () => seo.value.description || undefined,
    ogType: () => seo.value.type ?? 'website',
    ogUrl: () => url.value,
    ogImage: () => image.value,
    ogLocale: 'zh_CN',
    ogSiteName: () => seo.value.siteName,
    twitterCard: () => (image.value ? 'summary_large_image' : 'summary'),
    articlePublishedTime: () => seo.value.publishedTime,
    articleModifiedTime: () => seo.value.modifiedTime,
    articleTag: () => seo.value.tags,
  })
}
