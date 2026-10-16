/**
 * vitest setup for notification-ui tests.
 *
 * The shared workspace setup side effect (see @speed/test-utils/setup) --
 * jest-dom matchers plus the explicit RTL cleanup between tests -- plus
 * one environment adaptation this package's scans need.
 *
 * jsdom ships `HTMLCanvasElement.getContext()` as a not-implemented stub
 * that emits a `jsdomError` ("Not implemented: HTMLCanvasElement's
 * getContext() method: without installing the canvas npm package") and
 * returns null. axe-core's accessible-text computation probes a canvas to
 * detect icon fonts smuggled in as text (its `_isIconLigature` check),
 * which runs for a visible text node -- this package renders short,
 * standalone text nodes (a notification badge's count) that reach it, so
 * every axe scan here would otherwise print that jsdom error. axe handles
 * the absent context (a null return) without failing the scan -- verified:
 * the probe returns null and the scan completes. The stub below returns
 * null directly, the same value jsdom already produced, only without the
 * emitted error, so no axe verdict changes: every rule still runs and the
 * same result is asserted; only the jsdom stub's noise leaves the output.
 */
import '@speed/test-utils/setup'

if (typeof HTMLCanvasElement !== 'undefined') {
  HTMLCanvasElement.prototype.getContext = (() =>
    null) as unknown as HTMLCanvasElement['getContext']
}
