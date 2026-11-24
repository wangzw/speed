/**
 * Account forms' shared action-row geometry, measured in a real browser.
 */

import Button from '@mui/material/Button'
import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { PhoneCountryDefaultProvider } from '@speed/ui-kit'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { identifier, profileBody } from '../test-utils/fixtures.js'
import {
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { ContactVerificationSection } from './ContactVerificationSection.js'
import { IdentifiersSection } from './IdentifiersSection.js'
import { MfaSection } from './MfaSection.js'
import { PasswordSection } from './PasswordSection.js'
import { PhoneSection } from './PhoneSection.js'
import { ProfileSection } from './ProfileSection.js'
import { FlowStep } from './internal/flow-step.js'

const PROFILE_PATH = '/api/v1/auth/me'
const FACTORS_PATH = '/api/v1/auth/factors'
const METHODS_PATH = '/api/v1/auth/methods'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'
const PHONE_PATH = '/api/v1/auth/phone'
const OTPAUTH_URI = 'otpauth://totp/speed:ada?issuer=speed'

const ACTION_GAP = 8

function sameTop(a: Element, b: Element): boolean {
  return Math.abs(a.getBoundingClientRect().top - b.getBoundingClientRect().top) <= 1
}

function rightOf(primary: Element, secondary: Element): boolean {
  return primary.getBoundingClientRect().left > secondary.getBoundingClientRect().right
}

function expectRightEdge(a: Element, b: Element): void {
  const aBox = a.getBoundingClientRect()
  const bBox = b.getBoundingClientRect()
  expect(Math.abs(aBox.right - bBox.right)).toBeLessThanOrEqual(1)
}

function actionRowOf(action: Element): HTMLElement {
  const row = action.closest<HTMLElement>('[data-action-row]')
  if (row === null) {
    throw new Error('the action is not inside an action row')
  }
  return row
}

function expectOneActionGroup(primary: Element, secondary: Element): void {
  const primaryBox = primary.getBoundingClientRect()
  const secondaryBox = secondary.getBoundingClientRect()
  expect(sameTop(primary, secondary)).toBe(true)
  expect(rightOf(primary, secondary)).toBe(true)
  expect(primaryBox.left - secondaryBox.right).toBeCloseTo(ACTION_GAP, 0)
  const row = actionRowOf(primary)
  expect(actionRowOf(secondary)).toBe(row)
  const rowBox = row.getBoundingClientRect()
  const contentRight =
    rowBox.right - Number.parseFloat(getComputedStyle(row).paddingRight)
  expect(Math.abs(primaryBox.right - contentRight)).toBeLessThanOrEqual(1)
}

function recordStatusHistories(): {
  readonly stop: () => void
  readonly historyOf: (node: Element) => readonly string[]
} {
  const histories = new WeakMap<Element, string[]>()
  const observer = new MutationObserver(() => {
    for (const node of document.querySelectorAll('[role="status"]')) {
      const history = histories.get(node) ?? []
      history.push(node.textContent ?? '')
      histories.set(node, history)
    }
  })
  observer.observe(document.body, {
    childList: true,
    subtree: true,
    characterData: true,
  })
  return {
    stop: () => observer.disconnect(),
    historyOf: (node) => histories.get(node) ?? [],
  }
}

async function statusWithText(message: string): Promise<Element> {
  await waitFor(() => {
    expect(
      [...document.querySelectorAll('[role="status"]')].some(
        (node) => node.textContent === message,
      ),
    ).toBe(true)
  })
  const node = [...document.querySelectorAll('[role="status"]')].find(
    (candidate) => candidate.textContent === message,
  )
  if (node === undefined) {
    throw new Error(`no status region carried ${message}`)
  }
  return node
}

describe('account-ui form geometry', () => {
  it('stacks the change-address submit beneath its field, closed on the right edge', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'pending', email: 'ada@x.test', since: null },
          }),
        )
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<ContactVerificationSection session={rig.session} />)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.verification.change }),
    )
    const field = screen.getByLabelText(zhCN.verification.changeLabel)
    const changeForm = field.closest('form')
    expect(changeForm).not.toBeNull()
    const submit = within(changeForm as HTMLElement).getByRole('button', {
      name: zhCN.verification.changeSubmit,
    })

    const fieldBox = field.getBoundingClientRect()
    const submitBox = submit.getBoundingClientRect()
    expect(submitBox.top).toBeGreaterThanOrEqual(fieldBox.bottom - 1)
    expect(submitBox.left).toBeGreaterThanOrEqual(fieldBox.left - 1)
    expect(submitBox.left).toBeLessThanOrEqual(fieldBox.right)
    expectRightEdge(submit, field)
  })

  it('ends the no-address add form\'s primary flush with its field', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'pending', email: '', since: null },
          }),
        )
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<ContactVerificationSection session={rig.session} />)

    const field = await screen.findByLabelText(zhCN.verification.addLabel)
    const submit = screen.getByRole('button', {
      name: zhCN.verification.addSubmit,
    })
    const fieldBox = field.getBoundingClientRect()
    const submitBox = submit.getBoundingClientRect()
    expect(submitBox.top).toBeGreaterThanOrEqual(fieldBox.bottom - 1)
    expect(Math.abs(submitBox.right - fieldBox.right)).toBeLessThanOrEqual(1)
    expect(submit).toHaveClass('MuiButton-contained')
  })

  it('replaces the contact list with the add face, closed by its primary on the right', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [identifier({ id: 'id-1', kind: 'email' })],
          }),
        )
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="contact" />,
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )

    expect(screen.queryByRole('table')).toBeNull()
    const submit = await screen.findByRole('button', {
      name: zhCN.identifiers.add.submitEmail,
    })
    const cancel = screen.getByRole('button', {
      name: zhCN.identifiers.add.cancel,
    })
    expectOneActionGroup(submit, cancel)
  })

  it('keeps the email add face\'s two steps on one right-closed action row', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify'
      ) {
        return jsonResponse(200, { state: 'added' })
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<IdentifiersSection session={rig.session} />)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )

    const send = screen.getByRole('button', {
      name: zhCN.identifiers.add.submitEmail,
    })
    const cancel = screen.getByRole('button', {
      name: zhCN.identifiers.add.cancel,
    })
    expectOneActionGroup(send, cancel)
    expect(send).toHaveClass('MuiButton-contained')
    expect(cancel).not.toHaveClass('MuiButton-contained')
    expectRightEdge(send, actionRowOf(send))

    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(send)
    await screen.findByLabelText(zhCN.identifiers.add.code.label)

    const verify = screen.getByRole('button', {
      name: zhCN.identifiers.add.verify,
    })
    const change = screen.getByRole('button', {
      name: zhCN.identifiers.add.change,
    })
    expect(verify).toHaveAttribute('aria-disabled', 'true')
    expectOneActionGroup(verify, change)
    expect(verify).toHaveClass('MuiButton-contained')
    expect(change).not.toHaveClass('MuiButton-contained')
    expectRightEdge(verify, actionRowOf(verify))

    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.code.label),
      '123456',
    )
    expect(verify).not.toHaveAttribute('aria-disabled', 'true')
    expectOneActionGroup(verify, change)
  })

  it('keeps the enrollment confirm and cancel on one row, with the fields capped', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, {
          methods: [],
          signup: { enabled: false, password: { enabled: false } },
          mfa: { factors: ['totp'] },
          human: {
            phone: { required: false, provider: 'off' },
            other: { required: false, provider: 'off' },
          },
        })
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'enroll',
          next: { kind: 'webauthn', options: { otpauth_uri: OTPAUTH_URI } },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        return jsonResponse(200, { seq: 2, state: 'ready', next: { kind: 'complete' } })
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<MfaSection session={rig.session} />)
    const user = userEvent.setup()

    await user.click(await screen.findByTestId('mfa-add-totp'))

    const codeField = await screen.findByLabelText(zhCN.flowStep.codeLabel)
    const confirm = screen.getByRole('button', {
      name: zhCN.mfa.enroll.confirmLabel,
    })
    const cancel = screen.getByRole('button', {
      name: zhCN.mfa.enroll.cancelLabel,
    })
    expect(sameTop(confirm, cancel)).toBe(true)
    expect(rightOf(confirm, cancel)).toBe(true)
    expect(
      confirm.getBoundingClientRect().left -
        cancel.getBoundingClientRect().right,
    ).toBeLessThanOrEqual(32)
    expectOneActionGroup(confirm, cancel)

    expect(
      screen.getByRole('heading', {
        level: 3,
        name: zhCN.mfa.enroll.title.replace(
          '{{method}}',
          zhCN.mfa.factor.kind.totp,
        ),
      }),
    ).toHaveClass('MuiTypography-subtitle1')

    expect(codeField.getBoundingClientRect().width).toBeLessThanOrEqual(601)
  })

  it('closes the contact row verify form on its primary action', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-unproved',
                kind: 'email',
                value: 'ada@x.test',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'admin',
              }),
            ],
          }),
        )
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="contact" />,
    )
    const user = userEvent.setup()

    const contactRow = (await screen.findByText('ada@x.test')).closest('tr')
    expect(contactRow).not.toBeNull()
    await user.click(
      within(contactRow as HTMLElement).getByRole('button', {
        name: zhCN.identifiers.verify,
      }),
    )
    const submit = await screen.findByRole('button', {
      name: zhCN.identifiers.add.submitEmail,
    })
    const cancel = screen.getByRole('button', {
      name: zhCN.identifiers.add.cancel,
    })
    expectOneActionGroup(submit, cancel)
  })

  it('closes every factor removal on one right rail', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, {
          methods: [],
          signup: { enabled: false, password: { enabled: false } },
          mfa: { factors: ['totp'] },
          human: {
            phone: { required: false, provider: 'off' },
            other: { required: false, provider: 'off' },
          },
        })
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            { id: 'f-totp', kind: 'totp', label: 'App', status: 'active', created_at: null },
            { id: 'f-sms', kind: 'sms', label: 'Phone', status: 'active', created_at: null },
          ],
        })
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<MfaSection session={rig.session} />)
    const user = userEvent.setup()

    const factorRow = (name: string): HTMLElement => {
      const row = screen
        .getAllByRole('listitem')
        .find((item) => within(item).queryByText(name, { exact: true }) !== null)
      if (row === undefined) {
        throw new Error(`no factor row reads as ${name}`)
      }
      return row
    }
    const removeIn = (name: string): HTMLElement =>
      within(factorRow(name)).getByRole('button', { name: zhCN.mfa.remove })
    await screen.findAllByText(zhCN.mfa.factor.kind.totp)
    const totpRemove = removeIn(zhCN.mfa.factor.kind.totp)
    const smsRemove = removeIn('Phone')
    expect(screen.getByTestId('mfa-add-totp')).toBeTruthy()
    expect(screen.queryByTestId('mfa-add-sms')).toBeNull()
    expectRightEdge(totpRemove, smsRemove)
    expect(sameTop(totpRemove, smsRemove)).toBe(false)
  })

  it('ends the profile save beneath its field, flush on the right', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody())
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<ProfileSection />)

    const field = await screen.findByLabelText(zhCN.profile.displayName.label)
    const submit = screen.getByRole('button', { name: zhCN.profile.save })
    const fieldBox = field.getBoundingClientRect()
    const submitBox = submit.getBoundingClientRect()
    expect(submitBox.top).toBeGreaterThanOrEqual(fieldBox.bottom - 1)
    expectRightEdge(submit, field)
  })

  it('ends the password submit flush with its fields', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ password_set: true }))
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<PasswordSection session={rig.session} />)

    const field = await screen.findByLabelText(zhCN.password.new.label)
    const submit = screen.getByRole('button', { name: zhCN.password.submit })
    const fieldBox = field.getBoundingClientRect()
    const submitBox = submit.getBoundingClientRect()
    expect(Math.abs(submitBox.right - fieldBox.right)).toBeLessThanOrEqual(1)
    expect(submitBox.left).toBeGreaterThan(fieldBox.left)
  })

  it('closes the phone form\'s action rows on their right primary', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <PhoneCountryDefaultProvider country="US">
        <PhoneSection
          session={rig.session}
          onCancel={() => undefined}
          onAdded={() => undefined}
        />
      </PhoneCountryDefaultProvider>,
    )
    const user = userEvent.setup()

    const send = await screen.findByRole('button', { name: zhCN.phone.send })
    const close = screen.getByRole('button', { name: zhCN.phone.close })
    expectOneActionGroup(send, close)

    await user.type(
      screen.getByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      '4155552671',
    )
    await user.click(send)
    await screen.findByLabelText(zhCN.phone.code.label)

    const verify = screen.getByRole('button', { name: zhCN.phone.verify })
    const back = screen.getByRole('button', { name: zhCN.phone.close })
    expect(verify).toHaveAttribute('aria-disabled', 'true')
    expectOneActionGroup(verify, back)
  })

  it('renders a host secondary control beside a step primary on one row', async () => {
    await page.viewport(1100, 900)
    const { unmount } = renderWithProviders(
      <FlowStep
        step={{ kind: 'challenge', fields: [{ name: 'code', kind: 'code' }] }}
        sequence={0}
        busy={false}
        answerStep="factor"
        submitLabel="Confirm"
        onSubmit={() => undefined}
        secondaryAction={<Button color="inherit">Cancel</Button>}
      />,
    )
    const confirm = screen.getByRole('button', { name: 'Confirm' })
    const cancel = screen.getByRole('button', { name: 'Cancel' })
    expectOneActionGroup(confirm, cancel)
    unmount()
  })
})

describe('account-ui sent notices in Chromium', () => {
  it('inserts the phone section\'s sent region empty and fills it a frame later', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'POST' && call.path === PHONE_PATH) {
        return jsonResponse(200, { expires_in: 300, resend_after: 60 })
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <PhoneCountryDefaultProvider country="US">
        <PhoneSection
          session={rig.session}
          onCancel={() => undefined}
          onAdded={() => undefined}
        />
      </PhoneCountryDefaultProvider>,
    )
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      '4155552671',
    )

    const recorded = recordStatusHistories()
    try {
      await user.click(screen.getByRole('button', { name: zhCN.phone.send }))
      const message = zhCN.phone.sent.replace('{{phone}}', '+14155552671')
      const region = await statusWithText(message)
      const history = recorded.historyOf(region)
      expect(history[0]).toBe('')
      expect(history).toContain(message)
    } finally {
      recorded.stop()
    }
  })

  it('inserts the identifier add face\'s sent region empty and fills it a frame later', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<IdentifiersSection session={rig.session} />)
    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )

    const recorded = recordStatusHistories()
    try {
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
      )
      const message = zhCN.identifiers.add.sent.replace(
        '{{value}}',
        'ada@x.test',
      )
      const region = await statusWithText(message)
      const history = recorded.historyOf(region)
      expect(history[0]).toBe('')
      expect(history).toContain(message)
    } finally {
      recorded.stop()
    }
  })
})
