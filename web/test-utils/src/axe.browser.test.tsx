/**
 * axe.browser.test.tsx -- the aria-hidden-focus decision only a browser
 * can make, and the report a browser cannot decide in the same pass.
 *
 * When an aria-modal dialog is open, axe leaves aria-hidden-focus
 * incomplete: its static analysis cannot tell whether the dialog's focus
 * trap keeps the aria-hidden background out of the tab order. The
 * harness answers that question with real key presses (see axe.ts), so
 * this suite pins the outcomes that decision can reach with fixtures
 * built for the purpose:
 *
 * - a dialog whose keyboard focus is trapped is determined, and the scan
 *   prints no aria-hidden-focus report;
 * - a dialog that does not trap focus escapes to the aria-hidden
 *   background on Tab, and the scan fails naming the escape. Without
 *   this case the passing one could be vacuous: a harness that never
 *   exercised the trap would pass both;
 * - a dialog that traps forward Tab only lets Shift+Tab escape, and the
 *   scan fails naming that direction. Without a fixture that escapes
 *   one way only, a harness that pressed just one of the two directions
 *   would still pass the two cases above;
 * - an open dialog whose background is not aria-hidden fails the
 *   page-context exemption rather than having the page rules skipped
 *   silently;
 * - an open Modal-based overlay outside the dialog and drawer families
 *   (a menu, whose paper is not aria-modal) is reported with the reason
 *   its focus trap was not exercised, and does not fail the scan.
 *
 * The fixtures are plain DOM rather than a rendered MUI component so the
 * outcomes differ only in the one property under test. MUI's marker
 * classes on the root are what the harness uses to recognize each family.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { expectNoAxeViolations } from './axe.js'

const BACKGROUND_ID = 'axe-fixture-background'
const DIALOG_ID = 'axe-fixture-dialog'
const MENU_ID = 'axe-fixture-menu'

function tabbables(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>('button, [href], [tabindex]')]
    .filter((element) => element.tabIndex >= 0)
}

/**
 * Which Tab directions the fixture dialog's own trap intercepts. A trap
 * that holds every direction decides the rule; one that misses a
 * direction lets focus escape that way.
 */
type TrapDirections = 'both' | 'forward' | 'none'

function trapTab(root: HTMLElement, directions: TrapDirections): void {
  if (directions === 'none') {
    return
  }
  root.addEventListener('keydown', (event) => {
    if (event.key !== 'Tab') {
      return
    }
    const order = tabbables(root)
    const firstTabbable = order[0]
    const lastTabbable = order[order.length - 1]
    if (
      event.shiftKey &&
      directions === 'both' &&
      document.activeElement === firstTabbable
    ) {
      event.preventDefault()
      lastTabbable.focus()
    } else if (!event.shiftKey && document.activeElement === lastTabbable) {
      event.preventDefault()
      firstTabbable.focus()
    }
  })
}

function buildBackground(): void {
  const background = document.createElement('div')
  background.id = BACKGROUND_ID
  // MUI hides the page behind an open Modal this way; the harness's
  // page-context exemption is allowed only after it finds this.
  background.setAttribute('aria-hidden', 'true')
  const backgroundButton = document.createElement('button')
  backgroundButton.type = 'button'
  backgroundButton.textContent = 'Background action'
  background.append(backgroundButton)
  document.body.append(background)
}

function buildOpenDialog(directions: TrapDirections): void {
  buildBackground()

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

  trapTab(root, directions)

  document.body.append(root)
  first.focus()
}

/**
 * A MUI Menu's shape: a Modal root that is not a dialog or a drawer, a
 * full-viewport backdrop, and a paper with no aria-modal. The backdrop is
 * what a real MUI Modal renders and what makes axe's probe reach the
 * menu as a modal, so the rule is incomplete rather than violated -- the
 * state the harness reports with its reason.
 */
function buildOpenMenu(): void {
  buildBackground()

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

afterEach(() => {
  document.getElementById(BACKGROUND_ID)?.remove()
  document.getElementById(DIALOG_ID)?.remove()
  document.getElementById(MENU_ID)?.remove()
})

describe('aria-hidden-focus under an open aria-modal dialog', () => {
  it('is determined by a trap that holds, and prints no report', async () => {
    buildOpenDialog('both')
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

  it('fails the scan when Tab escapes to the aria-hidden background', async () => {
    buildOpenDialog('none')
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /focus trap does not hold/,
    )
  })

  it('fails the scan when only Shift+Tab escapes to the aria-hidden background', async () => {
    // Forward Tab cycles inside the dialog; Shift+Tab from the first
    // tabbable walks back out to the background. A harness that pressed
    // Tab only would pass this fixture, so the case pins that direction.
    buildOpenDialog('forward')
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /Shift\+Tab moved focus to .*outside the overlay/,
    )
  })

  it('fails the scan when the open overlay leaves its background readable', async () => {
    buildOpenDialog('both')
    document.getElementById(BACKGROUND_ID)?.removeAttribute('aria-hidden')
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /page-context exemption refused/,
    )
  })
})

describe('aria-hidden-focus under an open menu', () => {
  it('reports the unexercised trap with the reason, and does not fail the scan', async () => {
    buildOpenMenu()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    try {
      await expect(expectNoAxeViolations()).resolves.toBeUndefined()
      expect(warn).toHaveBeenCalledWith(
        expect.stringContaining(
          'the open overlay is not an aria-modal dialog or drawer, so its focus trap was not exercised',
        ),
      )
    } finally {
      warn.mockRestore()
    }
  })
})
