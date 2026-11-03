/**
 * The last-sign-in-way journey: an account opened by an enterprise just-in-time
 * first login holds one way to sign in that is not a passkey -- the enterprise
 * identity the login bound. A passkey is enrolled during that first login,
 * which the journey proves is not one of the three ways the rule counts (a
 * password credential, a proved phone number, an external identity). Unlinking
 * the enterprise identity while it is the only such way asks for fresh proof;
 * the owner answers the challenge in the page with the passkey just enrolled,
 * and the unlink is then refused: the dedicated sentence is rendered, and the
 * binding stays listed. The owner then sets a password from the same account
 * page; the external identity is no longer the only non-passkey way, and the
 * same unlink now succeeds.
 *
 * WHY THE ACCOUNT IS OPENED BY AN ENTERPRISE JUST-IN-TIME LOGIN. A social first
 * login opens an account with a password and the provider's address, and a
 * phone-code first login now opens one with a password too, so neither can
 * produce the state this journey starts from: no password, no email, no phone,
 * one external identity. An enterprise just-in-time first login opens it
 * instead: it asks for a username before any account exists and writes neither a
 * password nor the provider's declared address. The provider is registered and
 * its email domain verified through the admin surface, and the account itself is
 * opened by the login -- nothing seeds it out of band.
 *
 * THE ENTERPRISE IDENTITY IS THE EXTERNAL IDENTITY UNBOUND. The just-in-time
 * login binds the provider's identity to the account under its own provider
 * name, and the account page lists it under the social accounts card as
 * "Enterprise SSO". No social identity is bound beside it: a second external
 * identity would be a second way to sign in, so removing either would leave the
 * other and would not exercise the last-way rule.
 *
 * THE DEPLOYMENT REQUIRES A FACTOR, AND THAT IS LOAD-BEARING. A step-up accepts
 * only a factor whose activation predates the session's authentication time, and
 * only one whose channel differs from the channels the session already proved.
 * A login that proves an existing factor as its second factor cannot be
 * strengthened by that same factor afterwards. The journey therefore needs a
 * login whose session records one factor: the sign-in flow registers the
 * passkey during the login (an enrolling login), and a factor registered during
 * the authentication does not strengthen it. The enterprise just-in-time login
 * is such a login. The operator who registers the provider is subject to the
 * same policy, so that sign-in enrols a passkey of its own in a separate browser
 * context; the account it sets up is the journey's only product-created account.
 *
 * EVERYTHING IS DRIVEN THROUGH THE UI. The social round trip is the harness's
 * real OpenID Connect provider: the browser leaves for its authorization page
 * and clicks its control, so no route is intercepted and no response is faked.
 * The DNS publication is a FIXTURE ACTION: the product renders the record name
 * and value, and the spec writes that value into the running DNS stub, standing
 * in for the operator's act at the domain's DNS. The passkey is Chromium's
 * virtual authenticator (helpers/virtual-authenticator.ts): the page still calls
 * `navigator.credentials.create`/`.get`, and the fixture answers as a platform
 * authenticator would.
 *
 * WHAT IS ASSERTED. The refusal is the dedicated last-login-method answer, and
 * this spec asserts its shipped sentence: it says the account has no other way
 * to sign in and names setting a password or linking another account as the
 * remedy. It also asserts that the binding survives the refusal, that the
 * account's contact list stays empty, and that the provider's address is not
 * written into the account.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts.
 */
// @journey journey-last-login-way.md
// @covers REQ-security-040, REQ-security-013, REQ-account-023, REQ-account-061, REQ-account-067, REQ-signup-018, REQ-signin-022
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import type { DemoTarget } from '../fixtures/demo'
import type { DNSStub } from '../fixtures/dns-stub'
import {
  enterpriseIdentityProviderClaims,
  enterpriseIdentityProviderClient,
  enterpriseIdentityProviderDomain,
  enterpriseIdentityProviderUser,
} from '../fixtures/identity-provider'
import type { EnterpriseIdentityProvider } from '../fixtures/identity-provider'
import { waitForVerificationCode } from '../fixtures/log'
import { createAccounts, type Accounts } from '../helpers/accounts'
import { expectAccountMenuName } from '../helpers/account-menu'
import {
  expandAccountRows,
  openAccountSection,
  selectAccountSection,
} from '../helpers/account-sections'
import { copy, copyPattern } from '../helpers/i18n'
import { approveEnterpriseSignIn } from '../helpers/social'
import {
  addVirtualAuthenticator,
  type VirtualAuthenticator,
} from '../helpers/virtual-authenticator'

// The operator's sign-in, the provider's round trip, the passkey ceremonies and
// the step-up run inside this file's per-test bound; fixture assembly included.
test.describe.configure({ timeout: 420_000 })

/** The seeded platform administrator the review assembly writes. */
const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
} as const

/** The tenant the provider is registered in (the enterprise fixtures' seed). */
const TARGET_TENANT = 'globex'

/** The provider the operator registers for the just-in-time login. */
const PROVIDER_NAME = 'Globex Workforce'

/** The username the employee chooses when the just-in-time login opens the
 * account. Nothing seeds it: the step collects it before any account exists. */
const JIT_USERNAME = 'globex-lastway'

/** The password the account sets after the refused unlink. */
const NEW_PASSWORD = 'e2e-Password-789'

/** The product's sign-in surface heading. */
function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

/** The tenant area's home heading, the rendered proof of a landed session. */
function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

/** The account settings' security pane heading, the page's one h1. */
function securityHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'account.sections.security'),
    exact: true,
  })
}

/** One account section card, named by its own h2 title. */
function section(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

/** The security pane's sign-in-ways card. */
function identifiersSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.loginWays'))
}

/** The security pane's two-step-verification card. */
function mfaSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.mfa'))
}

/** The security pane's social accounts card. */
function bindingsSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.social'))
}

/** The profile pane's phone card. */
function phoneCard(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.phone'))
}

/** The profile pane's email card, which lists the contact addresses. */
function emailCard(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.email'))
}

/** The bound enterprise identity's row, labelled by its provider family. */
function ssoRow(page: Page): Locator {
  return bindingsSection(page)
    .getByRole('listitem')
    .filter({ hasText: copy('accountUi', 'bindings.provider.oidc') })
}

/**
 * The row's unlink action. An enterprise binding is named by its raw provider
 * (`oidc:<id>`), not by the "Enterprise SSO" family label the row shows, so the
 * accessible name leaves the identifier open.
 */
function ssoUnlink(page: Page): Locator {
  return bindingsSection(page).getByRole('button', {
    name: copyPattern('accountUi', 'bindings.unbindAriaWithProvider', {
      provider: 'oidc:.+',
    }),
  })
}

/** The sign-in flow's passkey kind, on the factor and the enroll steps alike. */
function passkeyKind(scope: Page | Locator): Locator {
  return scope.getByRole('button', {
    name: copy('authUi', 'signInFlow.factorKind.webauthn'),
    exact: true,
  })
}

/** The verification banner's link, which opens the address's proof. */
function verificationBanner(page: Page): Locator {
  return page.getByRole('region', {
    name: `${copy('demo', 'verificationBanner.region')}: ${copy(
      'demo',
      'verificationBanner.pending',
      { email: OPERATOR.identifier },
    )}`,
    exact: true,
  })
}

/** The profile pane's email card, which carries the verification surface. */
function verificationSection(page: Page): Locator {
  return emailCard(page)
}

/** The header's admin entry (a link, while the operator may reach it). */
function adminEntry(page: Page): Locator {
  return page
    .getByRole('banner')
    .getByRole('link', { name: copy('demo', 'nav.admin'), exact: true })
}

/** The admin's navigation landmark. */
function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

/** One admin navigation link by its page title. */
function adminLink(page: Page, label: string): Locator {
  return adminNav(page).getByRole('link', { name: label, exact: true })
}

/** The tenant selector of a tenant-addressed admin page. */
function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'common.tenant'),
    exact: true,
  })
}

/** The SSO page's provider write section. */
function providerSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.provider.title'),
    exact: true,
  })
}

/** The SSO page's domain section. */
function domainSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.domains.title'),
    exact: true,
  })
}

/** The SSO page's provider list row for one registered provider. */
function providerRow(page: Page, name: string): Locator {
  return page
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: name })
}

/**
 * Register a passkey on the enroll step the sign-in flow parks on. An enrolling
 * login's registration step stays parked until the viewer clicks, so the prompt
 * and then the flow's own submit are the two interactions.
 */
async function registerPasskeyOnSignIn(scope: Page | Locator): Promise<void> {
  const choice = passkeyKind(scope)
  await expect(choice).toBeVisible({ timeout: 30_000 })
  await choice.click()
  const prompt = scope.getByText(
    copy('authUi', 'signInFlow.passkeyRegisterPrompt'),
    { exact: true },
  )
  await expect(prompt).toBeVisible({ timeout: 15_000 })
  await scope
    .getByRole('button', {
      name: copy('authUi', 'signInFlow.submit'),
      exact: true,
    })
    .click()
  await expect(prompt).toHaveCount(0, { timeout: 20_000 })
}

/** Confirm one danger dialog by the label the caller's section supplies. */
async function confirmDialog(
  page: Page,
  title: string,
  confirmLabel: string,
): Promise<void> {
  const dialog = page.getByRole('dialog', { name: title, exact: true })
  await expect(dialog).toBeVisible()
  await dialog
    .getByRole('button', { name: confirmLabel, exact: true })
    .click()
}

/** Clear the shared step-up challenge with a passkey the account holds. */
async function proveWithPasskey(page: Page): Promise<void> {
  const dialog = page.getByRole('dialog', {
    name: copy('accountUi', 'mfa.stepUp.title'),
    exact: true,
  })
  await expect(dialog).toBeVisible({ timeout: 15_000 })
  const passkey = dialog.getByRole('button', {
    name: copy('accountUi', 'mfa.factor.kind.webauthn'),
    exact: true,
  })
  await expect(passkey).toBeVisible({ timeout: 15_000 })
  await passkey.click()

  // Choosing the passkey starts the assertion ceremony on arrival, so there
  // is no start control. When the ceremony settles, the ready state's
  // `complete` step is the dialog's own confirmation and the dialog closes
  // without a click; the prompt leaving is what distinguishes that settled
  // step from the ceremony still running.
  await expect(
    dialog.getByText(copy('accountUi', 'flowStep.passkeyPrompt'), {
      exact: true,
    }),
  ).toHaveCount(0, { timeout: 30_000 })
  await expect(dialog).toHaveCount(0, { timeout: 30_000 })
}

/** Select one organization through a tenant-addressed page's selector. */
async function selectTenant(page: Page, tenant: string): Promise<void> {
  await tenantSelector(page).click()
  await page.getByRole('option', { name: tenant, exact: true }).click()
}

/**
 * Register one enabled OIDC just-in-time provider through the admin's provider
 * form, naming the local fixture's issuer, client and claim mapping. The
 * provider belongs to the tenant the page is switched to.
 */
async function registerJITProvider(
  page: Page,
  provider: EnterpriseIdentityProvider,
  name: string,
): Promise<void> {
  const form = providerSection(page)
  const protocol = form.getByLabel(
    copy('adminUi', 'sso.provider.protocolLabel'),
    { exact: true },
  )
  const nameField = form.getByLabel(
    copy('adminUi', 'sso.provider.nameLabel'),
    { exact: true },
  )
  const provisioningField = form.getByLabel(
    copy('adminUi', 'sso.provider.provisioningLabel'),
    { exact: true },
  )
  const create = form.getByRole('button', {
    name: copy('adminUi', 'sso.provider.createSubmit'),
    exact: true,
  })

  await protocol.fill('oidc')
  await nameField.fill(name)
  await provisioningField.fill('jit')

  // The enabled flag is a three-state field whose untouched value leaves the
  // module's default; the journey's provider is enabled, so the operator
  // states it.
  await form
    .getByRole('combobox', {
      name: copy('adminUi', 'sso.provider.enabledLabel'),
      exact: true,
    })
    .click()
  await page
    .getByRole('option', {
      name: copy('adminUi', 'sso.provider.tri.true'),
      exact: true,
    })
    .click()

  // The provider configuration names the local issuer, its client identifier
  // and the claim mapping that finds the user's address and display name.
  await form
    .getByLabel(copy('adminUi', 'sso.provider.configLabel'), {
      exact: true,
    })
    .fill(
      JSON.stringify({
        issuer: provider.issuer,
        client_id: provider.clientID,
        claims: enterpriseIdentityProviderClaims,
      }),
    )
  await form
    .getByLabel(copy('adminUi', 'sso.provider.clientSecretLabel'), {
      exact: true,
    })
    .fill(enterpriseIdentityProviderClient.secret)
  await create.click()

  // The write confirms the created provider by name, the form returns to its
  // initial state, and the list carries the provider under this tenant.
  await expect(
    page.getByText(copy('adminUi', 'sso.provider.created', { name }), {
      exact: true,
    }),
  ).toBeVisible()
  await expect(nameField).toHaveValue('')

  const row = providerRow(page, name)
  await expect(row).toHaveCount(1)
  await expect(row.getByText(name, { exact: true })).toBeVisible()
  await expect(row.getByText('oidc', { exact: true })).toBeVisible()
  await expect(
    row.getByText(copy('adminUi', 'sso.enabledYes'), { exact: true }),
  ).toBeVisible()
}

/**
 * Claim the fixture's email domain for one provider, publish the record the
 * one-time panel hands out through the DNS stub, and verify it.
 */
async function claimAndVerifyDomain(
  page: Page,
  stub: DNSStub,
  providerName: string,
): Promise<void> {
  const domains = domainSection(page)
  await domains
    .getByRole('combobox', {
      name: copy('adminUi', 'sso.domains.providerLabel'),
      exact: true,
    })
    .click()
  await page.getByRole('option', { name: providerName, exact: true }).click()
  await domains
    .getByLabel(copy('adminUi', 'sso.domains.domainLabel'), { exact: true })
    .fill(enterpriseIdentityProviderDomain)
  await domains
    .getByRole('button', {
      name: copy('adminUi', 'sso.domains.claim'),
      exact: true,
    })
    .click()

  await expect(
    page.getByText(
      copy('adminUi', 'sso.domains.claimed', {
        domain: enterpriseIdentityProviderDomain,
        name: providerName,
      }),
      { exact: true },
    ),
  ).toBeVisible()

  // The one-time panel carries the DNS token and, beside it, the record name
  // and full record value the operator publishes.
  const panel = page.getByRole('status').filter({
    hasText: copy('adminUi', 'sso.domains.claimToken'),
  })
  await expect(panel).toBeVisible()
  const recordValue = panel.getByText(/^speed-authn=[0-9a-f]{64}$/)
  await expect(recordValue).toBeVisible()
  const publishedValue = ((await recordValue.textContent()) ?? '').trim()

  // FIXTURE ACTION: the operator's own act at the domain's DNS is to publish
  // the record value the panel shows. The stub stands in for that DNS zone and
  // answers only this record name.
  stub.setTXT([publishedValue])

  const domainRow = domains
    .getByRole('listitem')
    .filter({ hasText: enterpriseIdentityProviderDomain })
  await expect(domainRow).toHaveCount(1)
  const verifyButton = domainRow.getByRole('button', {
    name: copy('adminUi', 'sso.domains.verifyAria', {
      domain: enterpriseIdentityProviderDomain,
    }),
    exact: true,
  })
  await expect(verifyButton).toBeVisible()
  await verifyButton.click()

  await expect(
    page.getByText(
      copy('adminUi', 'sso.domains.verified', {
        domain: enterpriseIdentityProviderDomain,
        name: providerName,
      }),
      { exact: true },
    ),
  ).toBeVisible()
  await expect(
    domainRow.getByText(copy('adminUi', 'sso.domains.state.verified'), {
      exact: true,
    }),
  ).toBeVisible()
}

/**
 * The operator's precondition: sign in under the deployment's factor
 * requirement, enrol the passkey that sign-in demands, and prove the account's
 * own address, which the admin's human writes are gated on.
 *
 * The operator's browser context is the spec's own; the account it sets up is
 * the only account the operation leaves for the journey.
 */
async function prepareOperator(
  operatorPage: Page,
  operatorContext: BrowserContext,
  server: DemoTarget['server'],
  operatorAccounts: Accounts,
): Promise<VirtualAuthenticator> {
  await operatorAccounts.signIn({
    identifier: OPERATOR.username,
    password: OPERATOR.password,
  })
  // The deployment requires a factor of every account, so the operator's own
  // sign-in parks at the enroll step before the session exists. The sign-in
  // surface also offers a passkey as a first-factor method, so the step is
  // addressed inside the password channel's own panel.
  const passwordPanel = operatorPage.getByRole('tabpanel', {
    name: copy('authUi', 'passwordSignIn.title'),
    exact: true,
  })
  const authenticator = await addVirtualAuthenticator(
    operatorContext,
    operatorPage,
  )
  await registerPasskeyOnSignIn(passwordPanel)
  await expect(homeHeading(operatorPage)).toBeVisible({ timeout: 30_000 })

  // The journey starts with an operator whose address is proved. The review
  // seed writes the address as an administrator's assertion, so the spec
  // reaches the precondition the way a person does -- the standing notice's
  // verify entry, then the code the review mail carrier writes to the process
  // log.
  const banner = verificationBanner(operatorPage)
  await expect(banner).toBeVisible()
  const beforeCode = server.log.mark()
  await banner
    .getByRole('link', {
      name: copy('demo', 'verificationBanner.verify'),
      exact: true,
    })
    .click()

  const proof = verificationSection(operatorPage)
  await expect(proof).toBeVisible()
  const { code } = await waitForVerificationCode(
    server.log,
    'mail',
    OPERATOR.identifier,
    { after: beforeCode },
  )
  await proof
    .getByLabel(copy('accountUi', 'verification.codeLabel'), { exact: true })
    .fill(code)
  await proof
    .getByRole('button', {
      name: copy('accountUi', 'verification.verify'),
      exact: true,
    })
    .click()
  // The account holds an active factor, so proving the address asks for fresh
  // local proof; the operator's own passkey answers the shared dialog.
  const verified = proof.getByText(
    copy('accountUi', 'verification.verifiedDescription', {
      email: OPERATOR.identifier,
    }),
    { exact: true },
  )
  const stepUp = operatorPage.getByRole('dialog', {
    name: copy('accountUi', 'mfa.stepUp.title'),
    exact: true,
  })
  await expect(verified.or(stepUp)).toBeVisible({ timeout: 15_000 })
  if (await stepUp.isVisible()) {
    await proveWithPasskey(operatorPage)
  }
  await expect(verified).toBeVisible({ timeout: 15_000 })
  await expect(banner).toHaveCount(0)

  return authenticator
}

// The journey drives the provider's real round trip and the domain's TXT
// lookup, and requires a factor of every account.
test.use({ enterpriseSSO: true, mfaRequired: true })

test('last-login-way: removing the last way that is not a passkey is refused', async ({
  demo,
  browser,
}) => {
  const { page, server, accounts } = demo
  const provider = server.enterpriseIdentityProvider
  if (provider === undefined) {
    throw new Error(
      'the enterprise fixture did not start the identity provider this spec drives',
    )
  }
  const stub = server.dnsStub
  if (stub === undefined) {
    throw new Error(
      'the enterprise fixture did not start the DNS stub this spec publishes into',
    )
  }

  let operatorContext: BrowserContext | undefined
  let operatorAuthenticator: VirtualAuthenticator | undefined
  let authenticator: VirtualAuthenticator | undefined

  try {
    await test.step('precondition: the operator registers a just-in-time provider and verifies its email domain', async () => {
      // The operator is subject to the same factor requirement as every other
      // account, so its sign-in enrols a passkey. A separate context keeps
      // that device's credential away from the account this journey opens.
      operatorContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      const operatorPage = await operatorContext.newPage()
      const operatorAccounts = createAccounts({
        page: operatorPage,
        server,
      })
      operatorAuthenticator = await prepareOperator(
        operatorPage,
        operatorContext,
        server,
        operatorAccounts,
      )

      await operatorPage
        .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
        .click()
      await expect(homeHeading(operatorPage)).toBeVisible()

      await expect(adminEntry(operatorPage)).toBeVisible()
      await adminEntry(operatorPage).click()
      await expect(adminNav(operatorPage)).toBeVisible()
      await adminLink(operatorPage, copy('adminUi', 'sso.title')).click()
      await expect(
        operatorPage.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'sso.title'),
          exact: true,
        }),
      ).toBeVisible()

      // No tenant is chosen yet, so the page prompts for one and renders no
      // provider list.
      await selectTenant(operatorPage, TARGET_TENANT)
      await expect(
        operatorPage.getByRole('heading', {
          name: copy('adminUi', 'sso.empty.title'),
          exact: true,
        }),
      ).toBeVisible()

      await registerJITProvider(operatorPage, provider, PROVIDER_NAME)
      await claimAndVerifyDomain(operatorPage, stub, PROVIDER_NAME)

      // The operator's work is done; its context is closed so its session does
      // not linger beside the journey's own.
      await operatorAuthenticator.close()
      operatorAuthenticator = undefined
      await operatorContext.close()
      operatorContext = undefined
    })

    await test.step('the enterprise just-in-time login opens the account with a username and no password', async () => {
      await accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()

      // The provider appears only after the address's domain is discovered.
      const email = page.getByRole('textbox', {
        name: copy('authUi', 'enterprise.emailLabel'),
        exact: true,
      })
      await expect(email).toBeVisible()
      await expect(
        page.getByRole('button', { name: PROVIDER_NAME, exact: true }),
      ).toHaveCount(0)

      await email.fill(enterpriseIdentityProviderUser.email)
      await page
        .getByRole('button', {
          name: copy('authUi', 'enterprise.continue'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('button', { name: PROVIDER_NAME, exact: true }),
      ).toBeVisible()

      await page
        .getByRole('button', { name: PROVIDER_NAME, exact: true })
        .click()
      await approveEnterpriseSignIn(page)

      // The callback parks the first login at the username step: the account is
      // opened by the answer, not before it.
      const username = page.getByRole('textbox', {
        name: copy('authUi', 'signInFlow.usernameLabel'),
        exact: true,
      })
      await expect(username).toBeVisible({ timeout: 15_000 })
      await accounts.answerSignupUsername(JIT_USERNAME)

      // The deployment requires a factor, so the flow parks at the enroll step
      // before the session exists. The passkey registered here belongs to the
      // login, so it does not raise the session's level: the session records
      // the enterprise identity alone, which is what lets this passkey answer
      // the later step-up.
      await expect(
        page.getByText(copy('authUi', 'signInFlow.enrollTitle'), {
          exact: true,
        }),
      ).toBeVisible({ timeout: 30_000 })
      authenticator = await addVirtualAuthenticator(page.context(), page)
      await registerPasskeyOnSignIn(page)
      await expect(homeHeading(page)).toBeVisible({ timeout: 30_000 })

      // The account carries the provider's display name, because no
      // administrator prepared one.
      await expectAccountMenuName(
        page.getByTestId('account-entry-trigger'),
        enterpriseIdentityProviderUser.name,
      )
    })

    await test.step('the account page lists the username and the enterprise identity, no contact, and one passkey', async () => {
      await openAccountSection(page, 'security')
      await expandAccountRows(page)

      // The account's only login name is the username it chose: the enterprise
      // login wrote no address and no phone.
      const identifiers = identifiersSection(page)
      await expect(
        identifiers.getByText(
          copy('accountUi', 'identifiers.usage.loginAs', {
            kind: copy('accountUi', 'identifiers.kind.username'),
            value: JIT_USERNAME,
          }),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        identifiers.getByText(copy('accountUi', 'identifiers.usage.emailNone'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        identifiers.getByText(copy('accountUi', 'identifiers.usage.phoneNone'), {
          exact: true,
        }),
      ).toBeVisible()

      // The enterprise identity is listed as a login way of its own.
      await expect(ssoRow(page)).toBeVisible()

      // No password is set, so the password section offers the first-password
      // form and no current-password field.
      const password = section(page, copy('accountUi', 'password.title'))
      await expect(
        password.getByRole('textbox', {
          name: copy('accountUi', 'password.current.label'),
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(
        password.getByRole('textbox', {
          name: copy('accountUi', 'password.new.label'),
          exact: true,
        }),
      ).toBeVisible()

      // The passkey registered during the sign-in is the account's one active
      // factor: its group holds that one row, the row's own actions sit beside
      // it, and the group's action reads as a plain add.
      const passkeyGroup = mfaSection(page).getByRole('region', {
        name: copy('accountUi', 'mfa.factor.kind.webauthn'),
        exact: true,
      })
      await expect(passkeyGroup.getByRole('listitem')).toHaveCount(1)
      await expect(
        passkeyGroup.getByRole('button', {
          name: copy('accountUi', 'mfa.rename'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        passkeyGroup.getByRole('button', {
          name: copy('accountUi', 'mfa.remove'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        passkeyGroup.getByRole('button', {
          name: copy('accountUi', 'mfa.addAria', {
            method: copy('accountUi', 'mfa.factor.kind.webauthn'),
          }),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        passkeyGroup.getByText(
          copy('accountUi', 'mfa.factor.status.disabled'),
          { exact: true },
        ),
      ).toHaveCount(0)

      // The contact list is empty: the profile pane's email card lists no
      // address row, and the phone card names no number.
      await selectAccountSection(page, 'profile')
      await expandAccountRows(page)
      await expect(emailCard(page).getByRole('row')).toHaveCount(0)
      await expect(
        phoneCard(page).getByText(copy('demo', 'account.rows.value.notBound'), {
          exact: true,
        }),
      ).toBeVisible()
      // The unlink step drives the security pane's social card.
      await selectAccountSection(page, 'security')
      await expandAccountRows(page)
    })

    await test.step('unlinking the enterprise identity asks for fresh proof and is then refused', async () => {
      await ssoUnlink(page).click()
      await confirmDialog(
        page,
        copy('accountUi', 'bindings.confirmTitle'),
        copy('accountUi', 'bindings.confirmLabel'),
      )

      // The unlink asks for fresh local multi-factor proof. The session proved
      // the enterprise identity alone (the passkey registered during the login
      // does not count), and the passkey's channel differs from it, so the
      // shared step-up dialog offers the passkey and the owner answers it in
      // the page.
      await proveWithPasskey(page)

      // The won challenge replays the unlink, which the account module then
      // refuses to remove as the last non-passkey way: the refusal renders in
      // the bindings section's result region, and the row is still listed.
      await expect(
        bindingsSection(page)
          .getByRole('alert')
          .filter({ hasText: copy('accountUi', 'errors.authn.last_login_method') }),
      ).toBeVisible({ timeout: 15_000 })
      await expect(ssoRow(page)).toBeVisible()
      await expect(
        bindingsSection(page).getByText(
          copyPattern('accountUi', 'bindings.unlinked', {
            provider: 'oidc:.+',
          }),
        ),
      ).toHaveCount(0)
    })

    await test.step('the owner sets a password from the account page', async () => {
      const password = section(page, copy('accountUi', 'password.title'))
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.new.label'),
          exact: true,
        })
        .fill(NEW_PASSWORD)
      await password
        .getByRole('textbox', {
          name: copy('accountUi', 'password.confirm.label'),
          exact: true,
        })
        .fill(NEW_PASSWORD)
      await password
        .getByRole('button', {
          name: copy('accountUi', 'password.submitFirst'),
          exact: true,
        })
        .click()
      await expect(
        page.getByText(copy('accountUi', 'password.changedFirst'), {
          exact: true,
        }),
      ).toBeVisible({ timeout: 15_000 })
      // The write keeps the page on the security pane that started it.
      await expect(securityHeading(page)).toBeVisible()
    })

    await test.step('the same unlink now succeeds', async () => {
      await ssoUnlink(page).click()
      await confirmDialog(
        page,
        copy('accountUi', 'bindings.confirmTitle'),
        copy('accountUi', 'bindings.confirmLabel'),
      )
      await expect(
        bindingsSection(page).getByText(
          copyPattern('accountUi', 'bindings.unlinked', {
            provider: 'oidc:.+',
          }),
        ),
      ).toBeVisible({ timeout: 15_000 })
      await expect(ssoUnlink(page)).toHaveCount(0)
    })
  } finally {
    await authenticator?.close().catch(() => undefined)
    await operatorAuthenticator?.close().catch(() => undefined)
    await operatorContext?.close().catch(() => undefined)
  }
})
