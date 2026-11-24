// @journey journey-operator-first-run.md
// @covers REQ-signin-006, REQ-account-010, REQ-account-015, REQ-account-016, REQ-account-017, REQ-account-019, REQ-account-020, REQ-platform-admin-001, REQ-platform-admin-017, REQ-platform-admin-029, REQ-platform-admin-031, REQ-platform-config-001, REQ-platform-config-003, REQ-presentation-027, REQ-presentation-028
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import {
  expandAccountRows,
  selectAccountSection,
} from '../helpers/account-sections'
import {
  adminEntry,
  backToApp,
  openAccountMenu,
  organizationRow,
} from '../helpers/account-menu'
import { copy, copyPattern } from '../helpers/i18n'

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

const SETTING_KEY = 'ratelimit.limit'
const SETTING_TYPE = 'int'

const SIBLING_KEYS = ['ratelimit.window', 'ratelimit.address-rate'] as const

const FOREIGN_KEY = 'authn.account.signup'

const NEW_VALUE = '150'

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
    name: copy('demo', 'account.rows.label.email'),
    exact: true,
  })
}

function identifiersSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.loginWays'),
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

const AUDIT_LIST_PATH = '/api/v1/system/audit/records'

function observeLoginActor(page: Page): () => Promise<string> {
  let actor = ''
  page.on('response', (response) => {
    if (response.request().method() !== 'GET' || !response.ok()) {
      return
    }
    if (new URL(response.url()).pathname !== AUDIT_LIST_PATH) {
      return
    }
    response
      .json()
      .then(
        (body: {
          readonly entries: readonly {
            readonly action: string
            readonly actor: string
          }[]
        }) => {
          const login = body.entries.find(
            (record) => record.action === 'authn.login' && record.actor !== '',
          )
          if (login !== undefined) actor = login.actor
        },
      )
      .catch(() => undefined)
  })
  return async () => {
    await expect
      .poll(() => actor, {
        message: 'the audit list read never answered the login record actor',
      })
      .not.toBe('')
    return actor
  }
}

const ACCOUNT_ID_ATTRIBUTES = [
  'title',
  'aria-label',
  'aria-description',
  'href',
  'value',
  'placeholder',
] as const

test('operator-first-run: the operator proves the email and lands the first settings write', async ({
  demo,
}) => {
  const { page, server, accounts } = demo
  const loginActor = observeLoginActor(page)

  await test.step('the operator signs in and the pending-email notice names the address', async () => {
    await accounts.signIn({
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
    await expect(
      banner.getByText(
        copy('demo', 'verificationBanner.pending', {
          email: OPERATOR.identifier,
        }),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(
      banner.getByRole('link', {
        name: copy('demo', 'verificationBanner.verify'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      banner.getByRole('link', {
        name: copy('demo', 'verificationBanner.change'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(banner.getByRole('button')).toHaveCount(0)
  })

  await test.step('the admin opens from the account menu entry, and the settings page lists the declared items', async () => {
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

    const nav = adminNav(page)
    await expect(
      nav.getByRole('button', {
        name: copy('adminUi', 'nav.groups.identity'),
        exact: true,
      }),
    ).toBeVisible()

    await nav
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
      .getByRole('button', { name: 'ratelimit', exact: true })
      .click()
    await expect(valueField(page)).toBeVisible()
    for (const key of [SETTING_KEY, ...SIBLING_KEYS]) {
      await expect(
        page.getByRole('textbox', { name: key, exact: true }),
      ).toBeVisible()
    }
    await expect(
      page.getByRole('textbox', { name: FOREIGN_KEY, exact: true }),
    ).toHaveCount(0)

    const row = settingRow(page)
    await expect(row.getByText(SETTING_KEY, { exact: true })).toBeVisible()
    await expect(row.getByText(SETTING_TYPE, { exact: true })).toBeVisible()
    const declaredValue = await valueField(page).inputValue()
    await expect(
      row.getByText(
        copy('adminUi', 'settings.field.defaultValue', {
          value: declaredValue,
        }),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(
      row.getByText(copy('adminUi', 'settings.state.default'), {
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step("an unverified operator's settings write is refused and keeps its input", async () => {
    const row = settingRow(page)
    const declaredValue = await valueField(page).inputValue()

    await valueField(page).fill(NEW_VALUE)
    await page
      .getByRole('button', {
        name: copy('adminUi', 'settings.saveBar.save'),
        exact: true,
      })
      .click()

    const refusal = copy('adminUi', 'errors.authn.contact_required')
    await expect(page.getByRole('alert').filter({ hasText: refusal }))
      .toBeVisible()
    await expect(valueField(page)).toHaveAccessibleDescription(refusal)
    await expect(valueField(page)).toHaveAttribute('aria-invalid', 'true')

    await expect(valueField(page)).toHaveValue(NEW_VALUE)
    await expect(
      row.getByText(copy('adminUi', 'settings.state.default'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      row.getByText(
        copy('adminUi', 'settings.field.defaultValue', {
          value: declaredValue,
        }),
        { exact: true },
      ),
    ).toBeVisible()
  })

  await test.step('the verify entry sends a code and holds the resend control', async () => {
    await openAccountMenu(page)
    await backToApp(page).click()
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
    await expect(
      section.getByText(
        copy('accountUi', 'verification.target', {
          email: OPERATOR.identifier,
        }),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(
      section.getByRole('status').filter({
        hasText: copy('accountUi', 'verification.sent', {
          email: OPERATOR.identifier,
        }),
      }),
    ).toBeVisible()
    const codeField = section.getByLabel(
      copy('accountUi', 'verification.codeLabel'),
      { exact: true },
    )
    await expect(codeField).toBeFocused()
    await expect(
      section.getByRole('button', {
        name: copy('accountUi', 'verification.resend'),
        exact: true,
      }),
    ).toBeDisabled()
    await expect(
      section.getByText(
        copyPattern('accountUi', 'verification.resendIn', {
          seconds: '\\d+',
        }),
      ),
    ).toBeVisible()

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

    await expect(verificationSection(page)).toBeVisible()
    await expect(
      page.getByText(
        copy('accountUi', 'verification.verifiedDescription', {
          email: OPERATOR.identifier,
        }),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(verificationBanner(page, OPERATOR.identifier)).toHaveCount(0)

    await selectAccountSection(page, 'security')
    await expandAccountRows(page)
    const identifiers = identifiersSection(page)
    await expect(
      identifiers.getByText(
        copy('accountUi', 'identifiers.usage.loginAs', {
          kind: copy('accountUi', 'identifiers.kind.username'),
          value: OPERATOR.username,
        }),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(
      identifiers.getByRole('combobox', {
        name: copy('accountUi', 'identifiers.kind.email'),
        exact: true,
      }),
    ).toContainText(OPERATOR.identifier)
  })

  await test.step('the same write now lands and the item reads overridden', async () => {
    await openAccountMenu(page)
    await adminEntry(page).click()
    const nav = adminNav(page)
    await nav
      .getByRole('link', {
        name: copy('adminUi', 'settings.title'),
        exact: true,
      })
      .click()
    await namespaceNav(page)
      .getByRole('button', { name: 'ratelimit', exact: true })
      .click()
    await expect(valueField(page)).toBeVisible()

    const row = settingRow(page)
    await valueField(page).fill(NEW_VALUE)
    await page
      .getByRole('button', {
        name: copy('adminUi', 'settings.saveBar.save'),
        exact: true,
      })
      .click()

    const saved = copy('adminUi', 'settings.saved')
    await expect(page.getByText(saved, { exact: true })).toBeVisible()
    await expect(
      page.getByText(copy('adminUi', 'errors.authn.contact_required'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(
      row.getByText(copy('adminUi', 'settings.state.overridden'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect
      .poll(() => valueField(page).inputValue(), {
        message: 'the row did not re-read the written value',
      })
      .toBe(NEW_VALUE)

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
      .toBe(NEW_VALUE)
    await expect(
      row.getByText(copy('adminUi', 'settings.state.overridden'), {
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('the audit trail records the write, and its detail shows the fields', async () => {
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
    const writeLink = table
      .getByRole('link')
      .filter({ hasText: /^settings\.set$/ })
    await expect(writeLink).toBeVisible()
    const writeRow = writeLink.locator('xpath=ancestor::tr[1]')

    await page
      .getByLabel(copy('adminUi', 'audit.filters.action'), { exact: true })
      .fill('authn.login')
    await page
      .getByRole('button', {
        name: copy('adminUi', 'audit.filters.apply'),
        exact: true,
      })
      .click()
    const loginRow = table
      .getByRole('link', { name: 'authn.login', exact: true })
      .first()
      .locator('xpath=ancestor::tr[1]')
    await expect(loginRow).toBeVisible()
    await expect(
      loginRow.getByText(`${OPERATOR.displayName} (${OPERATOR.username})`, {
        exact: true,
      }),
    ).toHaveCount(2)

    const accountId = await loginActor()
    for (const attribute of ACCOUNT_ID_ATTRIBUTES) {
      await expect(
        loginRow.locator(`[${attribute}*="${accountId}"]`),
        `the login row carried the account id in a ${attribute} attribute`,
      ).toHaveCount(0)
    }

    await page
      .getByRole('button', {
        name: copy('adminUi', 'audit.filters.reset'),
        exact: true,
      })
      .click()
    await expect(writeLink).toBeVisible()

    const outcome = (
      (await writeRow.getByRole('cell').last().textContent()) ?? ''
    ).trim()
    expect(outcome).not.toBe('')

    await page
      .getByLabel(copy('adminUi', 'audit.filters.action'), { exact: true })
      .fill('settings.set')
    await page
      .getByLabel(copy('adminUi', 'audit.filters.outcome'), { exact: true })
      .fill(outcome)
    expect(await table.locator('tbody tr').count()).toBeGreaterThan(1)

    await page
      .getByRole('button', {
        name: copy('adminUi', 'audit.filters.apply'),
        exact: true,
      })
      .click()

    await expect(table.locator('tbody tr')).toHaveCount(1)
    await expect(writeLink).toBeVisible()
    await writeLink.click()

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
    ).toHaveText(outcome)
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.actor')),
    ).toHaveText(`${OPERATOR.displayName} (${OPERATOR.username})`)
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.tenant')),
    ).toHaveText(copy('adminUi', 'common.emptyValue'))
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.purpose')),
    ).toHaveText(copy('adminUi', 'common.emptyValue'))

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
})
