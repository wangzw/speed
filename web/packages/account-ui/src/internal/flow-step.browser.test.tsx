/**
 * FlowStep in a real browser: axe's label-content-name-mismatch (WCAG 2.5.3
 * Label in Name) over the manual-entry disclosure, and the focus a step and
 * its host challenge keep while an answer is in flight.
 *
 * The rule reads a control's visible text through the canvas text
 * measurement API, which jsdom answers with null; the unit suite's scan
 * therefore cannot decide it, and this case is that unit case's deciding
 * verdict (see @speed/test-utils/axe's BROWSER_DETERMINATIONS).
 *
 * Only the browser tier watches a focused control survive a state change
 * the way a person's browser does: jsdom keeps a focused button focused
 * even after it turns natively disabled, so a regression back to a native
 * `disabled` would pass there and drop the operator's place here.
 */
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import type { AuthnStep } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import {
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../../test-utils/real-client.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStep } from './flow-step.js'
import { StepUpChallenge } from './step-up-challenge.js'

const OTPAUTH_URI =
  'otpauth://totp/Example%20Co:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example%20Co'

const TOTP_STEP: AuthnStep = {
  kind: 'webauthn',
  options: { otpauth_uri: OTPAUTH_URI },
}

const CHALLENGE_STEP: AuthnStep = {
  kind: 'challenge',
  fields: [{ name: 'code', kind: 'code' }],
}

const FACTORS_PATH = '/api/v1/auth/factors'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

const ACTIVE_FACTOR = {
  id: 'factor-1',
  kind: 'totp',
  label: 'Authenticator app',
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
}

describe('FlowStep Label in Name in Chromium', () => {
  it('settles Label in Name with the account FlowStep manual disclosure open', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <FlowStep
        step={TOTP_STEP}
        sequence={1}
        busy={false}
        answerStep="answer"
        submitLabel="Continue"
        onSubmit={vi.fn()}
      />,
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.flowStep.totpManualToggle }),
    )
    await expectNoAxeViolations({
      decidedInBrowser: 'flowStepAccountManualDisclosure',
    })
  })
})

describe('FlowStep focus in Chromium', () => {
  it('keeps focus on the submit and the code field usable while the answer is in flight', async () => {
    await page.viewport(900, 900)
    const user = userEvent.setup()
    function Harness() {
      const [busy, setBusy] = useState(false)
      return (
        <FlowStep
          step={CHALLENGE_STEP}
          sequence={1}
          busy={busy}
          answerStep="answer"
          submitLabel="Continue"
          onSubmit={() => setBusy(true)}
        />
      )
    }
    renderWithProviders(<Harness />)

    const field = screen.getByLabelText(zhCN.flowStep.codeLabel)
    await user.type(field, '123456')
    const submit = screen.getByRole('button', { name: 'Continue' })
    await user.click(submit)

    await waitFor(() => expect(submit).toHaveAttribute('aria-busy', 'true'))
    // The control that started the answer keeps the focus the click gave
    // it; a native disable would have moved it to the document body.
    expect(document.activeElement).toBe(submit)
    expect(submit).toHaveAttribute('aria-disabled', 'true')

    // The field is locked but stays focusable and unchangeable.
    expect(field).toHaveAttribute('readonly')
    field.focus()
    expect(field).toHaveFocus()
    await user.keyboard('9')
    expect((field as HTMLInputElement).value).toBe('123456')
  })

  it('keeps focus on the chosen factor and the dialog cancel available while the answer is in flight', async () => {
    await page.viewport(900, 900)
    let answering = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, { factors: [ACTIVE_FACTOR] })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'Authenticator app' }],
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        answering = true
        return new Promise<Response>(() => {})
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const onCancel = vi.fn()
    renderWithProviders(
      <StepUpChallenge
        open
        session={rig.session}
        onSuccess={vi.fn()}
        onCancel={onCancel}
      />,
    )
    const user = userEvent.setup()

    const totp = await screen.findByRole('button', {
      name: zhCN.mfa.factor.kind.totp,
    })
    await user.click(totp)
    await waitFor(() => expect(answering).toBe(true))
    await waitFor(() => expect(totp).toHaveAttribute('aria-busy', 'true'))
    // The option that started the answer keeps the click's focus, and its
    // progress state does not take the operator's place away.
    expect(document.activeElement).toBe(totp)

    // The step's cancel stays the way out while the answer is in flight --
    // the same exit the Escape key offers.
    const cancel = screen.getByRole('button', {
      name: zhCN.mfa.stepUp.cancelLabel,
    })
    expect(cancel).not.toHaveAttribute('aria-disabled')
    expect(cancel).not.toHaveAttribute('aria-busy')
    expect(cancel).not.toHaveAccessibleDescription()
    cancel.focus()
    expect(cancel).toHaveFocus()
    await user.click(cancel)
    await waitFor(() => expect(onCancel).toHaveBeenCalledTimes(1))
  })
})
