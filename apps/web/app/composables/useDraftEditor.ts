import { ApiError, describeError, useApi } from '~/composables/useApi'
import { draftFrom, emptyDraft, toPayload, type ArticleDetail, type DraftForm, type PublishResult } from '~/utils/articles'

export type SaveState = 'saved' | 'dirty' | 'saving' | 'error' | 'conflict'

interface Backup { revision: number, form: DraftForm, at: number }

/**
 * Draft editing state (UI/UX §7.4): debounced autosave of the full snapshot with expected_revision,
 * a serial save queue, a local backup that survives reloads and network loss, and conflict handling that
 * never discards the local text.
 */
export function useDraftEditor(articleId: string) {
  const api = useApi()
  const base = `/api/v1/admin/articles/${articleId}`
  const detail = ref<ArticleDetail | null>(null)
  const form = ref<DraftForm>(emptyDraft())
  const revision = ref(0)
  const savedJson = ref(JSON.stringify(toPayload(emptyDraft())))
  const savedAt = ref<string | null>(null)
  const state = ref<SaveState>('saved')
  const error = ref('')
  const fieldErrors = ref<Record<string, string[]>>({})
  const conflict = ref<{ serverRevision: number, server: ArticleDetail | null } | null>(null)
  const recovered = ref<Backup | null>(null)
  const loaded = ref(false)

  const dirty = computed(() => JSON.stringify(toPayload(form.value)) !== savedJson.value)
  const backupKey = `xwb:draft:${articleId}`
  let timer: ReturnType<typeof setTimeout> | undefined
  let inflight: Promise<boolean> | null = null

  function writeBackup() {
    try {
      localStorage.setItem(backupKey, JSON.stringify({ revision: revision.value, form: form.value, at: Date.now() } satisfies Backup))
    }
    catch { /* storage unavailable: autosave still protects the text */ }
  }
  function clearBackup() {
    try {
      localStorage.removeItem(backupKey)
    }
    catch { /* ignore */ }
  }
  function readBackup(): Backup | null {
    try {
      const raw = localStorage.getItem(backupKey)
      return raw ? (JSON.parse(raw) as Backup) : null
    }
    catch {
      return null
    }
  }

  function apply(d: ArticleDetail) {
    detail.value = d
    form.value = draftFrom(d.draft)
    revision.value = d.draft.revision
    savedJson.value = JSON.stringify(toPayload(form.value))
    savedAt.value = d.draft.saved_at
    state.value = 'saved'
  }

  async function load() {
    const d = await api.get<ArticleDetail>(base)
    apply(d)
    // Unsaved local work from an earlier session (crash, closed tab, network loss) is offered back.
    const b = readBackup()
    if (b && JSON.stringify(toPayload(b.form)) !== savedJson.value) recovered.value = b
    else clearBackup()
    loaded.value = true
  }

  async function refreshDetail() {
    const d = await api.get<ArticleDetail>(base)
    detail.value = d
    return d
  }

  function schedule(ms = 1500) {
    clearTimeout(timer)
    timer = setTimeout(() => void save(), ms)
  }

  watch(form, () => {
    if (!loaded.value || conflict.value) return
    if (dirty.value) {
      if (state.value !== 'saving') state.value = 'dirty'
      writeBackup()
      if (detail.value?.status !== 'TRASHED') schedule()
    }
  }, { deep: true })

  async function save(): Promise<boolean> {
    clearTimeout(timer)
    if (conflict.value || detail.value?.status === 'TRASHED') return false
    if (inflight) {
      await inflight // serial queue: never two saves with the same expected_revision
      return dirty.value ? save() : true
    }
    if (!dirty.value) {
      state.value = 'saved'
      return true
    }
    const payload = toPayload(form.value)
    const json = JSON.stringify(payload)
    state.value = 'saving'
    inflight = (async () => {
      try {
        const res = await api.put<{ revision: number, saved_at: string, has_unpublished_changes: boolean }>(
          `${base}/draft`, { ...payload, expected_revision: revision.value })
        revision.value = res!.revision
        savedJson.value = json
        savedAt.value = res!.saved_at
        if (detail.value) {
          detail.value.has_unpublished_changes = res!.has_unpublished_changes
          detail.value.draft.revision = res!.revision
        }
        error.value = ''
        fieldErrors.value = {}
        state.value = dirty.value ? 'dirty' : 'saved'
        if (dirty.value) writeBackup()
        else clearBackup()
        return true
      }
      catch (err) {
        writeBackup() // whatever happens, the local text is kept
        if (err instanceof ApiError && err.code === 'DRAFT_REVISION_CONFLICT') {
          conflict.value = { serverRevision: Number(err.details.current_revision ?? 0), server: null }
          state.value = 'conflict'
          refreshDetail().then((d) => { if (conflict.value) conflict.value.server = d }).catch(() => {})
          return false
        }
        state.value = 'error'
        fieldErrors.value = err instanceof ApiError ? err.fields : {}
        error.value = describeError(err)
        // Network / server trouble: retry later automatically; validation errors wait for the user.
        if (!(err instanceof ApiError) || err.status === 0 || err.status >= 500 || err.status === 429) {
          schedule(err instanceof ApiError && err.retryAfter ? err.retryAfter * 1000 : 10_000)
        }
        return false
      }
      finally {
        inflight = null
      }
    })()
    const ok = await inflight
    if (ok && dirty.value) schedule(500)
    return ok
  }

  /** Saves pending edits now (Ctrl+S, before publishing, before leaving). */
  async function flush(): Promise<boolean> {
    clearTimeout(timer)
    if (inflight) await inflight
    return dirty.value ? save() : !conflict.value
  }

  /** Conflict option 1: take the server draft; the local text stays available in `discarded`. */
  const discarded = ref<DraftForm | null>(null)
  async function useServerVersion() {
    discarded.value = { ...form.value, tag_ids: [...form.value.tag_ids] }
    apply(await api.get<ArticleDetail>(base))
    conflict.value = null
    clearBackup()
  }

  /** Conflict option 2: an explicit decision to overwrite the newer server draft with the local text. */
  async function keepMine() {
    const d = await api.get<ArticleDetail>(base)
    detail.value = d
    revision.value = d.draft.revision
    savedJson.value = JSON.stringify(toPayload(draftFrom(d.draft)))
    conflict.value = null
    await save()
  }

  function restoreBackup() {
    if (recovered.value) form.value = { ...emptyDraft(), ...recovered.value.form }
    recovered.value = null
  }
  function dismissBackup() {
    recovered.value = null
    clearBackup()
  }

  async function publish(): Promise<PublishResult> {
    if (!(await flush())) throw new ApiError(409, state.value === 'conflict' ? 'DRAFT_REVISION_CONFLICT' : 'SAVE_FAILED',
      state.value === 'conflict' ? '草稿存在版本冲突，请先处理后再发布。' : '草稿尚未保存成功，请先解决保存问题。')
    try {
      const res = await api.post<PublishResult>(`${base}/publish`, { expected_draft_revision: revision.value })
      await refreshDetail()
      return res!
    }
    catch (err) {
      if (err instanceof ApiError && err.code === 'DRAFT_REVISION_CONFLICT') {
        conflict.value = { serverRevision: Number(err.details.current_revision ?? 0), server: await refreshDetail().catch(() => null) }
        state.value = 'conflict'
      }
      throw err
    }
  }

  async function lifecycle(action: 'withdraw' | 'restore' | 'trash') {
    if (action === 'trash') await api.del(base)
    else await api.post(`${base}/${action}`)
    await refreshDetail()
  }

  onBeforeUnmount(() => clearTimeout(timer))

  return {
    detail, form, revision, savedAt, state, error, fieldErrors, conflict, recovered, discarded, dirty, loaded,
    load, save, flush, publish, lifecycle, useServerVersion, keepMine, restoreBackup, dismissBackup,
  }
}
