// @journey journey-account-offboarding.md
// @covers REQ-platform-admin-012, REQ-presentation-027, REQ-security-022, REQ-security-025, REQ-signin-006, REQ-signin-007, REQ-signin-030, REQ-tenancy-022
import type { BrowserContext, Locator, Page } from '@playwright/test'

import {
  createAccounts,
  type Accounts,
} from '../helpers/accounts'
import {
  adminEntry,
  closeAccountMenu,
  openAccountMenu,
  organizationRow,
} from '../helpers/account-menu'
import {
  expandAccountRows,
  openAccountSection,
  selectAccountSection,
} from '../helpers/account-sections'
import { expect, test } from '../fixtures/demo'
import { e2eImpersonationFactors } from '../fixtures/config'
import { waitForVerificationCode } from '../fixtures/log'
import type { E2EServer } from '../fixtures/server'
import { scaledTimeout } from '../fixtures/time-scale'
import { copy, copyPattern } from '../helpers/i18n'
import { chooseSelectOption } from '../helpers/select-option'
import { switchToTenant } from '../helpers/switch-tenant'
import { totpCode } from '../helpers/totp'

// The admin tables render their low-priority columns only at desktop widths; keep the viewport wide enough for the status column and row controls this spec reads.
test.use({ seedImpersonation: true, viewport: { width: 1920, height: 1080 } })

const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
} as const

const MEMBER = {
  identifier: 'member',
  password: 'member-dev-secret',
  displayName: 'Demo Member',
} as const

const TENANT = 'acme'

const TOTP_SEED = e2eImpersonationFactors.totpSeed

const WRONG_PASSWORD = 'not-the-member-password'

const ACCOUNT_PROFILE_PATH = '/account/profile'

const PLATFORM_TENANT = 'system'

function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

async function openConsole(page: Page): Promise<void> {
  await switchToTenant(page, PLATFORM_TENANT)
  await openAccountMenu(page)
  await expect(adminEntry(page)).toBeVisible()
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
}

function accountsTable(page: Page): Locator {
  return page.getByRole('table', {
    name: copy('uiKit', 'dataTable.ariaLabel'),
    exact: true,
  })
}

function memberRow(page: Page): Locator {
  return accountsTable(page)
    .locator('tbody tr')
    .filter({ hasText: MEMBER.displayName })
}

function detailTitle(): string {
  return `${MEMBER.displayName} (${MEMBER.identifier})`
}

function manageButton(row: Locator): Locator {
  return row.getByRole('button', {
    name: copyPattern('adminUi', 'accounts.manageAria', { name: '[^"]*' }),
  })
}

function statusSavedNotice(): string {
  return copy('adminUi', 'accounts.statusSaved', {
    account: copy('adminUi', 'common.accountReference.nameWithIdentifier', {
      name: MEMBER.displayName,
      identifier: MEMBER.identifier,
    }),
  })
}

function statusLine(panel: Locator, status: string): Locator {
  return panel.getByText(
    copy('adminUi', 'accounts.detail.statusLine', { value: status }),
    { exact: true },
  )
}

function statusFilter(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'accounts.filter.status'),
    exact: true,
  })
}

function identifierKind(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'accounts.identifierKindLabel'),
    exact: true,
  })
}

function identifierPrefixField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('adminUi', 'accounts.identifierLabel'),
    exact: true,
  })
}

function searchButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'accounts.search'),
    exact: true,
  })
}

/**
 * Walks the tab order forward until the element carrying `label` holds
 * focus. A natively disabled direction is skipped by Tab, so the walk never
 * reaches it and the caller's focus assertion is what fails.
 */
async function tabUntilFocused(page: Page, label: string): Promise<void> {
  for (let step = 0; step < 200; step += 1) {
    await page.keyboard.press('Tab')
    // When a native `disabled` drops the direction from the tab order and
    // Tab lands nowhere, no `:focus` element exists and the default wait
    // would stall each step. The short bound makes that failure path fail
    // fast; the assertion the caller makes is still what reports it.
    const focusedLabel = await page
      .locator(':focus')
      .getAttribute('aria-label', { timeout: scaledTimeout(1_000) })
      .catch(() => null)
    if (focusedLabel === label) {
      return
    }
  }
}

function emptyState(page: Page): Locator {
  return page.getByRole('heading', {
    name: copy('adminUi', 'accounts.empty.title'),
    exact: true,
  })
}

function sessionEndedHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('authUi', 'sessionEnded.title'),
    exact: true,
  })
}

function profileHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'account.sections.profile'),
    exact: true,
  })
}

function sessionsSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.blocks.devices'),
    exact: true,
  })
}

function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}
function identifierField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'passwordSignIn.identifierLabel'),
    exact: true,
  })
}
function passwordField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('authUi', 'passwordSignIn.passwordLabel'),
    exact: true,
  })
}
function signInButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'passwordSignIn.submit'),
    exact: true,
  })
}

async function proveOperatorEmail(
  page: Page,
  server: E2EServer,
): Promise<void> {
  await selectAccountSection(page, 'profile')
  await expandAccountRows(page)
  const email = page.getByRole('region', {
    name: copy('demo', 'account.rows.label.email'),
    exact: true,
  })
  const before = server.log.mark()
  await email
    .getByRole('button', {
      name: copy('accountUi', 'verification.send'),
      exact: true,
    })
    .click()
  const { code } = await waitForVerificationCode(
    server.log,
    'mail',
    OPERATOR.identifier,
    { after: before },
  )
  await email
    .getByRole('textbox', {
      name: copy('accountUi', 'verification.codeLabel'),
      exact: true,
    })
    .fill(code)
  await email
    .getByRole('button', {
      name: copy('accountUi', 'verification.verify'),
      exact: true,
    })
    .click()
  await expect(
    email.getByText(copy('accountUi', 'identifiers.verified'), {
      exact: true,
    }),
  ).toBeVisible()
}

async function signInOperatorWithFactor(
  page: Page,
  accounts: Accounts,
): Promise<void> {
  await accounts.signIn({
    identifier: OPERATOR.username,
    password: OPERATOR.password,
  })
  const passwordPanel = page.getByRole('tabpanel', {
    name: copy('authUi', 'passwordSignIn.title'),
    exact: true,
  })
  await passwordPanel
    .getByRole('button', {
      name: copy('authUi', 'signInFlow.factorKind.totp'),
      exact: true,
    })
    .click()
  const operatorCode = passwordPanel.getByRole('textbox', {
    name: copy('authUi', 'smsSignIn.codeLabel'),
    exact: true,
  })
  await expect(operatorCode).toBeVisible()
  await operatorCode.fill(totpCode(TOTP_SEED))
  await passwordPanel
    .getByRole('button', {
      name: copy('authUi', 'signInFlow.submit'),
      exact: true,
    })
    .click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('demo', 'home.heading'),
      exact: true,
    }),
  ).toBeVisible()
}

test('account-offboarding: an operator disables a member and the member returns after the restore', async ({
  demo,
  browser,
}) => {
  const { page, server, accounts } = demo
  let memberAContext: BrowserContext | undefined
  let memberBContext: BrowserContext | undefined
  let memberAPage: Page | undefined
  let memberBPage: Page | undefined

  try {
    await test.step('precondition: the operator signs in with a factor, and the member holds two sessions', async () => {
      await signInOperatorWithFactor(page, accounts)

      await proveOperatorEmail(page, server)

      const options = {
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      }
      memberAContext = await browser.newContext(options)
      memberBContext = await browser.newContext(options)
      memberAPage = await memberAContext.newPage()
      memberBPage = await memberBContext.newPage()

      const memberA = createAccounts({ page: memberAPage, server })
      const memberB = createAccounts({ page: memberBPage, server })
      await memberA.signIn({
        identifier: MEMBER.identifier,
        password: MEMBER.password,
      })
      await memberB.signIn({
        identifier: MEMBER.identifier,
        password: MEMBER.password,
      })
      await expect(
        memberBPage.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toBeVisible()

      await openAccountSection(memberAPage, 'sessions')
      const sessions = sessionsSection(memberAPage)
      await expect(sessions.getByRole('listitem')).toHaveCount(2)
      await expect(
        sessions.getByText(copy('accountUi', 'sessions.current'), {
          exact: true,
        }),
      ).toHaveCount(1)
      await expect(sessions.getByRole('table')).toHaveCount(0)
      await expect(sessions.getByRole('columnheader')).toHaveCount(0)
      const activityPrefix = copy('accountUi', 'sessions.rowActivity', {
        time: '',
      })
      await expect(
        sessions.getByText(activityPrefix, { exact: false }),
      ).toHaveCount(2)
    })

    await test.step('step 1: the operator enters the admin and opens the accounts page', async () => {
      await openConsole(page)
      await adminNav(page)
        .getByRole('link', {
          name: copy('adminUi', 'accounts.title'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'accounts.title'),
          exact: true,
        }),
      ).toBeVisible()

      await expect(accountsTable(page).locator('tbody tr')).toHaveCount(3)
    })

    await test.step("step 2: the operator filters by the member's identifier and enabled status and opens the detail", async () => {
      await chooseSelectOption(
        identifierKind(page),
        page.getByRole('option', {
          name: copy('adminUi', 'accounts.identifierKind.username'),
          exact: true,
        }),
      )
      await identifierPrefixField(page).fill(MEMBER.identifier)
      await chooseSelectOption(
        statusFilter(page),
        page.getByRole('option', {
          name: copy('adminUi', 'accounts.status.active'),
          exact: true,
        }),
      )

      await expect(accountsTable(page).locator('tbody tr')).toHaveCount(3)

      await searchButton(page).click()
      await expect(accountsTable(page).locator('tbody tr')).toHaveCount(1)
      const row = memberRow(page)
      await expect(row).toHaveCount(1)
      await expect(
        row.getByText(copy('adminUi', 'accounts.status.active'), {
          exact: true,
        }),
      ).toBeVisible()

      await manageButton(row).click()
      const dialog = page.getByRole('dialog', { name: detailTitle() })
      await expect(dialog).toBeVisible()
      await expect(
        statusLine(dialog, copy('adminUi', 'accounts.status.active')),
      ).toBeVisible()
    })

    await test.step('step 3: the operator saves the account as disabled, and the detail and the list show it', async () => {
      const dialog = page.getByRole('dialog', { name: detailTitle() })
      await chooseSelectOption(
        dialog.getByRole('combobox', {
          name: copy('adminUi', 'accounts.status.label'),
          exact: true,
        }),
        page.getByRole('option', {
          name: copy('adminUi', 'accounts.status.disabled'),
          exact: true,
        }),
      )
      await dialog
        .getByRole('button', {
          name: copy('adminUi', 'accounts.status.submit'),
          exact: true,
        })
        .click()

      await expect(
        dialog.getByText(statusSavedNotice(), { exact: true }),
      ).toBeVisible()
      await expect(
        statusLine(dialog, copy('adminUi', 'accounts.status.disabled')),
      ).toBeVisible()

      await dialog
        .getByRole('button', {
          name: copy('uiKit', 'sidePanel.closeLabel'),
          exact: true,
        })
        .click()
      await expect(dialog).toHaveCount(0)

      await expect(emptyState(page)).toBeVisible()
      await chooseSelectOption(
        statusFilter(page),
        page.getByRole('option', {
          name: copy('adminUi', 'accounts.status.disabled'),
          exact: true,
        }),
      )
      await searchButton(page).click()
      const row = memberRow(page)
      await expect(row).toHaveCount(1)
      await expect(
        row.getByText(copy('adminUi', 'accounts.status.disabled'), {
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step("step 4: the member's next request is refused and the page becomes the session-ended surface", async () => {
      if (memberBPage === undefined) {
        throw new Error('the member page was never opened')
      }
      const memberB = memberBPage
      const ended = sessionEndedHeading(memberB)

      if (!(await ended.isVisible())) {
        const trigger = memberB
          .getByTestId('account-entry-trigger')
        if ((await trigger.count()) > 0) {
          await trigger.click().catch(() => undefined)
          await memberB
            .getByRole('menuitem', {
              name: copy('demo', 'account.sections.profile'),
              exact: true,
            })
            .click()
            .catch(() => undefined)
        }
      }

      await expect(ended).toBeVisible()
      await expect(
        memberB.getByText(copy('authUi', 'sessionEnded.description'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        memberB.getByRole('button', {
          name: copy('authUi', 'sessionEnded.signInAction'),
          exact: true,
        }),
      ).toBeVisible()

      await expect(profileHeading(memberB)).toHaveCount(0)
      await expect(
        memberB.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toHaveCount(0)
    })

    await test.step('alternate path: signing in while disabled answers the same failure as a wrong password', async () => {
      if (memberBPage === undefined) {
        throw new Error('the member page was never opened')
      }
      const memberB = memberBPage
      await memberB
        .getByRole('button', {
          name: copy('authUi', 'sessionEnded.signInAction'),
          exact: true,
        })
        .click()
      await expect(signInHeading(memberB)).toBeVisible()

      const failure = memberB.getByText(
        copy('authUi', 'passwordSignIn.loginFailed'),
        { exact: true },
      )

      await identifierField(memberB).fill(MEMBER.identifier)
      await passwordField(memberB).fill(WRONG_PASSWORD)
      await signInButton(memberB).click()
      await expect(failure).toBeVisible()

      await passwordField(memberB).fill(MEMBER.password)
      await signInButton(memberB).click()
      await expect(failure).toBeVisible()
      await expect(
        memberB.getByText(copy('authUi', 'errors.authn.account_unavailable'), {
          exact: true,
        }),
      ).toHaveCount(0)
    })

    await test.step('alternate path: the operator restores the account and the member signs in again in place', async () => {
      if (memberBPage === undefined) {
        throw new Error('the member page was never opened')
      }
      const memberB = memberBPage

      const row = memberRow(page)
      await expect(row).toHaveCount(1)
      await manageButton(row).click()
      const dialog = page.getByRole('dialog', { name: detailTitle() })
      await expect(dialog).toBeVisible()
      await expect(
        statusLine(dialog, copy('adminUi', 'accounts.status.disabled')),
      ).toBeVisible()
      await chooseSelectOption(
        dialog.getByRole('combobox', {
          name: copy('adminUi', 'accounts.status.label'),
          exact: true,
        }),
        page.getByRole('option', {
          name: copy('adminUi', 'accounts.status.active'),
          exact: true,
        }),
      )
      await dialog
        .getByRole('button', {
          name: copy('adminUi', 'accounts.status.submit'),
          exact: true,
        })
        .click()
      await expect(
        dialog.getByText(statusSavedNotice(), { exact: true }),
      ).toBeVisible()
      await expect(
        statusLine(dialog, copy('adminUi', 'accounts.status.active')),
      ).toBeVisible()
      await dialog
        .getByRole('button', {
          name: copy('uiKit', 'sidePanel.closeLabel'),
          exact: true,
        })
        .click()
      await expect(dialog).toHaveCount(0)

      await expect(emptyState(page)).toBeVisible()
      await chooseSelectOption(
        statusFilter(page),
        page.getByRole('option', {
          name: copy('adminUi', 'accounts.status.active'),
          exact: true,
        }),
      )
      await searchButton(page).click()
      await expect(memberRow(page)).toHaveCount(1)
      await expect(
        memberRow(page).getByText(copy('adminUi', 'accounts.status.active'), {
          exact: true,
        }),
      ).toBeVisible()

      await memberB.reload()
      await expect(signInHeading(memberB)).toBeVisible()
      await identifierField(memberB).fill(MEMBER.identifier)
      await passwordField(memberB).fill(MEMBER.password)
      await signInButton(memberB).click()
      await expect(profileHeading(memberB)).toBeVisible()
      expect(new URL(memberB.url()).pathname).toBe(ACCOUNT_PROFILE_PATH)
    })

    await test.step('alternate path: the session list holds only the new device, and the account menu names the organization', async () => {
      if (memberBPage === undefined) {
        throw new Error('the member page was never opened')
      }
      const memberB = memberBPage

      await selectAccountSection(memberB, 'sessions')
      const sessions = sessionsSection(memberB)
      await expect(sessions.getByRole('listitem')).toHaveCount(1)
      await expect(
        sessions.getByText(copy('accountUi', 'sessions.current'), {
          exact: true,
        }),
      ).toHaveCount(1)

      await switchToTenant(memberB, TENANT)
      const menu = await openAccountMenu(memberB)
      await expect(menu.getByText(TENANT, { exact: true })).toBeVisible()
      if ((await organizationRow(memberB, TENANT).count()) > 0) {
        await organizationRow(memberB, TENANT).click()
        await expect(
          menu
            .getByRole('group', {
              name: copy('demo', 'userMenu.organizations'),
              exact: true,
            })
            .getByRole('menuitem', { name: TENANT, exact: true }),
        ).toHaveCount(0)
      }
      await closeAccountMenu(memberB)
    })
  } finally {
    await memberAContext?.close().catch(() => undefined)
    await memberBContext?.close().catch(() => undefined)
  }
})

test('account-offboarding: the save control keeps keyboard focus through its write and explains an unmet precondition', async ({
  demo,
}) => {
  const { page, server, accounts } = demo

  const statusWrites: string[] = []
  page.on('request', (outgoing) => {
    if (outgoing.method() !== 'POST') return
    const path = new URL(outgoing.url()).pathname
    if (
      path.startsWith('/api/v1/system/auth/accounts/') &&
      path.endsWith('/status')
    ) {
      statusWrites.push(path)
    }
  })

  await test.step('the operator signs in with a factor, proves the address and opens the member detail', async () => {
    await signInOperatorWithFactor(page, accounts)
    await proveOperatorEmail(page, server)

    await openConsole(page)
    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'accounts.title'),
        exact: true,
      })
      .click()
    await chooseSelectOption(
      identifierKind(page),
      page.getByRole('option', {
        name: copy('adminUi', 'accounts.identifierKind.username'),
        exact: true,
      }),
    )
    await identifierPrefixField(page).fill(MEMBER.identifier)
    await chooseSelectOption(
      statusFilter(page),
      page.getByRole('option', {
        name: copy('adminUi', 'accounts.status.active'),
        exact: true,
      }),
    )
    await searchButton(page).click()
    const row = memberRow(page)
    await expect(row).toHaveCount(1)
    await manageButton(row).click()
    await expect(
      page.getByRole('dialog', { name: detailTitle() }),
    ).toBeVisible()
  })

  const dialog = page.getByRole('dialog', { name: detailTitle() })
  const statusSelect = dialog.getByRole('combobox', {
    name: copy('adminUi', 'accounts.status.label'),
    exact: true,
  })
  const saveStatus = dialog.getByRole('button', {
    name: copy('adminUi', 'accounts.status.submit'),
    exact: true,
  })

  await test.step('an unmet precondition is Tab-reachable, announced once, and its activation sends nothing', async () => {
    await expect(saveStatus).toHaveAttribute('aria-disabled', 'true')
    await expect(saveStatus).toHaveAccessibleDescription(
      copy('adminUi', 'accounts.status.unchanged'),
    )

    await statusSelect.focus()
    await page.keyboard.press('Tab')
    await expect(saveStatus).toBeFocused()

    const writesBeforeActivation = statusWrites.length
    await page.keyboard.press('Enter')
    await expect(saveStatus).toHaveAttribute('aria-disabled', 'true')
    await expect(saveStatus).toHaveAccessibleDescription(
      copy('adminUi', 'accounts.status.unchanged'),
    )
    expect(
      statusWrites,
      'the unavailable activation sent no request',
    ).toHaveLength(writesBeforeActivation)
  })

  await test.step('a keyboard-started write keeps focus on the control and Tab moves to the next one', async () => {
    await chooseSelectOption(
      statusSelect,
      page.getByRole('option', {
        name: copy('adminUi', 'accounts.status.disabled'),
        exact: true,
      }),
    )
    await expect(saveStatus).not.toHaveAttribute('aria-disabled')

    await saveStatus.focus()
    await expect(saveStatus).toBeFocused()
    const sent = page.waitForRequest((outgoing) => {
      if (outgoing.method() !== 'POST') return false
      const path = new URL(outgoing.url()).pathname
      return (
        path.startsWith('/api/v1/system/auth/accounts/') &&
        path.endsWith('/status')
      )
    })
    const landing = page.waitForResponse((response) => {
      if (response.request().method() !== 'POST') return false
      const path = new URL(response.url()).pathname
      return (
        path.startsWith('/api/v1/system/auth/accounts/') &&
        path.endsWith('/status')
      )
    })

    await page.keyboard.press('Enter')
    await expect(saveStatus).toBeFocused()
    await sent
    await expect(saveStatus).toBeFocused()

    const response = await landing
    expect(response.ok()).toBe(true)
    await expect(
      dialog.getByText(statusSavedNotice(), { exact: true }),
    ).toBeVisible()

    await expect(saveStatus).toHaveAttribute('aria-disabled', 'true')
    await expect(saveStatus).toBeFocused()

    await page.keyboard.press('Tab')
    await expect(
      dialog.getByRole('combobox', {
        name: copy('adminUi', 'accounts.mfa.label'),
        exact: true,
      }),
    ).toBeFocused()

    expect(statusWrites).toHaveLength(1)
  })
})

/**
 * Checks that both paging directions stay in the tab order at a list
 * boundary, announce unavailable, and send no read when activated.
 */
test('account-offboarding: the pager directions stay reachable at the list boundary and start no read', async ({
  demo,
}) => {
  const { page, server, accounts } = demo

  // The accounts list read, observed rather than faked: the unavailable
  // activation must add none.
  const listReads: string[] = []
  page.on('request', (outgoing) => {
    if (outgoing.method() !== 'GET') return
    if (new URL(outgoing.url()).pathname === '/api/v1/system/auth/accounts') {
      listReads.push(outgoing.url())
    }
  })

  await test.step('the operator signs in with a factor, proves the address and opens the accounts list', async () => {
    await signInOperatorWithFactor(page, accounts)
    await proveOperatorEmail(page, server)
    await openConsole(page)
    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'accounts.title'),
        exact: true,
      })
      .click()
    await expect(accountsTable(page)).toBeVisible()
  })

  // MUI's en-US locale object is empty: English is the component's own
  // default. The labels are read from the rendered directions, so the spec
  // carries no English source of its own. The actions slot holds exactly
  // the two directions.
  const actions = page.locator('.MuiTablePagination-actions')
  const previous = actions.getByRole('button').first()
  const next = actions.getByRole('button').last()
  const previousLabel = (await previous.getAttribute('aria-label')) ?? ''
  const nextLabel = (await next.getAttribute('aria-label')) ?? ''
  expect(previousLabel).not.toBe('')
  expect(nextLabel).not.toBe('')
  // The footer's range readout is MUI's own text node; no role names it.
  const displayedRows = page.locator('.MuiTablePagination-displayedRows')

  // The opened roster sent its own read. Without this, a listener that never
  // fired would make every "sends no read" check below pass for the wrong
  // reason.
  await expect
    .poll(() => listReads.length, { timeout: scaledTimeout(10_000) })
    .toBeGreaterThan(0)

  await test.step('both boundary directions announce themselves unavailable and stay focusable', async () => {
    await expect(previous).toHaveAttribute('aria-disabled', 'true')
    await expect(next).toHaveAttribute('aria-disabled', 'true')
    // The aria state, not a native `disabled` that would drop them from the
    // tab order. Playwright's `toBeDisabled` reads `aria-disabled` too, so
    // it cannot tell the two apart.
    await expect(previous).not.toHaveAttribute('disabled')
    await expect(next).not.toHaveAttribute('disabled')
  })

  for (const [direction, name, label] of [
    [previous, 'previous', previousLabel],
    [next, 'next', nextLabel],
  ] as const) {
    await test.step(`the ${name} direction is Tab-reachable, and Enter keeps the page and sends no read`, async () => {
      // From the toolbar, so the walk proves the direction really is in the
      // tab order rather than being focused directly.
      await searchButton(page).focus()
      await tabUntilFocused(page, label)
      await expect(direction).toBeFocused()
      await expect(direction).toHaveAttribute('aria-disabled', 'true')

      const pageText = await displayedRows.textContent()
      const readsBefore = listReads.length
      await page.keyboard.press('Enter')

      await expect(direction).toBeFocused()
      await expect(displayedRows).toHaveText(pageText ?? '')
      // Give a read the activation did start time to reach the listener
      // before the count is compared.
      await page.waitForTimeout(scaledTimeout(250))
      expect(
        listReads,
        'the unavailable activation sent no list read',
      ).toHaveLength(readsBefore)
    })
  }
})
