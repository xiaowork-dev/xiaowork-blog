import { DEFAULT_NAV, FALLBACK_SITE, type SiteInfo } from '~/utils/public'

/**
 * Public site profile (GET /api/v1/site), loaded once per SSR request and reused after hydration.
 * A failure falls back to the deployment defaults so the shell and articles still render.
 */
export async function useSite() {
  const requestFetch = useRequestFetch()
  const { data } = await useAsyncData('site', () =>
    requestFetch<{ data: SiteInfo }>('/api/v1/site').then(r => r.data).catch(() => FALLBACK_SITE), { dedupe: 'defer' })
  const site = computed<SiteInfo>(() => data.value ?? FALLBACK_SITE)
  const navigation = computed(() => site.value.navigation_links.length ? site.value.navigation_links : DEFAULT_NAV)
  return { site, navigation }
}
