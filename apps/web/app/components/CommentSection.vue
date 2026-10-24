<script setup lang="ts">
// Article comments (PRD COMMENT-01~08, UI/UX §5). Loaded in the browser so the article HTML stays the
// same for every reader; new comments appear at once because they are public at once (no review queue).
import { describeError } from '~/composables/useApi'
import type { CommentListMeta, PublicComment } from '~/utils/comments'

const props = defineProps<{ articleId: string, commentsEnabled: boolean }>()

interface Thread {
  replies: PublicComment[]
  page: number
  total: number
  expanded: boolean
  loading: boolean
  error: string
}

const api = useApi()
const { user } = useAuth()
const route = useRoute()

const sort = ref<'latest' | 'oldest'>('latest')
const roots = ref<PublicComment[]>([])
const meta = ref<CommentListMeta | null>(null)
const page = ref(0)
const loading = ref(false)
const loadError = ref('')
const threads = reactive<Record<string, Thread>>({})
const replyTarget = ref<{ rootId: string, comment: PublicComment } | null>(null)
const pendingDelete = ref<PublicComment | null>(null)
const deleting = ref(false)
const deleteError = ref('')
const dialog = ref<HTMLDialogElement | null>(null)
const notice = ref('')

const enabled = computed(() => meta.value?.comments_enabled ?? props.commentsEnabled)
const count = computed(() => meta.value?.comment_count ?? 0)
const hasMore = computed(() => !!meta.value && page.value < meta.value.total_pages)
const canPost = computed(() => enabled.value && !!user.value && user.value.email_verified && user.value.status === 'ACTIVE')
const loginLink = computed(() => ({ path: '/auth/login', query: { redirect: `${route.path}#comments` } }))

// Thread state is created when a root arrives (never during render).
function initThread(root: PublicComment) {
  threads[root.id] ??= {
    replies: [...(root.replies ?? [])], page: 0, total: root.reply_count ?? 0, expanded: false, loading: false, error: '',
  }
}

function threadOf(root: PublicComment): Thread {
  initThread(root)
  return threads[root.id]!
}

async function load(reset: boolean) {
  loading.value = true
  loadError.value = ''
  try {
    const next = reset ? 1 : page.value + 1
    const res = await api.getPage<PublicComment>(`/api/v1/articles/${props.articleId}/comments`, { page: next, sort: sort.value })
    if (reset) {
      roots.value = []
      for (const k of Object.keys(threads)) delete threads[k]
    }
    const seen = new Set(roots.value.map(c => c.id))
    const fresh = res.data.filter(c => !seen.has(c.id))
    fresh.forEach(initThread)
    roots.value.push(...fresh)
    meta.value = res.meta as CommentListMeta
    page.value = next
  }
  catch (err) {
    loadError.value = describeError(err)
  }
  finally {
    loading.value = false
  }
}

async function loadReplies(root: PublicComment) {
  const t = threadOf(root)
  t.loading = true
  t.error = ''
  try {
    const next = t.expanded ? t.page + 1 : 1
    const res = await api.getPage<PublicComment>(`/api/v1/comments/${root.id}/replies`, { page: next })
    if (!t.expanded) t.replies = []
    const seen = new Set(t.replies.map(c => c.id))
    t.replies.push(...res.data.filter(c => !seen.has(c.id)))
    t.page = next
    t.total = res.meta.total ?? t.total
    t.expanded = true
  }
  catch (err) {
    t.error = describeError(err)
  }
  finally {
    t.loading = false
  }
}

function setSort(value: 'latest' | 'oldest') {
  if (sort.value === value) return
  sort.value = value
  load(true)
}

function onRootPosted(c: PublicComment) {
  initThread(c)
  if (sort.value === 'latest' || !hasMore.value) {
    if (sort.value === 'latest') roots.value.unshift(c)
    else roots.value.push(c)
  }
  if (meta.value) {
    meta.value.comment_count++
    meta.value.total++
  }
  announce('评论已发表。')
}

function startReply(rootId: string, c: PublicComment) {
  replyTarget.value = { rootId, comment: c }
}

function onReplyPosted(rootId: string, c: PublicComment) {
  const root = roots.value.find(r => r.id === rootId)
  if (root) {
    const t = threadOf(root)
    t.replies.push(c)
    t.total++
    root.reply_count = (root.reply_count ?? 0) + 1
  }
  if (meta.value) meta.value.comment_count++
  replyTarget.value = null
  announce('回复已发表。')
  nextTick(() => document.getElementById(`comment-${c.id}`)?.scrollIntoView({ block: 'nearest' }))
}

function askDelete(c: PublicComment) {
  pendingDelete.value = c
  deleteError.value = ''
  dialog.value?.showModal()
}

async function confirmDelete() {
  const c = pendingDelete.value
  if (!c) return
  deleting.value = true
  deleteError.value = ''
  try {
    await api.del(`/api/v1/comments/${c.id}`)
    const wasVisible = c.status === 'VISIBLE'
    Object.assign(c, { status: 'DELETED_AUTHOR', body_text: null, author: null, can_delete: false })
    if (wasVisible && meta.value) meta.value.comment_count--
    dialog.value?.close()
    announce('评论已删除。')
  }
  catch (err) {
    deleteError.value = describeError(err)
  }
  finally {
    deleting.value = false
  }
}

function announce(text: string) {
  notice.value = ''
  nextTick(() => { notice.value = text })
}

onMounted(() => load(true))
watch(() => props.articleId, () => {
  replyTarget.value = null
  load(true)
})
</script>

<template>
  <section id="comments" class="comments" aria-labelledby="comments-title">
    <header class="comments-head">
      <h2 id="comments-title">评论<span v-if="meta" class="comments-count">{{ count }}</span></h2>
      <div class="comments-sort" role="group" aria-label="评论排序">
        <button type="button" :aria-pressed="sort === 'latest'" @click="setSort('latest')">最新</button>
        <button type="button" :aria-pressed="sort === 'oldest'" @click="setSort('oldest')">最早</button>
      </div>
    </header>

    <!-- Composer or the reason it is not available (UI/UX §5.1). -->
    <div class="comments-entry">
      <p v-if="!enabled" class="comments-note">本文已关闭评论，历史评论仍可查看。</p>
      <ClientOnly v-else>
        <CommentComposer v-if="canPost" :article-id="articleId" @posted="onRootPosted" />
        <div v-else-if="!user" class="comments-note">
          <span>登录后参与讨论。</span>
          <NuxtLink :to="loginLink" class="button button-small">登录</NuxtLink>
          <NuxtLink to="/auth/register" class="button button-small button-secondary">注册</NuxtLink>
        </div>
        <div v-else-if="!user.email_verified" class="comments-note comments-verify">
          <p>验证邮箱后才能发表评论。请查收验证邮件，或重新发送：</p>
          <ResendVerification :email="user.email" />
        </div>
        <p v-else class="comments-note">当前账号无法发表评论。</p>
        <template #fallback><p class="comments-note">正在加载评论区…</p></template>
      </ClientOnly>
    </div>

    <p class="visually-hidden" role="status" aria-live="polite">{{ notice }}</p>

    <ListSkeleton v-if="loading && !roots.length" :rows="2" />
    <StateBlock v-else-if="loadError && !roots.length" kind="error" title="评论加载失败" :message="loadError" :retrying="loading" @retry="load(true)" />
    <p v-else-if="meta && !roots.length" class="comments-empty">{{ enabled ? '还没有评论，来写第一条吧。' : '暂无评论。' }}</p>

    <ol v-if="roots.length" class="comment-list">
      <li v-for="root in roots" :key="root.id" class="comment-thread">
        <CommentItem :comment="root" :can-reply="canPost" @reply="startReply(root.id, $event)" @delete="askDelete">
          <ol v-if="threads[root.id]!.replies.length" class="comment-replies">
            <li v-for="r in threads[root.id]!.replies" :key="r.id">
              <CommentItem :comment="r" :can-reply="canPost" @reply="startReply(root.id, $event)" @delete="askDelete" />
            </li>
          </ol>
          <div v-if="threads[root.id]!.replies.length < threads[root.id]!.total || threads[root.id]!.error" class="replies-more">
            <button type="button" class="link-button" :disabled="threads[root.id]!.loading" @click="loadReplies(root)">
              {{ threads[root.id]!.loading ? '加载中…' : threads[root.id]!.expanded ? '加载更多回复' : `查看全部 ${threads[root.id]!.total} 条回复` }}
            </button>
            <span v-if="threads[root.id]!.error" class="field-error" role="alert">{{ threads[root.id]!.error }}</span>
          </div>
          <CommentComposer
            v-if="replyTarget?.rootId === root.id && canPost" :key="replyTarget.comment.id" :article-id="articleId"
            :parent="{ id: replyTarget.comment.id, nickname: replyTarget.comment.author?.nickname ?? null }" autofocus
            @posted="onReplyPosted(root.id, $event)" @cancel="replyTarget = null"
          />
        </CommentItem>
      </li>
    </ol>

    <div v-if="hasMore || (loadError && roots.length)" class="comments-more">
      <button type="button" class="button button-small button-secondary" :disabled="loading" @click="load(false)">
        {{ loading ? '加载中…' : loadError ? '加载失败，重试' : '加载更多评论' }}
      </button>
    </div>

    <dialog ref="dialog" class="media-dialog media-dialog-small" aria-labelledby="delete-comment-title">
      <h2 id="delete-comment-title">删除这条评论？</h2>
      <p>删除后正文不可恢复；如果已有回复，会显示“该评论已删除”的占位。</p>
      <p v-if="deleteError" class="alert alert-error" role="alert">{{ deleteError }}</p>
      <div class="dialog-actions">
        <button type="button" class="button button-secondary" @click="dialog?.close()">取消</button>
        <button type="button" class="button button-danger" :disabled="deleting" @click="confirmDelete">{{ deleting ? '删除中…' : '删除' }}</button>
      </div>
    </dialog>
  </section>
</template>
