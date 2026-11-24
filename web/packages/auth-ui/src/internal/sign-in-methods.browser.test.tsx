/**
 * DescriptorSignInForm's first step under the deployment's human-verification
 * gate, in a real browser: the form the sign-in screen renders for a
 * descriptor-only method starts the method's flow itself, so a start the
 * viewer asks for is held while the gate is read and the proof its surface
 * requires is obtained, and that proof rides the start request as
 * `human_proof`. A deployment that configures no gate starts at once -- no
 * proof control, no proof on the wire.
 *
 * The cases drive the Turnstile mechanism through the shared fake widget, so
 * the assertion is the control the block renders and the body the flow
 * receives, never a network call.
 */
import { describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { userEvent as browserUserEvent } from 'vitest/browser'
import {
  LOGIN_START,
  SSO_DISCOVER,
  makeHarness,
  openFlow,
  ssoProviders,
} from '@speed/test-utils/session-harness'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import { DescriptorSignInForm, EnterpriseSignIn } from './sign-in-methods.js'
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

describe('EnterpriseSignIn focus after a discovery in Chromium', () => {
  it('lands on the first provider button when the discovery lists providers', async () => {
    const harness = makeHarness({
      [SSO_DISCOVER]: () =>
        ssoProviders([
          { id: 'idp-1', name: 'Acme Okta', protocol: 'oidc' },
          { id: 'idp-2', name: 'Acme Azure', protocol: 'oidc' },
        ]),
    })
    renderWithProviders(<EnterpriseSignIn session={harness.session} />, {
      language: 'en-US',
    })
    const user = userEvent.setup()
    await user.type(
      screen.getByLabelText(enUS.enterprise.emailLabel),
      'nobody@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: enUS.enterprise.continue }),
    )
    const first = await screen.findByRole('button', { name: 'Acme Okta' })
    // The continue button the viewer activated was removed with the form;
    // real Chromium drops focus to the body, and the list's first provider
    // takes it from there.
    await waitFor(() => expect(first).toHaveFocus())
  })
})

describe('DescriptorSignInForm first-step human verification in Chromium', () => {
  it('holds a start under a gate that requires a proof and sends it once the proof arrives', async () => {
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
      const submit = screen.getByRole('button', {
        name: enUS.signInFlow.submit,
      })
      // Entering the form reads and solves nothing: the viewer's submit is
      // held while the gate is read and the proof its surface requires is
      // obtained.
      await user.click(submit)
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(fake.widgets[0]?.siteKey).toBe('site-1')
      expect(starts(harness)).toHaveLength(0)
      act(() => {
        fake.token('token-1')
      })
      // The held start leaves by itself once the proof arrives: the viewer
      // does not click a second time.
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

  it('keeps the descriptor submit focused and busy while its proof is outstanding', async () => {
    // The Turnstile control lives in a third-party frame the block does not
    // focus; the submit that asked keeps the focus it had. Activation goes
    // through the keyboard, the path this defect belongs to.
    const fake = installFakeTurnstile()
    try {
      // The start stays in flight after the proof arrives, so the form
      // surface -- and the control that asked -- stays on screen.
      let releaseStart: ((reply: ReturnType<typeof openFlow>) => void) | null =
        null
      const harness = makeHarness({
        [LOGIN_START]: () =>
          new Promise((resolve) => {
            releaseStart = resolve
          }),
      })
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
      await fillFirstStep()
      const submit = screen.getByRole('button', {
        name: enUS.signInFlow.submit,
      })
      submit.focus()
      expect(document.activeElement).toBe(submit)
      await browserUserEvent.keyboard('{Enter}')
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      await waitFor(() => expect(submit).toHaveAttribute('aria-busy', 'true'))
      expect(document.activeElement).toBe(submit)
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(starts(harness)).toHaveLength(1))
      // The held start left and its request is in flight; the control that
      // asked still holds focus.
      expect(document.activeElement).toBe(submit)
      act(() => {
        releaseStart?.(openFlow())
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
      expect(submit).not.toHaveAttribute('aria-disabled', 'true')
      expect(submit).not.toHaveAttribute('aria-busy')
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
