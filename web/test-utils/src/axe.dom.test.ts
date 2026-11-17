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
const MENU_ID = 'axe-jsdom-fixture-menu'

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

/**
 * A MUI Menu's shape (a Modal root outside the dialog and drawer
 * families), carrying a control whose visible text differs from its
 * accessible name. Label in Name cannot be decided without canvas text
 * measurement, which jsdom lacks, so it reports incomplete.
 */
function buildOpenMenu(): void {
  const background = document.createElement('div')
  background.id = BACKGROUND_ID
  background.setAttribute('aria-hidden', 'true')
  // aria-hidden-focus only has a question to leave incomplete when the
  // aria-hidden page holds something focusable.
  const backgroundButton = document.createElement('button')
  backgroundButton.type = 'button'
  backgroundButton.textContent = 'Background action'
  background.append(backgroundButton)
  document.body.append(background)

  const root = document.createElement('div')
  root.id = MENU_ID
  root.className = 'MuiModal-root MuiMenu-root'
  const backdrop = document.createElement('div')
  backdrop.className = 'MuiBackdrop-root'
  backdrop.style.position = 'fixed'
  backdrop.style.inset = '0'
  const paper = document.createElement('div')
  paper.className = 'MuiMenu-paper'
  const copy = document.createElement('button')
  copy.type = 'button'
  copy.setAttribute('aria-label', 'Copy setup key')
  copy.textContent = 'Copy'
  paper.append(copy)
  root.append(backdrop, paper)
  document.body.append(root)
}

/**
 * An open MUI Dialog whose paper carries a control whose visible text
 * differs from its accessible name, over an aria-hidden background that
 * holds a focusable button. Two rules are indeterminate here. The visible
 * modal makes axe's own modal probe answer yes, so aria-hidden-focus
 * cannot decide whether Tab reaches the background button; Label in Name
 * cannot be measured without canvas. A decision naming one of them must
 * silence only that rule: silencing every indeterminate rule would hide
 * the other.
 */
function buildOpenDialogWithMismatch(): void {
  const background = document.createElement('div')
  background.id = BACKGROUND_ID
  background.setAttribute('aria-hidden', 'true')
  const backgroundButton = document.createElement('button')
  backgroundButton.type = 'button'
  backgroundButton.textContent = 'Background action'
  background.append(backgroundButton)
  document.body.append(background)

  const root = document.createElement('div')
  root.id = DIALOG_ID
  root.className = 'MuiModal-root MuiDialog-root'
  const paper = document.createElement('div')
  paper.className = 'MuiDialog-paper'
  paper.setAttribute('role', 'dialog')
  paper.setAttribute('aria-modal', 'true')
  paper.setAttribute('aria-label', 'Fixture dialog')
  const copy = document.createElement('button')
  copy.type = 'button'
  copy.setAttribute('aria-label', 'Copy setup key')
  copy.textContent = 'Copy'
  paper.append(copy)
  root.append(paper)
  document.body.append(root)
}

afterEach(() => {
  document.getElementById(BACKGROUND_ID)?.remove()
  document.getElementById(DIALOG_ID)?.remove()
  document.getElementById(MENU_ID)?.remove()
  delete (globalThis as { __speedAxeKeyboardDriver?: () => Promise<void> })
    .__speedAxeKeyboardDriver
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

  it('reports a rule other than the one a jsdom case declares', async () => {
    // The declaration silences only the rules its registry entry names.
    // Here it names aria-hidden-focus, so the open dialog's Label in Name
    // indeterminate result must still be reported; a helper that silenced
    // every indeterminate rule would hide it.
    buildOpenDialogWithMismatch()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    try {
      await expect(
        expectNoAxeViolations({ decidedInBrowser: 'accountsDetailPanel' }),
      ).resolves.toBeUndefined()
      expect(warn).toHaveBeenCalledWith(
        expect.stringContaining('label-content-name-mismatch'),
      )
      expect(warn).not.toHaveBeenCalledWith(
        expect.stringContaining('aria-hidden-focus'),
      )
    } finally {
      warn.mockRestore()
    }
  })

  it('fails a browser-tier declaration whose rule stays incomplete', async () => {
    // The browser tier identifies itself through the real-keyboard driver
    // the setup installs; injecting one runs the declaration check here.
    // Label in Name stays incomplete in this scan, so the case the
    // declaration names did not decide it, and the scan must fail rather
    // than accept the exemption.
    buildOpenMenu()
    const host = globalThis as {
      __speedAxeKeyboardDriver?: () => Promise<void>
    }
    host.__speedAxeKeyboardDriver = async () => undefined
    await expect(
      expectNoAxeViolations({ decidedInBrowser: 'totpManualDisclosure' }),
    ).rejects.toThrow(/left it incomplete/)
  })
})
