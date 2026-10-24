import { describe, expect, it } from 'vitest'
import { eventText, linkProblem, type EditableLink } from './admin'

const social = (url: string, label = 'x'): EditableLink => ({ icon_key: 'link', label, url, is_enabled: true })
const nav = (url: string, type: 'INTERNAL' | 'EXTERNAL' = 'INTERNAL'): EditableLink => ({ label: 'x', link_type: type, url, is_enabled: true })

describe('admin link checks (mirror API-DD §5.8)', () => {
  it('accepts https and plain mailto profile links', () => {
    expect(linkProblem(social('https://github.com/x'), 'social')).toBe('')
    expect(linkProblem(social('mailto:hello@example.test'), 'social')).toBe('')
  })

  it('rejects unsafe or malformed profile links', () => {
    for (const u of ['javascript:alert(1)', 'http://example.com', 'data:text/html,x', 'mailto:hello@example.test?subject=x', 'mailto:x', '//example.com', 'https://']) {
      expect(linkProblem(social(u), 'social'), u).not.toBe('')
    }
    expect(linkProblem(social('https://x.test', ' '), 'social')).toBe('请填写名称')
  })

  it('keeps navigation on public paths or https', () => {
    expect(linkProblem(nav('/about'), 'nav')).toBe('')
    expect(linkProblem(nav('https://github.com', 'EXTERNAL'), 'nav')).toBe('')
    for (const u of ['/admin', '/account/profile', '/auth/login', '/api/v1/site', '//evil.test', 'about']) {
      expect(linkProblem(nav(u), 'nav'), u).not.toBe('')
    }
    expect(linkProblem(nav('http://x.test', 'EXTERNAL'), 'nav')).not.toBe('')
  })

  it('names audit actions', () => {
    expect(eventText('user.disable')).toBe('禁用用户')
    expect(eventText('UNKNOWN')).toBe('UNKNOWN')
  })
})
