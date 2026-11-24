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

const FACTOR_LABEL = 'Authenticator'

const ENROLL_METHODS: AuthnMethodList = {
  methods: [{ name: 'password', kind: 'form' }],
  signup: { enabled: false, password: { enabled: false } },
  mfa: { factors: ['totp', 'webauthn'] },
  human: {
    phone: { required: false, provider: 'off' },
    other: { required: false, provider: 'off' },
  },
  password_reset: { enabled: false, methods: [] },
}

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

function contentRight(element: Element): number {
  const box = element.getBoundingClientRect()
  const padding = Number.parseFloat(getComputedStyle(element).paddingRight)
  return box.right - (Number.isNaN(padding) ? 0 : padding)
}

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

    await screen.findByLabelText(accountEnUS.verification.codeLabel)
    expectEveryFormPlaced()

    await user.click(
      screen.getByRole('button', { name: accountEnUS.verification.change }),
    )
    await screen.findByLabelText(accountEnUS.verification.changeLabel)
    expectEveryFormPlaced()

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
    const base = createBackend('data')
    bindRequestFn(<T,>(url: string): Promise<T> => {
      if (url === '/api/v1/auth/factors') {
        return Promise.resolve({
          factors: [
            {
              id: 'factor-2',
              kind: 'webauthn',
              label: FACTOR_LABEL,
              status: 'active',
              created_at: null,
            },
          ],
        } as T)
      }
      return base<T>(url)
    })
    window.history.replaceState({}, '', '/account/security')
    renderDemoApp({ session: enrollmentSession() })
    await settle()
    const user = userEvent.setup()

    await user.click(await screen.findByTestId('security-row-password'))
    await screen.findByLabelText(accountEnUS.password.current.label)
    expectEveryFormPlaced()

    await user.click(screen.getByTestId('mfa-add-totp'))
    await screen.findByLabelText(accountEnUS.flowStep.codeLabel)
    expectEveryFormPlaced()

    await user.click(
      screen.getByRole('button', { name: accountEnUS.mfa.enroll.cancelLabel }),
    )
    await waitFor(() =>
      expect(screen.getByTestId('mfa-add-totp')).toBeTruthy(),
    )

    await user.click(screen.getByTestId('mfa-add-webauthn'))
    await screen.findByLabelText(accountEnUS.mfa.enroll.labelField)
    expectEveryFormPlaced()
    await user.click(
      screen.getByRole('button', { name: accountEnUS.mfa.enroll.cancelLabel }),
    )
    await waitFor(() =>
      expect(screen.getByTestId('mfa-add-webauthn')).toBeTruthy(),
    )

    const factorRow = screen
      .getAllByRole('listitem')
      .find((item) => within(item).queryByText(FACTOR_LABEL) !== null)
    expect(factorRow, 'no factor row reads as the named factor').toBeTruthy()
    await user.click(
      within(factorRow as HTMLElement).getByRole('button', {
        name: accountEnUS.mfa.rename,
      }),
    )
    const renameField = await screen.findByLabelText(accountEnUS.mfa.renameLabel)
    expectEveryFormPlaced()
    await user.clear(renameField)
    await user.type(renameField, 'Renamed authenticator')
  })
})
