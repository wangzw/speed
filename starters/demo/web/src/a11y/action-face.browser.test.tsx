/**
 * The account pages' action face in Chromium.
 *
 * The account sections share one action style: a text-bearing control that
 * does something to the page's data -- add, change, enable, disable -- renders
 * as an outlined small button. A single control that falls back to the
 * platform's text button reads as a different class of action beside its
 * neighbours. This case walks the two pages that carry such controls and
 * fails when one of them leaves the shared face.
 *
 * Scope is the page's main landmark, never the shell chrome. Four kinds of
 * control are out of scope because they are not this kind of action:
 *
 *  - an icon-only control (the overflow "Actions for ..." buttons) has no
 *    readable text to compare;
 *  - a menu item is a menu, not a button face;
 *  - a form's own actions (the submit and the cancel/resend beside it) are
 *    the form's, and are excluded by living inside its `<form>`;
 *  - an empty/error placeholder's retry is centred in the placeholder, not a
 *    row action.
 */

import { screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { page } from 'vitest/browser'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { setCurrentTenant, setTenantNames } from '../chrome.js'
import { renderDemoApp } from '../test-utils/render.js'
import { createBackend } from './backends.js'
import {
  MODES,
  SCAN_TENANT_ID,
  settle,
  staffSession,
} from './route-table.js'

/** The account pages whose bodies carry row and group actions. */
const ACCOUNT_PAGES = ['/account/profile', '/account/security'] as const

/** True when the control sits inside a centred placeholder block. */
function insideCentredPlaceholder(
  control: Element,
  scope: Element,
): boolean {
  for (
    let node = control.parentElement;
    node !== null && node !== scope;
    node = node.parentElement
  ) {
    const style = getComputedStyle(node)
    if (
      style.display === 'flex' &&
      style.flexDirection === 'column' &&
      style.alignItems === 'center' &&
      style.textAlign === 'center'
    ) {
      return true
    }
  }
  return false
}

/** True when a control is one of the page's text-bearing row actions. */
function isPageAction(control: Element, scope: Element): boolean {
  if ((control.textContent ?? '').trim() === '') {
    return false
  }
  if (control.getAttribute('role') === 'menuitem') {
    return false
  }
  if (control.getAttribute('type') === 'submit') {
    return false
  }
  if (control.closest('form') !== null) {
    return false
  }
  return !insideCentredPlaceholder(control, scope)
}

beforeEach(async () => {
  await page.viewport(1280, 900)
  setCurrentTenant(SCAN_TENANT_ID)
  setTenantNames(new Map([[SCAN_TENANT_ID, SCAN_TENANT_ID]]))
})

afterEach(() => {
  setCurrentTenant(null)
  setTenantNames(new Map())
})

describe('account page action face in Chromium', () => {
  for (const path of ACCOUNT_PAGES) {
    for (const mode of MODES) {
      it(`${mode}: ${path} renders every page action outlined`, async () => {
        bindRequestFn(createBackend(mode))
        window.history.replaceState({}, '', path)
        renderDemoApp({ session: staffSession() })
        await settle()
        const main = screen.getByRole('main')
        const offenders = Array.from(main.querySelectorAll('button'))
          .filter((control) => isPageAction(control, main))
          .filter(
            (control) => !control.classList.contains('MuiButton-outlined'),
          )
          .map((control) => (control.textContent ?? '').trim())
        expect(
          offenders,
          `route ${path} [${mode}] has page actions outside the outlined face`,
        ).toEqual([])
      })
    }
  }
})
