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

// The same widened async budget the unit suites use: a case waits on the
// event it asserts, and a loaded machine may take longer than the default
// one-second poll to settle it.
configure({ asyncUtilTimeout: 5_000 })

beforeEach(() => {
  // Pin the document's text to metric-stable families. The browser tier's
  // geometry assertions compare rendered widths; macOS resolves Arial
  // directly and the CI image installs fonts-liberation, whose Liberation
  // Sans is metric-compatible with Arial (and is what fontconfig aliases
  // Arial to). Without the pin, a platform's default sans-serif would move
  // the measurements between machines (see the browser-tier ADR's font
  // consequence). The inline style outranks the theme's body rule.
  //
  // The header matrix renders in en-US and zh-CN, so the stack names the
  // CJK families too. A Chinese glyph is full-width whichever of them
  // resolves, so a zh-CN run measures a count of em boxes rather than a
  // platform default's proportional metrics -- the same width on macOS
  // (PingFang SC) and on the CI image, where Playwright's system
  // dependencies provide the CJK font.
  document.body.style.fontFamily =
    'Arial, "Liberation Sans", "PingFang SC", "Noto Sans CJK SC", "WenQuanYi Zen Hei", sans-serif'
})

afterEach(() => {
  cleanup()
})
