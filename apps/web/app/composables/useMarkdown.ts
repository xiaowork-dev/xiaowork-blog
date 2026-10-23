import { createDocumentRenderer, createRenderer, loadHighlighter, type Render } from '~/utils/markdown'

/** Renderer with syntax highlighting; awaits Shiki once (used by SSR pages). */
export async function useMarkdownRenderer(): Promise<Render> {
  return createRenderer(await loadHighlighter())
}

/**
 * Renderer for live previews: renders immediately without highlighting, then upgrades once Shiki has
 * loaded in the browser, so typing is never blocked by the highlighter download.
 */
export function useLiveMarkdown() {
  const render = shallowRef<Render>(createRenderer(null))
  onMounted(async () => {
    try {
      render.value = createRenderer(await loadHighlighter())
    }
    catch {
      // keep the unhighlighted renderer; content stays fully readable
    }
  })
  return render
}

/** Public reading renderer (heading ids, TOC, code copy buttons) with highlighting; awaited by SSR pages. */
export async function useDocumentRenderer() {
  return createDocumentRenderer(await loadHighlighter())
}
