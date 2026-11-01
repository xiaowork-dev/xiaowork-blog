// Reader bookmarks ("收藏", P8-B12). The IDs of the signed-in reader's public bookmarks are loaded once in
// the browser (bookmark state is personal and never rendered on the server) and shared by every card.
import { describeError, useApi } from '~/composables/useApi'

export function useBookmarks() {
  const ids = useState<Set<string>>('bookmarks:ids', () => new Set())
  const loadedFor = useState<string | null>('bookmarks:loaded-for', () => null)
  const busy = useState<Set<string>>('bookmarks:busy', () => new Set())
  const { user } = useAuth()
  const api = useApi()
  const route = useRoute()

  async function load() {
    if (import.meta.server) return
    const uid = user.value?.id ?? null
    if (uid === loadedFor.value) return
    loadedFor.value = uid
    if (!uid) {
      ids.value = new Set()
      return
    }
    try {
      const res = await $fetch<{ data: string[] }>('/api/v1/me/bookmarks/ids', { credentials: 'same-origin' })
      ids.value = new Set(res.data)
    }
    catch {
      ids.value = new Set() // the button still works; it just starts unmarked
    }
  }

  const isBookmarked = (id: string) => ids.value.has(id)

  /** Adds or removes a bookmark; guests are sent to the login page first. Returns an error text or ''. */
  async function toggle(id: string): Promise<string> {
    if (!user.value) {
      await navigateTo({ path: '/auth/login', query: { redirect: route.fullPath } })
      return ''
    }
    if (busy.value.has(id)) return ''
    busy.value = new Set(busy.value).add(id)
    const on = !ids.value.has(id)
    try {
      if (on) await api.put(`/api/v1/me/bookmarks/${id}`)
      else await api.del(`/api/v1/me/bookmarks/${id}`)
      const next = new Set(ids.value)
      if (on) next.add(id)
      else next.delete(id)
      ids.value = next
      return ''
    }
    catch (err) {
      return describeError(err)
    }
    finally {
      const next = new Set(busy.value)
      next.delete(id)
      busy.value = next
    }
  }

  return { ids, busy, load, isBookmarked, toggle }
}
