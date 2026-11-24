import { afterEach, describe, expect, it, vi } from 'vitest'
import { expectNoAxeViolations } from './axe.js'

const BACKGROUND_ID = 'axe-fixture-background'
const DIALOG_ID = 'axe-fixture-dialog'
const MENU_ID = 'axe-fixture-menu'

interface BackgroundOptions {
  readonly ariaHidden?: boolean
  readonly inert?: boolean
}

function buildBackground({
  ariaHidden = true,
  inert = true,
}: BackgroundOptions = {}): void {
  const background = document.createElement('div')
  background.id = BACKGROUND_ID
  if (ariaHidden) {
    background.setAttribute('aria-hidden', 'true')
  }
  if (inert) {
    background.setAttribute('inert', '')
  }
  const backgroundButton = document.createElement('button')
  backgroundButton.type = 'button'
  backgroundButton.textContent = 'Background action'
  background.append(backgroundButton)
  document.body.append(background)
}

function buildOpenDialog(options: BackgroundOptions = {}): void {
  buildBackground(options)

  const root = document.createElement('div')
  root.id = DIALOG_ID
  root.className = 'MuiModal-root MuiDialog-root'
  const paper = document.createElement('div')
  paper.className = 'MuiDialog-paper'
  paper.setAttribute('role', 'dialog')
  paper.setAttribute('aria-modal', 'true')
  paper.setAttribute('aria-label', 'Fixture dialog')
  paper.tabIndex = -1
  const first = document.createElement('button')
  first.type = 'button'
  first.textContent = 'First'
  const second = document.createElement('button')
  second.type = 'button'
  second.textContent = 'Second'
  paper.append(first, second)
  root.append(paper)

  document.body.append(root)
  first.focus()
}

function buildOpenMenu(options: BackgroundOptions = {}): void {
  buildBackground(options)

  const root = document.createElement('div')
  root.id = MENU_ID
  root.className = 'MuiModal-root MuiMenu-root'
  const backdrop = document.createElement('div')
  backdrop.className = 'MuiBackdrop-root'
  backdrop.style.position = 'fixed'
  backdrop.style.inset = '0'
  backdrop.style.background = 'transparent'
  const paper = document.createElement('div')
  paper.className = 'MuiMenu-paper'
  paper.setAttribute('role', 'menu')
  paper.setAttribute('aria-label', 'Fixture menu')
  paper.tabIndex = -1
  const item = document.createElement('button')
  item.type = 'button'
  item.setAttribute('role', 'menuitem')
  item.textContent = 'First item'
  paper.append(item)
  root.append(backdrop, paper)

  document.body.append(root)
}

function buildExitingDialog(): void {
  const background = document.createElement('div')
  background.id = BACKGROUND_ID
  background.setAttribute('aria-hidden', 'true')
  document.body.append(background)

  const root = document.createElement('div')
  root.id = DIALOG_ID
  root.className = 'MuiModal-root MuiDialog-root'
  const sentinelStart = document.createElement('div')
  sentinelStart.setAttribute('data-testid', 'sentinelStart')
  sentinelStart.setAttribute('tabindex', '-1')
  const paper = document.createElement('div')
  paper.className = 'MuiDialog-paper'
  paper.setAttribute('role', 'dialog')
  paper.setAttribute('aria-modal', 'true')
  paper.setAttribute('aria-label', 'Fixture dialog')
  const sentinelEnd = document.createElement('div')
  sentinelEnd.setAttribute('data-testid', 'sentinelEnd')
  sentinelEnd.setAttribute('tabindex', '-1')
  root.append(sentinelStart, paper, sentinelEnd)
  document.body.append(root)
}

afterEach(() => {
  document.getElementById(BACKGROUND_ID)?.remove()
  document.getElementById(DIALOG_ID)?.remove()
  document.getElementById(MENU_ID)?.remove()
})

describe('an inert background under an open dialog in Chromium', () => {
  it('fails the scan when the background is aria-hidden but not inert', async () => {
    buildOpenDialog({ inert: false })
    await expect(expectNoAxeViolations()).rejects.toThrow(/Without inert/)
  })

  it('passes when the background is both aria-hidden and inert', async () => {
    buildOpenDialog()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    try {
      await expect(expectNoAxeViolations()).resolves.toBeUndefined()
      expect(warn).not.toHaveBeenCalledWith(
        expect.stringContaining('aria-hidden-focus'),
      )
    } finally {
      warn.mockRestore()
    }
  })

  it('fails the scan when the background is inert but not aria-hidden', async () => {
    buildOpenDialog({ ariaHidden: false })
    await expect(expectNoAxeViolations()).rejects.toThrow(/Without aria-hidden/)
  })
})

describe('a modal still exiting in Chromium', () => {
  it('is not asserted as an open overlay over its aria-hidden background', async () => {
    buildExitingDialog()
    await expect(expectNoAxeViolations()).resolves.toBeUndefined()
  })
})

describe('an inert background under an open menu in Chromium', () => {
  it('fails the scan when the background is aria-hidden but not inert', async () => {
    buildOpenMenu({ inert: false })
    await expect(expectNoAxeViolations()).rejects.toThrow(/Without inert/)
  })

  it('passes when the background is both aria-hidden and inert', async () => {
    buildOpenMenu()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    try {
      await expect(expectNoAxeViolations()).resolves.toBeUndefined()
      expect(warn).not.toHaveBeenCalledWith(
        expect.stringContaining('aria-hidden-focus'),
      )
    } finally {
      warn.mockRestore()
    }
  })
})
