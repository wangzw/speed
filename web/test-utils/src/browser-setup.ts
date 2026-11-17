/**
 * The browser tier's setup side effect: jest-dom matchers plus the
 * explicit RTL cleanup the unit setup also carries.
 *
 * It deliberately does not import the unit setup (@speed/test-utils/setup):
 * that module installs jsdom workarounds -- a canvas getContext stub and,
 * through the packages' own setups, a matchMedia stub -- which would
 * replace the very browser capabilities the browser tier exists to
 * measure. Layout, matchMedia and ResizeObserver are real here.
 */
import '@testing-library/jest-dom/vitest'
import { cleanup, configure } from '@testing-library/react'
import { afterEach, beforeEach } from 'vitest'
import { userEvent } from 'vitest/browser'
import { asyncBudgetMs } from './gate-time.js'
import type { AxeKeyboardDriver } from './axe.js'

/**
 * The real-keyboard driver the axe harness reads when it exercises a
 * focus trap (see axe.ts). The harness cannot import `vitest/browser`
 * itself: that module's declarations augment Vitest's matchers with an
 * older jest-dom set whose `toHaveStyle` rejects kebab-case keys, and
 * the harness is compiled into every jsdom program, where the
 * augmentation would narrow unrelated assertions. This setup runs only
 * in the browser tier, so the import -- and its augmentation -- stays
 * here, and the harness reads the driver off the global.
 */
const keyboardDriverHost = globalThis as {
  __speedAxeKeyboardDriver?: AxeKeyboardDriver
}
keyboardDriverHost.__speedAxeKeyboardDriver = (keys) =>
  userEvent.keyboard(keys)

/**
 * The caller's `GATE_TIME_SCALE`, injected by the browser tier's config
 * (`web/vitest.browser.config.ts`) because the browser page has no process
 * global to read it from: the read the unit setup uses would always yield
 * 1 here. The config parses the variable in Node, where the environment is
 * real, and a bad value fails the config load before a browser starts.
 */
declare const __SPEED_GATE_TIME_SCALE__: number

// The same widened async budget the unit suites use: a case waits on the
// event it asserts, and a loaded machine may take longer than the default
// one-second poll to settle it. The caller's GATE_TIME_SCALE multiplies it,
// with the value injected by the config (there is no process global here).
configure({ asyncUtilTimeout: asyncBudgetMs(__SPEED_GATE_TIME_SCALE__) })

/**
 * The metric-stable families every element's text is measured in. macOS
 * resolves Arial directly and the CI image installs fonts-liberation, whose
 * Liberation Sans is metric-compatible with Arial (and is what fontconfig
 * aliases Arial to).
 *
 * The header matrix renders in en-US and zh-CN, so the stack names the
 * CJK families too. A Chinese glyph is full-width whichever of them
 * resolves, so a zh-CN run measures a count of em boxes rather than a
 * platform default's proportional metrics -- the same width on macOS
 * (PingFang SC) and on the CI image, where Playwright's system
 * dependencies provide the CJK font.
 */
const PINNED_FONT_FAMILY =
  'Arial, "Liberation Sans", "PingFang SC", "Noto Sans CJK SC", "WenQuanYi Zen Hei", sans-serif'

/** The id of the style element that carries the pin. */
const FONT_PIN_ID = 'speed-browser-tier-font-pin'

beforeEach(() => {
  // Pin the text of every element. The browser tier's geometry assertions
  // compare rendered widths, and without the pin a platform's own fonts
  // would move the measurements between machines (see the browser-tier
  // ADR's font consequence). MUI's components declare the theme's font
  // stack on their own classes, and a declared value beats an inherited
  // one, so a pin on the body reaches only the text that inherits from it:
  // every table cell, chip and button would render in the stack's
  // `system-ui`, which is San Francisco on macOS and DejaVu Sans on the CI
  // image. The important declaration on every element outranks those
  // class rules. It also outranks the monospace slot and a preset's own
  // stack, so monospace text renders in the pinned family in this tier: it
  // measures layout invariants under one known font, not a family's
  // typography.
  if (document.getElementById(FONT_PIN_ID) !== null) return
  const pin = document.createElement('style')
  pin.id = FONT_PIN_ID
  pin.textContent = `body, body * { font-family: ${PINNED_FONT_FAMILY} !important; }`
  document.head.append(pin)
})

afterEach(() => {
  cleanup()
})
