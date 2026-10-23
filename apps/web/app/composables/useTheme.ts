// Light / dark / follow-system theme (PRD 4.2). The choice is kept in localStorage; the inline script in
// nuxt.config applies it before first paint, so there is no light/dark flash (UI/UX §10).
export type ThemeChoice = 'light' | 'dark' | 'system'

export const THEME_KEY = 'xwb:theme'
const ORDER: ThemeChoice[] = ['system', 'light', 'dark']
export const THEME_LABEL: Record<ThemeChoice, string> = { system: '跟随系统', light: '浅色', dark: '深色' }

export function useTheme() {
  const choice = useState<ThemeChoice>('theme', () => 'system')

  function apply(value: ThemeChoice) {
    choice.value = value
    const root = document.documentElement
    if (value === 'system') delete root.dataset.theme
    else root.dataset.theme = value
    try {
      if (value === 'system') localStorage.removeItem(THEME_KEY)
      else localStorage.setItem(THEME_KEY, value)
    }
    catch {
      // storage blocked (private mode): the choice lasts for this page only
    }
  }

  onMounted(() => {
    const current = document.documentElement.dataset.theme
    choice.value = current === 'light' || current === 'dark' ? current : 'system'
  })

  function cycle() {
    apply(ORDER[(ORDER.indexOf(choice.value) + 1) % ORDER.length]!)
  }

  return { choice, apply, cycle }
}
