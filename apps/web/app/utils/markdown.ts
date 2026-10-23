// Markdown → HTML for the editor preview and published articles (SAD §5.3, API-DD §5.1).
// Safety model (no reliance on a later sanitizer):
//   - html: false — raw HTML in the source is always escaped, never rendered;
//   - every link/image URL passes an allowlist (http/https/mailto/relative); javascript:, data:, vbscript:
//     and obfuscated variants are rejected;
//   - code is highlighted by Shiki, which escapes its input; unknown fence languages fall back to escaped text;
//   - the only markup we inject (task-list checkboxes, link/image attributes) is fixed.
// Kept free of Nuxt auto-imports so it can be unit tested with vitest.
import MarkdownItCtor from 'markdown-it'
import type { MarkdownIt, RendererRule, StateCore } from 'markdown-it'
import type { HighlighterCore } from 'shiki/core'

const LANGS = ['bash', 'css', 'diff', 'dockerfile', 'go', 'html', 'java', 'javascript', 'json', 'markdown', 'python',
  'rust', 'sql', 'typescript', 'vue', 'yaml'] as const

const ALIASES: Record<string, string> = {
  js: 'javascript', ts: 'typescript', sh: 'bash', shell: 'bash', shellscript: 'bash', zsh: 'bash', console: 'bash',
  yml: 'yaml', golang: 'go', py: 'python', md: 'markdown', docker: 'dockerfile', htm: 'html',
}

let highlighter: Promise<HighlighterCore> | null = null

/** Loads Shiki once (JavaScript regex engine, no WASM) with a fixed set of languages and two themes. */
export function loadHighlighter(): Promise<HighlighterCore> {
  highlighter ??= (async () => {
    const [{ createHighlighterCore }, { createJavaScriptRegexEngine }] = await Promise.all([
      import('shiki/core'), import('shiki/engine/javascript'),
    ])
    return createHighlighterCore({
      engine: createJavaScriptRegexEngine(),
      themes: [import('shiki/themes/github-light.mjs'), import('shiki/themes/github-dark.mjs')],
      langs: [
        import('shiki/langs/bash.mjs'), import('shiki/langs/css.mjs'), import('shiki/langs/diff.mjs'),
        import('shiki/langs/dockerfile.mjs'), import('shiki/langs/go.mjs'), import('shiki/langs/html.mjs'),
        import('shiki/langs/java.mjs'), import('shiki/langs/javascript.mjs'), import('shiki/langs/json.mjs'),
        import('shiki/langs/markdown.mjs'), import('shiki/langs/python.mjs'), import('shiki/langs/rust.mjs'),
        import('shiki/langs/sql.mjs'), import('shiki/langs/typescript.mjs'), import('shiki/langs/vue.mjs'),
        import('shiki/langs/yaml.mjs'),
      ],
    })
  })()
  return highlighter
}

function resolveLang(info: string): string | null {
  const name = info.trim().split(/\s+/)[0]?.toLowerCase() ?? ''
  const lang = ALIASES[name] ?? name
  return (LANGS as readonly string[]).includes(lang) ? lang : null
}

/**
 * URL allowlist. markdown-it passes the already entity-decoded URL; control characters and whitespace are
 * removed before reading the scheme so "jav&#x09;ascript:" or " javascript:" cannot slip through.
 */
export function isSafeUrl(url: string): boolean {
  // eslint-disable-next-line no-control-regex
  const compact = url.replace(/[\u0000- \u007f-\u009f]/g, '')
  const scheme = /^([a-z][a-z0-9+.-]*):/i.exec(compact)
  if (!scheme) return true // relative: /path, #anchor, ./x, ?q
  return ['http', 'https', 'mailto'].includes(scheme[1]!.toLowerCase())
}

function taskLists(md: MarkdownIt) {
  md.core.ruler.after('inline', 'task-lists', (state: StateCore) => {
    const tokens = state.tokens
    for (let i = 2; i < tokens.length; i++) {
      const inline = tokens[i]!
      if (inline.type !== 'inline' || tokens[i - 1]!.type !== 'paragraph_open' || tokens[i - 2]!.type !== 'list_item_open') continue
      const m = /^\[([ xX])\][  ]/.exec(inline.content)
      const first = inline.children?.[0]
      if (!m || !first || first.type !== 'text') continue
      first.content = first.content.slice(m[0].length)
      const box = new state.Token('html_inline', '', 0)
      box.content = m[1] === ' '
        ? '<input type="checkbox" class="task-checkbox" disabled> '
        : '<input type="checkbox" class="task-checkbox" checked disabled> '
      inline.children!.unshift(box)
      tokens[i - 2]!.attrJoin('class', 'task-list-item')
    }
  })
}

export type Render = (source: string) => string

/** Creates a renderer; pass the loaded highlighter (or null to render code blocks unhighlighted). */
export function createRenderer(hl: HighlighterCore | null): Render {
  const md: MarkdownIt = new MarkdownItCtor({
    html: false,
    linkify: true,
    typographer: false,
    highlight: (code, info) => {
      const lang = hl && resolveLang(info)
      if (!hl || !lang) return '' // markdown-it escapes and wraps it
      try {
        return hl.codeToHtml(code, { lang, themes: { light: 'github-light', dark: 'github-dark' }, defaultColor: false })
      }
      catch {
        return ''
      }
    },
  })
  md.validateLink = isSafeUrl
  taskLists(md)

  const fallback: RendererRule = (tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options)
  const defaultLink = md.renderer.rules.link_open ?? fallback
  md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
    const href = String(tokens[idx]!.attrGet('href') ?? '')
    if (/^https?:\/\//i.test(href)) {
      tokens[idx]!.attrSet('target', '_blank')
      tokens[idx]!.attrSet('rel', 'noopener noreferrer nofollow')
    }
    return defaultLink(tokens, idx, options, env, self)
  }
  const defaultImage = md.renderer.rules.image!
  md.renderer.rules.image = (tokens, idx, options, env, self) => {
    tokens[idx]!.attrSet('loading', 'lazy')
    tokens[idx]!.attrSet('decoding', 'async')
    return defaultImage(tokens, idx, options, env, self)
  }
  const defaultTable = md.renderer.rules.table_open ?? fallback
  md.renderer.rules.table_open = (tokens, idx, options, env, self) => '<div class="md-table">' + defaultTable(tokens, idx, options, env, self)
  md.renderer.rules.table_close = () => '</table></div>\n'

  return source => md.render(source)
}
