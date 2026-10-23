// Client behaviour for rendered Markdown (PRD 4.2): copy buttons on code blocks and click/keyboard
// zoom for images. Delegated listeners on the container, so the SSR HTML stays plain and inert.
import type { Ref } from 'vue'
import { copyText } from '~/utils/media'

export function useReadingEnhancements(container: Ref<HTMLElement | null>, openImage: (img: HTMLImageElement) => void) {
  function zoomable(target: EventTarget | null): HTMLImageElement | null {
    const img = target instanceof HTMLImageElement ? target : null
    return img && !img.closest('a') ? img : null
  }

  async function onClick(e: MouseEvent) {
    const button = (e.target as HTMLElement | null)?.closest<HTMLButtonElement>('.code-copy')
    if (button) {
      const code = button.closest('.code-block')?.querySelector('pre')?.textContent ?? ''
      const ok = await copyText(code)
      button.textContent = ok ? '已复制' : '复制失败'
      button.classList.toggle('copied', ok)
      setTimeout(() => {
        button.textContent = '复制'
        button.classList.remove('copied')
      }, 2000)
      return
    }
    const img = zoomable(e.target)
    if (img) openImage(img)
  }

  function onKey(e: KeyboardEvent) {
    const img = zoomable(e.target)
    if (img && (e.key === 'Enter' || e.key === ' ')) {
      e.preventDefault()
      openImage(img)
    }
  }

  function decorate() {
    const el = container.value
    if (!el) return
    for (const img of el.querySelectorAll<HTMLImageElement>('img')) {
      if (img.closest('a')) continue
      img.tabIndex = 0
      img.setAttribute('role', 'button')
      img.setAttribute('aria-label', `放大查看图片${img.alt ? `：${img.alt}` : ''}`)
    }
    for (const b of el.querySelectorAll<HTMLButtonElement>('.code-copy')) b.setAttribute('aria-label', '复制代码')
  }

  onMounted(() => {
    const el = container.value
    if (!el) return
    el.addEventListener('click', onClick)
    el.addEventListener('keydown', onKey)
    decorate()
  })
  onBeforeUnmount(() => {
    container.value?.removeEventListener('click', onClick)
    container.value?.removeEventListener('keydown', onKey)
  })
  return { decorate }
}
