import { describe, expect, it } from 'vitest'
import { absoluteUrl, formatDay, highlightParts, isoDay, pageWindow, parsePage, safeExternalUrl, safeInternalPath, tagTone } from './public'

describe('public helpers', () => {
  it('parses page numbers strictly', () => {
    expect(parsePage(undefined)).toBe(1)
    expect(parsePage('3')).toBe(3)
    for (const bad of ['0', '-1', 'abc', '1.5', '01', '9999999']) expect(parsePage(bad)).toBeNull()
  })

  it('builds a compact pager window', () => {
    expect(pageWindow(1, 1)).toEqual([1])
    expect(pageWindow(1, 3)).toEqual([1, 2, 3])
    expect(pageWindow(5, 10)).toEqual([1, null, 4, 5, 6, null, 10])
    expect(pageWindow(10, 10)).toEqual([1, null, 9, 10])
  })

  it('formats dates in the site time zone', () => {
    expect(formatDay('2025-12-31T17:00:00Z')).toBe('2026年1月1日')
    expect(isoDay('2025-12-31T17:00:00Z')).toBe('2026-01-01')
  })

  it('accepts only safe link targets', () => {
    expect(safeExternalUrl('https://github.com/x')).toBe(true)
    expect(safeExternalUrl('mailto:a@b.c')).toBe(true)
    for (const bad of ['javascript:alert(1)', 'http://x', 'data:text/html,x', '//evil.test']) expect(safeExternalUrl(bad)).toBe(false)
    expect(safeInternalPath('/about')).toBe(true)
    expect(safeInternalPath('//evil.test')).toBe(false)
    expect(safeInternalPath('https://x')).toBe(false)
  })

  it('splits text for highlighting without producing markup', () => {
    expect(highlightParts('Docker 容器化部署', ['容器', 'docker'])).toEqual([
      { text: 'Docker', hit: true }, { text: ' ', hit: false }, { text: '容器', hit: true }, { text: '化部署', hit: false },
    ])
    expect(highlightParts('a.b', ['.'])).toEqual([{ text: 'a', hit: false }, { text: '.', hit: true }, { text: 'b', hit: false }])
  })

  it('makes absolute URLs on the site origin', () => {
    expect(absoluteUrl('https://blog.example.com', '/posts/a')).toBe('https://blog.example.com/posts/a')
    expect(absoluteUrl('https://blog.example.com/', '/')).toBe('https://blog.example.com/')
  })
})

describe('tagTone', () => {
  it('is stable per slug and stays within the four tones', () => {
    for (const slug of ['go', 'nuxt', 'docker', 'postgresql', 'self-hosted', '中文', '']) {
      const t = tagTone(slug)
      expect(t).toBe(tagTone(slug))
      expect([0, 1, 2, 3]).toContain(t)
    }
    expect(new Set(['go', 'nuxt', 'docker', 'postgresql', 'self-hosted'].map(tagTone)).size).toBeGreaterThan(1)
  })
})
