/**
 * The account-ui forms' shared action-row geometry, measured in a real
 * browser.
 *
 * The rules these cases pin are all about boxes, not callbacks: a replace
 * or change form puts its submit beneath its field, the primary action
 * closes an inline form's row on the right, a wizard's confirm and cancel
 * share one row with the fields capped to the form width, the factor
 * list's removals close on one right rail, both phone steps close on their
 * primary, and the password form's submit ends flush with its fields.
 * jsdom lays nothing out, so only this tier can tell one arrangement from
 * another.
 */

import Button from '@mui/material/Button'
import { describe, expect, it } from 'vitest'
import { screen, within } from '@testing-library/react'
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
import { FlowStep } from './internal/flow-step.js'

const PROFILE_PATH = '/api/v1/auth/me'
const FACTORS_PATH = '/api/v1/auth/factors'
const METHODS_PATH = '/api/v1/auth/methods'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'
const PHONE_PATH = '/api/v1/auth/phone'
const OTPAUTH_URI = 'otpauth://totp/speed:ada?issuer=speed'

/** Two boxes are on the same line when their top edges agree. */
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

describe('account-ui form geometry', () => {
  it('stacks the change-address submit beneath its field', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'pending', email: 'ada@x.test', since: '' },
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
    // Scope to the change form: the resend control carries the same
    // "send a code" wording elsewhere in the card.
    const changeForm = field.closest('form')
    expect(changeForm).not.toBeNull()
    const submit = within(changeForm as HTMLElement).getByRole('button', {
      name: zhCN.verification.changeSubmit,
    })

    const fieldBox = field.getBoundingClientRect()
    const submitBox = submit.getBoundingClientRect()
    // The submit is on a later line and starts under the field, not at its
    // top edge on the right.
    expect(submitBox.top).toBeGreaterThanOrEqual(fieldBox.bottom - 1)
    expect(submitBox.left).toBeGreaterThanOrEqual(fieldBox.left - 1)
    expect(submitBox.left).toBeLessThanOrEqual(fieldBox.right)
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

    // The list gives way to the form while it is open.
    expect(screen.queryByRole('table')).toBeNull()
    const submit = await screen.findByRole('button', {
      name: zhCN.identifiers.add.submitEmail,
    })
    const cancel = screen.getByRole('button', {
      name: zhCN.identifiers.add.cancel,
    })
    expect(sameTop(submit, cancel)).toBe(true)
    expect(rightOf(submit, cancel)).toBe(true)
  })

  it('keeps the enrollment confirm and cancel on one row, with the fields capped', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, {
          methods: [],
          signup: { enabled: false, password: { enabled: false } },
          mfa: { factors: ['totp'] },
          human: { required: false, provider: 'off' },
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

    const confirm = await screen.findByRole('button', {
      name: zhCN.mfa.enroll.confirmLabel,
    })
    const cancel = screen.getByRole('button', {
      name: zhCN.mfa.enroll.cancelLabel,
    })
    expect(sameTop(confirm, cancel)).toBe(true)
    expect(rightOf(confirm, cancel)).toBe(true)

    // The wizard title is a sub-form heading, one step below the card title
    // (subtitle1, not the larger h6).
    expect(
      screen.getByRole('heading', {
        level: 3,
        name: zhCN.mfa.enroll.title,
      }),
    ).toHaveClass('MuiTypography-subtitle1')

    // The wizard's fields inherit the form width cap, not the card width.
    const codeField = screen.getByLabelText(zhCN.flowStep.codeLabel)
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
                verified_at: '',
                verified_via: '',
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

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.identifiers.actionsAria.replace('{{value}}', 'ada@x.test'),
      }),
    )
    await user.click(
      screen.getByRole('menuitem', { name: zhCN.identifiers.verify }),
    )
    const submit = await screen.findByRole('button', {
      name: zhCN.identifiers.add.submitEmail,
    })
    const cancel = screen.getByRole('button', {
      name: zhCN.identifiers.add.cancel,
    })
    expect(sameTop(submit, cancel)).toBe(true)
    expect(rightOf(submit, cancel)).toBe(true)
  })

  it('closes every factor removal on one right rail', async () => {
    await page.viewport(1100, 900)
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === METHODS_PATH) {
        return jsonResponse(200, {
          methods: [],
          signup: { enabled: false, password: { enabled: false } },
          mfa: { factors: ['totp'] },
          human: { required: false, provider: 'off' },
        })
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            { id: 'f-totp', kind: 'totp', label: 'App', status: 'active', created_at: '' },
            { id: 'f-sms', kind: 'sms', label: 'Phone', status: 'active', created_at: '' },
          ],
        })
      }
      return jsonResponse(404, { code: 'authn.bad_request' })
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(<MfaSection session={rig.session} />)
    const user = userEvent.setup()

    const removeName = (kind: 'totp' | 'sms'): string =>
      zhCN.mfa.removeAria.replace('{{factor}}', zhCN.mfa.factor.kind[kind])
    const totpRemove = await screen.findByRole('button', {
      name: removeName('totp'),
    })
    const smsRemove = screen.getByRole('button', { name: removeName('sms') })
    // One method offers Add/Manage and the other does not, but both
    // removals sit on the same right edge.
    expect(screen.getByTestId('mfa-add-totp')).toBeTruthy()
    expect(screen.queryByTestId('mfa-add-sms')).toBeNull()
    expectRightEdge(totpRemove, smsRemove)
    expect(sameTop(totpRemove, smsRemove)).toBe(false)
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
    // The submit's right edge lands on the field's right edge, not its left.
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
        <PhoneSection session={rig.session} onCancel={() => undefined} />
      </PhoneCountryDefaultProvider>,
    )
    const user = userEvent.setup()

    // Number step: the close control leads, the send primary closes the row.
    const send = await screen.findByRole('button', { name: zhCN.phone.send })
    const close = screen.getByRole('button', { name: zhCN.phone.close })
    expect(sameTop(send, close)).toBe(true)
    expect(rightOf(send, close)).toBe(true)

    await user.type(
      screen.getByLabelText(zhCN.phone.phone.label, { selector: 'input' }),
      '4155552671',
    )
    await user.click(send)
    await screen.findByLabelText(zhCN.phone.code.label)

    // Code step: the same arrangement, with the verify primary disabled
    // while the code field is empty.
    const verify = screen.getByRole('button', { name: zhCN.phone.verify })
    const back = screen.getByRole('button', { name: zhCN.phone.close })
    expect(verify).toBeDisabled()
    expect(sameTop(verify, back)).toBe(true)
    expect(rightOf(verify, back)).toBe(true)
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
    expect(sameTop(confirm, cancel)).toBe(true)
    expect(rightOf(confirm, cancel)).toBe(true)
    unmount()
  })
})
