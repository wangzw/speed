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
import { scaledTimeout } from '../fixtures/time-scale'
import { expectAccountMenuName } from '../helpers/account-menu'
import { createAccounts, type Accounts } from '../helpers/accounts'
import { adminEntry, openAccountMenu } from '../helpers/account-menu'
import {
  expandAccountRows,
  openAccountSection,
  selectAccountSection,
} from '../helpers/account-sections'
import { copy, copyPattern } from '../helpers/i18n'
import { chooseSelectOption } from '../helpers/select-option'
import { approveEnterpriseSignIn } from '../helpers/social'
import { switchToTenant } from '../helpers/switch-tenant'
import {
  addVirtualAuthenticator,
  type VirtualAuthenticator,
} from '../helpers/virtual-authenticator'

// The sign-in, the provider round trip, the passkey ceremonies, the step-up and fixture assembly exceed the tier's default; keep this bound above it.
test.describe.configure({ timeout: scaledTimeout(420_000) })

const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
} as const

const TARGET_TENANT = 'globex'

const PROVIDER_NAME = 'Globex Workforce'

const JIT_USERNAME = 'globex-lastway'

const NEW_PASSWORD = 'e2e-Password-789'

function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

function securityHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'account.sections.security'),
    exact: true,
  })
}

function section(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
}

function identifiersSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.loginWays'))
}

function mfaSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.mfa'))
}

function bindingsSection(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.social'))
}

function phoneCard(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.phone'))
}

function emailCard(page: Page): Locator {
  return section(page, copy('demo', 'account.rows.label.email'))
}

function ssoRow(page: Page): Locator {
  return bindingsSection(page)
    .getByRole('listitem')
    .filter({ hasText: copy('accountUi', 'bindings.provider.oidc') })
}

function ssoUnlink(page: Page): Locator {
  return bindingsSection(page).getByRole('button', {
    name: copyPattern('accountUi', 'bindings.unbindAriaWithProvider', {
      provider: 'oidc:.+',
    }),
  })
}

function passkeyKind(scope: Page | Locator): Locator {
  return scope.getByRole('button', {
    name: copy('authUi', 'signInFlow.factorKind.webauthn'),
    exact: true,
  })
}

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

function verificationSection(page: Page): Locator {
  return emailCard(page)
}

function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

function adminLink(page: Page, label: string): Locator {
  return adminNav(page).getByRole('link', { name: label, exact: true })
}

const PLATFORM_TENANT = 'system'

async function openConsole(page: Page): Promise<void> {
  await switchToTenant(page, PLATFORM_TENANT)
  await openAccountMenu(page)
  await expect(adminEntry(page)).toBeVisible()
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
}

function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'common.tenant'),
    exact: true,
  })
}

function providerSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.provider.title'),
    exact: true,
  })
}

function domainSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sso.domains.title'),
    exact: true,
  })
}

function providerRow(page: Page, name: string): Locator {
  return page
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: name })
}

async function registerPasskeyOnSignIn(scope: Page | Locator): Promise<void> {
  const choice = passkeyKind(scope)
  await expect(choice).toBeVisible()
  await choice.click()
  const prompt = scope.getByText(
    copy('authUi', 'signInFlow.passkeyRegisterPrompt'),
    { exact: true },
  )
  await expect(prompt).toBeVisible()
  await scope
    .getByRole('button', {
      name: copy('authUi', 'signInFlow.submit'),
      exact: true,
    })
    .click()
  await expect(prompt).toHaveCount(0)
}

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

async function proveWithPasskey(page: Page): Promise<void> {
  const dialog = page.getByRole('dialog', {
    name: copy('accountUi', 'mfa.stepUp.title'),
    exact: true,
  })
  await expect(dialog).toBeVisible()
  const passkey = dialog.getByRole('button', {
    name: copy('accountUi', 'mfa.factor.kind.webauthn'),
    exact: true,
  })
  await expect(passkey).toBeVisible()
  await passkey.click()

  await expect(
    dialog.getByText(copy('accountUi', 'flowStep.passkeyPrompt'), {
      exact: true,
    }),
  ).toHaveCount(0)
  await expect(dialog).toHaveCount(0)
}

async function selectTenant(page: Page, tenant: string): Promise<void> {
  await chooseSelectOption(
    tenantSelector(page),
    page.getByRole('option', { name: tenant, exact: true }),
  )
}

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

  await chooseSelectOption(
    form.getByRole('combobox', {
      name: copy('adminUi', 'sso.provider.enabledLabel'),
      exact: true,
    }),
    page.getByRole('option', {
      name: copy('adminUi', 'sso.provider.tri.true'),
      exact: true,
    }),
  )

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

async function claimAndVerifyDomain(
  page: Page,
  stub: DNSStub,
  providerName: string,
): Promise<void> {
  const domains = domainSection(page)
  await chooseSelectOption(
    domains.getByRole('combobox', {
      name: copy('adminUi', 'sso.domains.providerLabel'),
      exact: true,
    }),
    page.getByRole('option', { name: providerName, exact: true }),
  )
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

  const panel = page.getByRole('status').filter({
    hasText: copy('adminUi', 'sso.domains.claimToken'),
  })
  await expect(panel).toBeVisible()
  const recordValue = panel.getByText(/^speed-authn=[0-9a-f]{64}$/)
  await expect(recordValue).toBeVisible()
  const publishedValue = ((await recordValue.textContent()) ?? '').trim()

  stub.setTXT([publishedValue])

  const domainRow = domains
    .getByRole('listitem')
    .filter({ hasText: enterpriseIdentityProviderDomain })
  await expect(domainRow).toHaveCount(1)
  const domainActions = domainRow.getByRole('button', {
    name: copy('adminUi', 'sso.domains.actionsAria', {
      domain: enterpriseIdentityProviderDomain,
    }),
    exact: true,
  })
  await expect(domainActions).toBeVisible()
  await domainActions.click()
  const verifyItem = page.getByRole('menuitem', {
    name: copy('adminUi', 'sso.domains.verify'),
    exact: true,
  })
  await expect(verifyItem).toBeVisible()
  await verifyItem.click()

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
  const passwordPanel = operatorPage.getByRole('tabpanel', {
    name: copy('authUi', 'passwordSignIn.title'),
    exact: true,
  })
  const authenticator = await addVirtualAuthenticator(
    operatorContext,
    operatorPage,
  )
  await registerPasskeyOnSignIn(passwordPanel)
  await expect(homeHeading(operatorPage)).toBeVisible()

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
  await expect(verified.or(stepUp)).toBeVisible()
  if (await stepUp.isVisible()) {
    await proveWithPasskey(operatorPage)
  }
  await expect(verified).toBeVisible()
  await expect(banner).toHaveCount(0)

  return authenticator
}

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

      await openConsole(operatorPage)
      await adminLink(operatorPage, copy('adminUi', 'sso.title')).click()
      await expect(
        operatorPage.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'sso.title'),
          exact: true,
        }),
      ).toBeVisible()

      await selectTenant(operatorPage, TARGET_TENANT)
      await expect(
        operatorPage.getByRole('heading', {
          name: copy('adminUi', 'sso.empty.title'),
          exact: true,
        }),
      ).toBeVisible()

      await registerJITProvider(operatorPage, provider, PROVIDER_NAME)
      await claimAndVerifyDomain(operatorPage, stub, PROVIDER_NAME)

      await operatorAuthenticator.close()
      operatorAuthenticator = undefined
      await operatorContext.close()
      operatorContext = undefined
    })

    await test.step('the enterprise just-in-time login opens the account with a username and no password', async () => {
      await accounts.openSignIn()
      await expect(signInHeading(page)).toBeVisible()

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

      const username = page.getByRole('textbox', {
        name: copy('authUi', 'signInFlow.usernameLabel'),
        exact: true,
      })
      await expect(username).toBeVisible()
      await accounts.answerSignupUsername(JIT_USERNAME)

      await expect(
        page.getByText(copy('authUi', 'signInFlow.enrollTitle'), {
          exact: true,
        }),
      ).toBeVisible()
      authenticator = await addVirtualAuthenticator(page.context(), page)
      await registerPasskeyOnSignIn(page)
      await expect(homeHeading(page)).toBeVisible()

      await expectAccountMenuName(
        page.getByTestId('account-entry-trigger'),
        enterpriseIdentityProviderUser.name,
      )
    })

    await test.step('the account page lists the username and the enterprise identity, no contact, and one passkey', async () => {
      await openAccountSection(page, 'security')
      await expandAccountRows(page)

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

      await expect(ssoRow(page)).toBeVisible()

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

      await selectAccountSection(page, 'profile')
      await expandAccountRows(page)
      await expect(emailCard(page).getByRole('row')).toHaveCount(0)
      await expect(
        phoneCard(page).getByText(copy('demo', 'account.rows.value.notBound'), {
          exact: true,
        }),
      ).toBeVisible()
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

      await proveWithPasskey(page)

      await expect(
        bindingsSection(page)
          .getByRole('alert')
          .filter({ hasText: copy('accountUi', 'errors.authn.last_login_method') }),
      ).toBeVisible()
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
      ).toBeVisible()
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
      ).toBeVisible()
      await expect(ssoUnlink(page)).toHaveCount(0)
    })
  } finally {
    await authenticator?.close().catch(() => undefined)
    await operatorAuthenticator?.close().catch(() => undefined)
    await operatorContext?.close().catch(() => undefined)
  }
})
