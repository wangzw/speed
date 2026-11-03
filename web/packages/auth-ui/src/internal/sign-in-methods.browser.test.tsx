/**
 * DescriptorSignInForm's first step under the deployment's human-verification
 * gate, in a real browser: the form the sign-in screen renders for a
 * descriptor-only method starts the method's flow itself, so a gate that
 * requires a proof must stand on that first step and the proof it holds must
 * ride the start request as `human_proof`. A deployment that configures no
 * gate keeps the bare form -- no proof control, no proof on the wire.
 *
 * The cases drive the Turnstile mechanism through the shared fake widget, so
 * the assertion is the control the block renders and the body the flow
 * receives, never a network call.
 */
import { describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  LOGIN_START,
  makeHarness,
  openFlow,
} from '@speed/test-utils/session-harness'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import { DescriptorSignInForm } from './sign-in-methods.js'
import { renderWithProviders } from '../../test-utils/render.js'
import enUS from '../locales/en-US.json' with { type: 'json' }

/** The descriptor-only method the cases drive. */
const METHOD = 'directory'

/** Its first step, whose field the family knows by name. */
const FIELDS = [{ name: 'identifier', kind: 'text', required: true }] as const

/** The start requests the harness received. */
function starts(harness: ReturnType<typeof makeHarness>) {
  return harness.calls.filter((call) => call.path === '/api/v1/auth/flows')
}

/** Fill the first step's one field. */
async function fillFirstStep(): Promise<ReturnType<typeof userEvent.setup>> {
  const user = userEvent.setup()
  await user.type(
    screen.getByLabelText(enUS.passwordSignIn.identifierLabel),
    'alice',
  )
  return user
}

describe('DescriptorSignInForm first-step human verification in Chromium', () => {
  it('renders the proof control under a gate that requires one and starts with its proof', async () => {
    const fake = installFakeTurnstile()
    try {
      const harness = makeHarness({ [LOGIN_START]: () => openFlow() })
      renderWithProviders(
        <DescriptorSignInForm
          session={harness.session}
          method={METHOD}
          fields={FIELDS}
          humanGate={{
            phone: { required: true, provider: 'turnstile', site_key: 'site-1' },
            other: { required: true, provider: 'turnstile', site_key: 'site-1' },
          }}
        />,
        { language: 'en-US' },
      )
      const user = await fillFirstStep()
      // The deployment requires a proof: its control stands on the step
      // the form starts from.
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(fake.widgets[0]?.siteKey).toBe('site-1')
      const submit = screen.getByRole('button', {
        name: enUS.signInFlow.submit,
      })
      // No proof yet: the step does not start the flow.
      expect(submit).toBeDisabled()
      expect(starts(harness)).toHaveLength(0)
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(submit).toBeEnabled())
      await user.click(submit)
      await waitFor(() => expect(starts(harness)).toHaveLength(1))
      expect(starts(harness)[0]?.options?.body).toMatchObject({
        method: METHOD,
        input: { identifier: 'alice' },
        human_proof: 'token-1',
      })
    } finally {
      fake.restore()
    }
  })

  it('renders no proof control when the deployment configures no gate', async () => {
    const fake = installFakeTurnstile()
    try {
      const harness = makeHarness({ [LOGIN_START]: () => openFlow() })
      renderWithProviders(
        <DescriptorSignInForm
          session={harness.session}
          method={METHOD}
          fields={FIELDS}
          humanGate={{
            phone: { required: false, provider: 'off' },
            other: { required: false, provider: 'off' },
          }}
        />,
        { language: 'en-US' },
      )
      const user = await fillFirstStep()
      expect(fake.widgets).toHaveLength(0)
      const submit = screen.getByRole('button', {
        name: enUS.signInFlow.submit,
      })
      expect(submit).toBeEnabled()
      await user.click(submit)
      await waitFor(() => expect(starts(harness)).toHaveLength(1))
      expect(starts(harness)[0]?.options?.body).not.toHaveProperty(
        'human_proof',
      )
    } finally {
      fake.restore()
    }
  })
})
