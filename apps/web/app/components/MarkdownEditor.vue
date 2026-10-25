<script setup lang="ts">
// Markdown source editor (CodeMirror 6, SAD §0). Loaded only in the browser. Pasting or dropping images
// uploads them through the media API (P3) and inserts a site-relative /media/{id}/file reference.
import type { EditorView } from '@codemirror/view'
import { describeError, useApi } from '~/composables/useApi'
import { precheckImage, namePastedFile, type MediaItem } from '~/utils/media'

const props = defineProps<{ modelValue: string, readonly?: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [value: string]
  'save': []
  'notice': [message: string, tone: 'error' | 'success']
}>()

const host = ref<HTMLDivElement | null>(null)
const api = useApi()
let view: EditorView | null = null
let uploadSeq = 0

function wrap(before: string, after = before, sample = '文字') {
  if (!view) return
  const { from, to } = view.state.selection.main
  const selected = view.state.sliceDoc(from, to) || sample
  view.dispatch({
    changes: { from, to, insert: before + selected + after },
    selection: { anchor: from + before.length, head: from + before.length + selected.length },
  })
  view.focus()
}

/** Adds a prefix (e.g. "## ", "- ", "> ") to every selected line. */
function prefixLines(prefix: string | ((i: number) => string)) {
  if (!view) return
  const { from, to } = view.state.selection.main
  const first = view.state.doc.lineAt(from).number
  const last = view.state.doc.lineAt(to).number
  const changes = []
  for (let n = first; n <= last; n++) {
    const line = view.state.doc.line(n)
    changes.push({ from: line.from, insert: typeof prefix === 'string' ? prefix : prefix(n - first) })
  }
  view.dispatch({ changes })
  view.focus()
}

/** Inserts a block on its own lines at the cursor. */
function insertBlock(text: string) {
  if (!view) return
  const { from, to } = view.state.selection.main
  const before = from > 0 && view.state.sliceDoc(from - 1, from) !== '\n' ? '\n\n' : ''
  view.dispatch({ changes: { from, to, insert: before + text + '\n' }, selection: { anchor: from + before.length + text.length + 1 } })
  view.focus()
}

function insertText(text: string) {
  if (!view) return
  const { from, to } = view.state.selection.main
  view.dispatch({ changes: { from, to, insert: text }, selection: { anchor: from + text.length } })
  view.focus()
}

function replaceFirst(search: string, replacement: string) {
  if (!view) return
  const at = view.state.doc.toString().indexOf(search)
  if (at >= 0) view.dispatch({ changes: { from: at, to: at + search.length, insert: replacement } })
}

async function uploadFiles(files: File[], pos?: number) {
  if (!view) return
  for (const raw of files) {
    const file = namePastedFile(raw)
    const problem = precheckImage(file)
    if (problem) {
      emit('notice', `${file.name}：${problem}`, 'error')
      continue
    }
    const marker = `![上传中：${file.name} #${++uploadSeq}]()`
    const at = pos ?? view.state.selection.main.head
    view.dispatch({ changes: { from: at, insert: marker }, selection: { anchor: at + marker.length } })
    pos = undefined
    const form = new FormData()
    form.append('usage_kind', 'ARTICLE')
    form.append('file', file, file.name)
    try {
      const media = await api.upload<MediaItem>('/api/v1/admin/media', form)
      replaceFirst(marker, media ? `![${file.name.replace(/[[\]]/g, '')}](${media.url})` : '')
      emit('notice', `已上传 ${file.name}`, 'success')
    }
    catch (err) {
      replaceFirst(marker, '')
      emit('notice', `${file.name} 上传失败：${describeError(err)}`, 'error') // the text around it is untouched
    }
  }
}

onMounted(async () => {
  const [{ EditorView: View, keymap, placeholder }, { EditorState }, { basicSetup }, { markdown }, { indentWithTab }, { HighlightStyle, syntaxHighlighting }, { tags: t }] = await Promise.all([
    import('@codemirror/view'), import('@codemirror/state'), import('codemirror'), import('@codemirror/lang-markdown'),
    import('@codemirror/commands'), import('@codemirror/language'), import('@lezer/highlight'),
  ])
  // Markdown syntax colours from the site tokens (P8-B8), so the source reads well in both themes; the
  // default CodeMirror style (dark blue / purple on white) was hard to read on the dark background.
  const markdownStyle = HighlightStyle.define([
    { tag: t.heading, color: 'var(--text)', fontWeight: '700' },
    { tag: [t.strong], fontWeight: '700' },
    { tag: [t.emphasis], fontStyle: 'italic' },
    { tag: [t.strikethrough], textDecoration: 'line-through' },
    { tag: [t.link, t.url], color: 'var(--accent)' },
    { tag: [t.monospace], color: 'var(--violet)' },
    { tag: [t.quote], color: 'var(--muted)', fontStyle: 'italic' },
    { tag: [t.list], color: 'var(--teal)' },
    { tag: [t.processingInstruction, t.meta, t.contentSeparator], color: 'var(--muted)' },
    { tag: [t.labelName], color: 'var(--amber)' },
  ])
  const run = (fn: () => void) => () => (fn(), true)
  view = new View({
    parent: host.value!,
    state: EditorState.create({
      doc: props.modelValue,
      extensions: [
        basicSetup,
        markdown(),
        syntaxHighlighting(markdownStyle),
        View.lineWrapping,
        EditorState.readOnly.of(Boolean(props.readonly)),
        placeholder('在这里用 Markdown 写正文…'),
        keymap.of([
          { key: 'Mod-b', run: run(() => wrap('**', '**', '加粗文字')) },
          { key: 'Mod-i', run: run(() => wrap('*', '*', '斜体文字')) },
          { key: 'Mod-k', run: run(() => wrap('[', '](https://)', '链接文字')) },
          { key: 'Mod-e', run: run(() => wrap('`', '`', 'code')) },
          { key: 'Mod-s', preventDefault: true, run: run(() => emit('save')) },
          indentWithTab,
        ]),
        View.updateListener.of((u) => {
          if (u.docChanged) emit('update:modelValue', u.state.doc.toString())
        }),
        View.domEventHandlers({
          paste(e) {
            const files = Array.from(e.clipboardData?.files ?? []).filter(f => f.type.startsWith('image/'))
            if (!files.length || props.readonly) return false
            e.preventDefault()
            void uploadFiles(files)
            return true
          },
          drop(e, v) {
            const files = Array.from(e.dataTransfer?.files ?? []).filter(f => f.type.startsWith('image/'))
            if (!files.length || props.readonly) return false
            e.preventDefault()
            void uploadFiles(files, v.posAtCoords({ x: e.clientX, y: e.clientY }) ?? undefined)
            return true
          },
        }),
      ],
    }),
  })
})

// External changes (load from server, import .md, conflict resolution) replace the document.
watch(() => props.modelValue, (value) => {
  if (view && value !== view.state.doc.toString()) {
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: value } })
  }
})

onBeforeUnmount(() => view?.destroy())

defineExpose({ wrap, prefixLines, insertBlock, insertText, focus: () => view?.focus() })
</script>

<template>
  <div ref="host" class="md-editor" :aria-busy="!host" />
</template>
