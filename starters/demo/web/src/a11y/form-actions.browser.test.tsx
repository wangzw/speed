/**
 * The account pages' form geometry in Chromium.
 *
 * Every form on the profile and security panes puts its submit on a line of
 * its own below the fields, with the action row closing on the container's
 * content right edge. The rule binds the forms the page renders at rest and
 * the ones a reader opens: the change-address entry, the phone entry, the
 * password form, the identifier add face, the enrollment name form and its
 * wizard steps, and the factor row's rename editor.
 *
 * jsdom lays nothing out, so only the browser tier can tell an action row
 * below a field from one beside it, or a right-aligned row from a
 * shrink-wrapped one. The case walks the two routes in the populated state,
 * opens every form whose trigger a reader can reach, and measures each form
 * it finds. The rename editor is the discriminating case: the one-line
 * editor puts its submit beside the name field, which fails the
 * below-the-fields assertion on every viewport.
 */

import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { page } from 'vitest/browser'
import { accountUiResources } from '@speed/account-ui'
import type {
  AuthSession,
  AuthnFlowState,
  AuthnMethodList,
} from '@speed/auth-core'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from '../test-utils/session.js'
import { createBackend } from './backends.js'
import { settle, staffSession } from './route-table.js'

/** The account-ui bundle the routes render in en-US. */
const accountEnUS = accountUiResources['en-US'] as {
  readonly verification: {
    readonly change: string
    readonly changeLabel: string
    readonly codeLabel: string
  }
  readonly phone: {
    readonly phone: { readonly label: string }
  }
  readonly password: { readonly current: { readonly label: string } }
  readonly identifiers: {
    readonly add: {
      readonly value: { readonly labelEmail: string }
    }
  }
  readonly mfa: {
    readonly actionsAria: string
    readonly rename: string
    readonly renameLabel: string
    readonly enroll: {
      readonly labelField: string
      readonly confirmLabel: string
      readonly cancelLabel: string
    }
  }
  readonly flowStep: { readonly codeLabel: string }
}

/** The factor label the populated backend answers. */
const FACTOR_LABEL = 'Authenticator'

/** The one method document that makes the enrollment entry reachable. */
const ENROLL_METHODS: AuthnMethodList = {
  methods: [{ name: 'password', kind: 'form' }],
  signup: { enabled: false, password: { enabled: false } },
  mfa: { factors: ['totp'] },
  human: { required: false, provider: 'off' },
  password_reset: { enabled: false, methods: [] },
}

/**
 * The test session with a reachable enrollment: the discovery document
 * reports the authenticator app, and the flow answers one challenge step
 * before settling. Without the override the demo session offers no factor
 * kind, so the group's add action never renders.
 */
function enrollmentSession(): AuthSession {
  const base = createTestSession(authenticatedSnapshot())
  return {
    ...base,
    listMethods: () => Promise.resolve(ENROLL_METHODS),
    startAuthenticatedFlow: () =>
      Promise.resolve({
        seq: 1,
        state: 'enroll',
        next: {
          kind: 'challenge',
          fields: [{ name: 'code', kind: 'code' }],
        },
      } as unknown as AuthnFlowState),
    answerFlow: () =>
      Promise.resolve({
        seq: 2,
        state: 'ready',
        next: { kind: 'complete' },
      } as unknown as AuthnFlowState),
  }
}

/** The visible field controls inside one form, in document order. */
function visibleFields(form: HTMLFormElement): HTMLElement[] {
  return Array.from(
    form.querySelectorAll<HTMLElement>('input, textarea, select'),
  ).filter((field) => {
    if (field instanceof HTMLInputElement && field.type === 'hidden') {
      return false
    }
    const box = field.getBoundingClientRect()
    return box.width > 0 && box.height > 0
  })
}

/** A box's right edge minus its own right padding. */
function contentRight(element: Element): number {
  const box = element.getBoundingClientRect()
  const padding = Number.parseFloat(getComputedStyle(element).paddingRight)
  return box.right - (Number.isNaN(padding) ? 0 : padding)
}

/**
 * One form's submit sits below its fields and closes on the content edge.
 * A form with no submit control -- the profile section's inline field
 * commits on blur and Enter -- has no action row to place and is skipped.
 */
function expectSubmitPlacement(form: HTMLFormElement): void {
  const submit = form.querySelector<HTMLElement>(
    'button[type="submit"], input[type="submit"]',
  )
  if (submit === null) {
    return
  }
  const fields = visibleFields(form)
  expect(fields.length, 'the form has no visible field').toBeGreaterThan(0)

  const submitBox = submit.getBoundingClientRect()
  const fieldsBottom = Math.max(
    ...fields.map((field) => field.getBoundingClientRect().bottom),
  )
  expect(
    submitBox.top,
    'the submit starts above the fields instead of below them',
  ).toBeGreaterThanOrEqual(fieldsBottom - 0.5)
  expect(
    Math.abs(submitBox.right - contentRight(form)),
    'the submit does not close on the form content right edge',
  ).toBeLessThanOrEqual(1)
}

/** Every form in the main landmark obeys the action-row placement. */
function expectEveryFormPlaced(): void {
  const main = screen.getByRole('main')
  const forms = Array.from(main.querySelectorAll<HTMLFormElement>('form'))
  expect(forms.length).toBeGreaterThan(0)
  for (const form of forms) {
    expectSubmitPlacement(form)
  }
}

describe('account form action rows in Chromium', () => {
  it('places every form submit on the profile pane', async () => {
    await page.viewport(1280, 900)
    bindRequestFn(createBackend('data'))
    window.history.replaceState({}, '', '/account/profile')
    renderDemoApp({ session: staffSession() })
    await settle()
    const user = userEvent.setup()

    // The pending verification state renders the code form at rest.
    await screen.findByLabelText(accountEnUS.verification.codeLabel)
    expectEveryFormPlaced()

    // The change-address entry is its own form.
    await user.click(
      screen.getByRole('button', { name: accountEnUS.verification.change }),
    )
    await screen.findByLabelText(accountEnUS.verification.changeLabel)
    expectEveryFormPlaced()

    // The phone entry is the profile's third form, opened from its block.
    await user.click(
      screen.getByRole('button', { name: demoEnUS.account.action.add }),
    )
    await screen.findByLabelText(accountEnUS.phone.phone.label, {
      selector: 'input',
    })
    expectEveryFormPlaced()
  })

  it('places the profile add form submit with the address field', async () => {
    await page.viewport(1280, 900)
    bindRequestFn(createBackend('empty'))
    window.history.replaceState({}, '', '/account/profile')
    renderDemoApp({ session: staffSession() })
    await settle()
    const user = userEvent.setup()

    // An account with no pending verification reaches the add face from the
    // email block's own action.
    const emailBlock = await screen.findByRole('region', {
      name: demoEnUS.account.rows.label.email,
    })
    await user.click(
      within(emailBlock).getByRole('button', {
        name: demoEnUS.account.action.add,
      }),
    )
    await screen.findByLabelText(accountEnUS.identifiers.add.value.labelEmail)
    expectEveryFormPlaced()
  })

  it('places every form submit on the security pane, rename editor included', async () => {
    await page.viewport(1280, 900)
    bindRequestFn(createBackend('data'))
    window.history.replaceState({}, '', '/account/security')
    renderDemoApp({ session: enrollmentSession() })
    await settle()
    const user = userEvent.setup()

    // The password form lives in an expandable row.
    await user.click(await screen.findByTestId('security-row-password'))
    await screen.findByLabelText(accountEnUS.password.current.label)
    expectEveryFormPlaced()

    // The enrollment name form starts the wizard.
    await user.click(screen.getByTestId('mfa-add-totp'))
    await screen.findByLabelText(accountEnUS.mfa.enroll.labelField)
    expectEveryFormPlaced()

    // The wizard's first flow step is a form of its own.
    await user.click(
      screen.getByRole('button', { name: accountEnUS.mfa.enroll.confirmLabel }),
    )
    await screen.findByLabelText(accountEnUS.flowStep.codeLabel)
    expectEveryFormPlaced()

    // Closing the wizard restores the factor row, whose menu opens the
    // rename editor.
    await user.click(
      screen.getByRole('button', { name: accountEnUS.mfa.enroll.cancelLabel }),
    )
    await waitFor(() =>
      expect(screen.getByTestId('mfa-add-totp')).toBeTruthy(),
    )
    await user.click(
      screen.getByRole('button', {
        name: accountEnUS.mfa.actionsAria.replace('{{factor}}', FACTOR_LABEL),
      }),
    )
    await user.click(
      await screen.findByRole('menuitem', { name: accountEnUS.mfa.rename }),
    )
    await screen.findByLabelText(accountEnUS.mfa.renameLabel)
    expectEveryFormPlaced()
  })
})
