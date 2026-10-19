/**
 * The demo host's vitest setup side effect: jest-dom matchers, explicit RTL
 * cleanup, a desktop-default window.matchMedia stub, and a canvas-context
 * stub.
 *
 * RTL's automatic cleanup only fires when the test framework exposes afterEach
 * globally (it checks globalThis), which vitest does not, so the import must be
 * explicit or a rendered tree leaks into the next test. jsdom implements no
 * matchMedia either, and AppShell's responsive split reads it through MUI's
 * useMediaQuery, so the stub reports the desktop branch by default.
 *
 * jsdom ships `HTMLCanvasElement.getContext()` as a not-implemented stub that
 * emits a `jsdomError` ("Not implemented: HTMLCanvasElement's getContext()
 * method: without installing the canvas npm package") and returns null. The
 * route-level accessibility sweep (src/a11y) runs axe-core, whose
 * accessible-text computation probes a canvas to detect icon fonts smuggled in
 * as text (its `_isIconLigature` check), so that error would print once per
 * scan. The stub below returns null directly -- the same value jsdom already
 * produced, only without the emitted error, so no verdict changes; the demo's
 * axe scans and its ordinary suites both just stop printing the noise. The same
 * stub lives in @speed/notification-ui's setup for the same reason.
 */

import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

/**
 * An in-memory device storage for every suite.
 *
 * The Node runtime's experimental global `localStorage` shadows jsdom's and
 * is unavailable without `--localstorage-file` (it warns on access and yields
 * nothing), so the app's device-level choices -- the language and the colour
 * mode -- would hit a storage that is not there. This stand-in is installed
 * on the global window once; a suite that asserts on writes still installs
 * its own double with `vi.stubGlobal` (and `vi.unstubAllGlobals` restores
 * this one).
 */
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

if (typeof HTMLCanvasElement !== 'undefined') {
  HTMLCanvasElement.prototype.getContext = (() =>
    null) as unknown as HTMLCanvasElement['getContext']
}

afterEach(() => {
  cleanup()
})
