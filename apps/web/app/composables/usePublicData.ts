// SSR data loading for public pages with honest HTTP status codes: an API 404 becomes the 404 page,
// any other failure renders the error state with a retry and answers 503 so crawlers do not index it.
import type { WatchSource } from 'vue'
import type { AsyncDataRequestStatus } from '#app'

interface FetchFailure {
  statusCode?: number
  status?: number
}

export function failureStatus(err: unknown): number {
  const f = (err ?? {}) as FetchFailure
  return f.statusCode ?? f.status ?? 0
}

export async function usePublicData<T>(key: string | (() => string), fetcher: () => Promise<T>, watchSources: WatchSource[] = []) {
  const res = await useAsyncData<T>(key, fetcher, { watch: watchSources })
  const notFound = computed(() => failureStatus(res.error.value) === 404)
  if (import.meta.server && res.error.value && !notFound.value) {
    const event = useRequestEvent()
    if (event) setResponseStatus(event, 503)
  }
  if (notFound.value) {
    throw createError({ statusCode: 404, statusMessage: 'Not Found', fatal: true })
  }
  const loading = computed(() => (res.status.value as AsyncDataRequestStatus) === 'pending')
  return { ...res, loading }
}

/** 404 for a malformed ?page= or a page past the end, so every listed URL is a real page. */
export function notFoundPage(): never {
  throw createError({ statusCode: 404, statusMessage: 'Not Found', fatal: true })
}
