<script setup lang="ts">
// One comment (root or reply). Hidden and deleted comments keep their place as placeholders without
// author or text. Replies are drawn at a single indentation level whatever their real depth (UI/UX §5.2).
import { placeholderText, type PublicComment } from '~/utils/comments'
import { formatDateTime } from '~/utils/public'

const props = defineProps<{ comment: PublicComment, canReply: boolean }>()
defineEmits<{ reply: [comment: PublicComment], delete: [comment: PublicComment] }>()

const visible = computed(() => props.comment.status === 'VISIBLE')
const initial = computed(() => Array.from(props.comment.author?.nickname ?? '?')[0]?.toUpperCase() ?? '?')
</script>

<template>
  <article :id="`comment-${comment.id}`" class="comment" :class="{ 'comment-placeholder': !visible }" :aria-label="visible ? `${comment.author?.nickname} 的评论` : placeholderText(comment.status)">
    <div class="comment-avatar" aria-hidden="true">
      <img v-if="comment.author?.avatar_url" :src="comment.author.avatar_url" alt="" loading="lazy" width="36" height="36">
      <span v-else>{{ visible ? initial : '·' }}</span>
    </div>
    <div class="comment-main">
      <header class="comment-head">
        <template v-if="visible && comment.author">
          <span class="comment-author">{{ comment.author.nickname }}</span>
          <span v-if="comment.author.is_blog_owner" class="owner-badge">博主</span>
        </template>
        <span v-if="comment.reply_to" class="comment-target">
          回复 <template v-if="comment.reply_to.nickname">@{{ comment.reply_to.nickname }}</template><template v-else>已删除的评论</template>
        </span>
        <time class="comment-time" :datetime="comment.created_at">{{ formatDateTime(comment.created_at) }}</time>
      </header>
      <CommentText v-if="visible && comment.body_text" :text="comment.body_text" />
      <p v-else class="comment-text comment-gone">{{ placeholderText(comment.status) }}</p>
      <div v-if="visible || comment.can_delete" class="comment-actions">
        <button v-if="visible && canReply" type="button" class="link-button" @click="$emit('reply', comment)">回复</button>
        <button v-if="comment.can_delete" type="button" class="link-button danger" @click="$emit('delete', comment)">删除</button>
      </div>
      <slot />
    </div>
  </article>
</template>
