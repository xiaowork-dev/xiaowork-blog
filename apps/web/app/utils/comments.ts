// Comment DTOs (API-DD §5.5) and the plain-text renderer used by every comment view.
// Comments are text: the UI never uses v-html for them. Links are recognised only for http(s) and
// rendered with rel="nofollow ugc noopener noreferrer"; "@名字" is only styled, never linked.

export interface CommentAuthor {
  id: string
  nickname: string
  avatar_url: string | null
  is_blog_owner: boolean
}

export type CommentStatus = 'VISIBLE' | 'HIDDEN' | 'DELETED_AUTHOR' | 'DELETED_ADMIN'

export interface PublicComment {
  id: string
  article_id: string
  parent_id: string | null
  root_id: string | null
  body_text: string | null
  status: CommentStatus
  author: CommentAuthor | null
  created_at: string
  can_delete: boolean
  reply_count?: number
  reply_to?: { id: string, nickname: string | null }
  replies?: PublicComment[]
}

export interface CommentListMeta {
  page: number
  page_size: number
  total: number
  total_pages: number
  sort: 'latest' | 'oldest'
  comment_count: number
  comments_enabled: boolean
}

export interface MyComment {
  id: string
  parent_id: string | null
  root_id: string | null
  status: CommentStatus
  body_text: string | null
  created_at: string
  can_delete: boolean
  article: { id: string, available: boolean, title: string | null, canonical_path: string | null }
}

export interface AdminComment {
  id: string
  parent_id: string | null
  root_id: string | null
  status: CommentStatus
  body_text: string | null
  created_at: string
  hidden_at: string | null
  deleted_at: string | null
  reply_count: number
  author: { id: string, nickname: string, email: string, role: string, status: string }
  article: { id: string, title: string, status: string, canonical_path: string | null }
}

export const MAX_COMMENT_LENGTH = 2000

export const STATUS_TEXT: Record<CommentStatus, string> = {
  VISIBLE: '公开',
  HIDDEN: '已隐藏',
  DELETED_AUTHOR: '作者已删除',
  DELETED_ADMIN: '管理员已删除',
}

/** Placeholder shown instead of a hidden or deleted comment (UI/UX §5.2). */
export function placeholderText(status: CommentStatus): string {
  return status === 'HIDDEN' ? '该评论已被隐藏' : '该评论已删除'
}

export type Segment =
  | { kind: 'text', text: string }
  | { kind: 'link', text: string, href: string }
  | { kind: 'mention', text: string }

// URLs are ASCII-only here (RFC 3986 characters minus quotes), so Chinese text right after a link is
// not swallowed into it.
const TOKEN = /(https?:\/\/[\w\-.~:/?#[\]@!$&()*+,;=%]+)|(@[\p{L}\p{N}_\-.]{1,40})/giu
// Punctuation that usually ends a sentence rather than a URL.
const TRAILING = /[.,;:!?。，；：！？、)\]}）】」』>]+$/u

/** Splits comment text into text, safe links and mentions. Produces data, never HTML. */
export function commentSegments(text: string): Segment[] {
  const out: Segment[] = []
  let last = 0
  for (const m of text.matchAll(TOKEN)) {
    let token = m[0]
    let end = m.index + token.length
    if (m[1]) {
      const trail = TRAILING.exec(token)?.[0] ?? ''
      token = token.slice(0, token.length - trail.length)
      end -= trail.length
    }
    if (m.index > last) out.push({ kind: 'text', text: text.slice(last, m.index) })
    if (m[1] && isHttpUrl(token)) out.push({ kind: 'link', text: token, href: token })
    else if (m[1]) out.push({ kind: 'text', text: token })
    else out.push({ kind: 'mention', text: token })
    last = end
  }
  if (last < text.length) out.push({ kind: 'text', text: text.slice(last) })
  return mergeText(out)
}

function isHttpUrl(s: string): boolean {
  try {
    const u = new URL(s)
    return (u.protocol === 'http:' || u.protocol === 'https:') && !!u.hostname
  }
  catch {
    return false
  }
}

function mergeText(segments: Segment[]): Segment[] {
  const out: Segment[] = []
  for (const s of segments) {
    const prev = out[out.length - 1]
    if (s.kind === 'text' && prev?.kind === 'text') prev.text += s.text
    else out.push({ ...s })
  }
  return out
}

/** Characters as the API counts them (Unicode code points, after trimming). */
export function commentLength(text: string): number {
  return Array.from(text.trim()).length
}
