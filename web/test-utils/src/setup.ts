/**
 * The vitest setup side effect every DOM suite in the workspace shares:
 * jest-dom matchers plus an explicit cleanup between tests. React
 * Testing Library's automatic cleanup only fires when the test framework
 * exposes afterEach globally (it checks globalThis), which vitest
 * deliberately does not, so the import must be explicit -- otherwise a
 * rendered tree leaks into the next test.
 *
 * One environment adaptation is shared here too: jsdom ships
 * `HTMLCanvasElement.getContext()` as a not-implemented stub that emits a
 * `jsdomError` ("Not implemented: HTMLCanvasElement's getContext() method:
 * without installing the @napi-rs/canvas npm package") and returns null. axe-core
 * probes a canvas while computing accessible text -- its `_isIconLigature`
 * check, which detects icon fonts smuggled in as text -- so any axe scan
 * over a document with such a text node prints that error. The stub below
 * returns null directly, exactly the value jsdom already produced, so no
 * scan verdict changes and no caller sees a new shape: only the emitted
 * environment noise leaves the output.
 *
 * A package's own test-utils/setup.ts (the file its vitest config
 * points setupFiles at) imports this module and adds whatever else its
 * environment needs -- the desktop-default matchMedia stub, say (see
 * matchMedia.ts).
 */
import '@testing-library/jest-dom/vitest'
import { cleanup, configure } from '@testing-library/react'
import { afterEach } from 'vitest'
import { asyncBudgetMs, gateTimeScale } from './gate-time.js'

// The testing-library async budget: how long findBy*/waitFor keep
// polling for the condition a case asserts. It is the wait, not the
// work -- a case whose condition never arrives still fails. The default
// (one second) and a narrow value under a loaded machine expire while a
// query is about to settle, failing the case on the clock rather than on
// the behaviour it asserts. The caller's GATE_TIME_SCALE multiplies it,
// the same variable web/vitest.shared.ts multiplies the test budgets by.
configure({ asyncUtilTimeout: asyncBudgetMs(gateTimeScale()) })

afterEach(() => {
  cleanup()
})

if (typeof HTMLCanvasElement !== 'undefined') {
  HTMLCanvasElement.prototype.getContext = (() =>
    null) as unknown as HTMLCanvasElement['getContext']
}
