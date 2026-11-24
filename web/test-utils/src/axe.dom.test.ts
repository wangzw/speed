// @vitest-environment jsdom
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

function buildOpenMenu(): void {
  const background = document.createElement('div')
  background.id = BACKGROUND_ID
  background.setAttribute('aria-hidden', 'true')
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

function buildTooltipPortalFocusableRoot(): void {
  const portal = document.createElement('div')
  portal.id = TOOLTIP_ID
  portal.setAttribute('role', 'tooltip')
  portal.setAttribute('tabindex', '0')
  portal.textContent = 'Saved.'
  document.body.append(portal)
}

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
    await expect(expectNoAxeViolations()).resolves.toBeUndefined()
    expect(warn).not.toHaveBeenCalledWith(
      expect.stringContaining('real keyboard'),
    )
  })

  it('reports a rule other than the one a jsdom case declares', async () => {
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
    buildOpenDialog()
    buildTooltipPortal(false)
    await expect(expectNoAxeViolations()).resolves.toBeUndefined()
  })

  it('refuses a tooltip portal that carries a focusable control', async () => {
    buildOpenDialog()
    buildTooltipPortal(true)
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /background is not aria-hidden/,
    )
  })

  it('does not exempt a tooltip portal nested inside the overlay ancestor chain', async () => {
    buildNestedTooltipPortal()
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /background is not aria-hidden/,
    )
  })

  it('refuses a tooltip portal whose root itself is focusable', async () => {
    buildOpenDialog()
    buildTooltipPortalFocusableRoot()
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /background is not aria-hidden/,
    )
  })

  it('refuses a tooltip portal whose control is aria-hidden but still keyboard-focusable', async () => {
    buildOpenDialog()
    buildTooltipPortalWithHiddenControl()
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /background is not aria-hidden/,
    )
  })
})
