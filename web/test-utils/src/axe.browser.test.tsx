/**
 * axe.browser.test.tsx -- the aria-hidden-focus decision only a browser
 * can make.
 *
 * When an aria-modal dialog is open, axe leaves aria-hidden-focus
 * incomplete: its static analysis cannot tell whether the dialog's focus
 * trap keeps the aria-hidden background out of the tab order. The
 * harness answers that question with real key presses (see axe.ts), so
 * this suite pins the two outcomes that decision can reach with fixtures
 * built for the purpose:
 *
 * - a dialog whose keyboard focus is trapped is determined, and the scan
 *   prints no aria-hidden-focus report;
 * - a dialog that does not trap focus escapes to the aria-hidden
 *   background on Tab, and the scan fails naming the escape. Without
 *   this case the passing one could be vacuous: a harness that never
 *   exercised the trap would pass both;
 * - an open dialog whose background is not aria-hidden fails the
 *   page-context exemption rather than having the page rules skipped
 *   silently.
 *
 * The fixtures are plain DOM rather than a rendered MUI Dialog so the
 * two outcomes differ only in whether a trap is present. MUI's marker
 * classes on the root are what the harness uses to recognize a dialog.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { expectNoAxeViolations } from './axe.js'

const BACKGROUND_ID = 'axe-fixture-background'
const DIALOG_ID = 'axe-fixture-dialog'

function tabbables(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>('button, [href], [tabindex]')]
    .filter((element) => element.tabIndex >= 0)
}

function buildOpenDialog(trapFocus: boolean): void {
  const background = document.createElement('div')
  background.id = BACKGROUND_ID
  // MUI hides the page behind an open Modal this way; the harness's
  // page-context exemption is allowed only after it finds this.
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
  paper.tabIndex = -1
  const first = document.createElement('button')
  first.type = 'button'
  first.textContent = 'First'
  const second = document.createElement('button')
  second.type = 'button'
  second.textContent = 'Second'
  paper.append(first, second)
  root.append(paper)

  if (trapFocus) {
    root.addEventListener('keydown', (event) => {
      if (event.key !== 'Tab') {
        return
      }
      const order = tabbables(root)
      const firstTabbable = order[0]
      const lastTabbable = order[order.length - 1]
      if (event.shiftKey && document.activeElement === firstTabbable) {
        event.preventDefault()
        lastTabbable.focus()
      } else if (!event.shiftKey && document.activeElement === lastTabbable) {
        event.preventDefault()
        firstTabbable.focus()
      }
    })
  }

  document.body.append(background, root)
  first.focus()
}

afterEach(() => {
  document.getElementById(BACKGROUND_ID)?.remove()
  document.getElementById(DIALOG_ID)?.remove()
})

describe('aria-hidden-focus under an open aria-modal dialog', () => {
  it('is determined by a trap that holds, and prints no report', async () => {
    buildOpenDialog(true)
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
    buildOpenDialog(false)
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /focus trap does not hold/,
    )
  })

  it('fails the scan when the open overlay leaves its background readable', async () => {
    buildOpenDialog(true)
    document.getElementById(BACKGROUND_ID)?.removeAttribute('aria-hidden')
    await expect(expectNoAxeViolations()).rejects.toThrow(
      /page-context exemption refused/,
    )
  })
})
