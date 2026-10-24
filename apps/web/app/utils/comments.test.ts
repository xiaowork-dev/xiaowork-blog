import { describe, expect, it } from 'vitest'
import { commentLength, commentSegments } from './comments'

describe('comment text segments', () => {
  it('keeps plain text as text, including HTML-looking input', () => {
    expect(commentSegments('<script>alert(1)</script> & <b>')).toEqual([{ kind: 'text', text: '<script>alert(1)</script> & <b>' }])
  })

  it('recognises http(s) links and leaves sentence punctuation outside', () => {
    expect(commentSegments('看这里：https://example.com/a?b=1。谢谢')).toEqual([
      { kind: 'text', text: '看这里：' },
      { kind: 'link', text: 'https://example.com/a?b=1', href: 'https://example.com/a?b=1' },
      { kind: 'text', text: '。谢谢' },
    ])
    expect(commentSegments('(http://x.test/p).')).toEqual([
      { kind: 'text', text: '(' },
      { kind: 'link', text: 'http://x.test/p', href: 'http://x.test/p' },
      { kind: 'text', text: ').' },
    ])
  })

  it('never turns other schemes into links', () => {
    for (const s of ['javascript:alert(1)', 'data:text/html,<b>x</b>', 'vbscript:x', 'ftp://x.test', 'JaVaScRiPt:alert(1)']) {
      expect(commentSegments(s).every(seg => seg.kind !== 'link')).toBe(true)
    }
    // a quote cannot break out of the href: it ends the URL token
    const segs = commentSegments('https://x.test/"onmouseover="alert(1)')
    expect(segs[0]).toEqual({ kind: 'link', text: 'https://x.test/', href: 'https://x.test/' })
  })

  it('styles @mentions without linking them', () => {
    expect(commentSegments('@楼主 你好 @Bob_1，再见')).toEqual([
      { kind: 'mention', text: '@楼主' },
      { kind: 'text', text: ' 你好 ' },
      { kind: 'mention', text: '@Bob_1' },
      { kind: 'text', text: '，再见' },
    ])
  })

  it('counts characters like the API', () => {
    expect(commentLength('  👍🏻 你好 ')).toBe(5)
  })
})
