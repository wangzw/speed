/**
 * EnterpriseSignIn's discovery notices: the entry's one notice region
 * stands for its whole life, born empty, and each discovery result fills
 * the existing node. A live region announces only content changes that
 * follow its own insertion, so a notice that mounted together with its
 * region -- the no-match notice, or the providers title -- would be
 * silent. Text expectations read the bundle values, never inline
 * language.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DescriptorSignInForm, EnterpriseSignIn } from './sign-in-methods.js'
import { renderWithProviders } from '../../test-utils/render.js'
import {
  CAPTCHA_CHALLENGE,
  SSO_DISCOVER,
  captchaChallenge,
  makeHarness,
  ssoProviders,
} from '@speed/test-utils/session-harness'
import type { AuthnHumanGate } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import uiKitZhCN from '../../../ui-kit/src/locales/zh-CN.json' with { type: 'json' }

const MAIL = 'nobody@example.com'

/** A harness whose SSO discovery answers with the given providers. */
function harnessFor(
  providers: readonly { id: string; name: string; protocol: string }[],
) {
  return makeHarness({ [SSO_DISCOVER]: () => ssoProviders(providers) })
}

/** Submit an email through the entry's first step. */
async function discoverEmail(): Promise<void> {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText(zhCN.enterprise.emailLabel), MAIL)
  await user.click(
    screen.getByRole('button', { name: zhCN.enterprise.continue }),
  )
}

/** The status region whose text is `text`, once one carries it. */
async function findNotice(text: string): Promise<HTMLElement> {
  let notice: HTMLElement | undefined
  await waitFor(() => {
    notice = screen
      .getAllByRole('status')
      .find((region) => region.textContent === text)
    expect(notice).toBeDefined()
  })
  return notice as HTMLElement
}

describe('EnterpriseSignIn discovery notices', () => {
  it('fill a status region that stood empty with the no-match notice', async () => {
    const harness = harnessFor([])
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    // The regions in the tree before the interaction are silent: the
    // notice must fill one of them, not mount with its text.
    const before = screen.getAllByRole('status')
    expect(before.every((region) => region.textContent === '')).toBe(true)
    await discoverEmail()
    const notice = await findNotice(zhCN.enterprise.noProviders)
    // The node that announced the result was already in the tree.
    expect(before).toContain(notice)
    expect(screen.getAllByRole('status')).toContain(notice)
  })

  it('fill the same region with the providers title', async () => {
    const harness = harnessFor([
      { id: 'idp-1', name: 'Acme Okta', protocol: 'oidc' },
    ])
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    const before = screen.getAllByRole('status')
    expect(before.every((region) => region.textContent === '')).toBe(true)
    await discoverEmail()
    const notice = await findNotice(zhCN.enterprise.providersTitle)
    expect(before).toContain(notice)
    expect(
      screen.getByRole('button', { name: 'Acme Okta' }),
    ).toBeInTheDocument()
  })

  it('empties the notice again when the viewer returns to the email field', async () => {
    // A repeated identical result must be a change, not a rewrite: going
    // back to the email field clears the region so the next discovery's
    // notice is announced again.
    const harness = harnessFor([])
    renderWithProviders(<EnterpriseSignIn session={harness.session} />)
    await discoverEmail()
    const notice = await findNotice(zhCN.enterprise.noProviders)
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.enterprise.editEmail }),
    )
    await waitFor(() => expect(notice.textContent).toBe(''))
  })
})

/** A gate whose phone surface runs the captcha and whose other surface is
 * off. */
const PHONE_CAPTCHA: AuthnHumanGate = {
  phone: { required: true, provider: 'captcha' },
  other: { required: false, provider: 'off' },
}

/** A gate whose other surface runs the captcha and whose phone surface is
 * off. */
const OTHER_CAPTCHA: AuthnHumanGate = {
  phone: { required: false, provider: 'off' },
  other: { required: true, provider: 'captcha' },
}

describe('DescriptorSignInForm human-verification surface', () => {
  it('derives the phone surface from a phone field', async () => {
    const harness = makeHarness({
      [CAPTCHA_CHALLENGE]: () => captchaChallenge(),
    })
    renderWithProviders(
      <DescriptorSignInForm
        session={harness.session}
        method="sms-code"
        fields={[{ name: 'phone', kind: 'phone', required: true }]}
        humanGate={PHONE_CAPTCHA}
      />,
    )
    // The form's first step sends SMS, so the phone surface's captcha is
    // what it must present.
    await screen.findByLabelText(uiKitZhCN.humanVerification.captcha.answerLabel)
    expect(
      harness.calls.filter(
        (call) => call.path === '/api/v1/auth/human/captcha/challenge',
      ),
    ).toHaveLength(1)
  })

  it('keeps the other surface for a form that does not name a phone', async () => {
    const harness = makeHarness({
      [CAPTCHA_CHALLENGE]: () => captchaChallenge(),
    })
    renderWithProviders(
      <DescriptorSignInForm
        session={harness.session}
        method="email-code"
        fields={[{ name: 'email', kind: 'text', required: true }]}
        humanGate={OTHER_CAPTCHA}
      />,
    )
    await screen.findByLabelText(uiKitZhCN.humanVerification.captcha.answerLabel)
  })

  it('carries a send surface the host names', async () => {
    const harness = makeHarness({
      [CAPTCHA_CHALLENGE]: () => captchaChallenge(),
    })
    renderWithProviders(
      <DescriptorSignInForm
        session={harness.session}
        method="one-time-code"
        fields={[{ name: 'identifier', kind: 'text', required: true }]}
        humanGate={PHONE_CAPTCHA}
        humanScope="phone"
      />,
    )
    await screen.findByLabelText(uiKitZhCN.humanVerification.captcha.answerLabel)
  })
})
