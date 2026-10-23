import { beforeAll, describe, expect, it } from 'vitest'
import { createRenderer, isSafeUrl, loadHighlighter, type Render } from './markdown'

let render: Render

beforeAll(async () => {
  render = createRenderer(await loadHighlighter())
})

// Any of these in the output means script could run.
function assertInert(html: string) {
  expect(html).not.toMatch(/<script/i)
  // Event-handler attribute on a real tag. Quoted attribute values are blanked first: text such as
  // alt="&quot; onerror=&quot;…" is escaped data inside a value, not an attribute.
  const tagsOnly = html.replace(/"[^"]*"/g, '""')
  expect(tagsOnly).not.toMatch(/<[a-z][^>]*\son[a-z]+\s*=/i)
  expect(html).not.toMatch(/(href|src)\s*=\s*"\s*(javascript|vbscript|data):/i)
  expect(html).not.toMatch(/<(iframe|object|embed|svg|style|form|meta|base)\b/i)
}

describe('XSS: raw HTML and dangerous URLs never become live markup', () => {
  const vectors: Record<string, string> = {
    'script tag': '<script>alert(1)</script>',
    'img onerror': '<img src=x onerror=alert(1)>',
    'svg onload': '<svg onload=alert(1)></svg>',
    'iframe': '<iframe src="https://evil.example"></iframe>',
    'inline html in paragraph': 'hello <b onclick="alert(1)">x</b>',
    'html in table cell': '| a |\n|---|\n| <img src=x onerror=alert(1)> |',
    'javascript link': '[x](javascript:alert(1))',
    'mixed-case scheme': '[x](JaVaScRiPt:alert(1))',
    'entity-obfuscated scheme': '[x](jav&#x09;ascript:alert(1))',
    'leading space scheme': '[x]( javascript:alert(1))',
    'vbscript link': '[x](vbscript:msgbox(1))',
    'data html link': '[x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)',
    'data svg image': '![x](data:image/svg+xml;base64,PHN2ZyBvbmxvYWQ9YWxlcnQoMSk+)',
    'javascript autolink': '<javascript:alert(1)>',
    'linkified javascript': 'see javascript:alert(1) here',
    'reference link': '[x][r]\n\n[r]: javascript:alert(1)',
    'title attribute breakout': '[x](https://ok.example "t\\" onmouseover=\\"alert(1)")',
    'image alt breakout': '![" onerror="alert(1)](/media/x.png)',
    'fence language injection': '```"><img src=x onerror=alert(1)>\ncode\n```',
    'code span': '`<script>alert(1)</script>`',
  }
  for (const [name, src] of Object.entries(vectors)) {
    it(name, () => assertInert(render(src)))
  }

  it('escapes instead of dropping', () => {
    expect(render('<script>alert(1)</script>')).toContain('&lt;script&gt;')
    expect(render('[x](javascript:alert(1))')).not.toContain('<a ')
  })
})

describe('URL allowlist', () => {
  it.each(['https://a.b', 'http://a.b', 'mailto:a@b.c', '/media/x/file', '#top', './rel', '?q=1'])('allows %s', (u) => {
    expect(isSafeUrl(u)).toBe(true)
  })
  it.each(['javascript:alert(1)', ' javascript:x', 'java\tscript:x', 'JAVASCRIPT:x', 'data:text/html,x', 'vbscript:x', 'file:///etc/passwd'])('blocks %s', (u) => {
    expect(isSafeUrl(u)).toBe(false)
  })
})

describe('supported Markdown', () => {
  it('renders headings, lists, quotes, tables, tasks, links and images', () => {
    const html = render([
      '# 标题', '## 小节', '- a', '1. b', '> 引用', '',
      '| 列 | 值 |', '|---|---|', '| x | y |', '',
      '- [ ] 待办', '- [x] 完成', '',
      '[外链](https://example.com) [站内](/about)', '',
      '![图](/media/00000000-0000-4000-8000-000000000000/file)', '', '~~删除~~',
    ].join('\n'))
    expect(html).toContain('<h1>标题</h1>')
    expect(html).toContain('<h2>小节</h2>')
    expect(html).toContain('<blockquote>')
    expect(html).toContain('<div class="md-table"><table>')
    expect(html).toMatch(/<li class="task-list-item"><input type="checkbox" class="task-checkbox" disabled> 待办/)
    expect(html).toMatch(/<input type="checkbox" class="task-checkbox" checked disabled> 完成/)
    expect(html).toContain('<a href="https://example.com" target="_blank" rel="noopener noreferrer nofollow">外链</a>')
    expect(html).toContain('<a href="/about">站内</a>')
    expect(html).toContain('loading="lazy"')
    expect(html).toContain('<s>删除</s>')
  })

  it('highlights known languages and escapes unknown ones', () => {
    const go = render('```go\nfmt.Println("<b>")\n```')
    expect(go).toContain('class="shiki')
    expect(go).toContain('&#x3C;b>') // Shiki escapes the code
    const unknown = render('```brainfuck\n<b>x</b>\n```')
    expect(unknown).toContain('&lt;b&gt;x&lt;/b&gt;')
    expect(unknown).not.toContain('class="shiki')
  })
})
