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

const ACCOUNT_PAGES = ['/account/profile', '/account/security'] as const

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
