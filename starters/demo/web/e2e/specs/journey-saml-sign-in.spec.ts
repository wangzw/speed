/**
 * J-enterprise-sso, SAML half -- an employee signs in through an SP-initiated
 * SAML provider, and a response the provider signed for another flow does not
 * sign anyone in.
 *
 * A SAML sign-in ends with the identity provider's page posting the signed
 * response to the product's assertion consumer service. That post is cross-site,
 * so the browser withholds the flow cookie (`SameSite=Lax`), and the product
 * answers it with a same-origin page that posts the form again to the resume
 * route, where the cookie travels. The server tests prove each half of that; this
 * journey is the guard that a real browser behaves as the design assumes:
 *
 *   - the cross-site post to the assertion consumer service carries no flow
 *     cookie and is answered with the bounce page, not a login;
 *   - the re-post to the resume route is same-origin and carries the flow
 *     cookie, which is what binds the response to the browser that started the
 *     flow.
 *
 * It is a regression guard on the cookie and the bounce, not a fail-before case:
 * with `SameSite=None` the sign-in would also complete.
 *
 * The journey's steps, all through the rendered product:
 *
 *   1. The operator registers a just-in-time SAML provider for the target tenant
 *      (the provider's entity identifier, https sign-on address, signing
 *      certificate and claim mapping) and claims and verifies the email domain.
 *   2. The employee uses the enterprise entry with the work address and chooses
 *      the provider.
 *   3. The employee approves at the identity provider, whose page posts the
 *      signed response. The browser's requests show the cross-site post to the
 *      assertion consumer service and the same-origin post to the resume route,
 *      and the employee lands signed in on the account the login opened.
 *
 * The second test is the attack the flow binding exists for. An attacker signs in
 * at the identity provider with a browser of their own and keeps the response it
 * issues for that flow; a page on another origin posts it into the victim's
 * browser while the victim's own flow is waiting at the provider. The response is
 * valid and signed, and it answers a request the victim's flow never made: the
 * victim lands on the sign-in failure and no session exists.
 *
 * Two things are not UI, and only these:
 *
 *   - The operator's own email address is proved through the review mail
 *     carrier's verification code, read from the process log (the sanctioned
 *     verification-code read), because the admin's human writes are gated on it.
 *   - The DNS publication is the fixture action the enterprise journey already
 *     names: the spec writes the record the product rendered into the DNS stub.
 *
 * No route is intercepted, no request is faked and no API is called from the
 * spec. The identity provider is a real SAML provider on loopback that the
 * fixtures start; the browser walks its page with a click. The browser accepts
 * the provider's self-minted https certificate for this spec alone, because the
 * product only takes an https single sign-on address.
 *
 * `@covers` names what the assertions can fail on: a SAML provider registered
 * for the tenant (REQ-platform-config-017), the domain's discovery of it
 * (REQ-signin-020) and a session established in the callback's answer with the
 * user returned to the page that started the sign-in (REQ-signin-027).
 */
// @journey journey-enterprise-sso-onboarding.md
// @covers REQ-platform-config-017, REQ-signin-020, REQ-signin-027
import type { Browser, Page, Request } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import type { DemoTarget } from '../fixtures/demo'
import {
  samlIdentityProviderClaims,
  samlIdentityProviderCopy,
  samlIdentityProviderUser,
} from '../fixtures/saml-identity-provider'
import { createAccounts } from '../helpers/accounts'
import { expectAccountMenuName } from '../helpers/account-menu'
import {
  TARGET_TENANT,
  adminLink,
  claimAndVerifyDomain,
  homeHeading,
  leaveConsole,
  openConsole,
  proveOperatorAddress,
  registerProvider,
  selectTenant,
  signInHeading,
  startEnterpriseSignIn,
} from '../helpers/enterprise-sso'
import { copy } from '../helpers/i18n'

/** The provider the operator registers for the SAML sign-in. */
const PROVIDER_NAME = 'Globex SAML'

/**
 * The flow cookie the product sets when a sign-in leaves for a provider. The
 * server's public URL is http://localhost, a loopback http address, where the
 * cookie carries no __Host- prefix.
 */
const FLOW_COOKIE = 'authn-flow'

/** A Cookie header that carries the flow cookie under its exact name. */
const FLOW_COOKIE_PAIR = new RegExp(`(?:^|;\\s*)${FLOW_COOKIE}=`)

// This journey drives a real SAML round trip and the domain's TXT lookup, so its
// server starts the SAML identity provider, the DNS stub and the enterprise SSO
// seams. The browser accepts the provider's self-minted certificate: the
// product only takes an https single sign-on address.
test.use({
  enterpriseSSO: true,
  samlIdentityProvider: true,
  ignoreHTTPSErrors: true,
})

/** The SAML provider fixture the server started for this spec. */
function samlProviderOf(demo: DemoTarget) {
  const provider = demo.server.samlIdentityProvider
  if (provider === undefined) {
    throw new Error(
      'the SAML fixture did not start the identity provider this spec drives',
    )
  }
  return provider
}

/**
 * The shared precondition: the operator registers the SAML provider, claims and
 * verifies the email domain, and signs out, leaving the sign-in surface.
 */
async function onboardSAMLProvider(demo: DemoTarget): Promise<void> {
  const { page, server, accounts } = demo
  const provider = samlProviderOf(demo)
  const stub = server.dnsStub
  if (stub === undefined) {
    throw new Error(
      'the enterprise fixture did not start the DNS stub this spec publishes into',
    )
  }

  await test.step("precondition: the operator signs in and proves the account's own address", () =>
    proveOperatorAddress(demo))

  await test.step('step 1: the operator registers a SAML provider for the target tenant and verifies its domain', async () => {
    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(homeHeading(page)).toBeVisible()
    await openConsole(page)
    await adminLink(page, copy('adminUi', 'sso.title')).click()
    await selectTenant(page, TARGET_TENANT)

    await registerProvider(page, {
      protocol: 'saml',
      name: PROVIDER_NAME,
      provisioning: 'jit',
      claim: 'email',
      config: {
        idp_entity_id: provider.entityID,
        idp_sso_url: provider.ssoURL,
        idp_certs: [provider.certificatePEM],
        claims: samlIdentityProviderClaims,
      },
    })
    await claimAndVerifyDomain(page, stub, PROVIDER_NAME)

    await leaveConsole(page)
    await accounts.signOut()
  })
}

/**
 * Walk the enterprise entry as the employee and leave for the identity
 * provider: the work address, the discovered provider, and the provider's own
 * page for the fixed user.
 */
async function leaveForTheIdentityProvider(
  page: Page,
  accounts: DemoTarget['accounts'],
): Promise<void> {
  await accounts.openSignIn()
  await expect(signInHeading(page)).toBeVisible()
  await startEnterpriseSignIn(
    page,
    PROVIDER_NAME,
    samlIdentityProviderUser.email,
  )
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: samlIdentityProviderCopy.heading,
      exact: true,
    }),
  ).toBeVisible()
  await expect(
    page.getByText(
      samlIdentityProviderCopy.accountSentence(samlIdentityProviderUser.email),
      { exact: true },
    ),
  ).toBeVisible()
}

/** Approve at the identity provider's page. */
async function approveAtTheIdentityProvider(page: Page): Promise<void> {
  await page
    .getByRole('button', {
      name: samlIdentityProviderCopy.approve,
      exact: true,
    })
    .click()
}

/** The requests a page sent to the callback routes, in the order it sent them. */
function watchCallbackRequests(page: Page): readonly Request[] {
  const seen: Request[] = []
  page.on('request', (outgoing) => {
    if (new URL(outgoing.url()).pathname.includes('/auth/callback/')) {
      seen.push(outgoing)
    }
  })
  return seen
}

/** The route a callback request addressed, past the provider segment. */
function callbackRoute(outgoing: Request): 'acs' | 'resume' {
  const segments = new URL(outgoing.url()).pathname.split('/').filter(Boolean)
  return segments[segments.length - 1] === 'resume' ? 'resume' : 'acs'
}

/**
 * Assert the bounce happened: the response was posted to the assertion consumer
 * service cross-site, with no flow cookie, and answered with a page; the form was
 * then posted again to the resume route, same-origin, with the flow cookie.
 */
async function expectTheBounce(
  seen: readonly Request[],
  resumeStatus: number,
): Promise<void> {
  const posts = seen.filter((outgoing) => outgoing.method() === 'POST')
  expect(
    posts.map(callbackRoute),
    'the browser posts the response to the ACS, and then to the resume route',
  ).toEqual(['acs', 'resume'])
  const [acs, resume] = posts
  if (acs === undefined || resume === undefined) {
    throw new Error('the callback requests did not include both posts')
  }

  const acsHeaders = await acs.allHeaders()
  expect(
    acsHeaders['sec-fetch-site'],
    'the identity provider is another site than the product',
  ).toBe('cross-site')
  expect(
    acsHeaders['cookie'] ?? '',
    'a cross-site post carries no Lax flow cookie',
  ).not.toContain(FLOW_COOKIE)
  const acsAnswer = await acs.response()
  expect(acsAnswer?.status(), 'the ACS answers the bounce page').toBe(200)
  expect(acsAnswer?.headers()['content-type']).toContain('text/html')

  const resumeHeaders = await resume.allHeaders()
  expect(
    resumeHeaders['sec-fetch-site'],
    'the bounce page posts back to the product itself',
  ).toBe('same-origin')
  expect(
    resumeHeaders['cookie'] ?? '',
    'a same-origin post carries the flow cookie that binds it to this browser',
  ).toMatch(FLOW_COOKIE_PAIR)
  expect((await resume.response())?.status()).toBe(resumeStatus)
}

test('saml-sign-in: an employee signs in at a SAML provider through the same-site bounce', async ({
  demo,
}) => {
  const { page, accounts } = demo
  await onboardSAMLProvider(demo)
  const callbacks = watchCallbackRequests(page)

  await test.step('step 2: the employee uses the enterprise entry and leaves for the identity provider', () =>
    leaveForTheIdentityProvider(page, accounts))

  await test.step('step 3: the employee approves, the response bounces through the product and the session is established', async () => {
    await approveAtTheIdentityProvider(page)

    // REQ-signin-027: the callback establishes the session and returns the
    // employee to the page that started the sign-in, the tenant home.
    await expect(homeHeading(page)).toBeVisible({ timeout: 15_000 })

    // The account the sign-in opened carries the provider's display name: no
    // administrator prepared one.
    await expectAccountMenuName(
      page.getByTestId('account-entry-trigger'),
      samlIdentityProviderUser.name,
    )

    // 303 is the answer that sets the session cookie and returns the employee.
    await expectTheBounce(callbacks, 303)
  })
})

/** A browser of its own, which shares no cookie with the victim's. */
async function attackerBrowserPage(
  browser: Browser,
): Promise<{ readonly page: Page; readonly close: () => Promise<void> }> {
  const context = await browser.newContext({
    locale: 'en-US',
    timezoneId: 'UTC',
    ignoreHTTPSErrors: true,
  })
  return { page: await context.newPage(), close: () => context.close() }
}

test('saml-sign-in: a response the provider signed for another flow does not sign the victim in', async ({
  demo,
  browser,
}) => {
  const { page, server, accounts } = demo
  const provider = samlProviderOf(demo)
  await onboardSAMLProvider(demo)

  const attacker = await attackerBrowserPage(browser)
  try {
    await test.step("the attacker signs in at the identity provider with a browser of their own and keeps the response it issues for that flow", async () => {
      provider.holdNextResponse()
      await leaveForTheIdentityProvider(
        attacker.page,
        createAccounts({ page: attacker.page, server }),
      )
      await approveAtTheIdentityProvider(attacker.page)
      await expect(
        attacker.page.getByRole('heading', {
          level: 1,
          name: samlIdentityProviderCopy.heldHeading,
          exact: true,
        }),
      ).toBeVisible()
    })

    const callbacks = watchCallbackRequests(page)

    await test.step("the victim's own sign-in is waiting at the identity provider", () =>
      leaveForTheIdentityProvider(page, accounts))

    await test.step("a page on another origin posts the attacker's response into the victim's browser", async () => {
      // The commit is the wait: the attacker page submits its form as it
      // loads, so the navigation it starts replaces the page the load would
      // otherwise settle on.
      await page.goto(provider.attackURL, { waitUntil: 'commit' })
    })

    await test.step('the response is refused: the victim lands on the sign-in failure and no session exists', async () => {
      await expect(
        page.getByText(copy('authUi', 'errors.authn.login_failed'), {
          exact: true,
        }),
      ).toBeVisible({ timeout: 15_000 })
      await expect(homeHeading(page)).toHaveCount(0)

      // The bounce happened, so the refusal is the flow binding's work: the
      // re-post was same-origin and carried the victim's own flow cookie, and
      // the response it carried answered another browser's request.
      await expectTheBounce(callbacks, 303)

      // No session: the deployment root is still the sign-in surface.
      await accounts.openSignIn()
      await expect(homeHeading(page)).toHaveCount(0)
    })
  } finally {
    await attacker.close()
  }
})
