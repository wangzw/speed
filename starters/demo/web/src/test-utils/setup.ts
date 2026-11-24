import '@testing-library/jest-dom/vitest'
import { cleanup, configure } from '@testing-library/react'
import { afterEach, vi } from 'vitest'
import { asyncBudgetMs, gateTimeScale } from './gate-time.js'

configure({ asyncUtilTimeout: asyncBudgetMs(gateTimeScale()) })

// Node's experimental global localStorage shadows jsdom's and is unavailable without a flag, so install an in-memory one.
const deviceStorage = new Map<string, string>()
Object.defineProperty(globalThis, 'localStorage', {
  configurable: true,
  writable: true,
  value: {
    getItem: (key: string) => deviceStorage.get(key) ?? null,
    setItem: (key: string, value: string) => {
      deviceStorage.set(key, value)
    },
    removeItem: (key: string) => {
      deviceStorage.delete(key)
    },
    clear: () => deviceStorage.clear(),
    key: (index: number) => [...deviceStorage.keys()][index] ?? null,
    get length() {
      return deviceStorage.size
    },
  },
})

// jsdom does not implement matchMedia; useMediaQuery would otherwise throw.
window.matchMedia = vi.fn().mockImplementation((query: string) => ({
  matches: true,
  media: query,
  onchange: null,
  addListener: () => {},
  removeListener: () => {},
  addEventListener: () => {},
  removeEventListener: () => {},
  dispatchEvent: () => true,
})) as unknown as typeof window.matchMedia

// jsdom has no canvas; axe's text probes must see getContext return null, never throw.
if (typeof HTMLCanvasElement !== 'undefined') {
  HTMLCanvasElement.prototype.getContext = (() =>
    null) as unknown as HTMLCanvasElement['getContext']
}

afterEach(() => {
  cleanup()
})
