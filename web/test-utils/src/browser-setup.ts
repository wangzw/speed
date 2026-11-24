// Do not import ./setup.ts here: its jsdom workarounds would replace the
// browser capabilities this tier exists to measure.
import '@testing-library/jest-dom/vitest'
import { cleanup, configure } from '@testing-library/react'
import { afterEach, beforeEach } from 'vitest'
import { userEvent } from 'vitest/browser'
import { asyncBudgetMs } from './gate-time.js'
import type { AxeKeyboardDriver } from './axe.js'

// `vitest/browser` stays in this browser-only setup: it augments Vitest's
// matchers with an older jest-dom set, and the harness is compiled into
// every jsdom program too.
const keyboardDriverHost = globalThis as {
  __speedAxeKeyboardDriver?: AxeKeyboardDriver
}
keyboardDriverHost.__speedAxeKeyboardDriver = (keys) =>
  userEvent.keyboard(keys)

declare const __SPEED_GATE_TIME_SCALE__: number

configure({ asyncUtilTimeout: asyncBudgetMs(__SPEED_GATE_TIME_SCALE__) })

const PINNED_FONT_FAMILY =
  'Arial, "Liberation Sans", "PingFang SC", "Noto Sans CJK SC", "WenQuanYi Zen Hei", sans-serif'

const FONT_PIN_ID = 'speed-browser-tier-font-pin'

beforeEach(() => {
  // The pin must reach every element (`body *` plus `!important`), not just
  // inherited text: MUI declares its own font stack on component classes.
  if (document.getElementById(FONT_PIN_ID) !== null) return
  const pin = document.createElement('style')
  pin.id = FONT_PIN_ID
  pin.textContent = `body, body * { font-family: ${PINNED_FONT_FAMILY} !important; }`
  document.head.append(pin)
})

afterEach(() => {
  cleanup()
})
