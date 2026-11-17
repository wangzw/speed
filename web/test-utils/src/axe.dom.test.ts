// @vitest-environment jsdom
/**
 * axe.dom.test.ts -- the jsdom tier's side of the environment decision.
 *
 * The harness decides which tier it runs in from the real-keyboard driver
 * the browser setup installs (see axe.ts), not from the user agent. A
 * jsdom case can set the user agent to a browser's for its own subject --
 * the enrollment panel suggests a device name from it -- and that must
 * not make the harness ask for keys this environment cannot press. The
 * dialog below is the state that would take the real-keyboard path if the
 * decision were made from the user agent: it is open, aria-modal, and
 * focus-trapped by nothing.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { expectNoAxeViolations, isBrowserTier } from './axe.js'

const BACKGROUND_ID = 'axe-jsdom-fixture-background'
const DIALOG_ID = 'axe-jsdom-fixture-dialog'

const CHROMIUM_UA =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 ' +
  '(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'

const userAgentDescriptor = Object.getOwnPropertyDescriptor(
  window.navigator,
  'userAgent',
)

function stubUserAgent(ua: string): void {
  Object.defineProperty(window.navigator, 'userAgent', {
    value: ua,
    configurable: true,
  })
}

function buildOpenDialog(): void {
  const background = document.createElement('div')
  background.id = BACKGROUND_ID
  background.setAttribute('aria-hidden', 'true')
  const backgroundButton = document.createElement('button')
  backgroundButton.type = 'button'
  backgroundButton.textContent = 'Background action'
  background.append(backgroundButton)

  const root = document.createElement('div')
  root.id = DIALOG_ID
  root.className = 'MuiModal-root MuiDialog-root'
  const paper = document.createElement('div')
  paper.className = 'MuiDialog-paper'
  paper.setAttribute('role', 'dialog')
  paper.setAttribute('aria-modal', 'true')
  paper.setAttribute('aria-label', 'Fixture dialog')
  const first = document.createElement('button')
  first.type = 'button'
  first.textContent = 'First'
  paper.append(first)
  root.append(paper)

  document.body.append(background, root)
}

afterEach(() => {
  document.getElementById(BACKGROUND_ID)?.remove()
  document.getElementById(DIALOG_ID)?.remove()
  if (userAgentDescriptor === undefined) {
    delete (window.navigator as { userAgent?: string }).userAgent
  } else {
    Object.defineProperty(window.navigator, 'userAgent', userAgentDescriptor)
  }
  vi.restoreAllMocks()
})

describe('the tier decision under jsdom', () => {
  it('stays in the jsdom tier when only the user agent names a browser', async () => {
    stubUserAgent(CHROMIUM_UA)
    expect(isBrowserTier()).toBe(false)

    buildOpenDialog()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    // The scan reports its indeterminate rule instead of rejecting for a
    // keyboard the jsdom tier does not have.
    await expect(expectNoAxeViolations()).resolves.toBeUndefined()
    expect(warn).not.toHaveBeenCalledWith(
      expect.stringContaining('real keyboard'),
    )
  })
})
