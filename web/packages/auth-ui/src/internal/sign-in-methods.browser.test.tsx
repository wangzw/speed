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

const METHOD = 'directory'

const FIELDS = [{ name: 'identifier', kind: 'text', required: true }] as const

function starts(harness: ReturnType<typeof makeHarness>) {
  return harness.calls.filter((call) => call.path === '/api/v1/auth/flows')
}

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
      await user.click(submit)
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(fake.widgets[0]?.siteKey).toBe('site-1')
      expect(starts(harness)).toHaveLength(0)
      act(() => {
        fake.token('token-1')
      })
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
    const fake = installFakeTurnstile()
    try {
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
