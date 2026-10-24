// Public blog DTOs (API-DD §5.1, §4.1) and small pure helpers shared by the public pages.

export interface MediaRef {
  id: string
  url: string
}

export interface TermRef {
  id: string
  name: string
  slug: string
}

export interface ArticleSummary {
  id: string
  slug: string
  title: string
  summary: string
  cover: MediaRef | null
  category: TermRef | null
  tags: TermRef[]
  first_published_at: string
  public_updated_at: string
  view_count: number
  comments_enabled: boolean
  canonical_path: string
}

export interface NeighbourArticle {
  slug: string
  title: string
  canonical_path: string
}

export interface PublicArticle extends Omit<ArticleSummary, 'canonical_path'> {
  body_markdown: string
  seo_title: string | null
  seo_description: string | null
  canonical_path: string
  resolved_from_old_slug: boolean
  previous_article: NeighbourArticle | null
  next_article: NeighbourArticle | null
}

export interface ListMeta {
  page: number
  page_size: number
  total: number
  total_pages: number
  category?: TermRef & { description: string | null }
  tag?: TermRef
  q?: string
}

export interface ArticlePage {
  data: ArticleSummary[]
  meta: ListMeta
}

export interface TermWithCount extends TermRef {
  article_count: number
  description?: string | null
}

export interface ArchiveMonth {
  year: number
  month: number
  articles: { id: string, slug: string, title: string, first_published_at: string, canonical_path: string }[]
}

export interface ArchiveResponse {
  data: ArchiveMonth[]
  meta: { years: { year: number, article_count: number }[], year: number | null, total: number, truncated: boolean }
}

export interface SocialLink {
  id: string
  icon_key: string
  label: string
  url: string
}

export interface NavLink {
  id: string
  label: string
  link_type: 'INTERNAL' | 'EXTERNAL'
  url: string
}

export interface SiteInfo {
  site_title: string
  site_description: string
  profile: { name: string, bio: string, avatar: MediaRef | null }
  logo: MediaRef | null
  favicon: MediaRef | null
  contact_email: string | null
  default_seo_title: string | null
  default_seo_description: string | null
  social_links: SocialLink[]
  navigation_links: NavLink[]
}

/** Used when /site cannot be reached, so the shell still renders (matches the migration defaults). */
export const FALLBACK_SITE: SiteInfo = {
  site_title: 'XiaoWork Blog', site_description: '', profile: { name: 'XiaoWork', bio: '', avatar: null },
  logo: null, favicon: null, contact_email: null, default_seo_title: null, default_seo_description: null,
  social_links: [], navigation_links: [],
}

/** The fixed top navigation (UI/UX §3.1) used until the administrator configures one in P7. */
export const DEFAULT_NAV: NavLink[] = [
  { id: 'home', label: '首页', link_type: 'INTERNAL', url: '/' },
  { id: 'categories', label: '分类', link_type: 'INTERNAL', url: '/categories' },
  { id: 'tags', label: '标签', link_type: 'INTERNAL', url: '/tags' },
  { id: 'archives', label: '归档', link_type: 'INTERNAL', url: '/archives' },
  { id: 'about', label: '关于', link_type: 'INTERNAL', url: '/about' },
]

/**
 * Parses ?page=. Returns null for anything that is not a positive integer so the page can answer 404
 * instead of silently showing page 1 under another URL.
 */
export function parsePage(raw: unknown): number | null {
  if (raw === undefined || raw === null || raw === '') return 1
  const s = Array.isArray(raw) ? String(raw[0]) : String(raw)
  if (!/^[1-9]\d{0,5}$/.test(s)) return null
  return Number(s)
}

/** Page numbers to show around the current page, with null marking a gap ("…"). */
export function pageWindow(current: number, total: number, radius = 1): (number | null)[] {
  if (total <= 1) return [1]
  const pages = new Set<number>([1, total])
  for (let p = current - radius; p <= current + radius; p++) {
    if (p >= 1 && p <= total) pages.add(p)
  }
  const sorted = [...pages].sort((a, b) => a - b)
  const out: (number | null)[] = []
  sorted.forEach((p, i) => {
    if (i > 0 && p - sorted[i - 1]! > 1) out.push(null)
    out.push(p)
  })
  return out
}

/**
 * The single display time zone of V1.0 (ADR-0005), identical to the Go archive buckets. Formatting in a
 * fixed zone also keeps SSR (container in UTC) and hydration (reader's browser) byte-identical.
 */
export const SITE_TIME_ZONE = 'Asia/Shanghai'

const dayFormat = new Intl.DateTimeFormat('zh-CN', { timeZone: SITE_TIME_ZONE, year: 'numeric', month: 'long', day: 'numeric' })
const isoFormat = new Intl.DateTimeFormat('en-CA', { timeZone: SITE_TIME_ZONE, year: 'numeric', month: '2-digit', day: '2-digit' })

/** "2026年10月22日" in the site time zone. */
export function formatDay(iso: string): string {
  return dayFormat.format(new Date(iso))
}

/** yyyy-mm-dd in the site time zone, for <time datetime> and day comparisons. */
export function isoDay(iso: string): string {
  return isoFormat.format(new Date(iso))
}

const timeFormat = new Intl.DateTimeFormat('zh-CN', { timeZone: SITE_TIME_ZONE, hour: '2-digit', minute: '2-digit', hour12: false })

/** "2026年10月22日 09:05" in the site time zone (comments, admin tables). */
export function formatDateTime(iso: string): string {
  const d = new Date(iso)
  return `${dayFormat.format(d)} ${timeFormat.format(d)}`
}

/** Only https:, mailto: and site-relative links are rendered as profile/nav links (API-DD §5.8). */
export function safeExternalUrl(url: string): boolean {
  return /^https:\/\/\S+$/i.test(url) || /^mailto:\S+$/i.test(url)
}

export function safeInternalPath(url: string): boolean {
  return /^\/(?!\/)\S*$/.test(url)
}

/** Splits text around case-insensitive occurrences of terms, for safe (text-only) highlighting. */
export function highlightParts(text: string, terms: string[]): { text: string, hit: boolean }[] {
  const clean = terms.map(t => t.trim()).filter(Boolean)
  if (!clean.length) return [{ text, hit: false }]
  const re = new RegExp(`(${clean.map(t => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|')})`, 'giu')
  return text.split(re).filter(p => p !== '').map(p => ({ text: p, hit: clean.some(t => t.toLowerCase() === p.toLowerCase()) }))
}

/** Absolute URL on the public site origin (canonical / OG / sitemap). */
export function absoluteUrl(siteUrl: string, path: string): string {
  return new URL(path, siteUrl.endsWith('/') ? siteUrl : siteUrl + '/').toString()
}
