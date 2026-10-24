<script setup lang="ts">
// /account/comments (UI/UX §6.3): own comment history with the article (while it is public), status and
// a confirmed delete. Withdrawn articles, hidden and deleted comments are shown as such.
import { describeError } from '~/composables/useApi'
import { placeholderText, STATUS_TEXT, type MyComment } from '~/utils/comments'
import { formatDateTime, parsePage, type ListMeta } from '~/utils/public'

definePageMeta({ layout: 'account', middleware: 'auth' })
useHead({ title: '我的评论 · XiaoWork Blog', meta: [{ name: 'robots', content: 'noindex, nofollow' }] })

const api = useApi()
const route = useRoute()
const page = computed(() => parsePage(route.query.page) ?? 1)

const failure = ref('')
const { data, status, refresh } = await useAsyncData(
  () => `my-comments:${page.value}`,
  async () => {
    failure.value = ''
    try {
      return await api.getPage<MyComment>('/api/v1/me/comments', { page: page.value }) as { data: MyComment[], meta: ListMeta }
    }
    catch (err) {
      failure.value = describeError(err)
      return null
    }
  },
  { server: false, watch: [page] },
)

const dialog = ref<HTMLDialogElement | null>(null)
const pending = ref<MyComment | null>(null)
const deleting = ref(false)
const deleteError = ref('')
const notice = ref('')

function askDelete(c: MyComment) {
  pending.value = c
  deleteError.value = ''
  dialog.value?.showModal()
}

async function confirmDelete() {
  const c = pending.value
  if (!c) return
  deleting.value = true
  try {
    await api.del(`/api/v1/comments/${c.id}`)
    Object.assign(c, { status: 'DELETED_AUTHOR', body_text: null, can_delete: false })
    dialog.value?.close()
    notice.value = '评论已删除。'
  }
  catch (err) {
    deleteError.value = describeError(err)
  }
  finally {
    deleting.value = false
  }
}

function badgeClass(s: MyComment['status']) {
  return s === 'VISIBLE' ? 'badge-success' : s === 'HIDDEN' ? 'badge-warning' : 'badge-muted'
}
</script>

<template>
  <div class="account-page">
    <h1 class="section-title">我的评论</h1>
    <p class="visually-hidden" role="status" aria-live="polite">{{ notice }}</p>
    <ListSkeleton v-if="!data && !failure" :rows="3" />
    <StateBlock v-else-if="failure" kind="error" title="评论记录加载失败" :message="failure" :retrying="status === 'pending'" @retry="refresh()" />
    <StateBlock v-else-if="data && !data.data.length" kind="empty" title="你还没有发表过评论">
      <NuxtLink to="/" class="button button-small button-secondary">去看看文章</NuxtLink>
    </StateBlock>
    <template v-else-if="data">
      <p class="section-desc">共 {{ data.meta.total }} 条</p>
      <ol class="my-comments">
        <li v-for="c in data.data" :key="c.id" class="account-card my-comment">
          <header class="my-comment-head">
            <NuxtLink v-if="c.article.available && c.article.canonical_path" :to="`${c.article.canonical_path}#comments`" class="my-comment-article">
              {{ c.article.title }}
            </NuxtLink>
            <span v-else class="muted-inline">文章已撤回或不再公开</span>
            <span class="badge" :class="badgeClass(c.status)">{{ STATUS_TEXT[c.status] }}</span>
          </header>
          <CommentText v-if="c.body_text" :text="c.body_text" />
          <p v-else class="comment-text comment-gone">{{ placeholderText(c.status) }}</p>
          <p v-if="c.status === 'HIDDEN'" class="muted">这条评论已被管理员隐藏，其他读者看不到正文。</p>
          <footer class="my-comment-foot">
            <time :datetime="c.created_at">{{ formatDateTime(c.created_at) }}</time>
            <span v-if="c.parent_id" class="muted-inline">回复</span>
            <button v-if="c.can_delete" type="button" class="link-button danger" @click="askDelete(c)">删除</button>
          </footer>
        </li>
      </ol>
      <PagerNav :page="page" :total-pages="data.meta.total_pages" />
    </template>

    <dialog ref="dialog" class="media-dialog media-dialog-small" aria-labelledby="delete-my-comment-title">
      <h2 id="delete-my-comment-title">删除这条评论？</h2>
      <p>删除后正文不可恢复；如果已有回复，文章页会显示“该评论已删除”的占位。</p>
      <p v-if="deleteError" class="alert alert-error" role="alert">{{ deleteError }}</p>
      <div class="dialog-actions">
        <button type="button" class="button button-secondary" @click="dialog?.close()">取消</button>
        <button type="button" class="button button-danger" :disabled="deleting" @click="confirmDelete">{{ deleting ? '删除中…' : '删除' }}</button>
      </div>
    </dialog>
  </div>
</template>
