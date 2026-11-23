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
const TOOLTIP_ID = 'axe-jsdom-fixture-tooltip'
const NESTED_HOST_ID = 'axe-jsdom-fixture-nested-host'

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

/**
 * A MUI Popper's portal root: a body child outside the open overlay carrying
 * `role="tooltip"`. With `focusable` it also holds a control, which makes it
 * interactive content the page behind the modal must not lose.
 */
function buildTooltipPortal(focusable: boolean): void {
  const portal = document.createElement('div')
  portal.id = TOOLTIP_ID
  portal.setAttribute('role', 'tooltip')
  if (focusable) {
    const action = document.createElement('button')
    action.type = 'button'
    action.textContent = 'Tooltip action'
    portal.append(action)
  } else {
    portal.textContent = 'Saved.'
  }
  document.body.append(portal)
}

/**
 * A role="tooltip" portal that is NOT a direct body child: it shares a
 * body-child host with the open dialog, so it sits inside the overlay's
 * ancestor chain rather than beside it. The exemption is only for a body
 * portal, so this nested one must still be read as background content.
 */
function buildNestedTooltipPortal(): void {
  const host = document.createElement('div')
  host.id = NESTED_HOST_ID

  const root = document.createElement('div')
  root.id = DIALOG_ID
  root.className = 'MuiModal-root MuiDialog-root'
  const paper = document.createElement('div')
  paper.className = 'MuiDialog-paper'
  paper.setAttribute('role', 'dialog')
  paper.setAttribute('aria-modal', 'true')
  paper.setAttribute('aria-label', 'Fixture dialog')
  root.append(paper)

  const portal = document.createElement('div')
  portal.id = TOOLTIP_ID
  portal.setAttribute('role', 'tooltip')
  portal.textContent = 'Saved.'

  host.append(root, portal)
  document.body.append(host)
}

/**
 * A body-child tooltip portal whose root itself carries `tabindex`: it is
 * keyboard-focusable without any descendant, so the portal is interactive
 * content rather than inert decoration.
 */
function buildTooltipPortalFocusableRoot(): void {
  const portal = document.createElement('div')
  portal.id = TOOLTIP_ID
  portal.setAttribute('role', 'tooltip')
  portal.setAttribute('tabindex', '0')
  portal.textContent = 'Saved.'
  document.body.append(portal)
}

/**
 * A body-child tooltip portal holding a control that is aria-hidden but
 * stays in the tab order: aria-hidden removes it from the accessibility
 * tree without making it unreachable by keyboard.
 */
function buildTooltipPortalWithHiddenControl(): void {
  const portal = document.createElement('div')
  portal.id = TOOLTIP_ID
  portal.setAttribute('role', 'tooltip')
  const action = document.createElement('button')
  action.type = 'button'
  action.setAttribute('aria-hidden', 'true')
  action.textContent = 'Tooltip action'
  portal.append(action)
  document.body.append(portal)
}

afterEach(() => {
  document.getElementById(BACKGROUND_ID)?.remove()
  document.getElementById(DIALOG_ID)?.remove()
  document.getElementById(MENU_ID)?.remove()
  document.getElementById(TOOLTIP_ID)?.remove()
  document.getElementById(NESTED_HOST_ID)?.remove()
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

  it('exempts a tooltip portal that carries no focusable control', async () => {
    // MUI's Popper root carries role="tooltip" and is portaled to the body,
    // outside the open modal's aria-hidden chain. A tooltip with nothing to
    // focus is not content a reader can reach behind the modal, so the
    // background check must not read it as page content.
    buildOpenDialog()
    buildTooltipPortal(false)
    await expect(expectNoAxeViolations()).resolves.toBeUndefined()
  })

  it('refuses a tooltip portal that carries a focusable control', async () => {
    // Only the role must not be enough: a portal that holds a real control
    // is interactive content left readable outside the modal, and the
    // background check fails instead of exempting it.
    buildOpenDialog()
    buildTooltipPortal(true)
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /background is not aria-hidden/,
    )
  })

  it('does not exempt a tooltip portal nested inside the overlay ancestor chain', async () => {
    // The exemption is for a body portal, which is the shape MUI's own
    // Popper produces. A role="tooltip" node nested beside the overlay
    // inside a shared body-child host is page content the walk reaches
    // through an ancestor, not a body portal, so the check must fail on
    // it rather than let the role exempt it at any depth.
    buildNestedTooltipPortal()
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /background is not aria-hidden/,
    )
  })

  it('refuses a tooltip portal whose root itself is focusable', async () => {
    // A portal root carrying tabindex is focusable with no descendant at
    // all. Counting descendants alone would exempt it as inert.
    buildOpenDialog()
    buildTooltipPortalFocusableRoot()
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /background is not aria-hidden/,
    )
  })

  it('refuses a tooltip portal whose control is aria-hidden but still keyboard-focusable', async () => {
    // aria-hidden takes a control out of the accessibility tree without
    // removing it from the tab order, so the portal still holds content a
    // keyboard can reach and must not be exempted.
    buildOpenDialog()
    buildTooltipPortalWithHiddenControl()
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /background is not aria-hidden/,
    )
  })
})
