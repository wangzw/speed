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

const PROVIDER_NAME = 'Globex SAML'

const FLOW_COOKIE = 'authn-flow'

const FLOW_COOKIE_PAIR = new RegExp(`(?:^|;\\s*)${FLOW_COOKIE}=`)

test.use({
  enterpriseSSO: true,
  samlIdentityProvider: true,
  ignoreHTTPSErrors: true,
})

function samlProviderOf(demo: DemoTarget) {
  const provider = demo.server.samlIdentityProvider
  if (provider === undefined) {
    throw new Error(
      'the SAML fixture did not start the identity provider this spec drives',
    )
  }
  return provider
}

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

async function approveAtTheIdentityProvider(page: Page): Promise<void> {
  await page
    .getByRole('button', {
      name: samlIdentityProviderCopy.approve,
      exact: true,
    })
    .click()
}

function watchCallbackRequests(page: Page): readonly Request[] {
  const seen: Request[] = []
  page.on('request', (outgoing) => {
    if (new URL(outgoing.url()).pathname.includes('/auth/callback/')) {
      seen.push(outgoing)
    }
  })
  return seen
}

function callbackRoute(outgoing: Request): 'acs' | 'resume' {
  const segments = new URL(outgoing.url()).pathname.split('/').filter(Boolean)
  return segments[segments.length - 1] === 'resume' ? 'resume' : 'acs'
}

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

    await expect(homeHeading(page)).toBeVisible()

    await expectAccountMenuName(
      page.getByTestId('account-entry-trigger'),
      samlIdentityProviderUser.name,
    )

    await expectTheBounce(callbacks, 303)
  })
})

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
      // The commit is the wait: the attacker page submits on load, so the navigation it starts replaces the page the load would otherwise settle on.
      await page.goto(provider.attackURL, { waitUntil: 'commit' })
    })

    await test.step('the response is refused: the victim lands on the sign-in failure and no session exists', async () => {
      await expect(
        page.getByText(copy('authUi', 'errors.authn.login_failed'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(homeHeading(page)).toHaveCount(0)

      await expectTheBounce(callbacks, 303)

      await accounts.openSignIn()
      await expect(homeHeading(page)).toHaveCount(0)
    })
  } finally {
    await attacker.close()
  }
})
