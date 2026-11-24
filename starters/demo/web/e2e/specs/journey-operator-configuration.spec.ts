// @journey journey-operator-configuration.md
// @covers REQ-platform-admin-001, REQ-platform-admin-029, REQ-platform-admin-031, REQ-platform-config-001, REQ-platform-config-003, REQ-platform-config-007, REQ-signup-002, REQ-signup-003, REQ-presentation-027
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import {
  adminEntry,
  openAccountMenu,
  organizationRow,
} from '../helpers/account-menu'
import { copy } from '../helpers/i18n'

// The settings table renders its type column only from `lg`; keep the viewport wide enough for the key, type and current value this spec reads.
test.use({ viewport: { width: 1920, height: 1080 } })

const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
  organization: 'acme',
} as const

const SYSTEM_ORGANIZATION = 'system'

async function enterSystemOrganization(page: Page): Promise<void> {
  await openAccountMenu(page)
  const currentRow = organizationRow(page, OPERATOR.organization)
  await expect(currentRow).toBeVisible()
  await currentRow.click()
  const row = page
    .getByRole('menu')
    .getByRole('menuitem', { name: SYSTEM_ORGANIZATION, exact: true })
  await expect(row).toBeVisible()
  await row.click()
  await expect(page.getByRole('menu')).toHaveCount(0)
  await expect(
    page.getByRole('status').filter({
      hasText: copy('tenancyUi', 'tenantSwitcher.switchedTo', {
        tenant: SYSTEM_ORGANIZATION,
      }),
    }),
  ).toBeVisible()
}

const SETTING_KEY = 'authn.account.password-signup'
const SETTING_NAMESPACE = 'authn.account'
const SETTING_TYPE = 'string'

const OPEN = 'open'
const CLOSED = 'closed'

const FOREIGN_KEY = 'ratelimit.limit'

const SIBLING_KEY = 'authn.account.signup'

function verificationBanner(page: Page, email: string): Locator {
  return page.getByRole('region', {
    name: `${copy('demo', 'verificationBanner.region')}: ${copy(
      'demo',
      'verificationBanner.pending',
      { email },
    )}`,
    exact: true,
  })
}

function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
    exact: true,
  })
}

function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

function namespaceNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'settings.nav.label'),
    exact: true,
  })
}

function settingsTable(page: Page): Locator {
  return page.getByRole('table')
}

function settingRow(page: Page): Locator {
  return settingsTable(page)
    .getByRole('row')
    .filter({
      has: page.getByRole('textbox', { name: SETTING_KEY, exact: true }),
    })
}

function valueField(page: Page): Locator {
  return page.getByRole('textbox', { name: SETTING_KEY, exact: true })
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function term(page: Page, label: string): Locator {
  return page.locator('dt', {
    hasText: new RegExp(`^${escapeRegExp(label)}$`),
  })
}

function fieldValue(page: Page, label: string): Locator {
  return term(page, label).locator('xpath=following-sibling::dd[1]')
}

function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

function registerHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.registerHeading'),
    exact: true,
  })
}

function registerEntry(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'mode.registerEntry'),
    exact: true,
  })
}

function passwordFormParts(page: Page): readonly Locator[] {
  return [
    page.getByLabel(copy('authUi', 'register.usernameLabel'), { exact: true }),
    page.getByLabel(copy('authUi', 'register.emailLabel'), { exact: true }),
    page.getByLabel(copy('authUi', 'register.passwordLabel'), { exact: true }),
    page.getByLabel(copy('authUi', 'register.confirmPasswordLabel'), {
      exact: true,
    }),
    page.getByRole('button', {
      name: copy('authUi', 'register.submit'),
      exact: true,
    }),
    page.getByText(copy('authUi', 'register.description'), { exact: true }),
  ]
}

function saveButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'settings.saveBar.save'),
    exact: true,
  })
}

function datetimeBound(instant: Date, slackMs: number): string {
  return new Date(instant.getTime() + slackMs).toISOString().slice(0, 16)
}

test('operator-configuration: a switch is closed and reopened, and the audit trail carries both writes', async ({
  demo,
  browser,
}) => {
  const { page, server } = demo
  let firstSaveAt: Date | undefined
  let visitor: BrowserContext | undefined

  try {
    await test.step('precondition: the operator signs in and proves the account email', async () => {
      await demo.accounts.signIn({
        identifier: OPERATOR.username,
        password: OPERATOR.password,
      })
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toBeVisible()
      const banner = verificationBanner(page, OPERATOR.identifier)
      await expect(banner).toBeVisible()

      const beforeCode = server.log.mark()
      await banner
        .getByRole('link', {
          name: copy('demo', 'verificationBanner.verify'),
          exact: true,
        })
        .click()

      const section = verificationSection(page)
      await expect(section).toBeVisible()
      const codeField = section.getByLabel(
        copy('accountUi', 'verification.codeLabel'),
        { exact: true },
      )
      await expect(codeField).toBeFocused()

      const { code } = await waitForVerificationCode(
        server.log,
        'mail',
        OPERATOR.identifier,
        { after: beforeCode },
      )
      await codeField.fill(code)
      await section
        .getByRole('button', {
          name: copy('accountUi', 'verification.verify'),
          exact: true,
        })
        .click()

      await expect(
        page.getByRole('region', {
          name: copy('accountUi', 'verification.verifiedTitle'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(verificationBanner(page, OPERATOR.identifier)).toHaveCount(0)
    })

    await test.step('step 1: the admin settings page lists the declared items grouped by namespace', async () => {
      await page
        .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toBeVisible()

      await enterSystemOrganization(page)
      const adminCard = page
        .getByRole('heading', {
          level: 2,
          name: copy('demo', 'home.cards.admin.title'),
          exact: true,
        })
        .locator('xpath=ancestor::a[1]')
      await expect(adminCard).toBeVisible()
      const cardHref = await adminCard.getAttribute('href')
      expect(cardHref).not.toBeNull()

      await openAccountMenu(page)
      const entry = adminEntry(page)
      await expect(entry).toBeVisible()
      const entryHref = await entry.getAttribute('href')
      expect(entryHref).not.toBeNull()
      expect(entryHref).toBe(cardHref)

      await entry.click()

      await adminNav(page)
        .getByRole('link', {
          name: copy('adminUi', 'settings.title'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'settings.title'),
          exact: true,
        }),
      ).toBeVisible()

      await expect(
        namespaceNav(page).getByRole('button', {
          name: copy('adminUi', 'settings.nav.all'),
          exact: true,
        }),
      ).toBeVisible()
      await namespaceNav(page)
        .getByRole('button', { name: SETTING_NAMESPACE, exact: true })
        .click()
      await expect(valueField(page)).toBeVisible()
      await expect(
        page.getByRole('textbox', { name: SIBLING_KEY, exact: true }),
      ).toBeVisible()
      await expect(
        page.getByRole('textbox', { name: FOREIGN_KEY, exact: true }),
      ).toHaveCount(0)

      const row = settingRow(page)
      await expect(row.getByText(SETTING_KEY, { exact: true })).toBeVisible()
      await expect(row.getByText(SETTING_TYPE, { exact: true })).toBeVisible()
      await expect(valueField(page)).toHaveValue(OPEN)
      await expect(
        row.getByText(copy('adminUi', 'settings.state.overridden'), {
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 2: the switch is saved as closed and the row re-reads it', async () => {
      const row = settingRow(page)

      await valueField(page).fill(CLOSED)
      firstSaveAt = new Date()
      await saveButton(page).click()

      await expect(
        page.getByText(copy('adminUi', 'settings.saved'), { exact: true }),
      ).toBeVisible()
      await expect(
        row.getByText(copy('adminUi', 'settings.state.overridden'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect
        .poll(() => valueField(page).inputValue(), {
          message: 'the row did not re-read the written value',
        })
        .toBe(CLOSED)

      await page
        .getByRole('button', {
          name: copy('adminUi', 'common.refresh'),
          exact: true,
        })
        .click()
      await expect
        .poll(() => valueField(page).inputValue(), {
          message: 'the refreshed row lost the written value',
        })
        .toBe(CLOSED)
      await expect(
        row.getByText(copy('adminUi', 'settings.state.overridden'), {
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 3: the anonymous surface keeps the register entry but drops the password form', async () => {
      visitor = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      const anonymous = await visitor.newPage()
      await anonymous.goto(`${server.url}/`)
      await expect(signInHeading(anonymous)).toBeVisible()

      await expect(registerEntry(anonymous)).toBeVisible()
      await registerEntry(anonymous).click()
      await expect(registerHeading(anonymous)).toBeVisible()

      for (const part of passwordFormParts(anonymous)) {
        await expect(part).toHaveCount(0)
      }

      await expect(
        anonymous.getByRole('button', {
          name: copy('authUi', 'social.provider.google'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        anonymous.getByRole('button', {
          name: copy('authUi', 'mode.signInEntry'),
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 4: the switch is saved as open again', async () => {
      const row = settingRow(page)

      await valueField(page).fill(OPEN)
      await saveButton(page).click()

      await expect(
        page.getByText(copy('adminUi', 'settings.saved'), { exact: true }),
      ).toBeVisible()
      await expect(
        row.getByText(copy('adminUi', 'settings.state.overridden'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect
        .poll(() => valueField(page).inputValue(), {
          message: 'the row did not re-read the rewritten value',
        })
        .toBe(OPEN)
    })

    await test.step('step 5: the reloaded anonymous surface offers the password form again', async () => {
      if (visitor === undefined) {
        throw new Error('the anonymous context was never created')
      }
      const anonymous = visitor.pages()[0]
      if (anonymous === undefined) {
        throw new Error('the anonymous context has no page')
      }

      await anonymous.reload()
      await expect(signInHeading(anonymous)).toBeVisible()
      await expect(registerEntry(anonymous)).toBeVisible()
      await registerEntry(anonymous).click()
      await expect(registerHeading(anonymous)).toBeVisible()

      for (const part of passwordFormParts(anonymous)) {
        await expect(part).toBeVisible()
      }
    })

    await test.step('step 6: the audit query finds both writes in the time window', async () => {
      if (firstSaveAt === undefined) {
        throw new Error('the first save time was never captured')
      }
      const since = datetimeBound(firstSaveAt, 0)
      const until = datetimeBound(new Date(), 60_000)

      await adminNav(page)
        .getByRole('link', {
          name: copy('adminUi', 'audit.title'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'audit.title'),
          exact: true,
        }),
      ).toBeVisible()

      const table = page.getByRole('table')
      await expect(table.locator('tbody tr').first()).toBeVisible()

      await page
        .getByLabel(copy('adminUi', 'audit.filters.action'), { exact: true })
        .fill('settings.set')
      const otherAction = table
        .locator('tbody tr')
        .filter({ hasNot: page.getByRole('link', { name: 'settings.set' }) })
      await expect(otherAction.first()).toBeVisible()

      const day = 24 * 60 * 60 * 1000
      await page
        .getByLabel(copy('adminUi', 'audit.filters.since'), { exact: true })
        .fill(datetimeBound(new Date(), day))
      await page
        .getByLabel(copy('adminUi', 'audit.filters.until'), { exact: true })
        .fill(datetimeBound(new Date(), day + 60_000))
      await page
        .getByRole('button', {
          name: copy('adminUi', 'audit.filters.apply'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 2,
          name: copy('adminUi', 'audit.empty.title'),
          exact: true,
        }),
      ).toBeVisible()

      await page
        .getByLabel(copy('adminUi', 'audit.filters.since'), { exact: true })
        .fill(since)
      await page
        .getByLabel(copy('adminUi', 'audit.filters.until'), { exact: true })
        .fill(until)
      await page
        .getByRole('button', {
          name: copy('adminUi', 'audit.filters.apply'),
          exact: true,
        })
        .click()

      const rows = table.locator('tbody tr')
      await expect(rows).toHaveCount(2)
      for (const index of [0, 1]) {
        const row = rows.nth(index)
        await expect(
          row.getByRole('link', { name: 'settings.set', exact: true }),
        ).toBeVisible()
        await expect(row.getByText(SETTING_KEY, { exact: true })).toBeVisible()
        await expect(
          row.getByText(`${OPERATOR.displayName} (${OPERATOR.username})`, {
            exact: true,
          }),
        ).toBeVisible()
        await expect(row.getByText('success', { exact: true })).toBeVisible()
      }
    })

    await test.step('step 7: one record opens in full and returns to the query', async () => {
      const table = page.getByRole('table')
      await table
        .getByRole('link', { name: 'settings.set', exact: true })
        .first()
        .click()

      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'auditDetail.title'),
          exact: true,
        }),
      ).toBeVisible()

      for (const field of [
        'auditDetail.field.id',
        'auditDetail.field.tenant',
        'auditDetail.field.occurredAt',
        'auditDetail.field.actor',
        'auditDetail.field.source',
        'auditDetail.field.action',
        'auditDetail.field.object',
        'auditDetail.field.outcome',
        'auditDetail.field.purpose',
      ]) {
        await expect(term(page, copy('adminUi', field))).toBeVisible()
      }
      await expect(
        page.getByRole('heading', {
          level: 3,
          name: copy('adminUi', 'auditDetail.detailTitle'),
          exact: true,
        }),
      ).toBeVisible()

      await expect(
        fieldValue(page, copy('adminUi', 'auditDetail.field.action')),
      ).toHaveText('settings.set')
      await expect(
        fieldValue(page, copy('adminUi', 'auditDetail.field.object')),
      ).toHaveText(SETTING_KEY)
      await expect(
        fieldValue(page, copy('adminUi', 'auditDetail.field.outcome')),
      ).toHaveText('success')
      await expect(
        fieldValue(page, copy('adminUi', 'auditDetail.field.actor')),
      ).toHaveText(`${OPERATOR.displayName} (${OPERATOR.username})`)
      await expect(
        fieldValue(page, copy('adminUi', 'auditDetail.field.tenant')),
      ).toHaveText(copy('adminUi', 'common.emptyValue'))
      await expect(
        fieldValue(page, copy('adminUi', 'auditDetail.field.purpose')),
      ).toHaveText(copy('adminUi', 'common.emptyValue'))

      const detailHeading = page.getByRole('heading', {
        level: 3,
        name: copy('adminUi', 'auditDetail.detailTitle'),
        exact: true,
      })
      const detailList = detailHeading.locator('xpath=following-sibling::dl[1]')
      await expect(
        detailList.getByText(SETTING_KEY, { exact: true }),
      ).toBeVisible()
      await expect(detailList.getByText('global', { exact: true })).toBeVisible()
      await expect(
        fieldValue(page, 'tenant'),
      ).toHaveText(copy('adminUi', 'common.emptyValue'))
      await expect(
        fieldValue(page, 'action_id'),
      ).not.toHaveText(copy('adminUi', 'common.emptyValue'))
      await expect(detailList.getByText(OPEN, { exact: true })).toHaveCount(0)
      await expect(detailList.getByText(CLOSED, { exact: true })).toHaveCount(0)
      await expect(page.locator('dt', { hasText: /^value$/ })).toHaveCount(0)

      await page
        .getByRole('link', {
          name: copy('adminUi', 'auditDetail.back'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'audit.title'),
          exact: true,
        }),
      ).toBeVisible()
    })
  } finally {
    await visitor?.close().catch(() => undefined)
  }
})
