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
  // Pin the document's text to a metric-stable family. The browser tier's
  // geometry assertions compare rendered widths; macOS resolves Arial
  // directly and the CI image installs fonts-liberation, whose Liberation
  // Sans is metric-compatible with Arial (and is what fontconfig aliases
  // Arial to). Without the pin, a platform's default sans-serif would move
  // the measurements between machines (see the browser-tier ADR's font
  // consequence). The inline style outranks the theme's body rule; the
  // cases render in en-US so no CJK fallback enters the measurement.
  document.body.style.fontFamily = 'Arial, "Liberation Sans", sans-serif'
})

afterEach(() => {
  cleanup()
})
