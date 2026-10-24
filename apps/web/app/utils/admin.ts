// P7 admin DTOs (API-DD §4.3/§4.4, §5.8) and small helpers shared by the admin pages.
import type { MediaRef } from './public'

export interface OverviewArticle {
  id: string
  status: 'DRAFT' | 'PUBLISHED' | 'WITHDRAWN' | 'TRASHED'
  title: string
  public_title: string
  updated_at: string
  public_updated_at: string | null
  has_unpublished_changes: boolean
  canonical_path: string | null
}

export interface AdminEvent {
  id: string
  action: string
  object_type: string
  object_key: string
  actor_nickname: string
  created_at: string
}

export interface Overview {
  article_count: number
  published_count: number
  draft_count: number
  withdrawn_count: number
  trashed_count: number
  changed_count: number
  user_count: number
  unverified_user_count: number
  disabled_user_count: number
  new_user_count: number
  comment_count: number
  hidden_comment_count: number
  new_comment_count: number
  media_count: number
  recent_articles: OverviewArticle[]
  recent_events: AdminEvent[]
  notices: { code: string, count: number }[]
}

export interface AdminUser {
  id: string
  email: string
  nickname: string
  role: 'USER' | 'ADMIN'
  status: 'ACTIVE' | 'DISABLED'
  email_verified: boolean
  created_at: string
  avatar_url: string | null
  comment_count: number
}

export interface AdminUserDetail extends AdminUser {
  email_verified_at: string | null
  updated_at: string
  bio: string
  visible_comment_count: number
  hidden_comment_count: number
  active_session_count: number
  is_self: boolean
  can_disable: boolean
  can_enable: boolean
  recent_comments: {
    id: string
    status: string
    body_text: string | null
    created_at: string
    article: { id: string, title: string, canonical_path: string | null }
  }[]
}

export interface EditableLink {
  id?: string
  icon_key?: string
  label: string
  link_type?: 'INTERNAL' | 'EXTERNAL'
  url: string
  is_enabled: boolean
}

export interface AdminSite {
  site_title: string
  site_description: string
  profile_name: string
  profile_bio: string
  profile_avatar: MediaRef | null
  logo: MediaRef | null
  favicon: MediaRef | null
  contact_email: string | null
  default_seo_title: string | null
  default_seo_description: string | null
  updated_at: string
  social_links: EditableLink[]
  navigation_links: EditableLink[]
  icon_keys: string[]
}

export const ARTICLE_STATUS_TEXT: Record<OverviewArticle['status'], string> = {
  DRAFT: '草稿', PUBLISHED: '已发布', WITHDRAWN: '已撤回', TRASHED: '回收站',
}

export const ICON_LABELS: Record<string, string> = { github: 'GitHub', mail: '邮箱', rss: 'RSS', link: '链接' }

const EVENT_TEXT: Record<string, string> = {
  ARTICLE_PUBLISH: '发布文章', ARTICLE_WITHDRAW: '撤回文章', ARTICLE_TRASH: '文章移入回收站', ARTICLE_RESTORE: '恢复文章',
  'comment.hide': '隐藏评论', 'comment.unhide': '恢复评论', 'comment.delete': '删除评论',
  'user.disable': '禁用用户', 'user.enable': '恢复用户',
  'site.update': '修改网站设置', 'site.social_links': '修改个人栏外链', 'site.navigation_links': '修改顶部导航',
  'page.update': '更新关于页',
}

export function eventText(action: string): string {
  return EVENT_TEXT[action] ?? action
}

/** Mirrors the API rules (API-DD §5.8) for immediate feedback; the API stays authoritative. */
export function linkProblem(link: EditableLink, kind: 'social' | 'nav'): string {
  const url = link.url.trim()
  if (!link.label.trim()) return '请填写名称'
  if (kind === 'social') {
    if (/^mailto:/i.test(url)) return /^mailto:[^\s?#/,;:<>"]+@[^\s?#/,;:<>"]+\.[^\s?#/,;:<>"]+$/i.test(url) ? '' : '邮箱地址格式不正确'
    return /^https:\/\/[^\s/]+\S*$/i.test(url) ? '' : '只支持 https:// 或 mailto: 链接'
  }
  if (link.link_type === 'EXTERNAL') return /^https:\/\/[^\s/]+\S*$/i.test(url) ? '' : '外部链接必须以 https:// 开头'
  if (!/^\/(?!\/)\S*$/.test(url)) return '站内链接需以 / 开头，例如 /about'
  if (/^\/(admin|account|auth|api|media)(\/|$|\?|#)/.test(url)) return '不能链接到后台、账号或接口地址'
  return ''
}
