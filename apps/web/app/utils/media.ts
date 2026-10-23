// Shared media types and helpers (API-DD §5.7 DTOs).
export interface MediaItem {
  id: string
  url: string
  thumbnail_url: string
  original_filename: string
  mime_type: string
  size_bytes: number
  width: number
  height: number
  usage_kind: 'ARTICLE' | 'SITE' | 'PAGE' | 'AVATAR'
  created_at: string
  reference_count?: number
  uploaded_by?: { id: string, nickname: string }
}

export const MEDIA_ACCEPT = '.jpg,.jpeg,.png,.webp,.gif,image/jpeg,image/png,image/webp,image/gif'
export const MEDIA_MAX_BYTES = 10 * 1024 * 1024 // mirrors the API default; the API stays authoritative

const EXT_BY_TYPE: Record<string, string> = { 'image/jpeg': 'jpg', 'image/png': 'png', 'image/webp': 'webp', 'image/gif': 'gif' }

/** Quick client-side check so obviously wrong files fail before uploading. */
export function precheckImage(file: File, maxBytes = MEDIA_MAX_BYTES): string | null {
  const ext = file.name.includes('.') ? file.name.split('.').pop()!.toLowerCase() : ''
  if (!['jpg', 'jpeg', 'png', 'webp', 'gif'].includes(ext)) return '仅支持 JPG、PNG、WebP、GIF 图片'
  if (file.size === 0) return '文件为空'
  if (file.size > maxBytes) return `文件超过 ${formatBytes(maxBytes)} 上限`
  return null
}

/** Pasted clipboard images often have no useful name; give them one matching their type. */
export function namePastedFile(file: File): File {
  const ext = EXT_BY_TYPE[file.type]
  if (!ext || /\.(jpe?g|png|webp|gif)$/i.test(file.name)) return file
  return new File([file], `pasted-${Date.now()}.${ext}`, { type: file.type })
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

export function formatDate(iso: string): string {
  return new Date(iso).toLocaleString('zh-CN', { dateStyle: 'medium', timeStyle: 'short' })
}

/** Copies text, falling back to a hidden textarea where the async Clipboard API is unavailable (http). */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  }
  catch {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  }
}

const REFERENCE_LABELS: Record<string, string> = {
  article_drafts: '文章草稿', published_articles: '已发布文章', avatars: '用户头像', site_settings: '站点设置', pages: '关于页',
}

/** "仍被 文章草稿 1 处、站点设置 1 处 引用" from the 409 RESOURCE_IN_USE details. */
export function describeReferences(details: Record<string, unknown>): string {
  const refs = (details.references ?? {}) as Record<string, number>
  const parts = Object.entries(refs).filter(([, n]) => n > 0).map(([k, n]) => `${REFERENCE_LABELS[k] ?? k} ${n} 处`)
  return parts.length ? `仍被${parts.join('、')}引用，不能删除。请先在对应位置移除该图片。` : '该图片仍被引用，不能删除。'
}
