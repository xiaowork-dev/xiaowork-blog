// Article DTOs (API-DD §5.1–5.4) and display helpers shared by the admin pages and the editor.
export type ArticleStatus = 'DRAFT' | 'PUBLISHED' | 'WITHDRAWN' | 'TRASHED'

/** The full draft snapshot sent with PUT /admin/articles/{id}/draft (every field, nullable ones as null). */
export interface DraftForm {
  title: string
  summary: string | null
  body_markdown: string
  candidate_slug: string | null
  category_id: string | null
  tag_ids: string[]
  cover_media_id: string | null
  seo_title: string | null
  seo_description: string | null
  comments_enabled: boolean
}

export interface ArticleDetail {
  article_id: string
  status: ArticleStatus
  has_unpublished_changes: boolean
  first_published_at: string | null
  public_updated_at: string | null
  draft: DraftForm & { revision: number, saved_at: string, cover: { id: string, url: string } | null }
  current_publication: {
    publication_revision: number
    title: string
    slug: string
    canonical_path: string
    published_at: string
    is_public: boolean
  } | null
}

export interface ArticleListItem {
  id: string
  status: ArticleStatus
  title: string
  public_title: string | null
  slug: string | null
  candidate_slug: string | null
  category_name: string | null
  draft_revision: number
  saved_at: string
  has_unpublished_changes: boolean
  first_published_at: string | null
  public_updated_at: string | null
  trashed_at: string | null
}

export interface Term {
  id: string
  name: string
  slug: string
  description?: string | null
  sort_order: number
  is_active: boolean
  draft_count?: number
  published_count?: number
}

export interface PublishResult {
  article_id: string
  status: 'PUBLISHED'
  publication_revision: number
  slug: string
  canonical_path: string
  published_changed: boolean
  published_at: string
}

export function emptyDraft(): DraftForm {
  return {
    title: '', summary: null, body_markdown: '', candidate_slug: null, category_id: null, tag_ids: [],
    cover_media_id: null, seo_title: null, seo_description: null, comments_enabled: true,
  }
}

export function draftFrom(d: ArticleDetail['draft']): DraftForm {
  return {
    title: d.title, summary: d.summary, body_markdown: d.body_markdown, candidate_slug: d.candidate_slug,
    category_id: d.category_id, tag_ids: [...d.tag_ids], cover_media_id: d.cover_media_id, seo_title: d.seo_title,
    seo_description: d.seo_description, comments_enabled: d.comments_enabled,
  }
}

/** Normalises optional text inputs: blank strings are sent as null (API-DD §3.1 "null 表示清空"). */
export function toPayload(f: DraftForm): DraftForm {
  const opt = (v: string | null) => (v && v.trim() ? v.trim() : null)
  return {
    ...f, summary: opt(f.summary), candidate_slug: opt(f.candidate_slug), seo_title: opt(f.seo_title),
    seo_description: opt(f.seo_description), tag_ids: [...f.tag_ids],
  }
}

export const SLUG_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/

/** The badges of UI/UX §7.3/§7.4: lifecycle plus "存在未发布修改". */
export function statusBadges(status: ArticleStatus, changed: boolean): { label: string, tone: string }[] {
  switch (status) {
    case 'TRASHED':
      return [{ label: '回收站', tone: 'danger' }]
    case 'DRAFT':
      return [{ label: '草稿（未发布）', tone: 'muted' }]
    case 'WITHDRAWN':
      return [{ label: '已撤回', tone: 'warning' }, ...(changed ? [{ label: '有未发布修改', tone: 'accent' }] : [])]
    default:
      return [{ label: '已发布', tone: 'success' }, ...(changed ? [{ label: '有未发布修改', tone: 'accent' }] : [])]
  }
}
