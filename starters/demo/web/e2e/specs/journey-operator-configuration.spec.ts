/**
 * J11 -- an operator changes a runtime setting, watches it govern the
 * anonymous sign-in surface, changes it back, and finds both writes in the
 * audit trail.
 *
 * The platform administrator's email is proved first (the account page's
 * verification section, whose code comes from the review process log -- the
 * one non-UI read the tier allows), because a settings write from an
 * unproved account is refused. The operator then enters the console, opens
 * the settings page, narrows it to the setting's namespace and finds the
 * password-signup switch: current value "open", state "Overridden" (the
 * review assembly's seed wrote the global row). The operator types "closed"
 * into the row's value cell and saves; the one feedback region says the
 * setting was saved, the row re-reads as overridden with the new value, and
 * a page refresh shows the write is stored, not draft state.
 *
 * The anonymous window is a second browser context with no session. It
 * opens the deployment root, which renders the sign-in surface: the register
 * entry is still there (the first-login switch is still open, and the
 * assembled SMS and social methods can open an account), and register mode
 * still offers those account-opening methods, but the password registration
 * form is gone. The write took effect in the issuing process without a
 * restart. The operator then writes "open" again; after a reload the
 * anonymous login surface offers the password registration form once more,
 * exactly as before the first change.
 *
 * Both saves are then found on the audit page: the since/until bounds cover
 * the whole run, the action filter is settings.set, and applying it (typing
 * alone does not apply; a window a day after the run matches nothing, so the
 * temporal bounds are applied too) leaves the two rows, each with the setting key as
 * its object, the operator as its actor and success as its outcome. One
 * row's detail shows the record's full field set; the platform-level record
 * has no tenant, so the tenant row shows the placeholder; the detail section
 * carries the setting key and its layer ("global") and never the value. The
 * back entry returns to the query. The audit read is treated as eventually
 * consistent: every list assertion retries rather than assuming the rows are
 * present the instant the page opens.
 *
 * Everything is driven through the rendered product with mouse and keyboard:
 * roles and accessible names, visible text, focus and disabled state, the one
 * feedback region, and URL-level navigation. Expected copy is read from the
 * shipped en-US bundles through helpers/i18n.ts. The report's data values
 * (the setting key, its type, the action and outcome names, the layer name)
 * are the product's own answers and are asserted as the rendered data they
 * are.
 *
 * Two requirements the journey document lists are left out of `@covers`
 * because the main path never writes an invalid value: REQ-platform-config-006
 * (an out-of-domain value is refused and not stored) and REQ-presentation-028
 * (a failed write renders its code's text) belong to the document's alternate
 * path, which this spec does not drive.
 *
 * REQ-signup-002 is claimed narrowly: the spec proves the facet where the
 * first-login switch is open and the assembled account-opening methods remain,
 * so the register entry stays visible even after password signup is closed.
 * The other two cases its acceptance names (both switches closed, and only
 * password signup open) are not exercised here.
 */
// @journey journey-operator-configuration.md
// @covers REQ-platform-admin-001, REQ-platform-admin-029, REQ-platform-admin-031, REQ-platform-config-001, REQ-platform-config-003, REQ-platform-config-007, REQ-signup-002, REQ-signup-003, REQ-presentation-027
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import { copy } from '../helpers/i18n'

// The settings table's type column is the lowest-priority one: it renders only
// from its container's `lg` width, so the desktop viewport here is wide enough
// for the key, type and current value the journey's settings step documents.
test.use({ viewport: { width: 1920, height: 1080 } })

/** The seeded platform administrator the review assembly writes. */
const OPERATOR = {
  identifier: 'operator@demo.test',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

/** The one declared setting this journey writes. Its namespace is the module
 * that declared it; the value cell is labelled with the full key. */
const SETTING_KEY = 'authn.account.password-signup'
const SETTING_NAMESPACE = 'authn.account'
const SETTING_TYPE = 'string'

/** The two values this journey writes: the declared domain is closed/open. */
const OPEN = 'open'
const CLOSED = 'closed'

/** A declared key from another namespace, used to prove the narrowing. */
const FOREIGN_KEY = 'ratelimit.limit'

/** A declared sibling of the written item, inside the same namespace. */
const SIBLING_KEY = 'authn.account.signup'

/** The email-verification notice under the app bar. Its accessible name is its
 * purpose followed by the visible message, so the address it names is part of
 * the name. */
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

/** The account page's email-verification section. */
function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
    exact: true,
  })
}

/** The console's navigation landmark (the platform navigation). */
function consoleNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('consoleUi', 'shell.navLabel'),
    exact: true,
  })
}

/** The settings page's namespace navigation. */
function namespaceNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('consoleUi', 'settings.nav.label'),
    exact: true,
  })
}

/** The settings table. */
function settingsTable(page: Page): Locator {
  return page.getByRole('table')
}

/** The table row of the setting this journey writes. */
function settingRow(page: Page): Locator {
  return settingsTable(page)
    .getByRole('row')
    .filter({
      has: page.getByRole('textbox', { name: SETTING_KEY, exact: true }),
    })
}

/** The item's value field: the value cell is the draft input. */
function valueField(page: Page): Locator {
  return page.getByRole('textbox', { name: SETTING_KEY, exact: true })
}

/** Escape the characters a regular expression reads as syntax. */
function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * One definition term of an audit detail. The ARIA `term` role takes no name
 * from its content, so the term is found by its exact text rather than by an
 * accessible name.
 */
function term(page: Page, label: string): Locator {
  return page.locator('dt', {
    hasText: new RegExp(`^${escapeRegExp(label)}$`),
  })
}

/** One label/value row of an audit detail: the value cell beside its term. */
function fieldValue(page: Page, label: string): Locator {
  return term(page, label).locator('xpath=following-sibling::dd[1]')
}

/** The sign-in surface's page heading: the host's own title for the mode. */
function signInHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.heading'),
    exact: true,
  })
}

/** The register-mode page heading the host renders once the mode flips. */
function registerHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'signIn.registerHeading'),
    exact: true,
  })
}

/** The register entry: the button that switches the surface to register mode. */
function registerEntry(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('authUi', 'mode.registerEntry'),
    exact: true,
  })
}

/** The password registration form's pieces, all absent while that switch is
 * closed. They are the form's own content, so the assertion does not depend on
 * how many account-opening channels happen to sit beside it. */
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

/** The save bar's one button. */
function saveButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('consoleUi', 'settings.saveBar.save'),
    exact: true,
  })
}

/**
 * A `datetime-local` input's value for a wall-clock instant, rendered in UTC.
 * The browser is pinned to UTC by the Playwright config, so the field reads a
 * UTC ``YYYY-MM-DDTHH:mm`` as UTC; the spec's own window is deliberately wider
 * than the two saves so a minute boundary can never cut a write out.
 */
function datetimeBound(instant: Date, slackMs: number): string {
  return new Date(instant.getTime() + slackMs).toISOString().slice(0, 16)
}

test('operator-configuration: a switch is closed and reopened, and the audit trail carries both writes', async ({
  demo,
  browser,
}) => {
  const { page, server } = demo
  // The audit window covers the whole run: the bound below is taken before
  // the operator signs in, so every record this journey makes falls inside it.
  const windowStart = new Date()
  let visitor: BrowserContext | undefined

  try {
    await test.step('precondition: the operator signs in and proves the account email', async () => {
      // The journey's precondition is an operator whose email is proved. The
      // review seed writes the address unproved, so the spec reaches the
      // precondition the way a person does: the standing notice's verify entry,
      // the code from the review process log, and the verify action.
      await demo.accounts.signIn({
        identifier: OPERATOR.identifier,
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

      // The cursor is taken before the entry is followed: the verification
      // section sends its one code after the arrival, and only that code may
      // be used.
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

    await test.step('step 1: the console settings page lists the declared items grouped by namespace', async () => {
      // The account page the verification landed on is inside the tenant
      // shell, whose main navigation carries the Home entry: step 1 of the
      // document starts on the home page.
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

      // The home console card and the header entry are two derivations of the
      // same target; the step drives the header one and checks they agree.
      const consoleEntry = page
        .getByRole('banner')
        .getByRole('link', { name: copy('demo', 'nav.console'), exact: true })
      const consoleCard = page
        .getByRole('heading', {
          level: 2,
          name: copy('demo', 'home.cards.console.title'),
          exact: true,
        })
        .locator('xpath=ancestor::a[1]')
      await expect(consoleEntry).toBeVisible()
      await expect(consoleCard).toBeVisible()
      const entryHref = await consoleEntry.getAttribute('href')
      expect(entryHref).not.toBeNull()
      await expect(consoleCard).toHaveAttribute('href', entryHref ?? '')

      await consoleEntry.click()

      // The settings page is opened from the console navigation.
      await consoleNav(page)
        .getByRole('link', {
          name: copy('consoleUi', 'settings.title'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('consoleUi', 'settings.title'),
          exact: true,
        }),
      ).toBeVisible()

      // The namespaces are the grouping the page offers; narrowing to the
      // setting's own namespace leaves that group's items and no other
      // namespace's key.
      await expect(
        namespaceNav(page).getByRole('button', {
          name: copy('consoleUi', 'settings.nav.all'),
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

      // The item names its key, its declared type and its current value; the
      // review assembly's seed wrote the global row, so its state is the
      // override rather than the declaration.
      const row = settingRow(page)
      await expect(row.getByText(SETTING_KEY, { exact: true })).toBeVisible()
      await expect(row.getByText(SETTING_TYPE, { exact: true })).toBeVisible()
      await expect(valueField(page)).toHaveValue(OPEN)
      await expect(
        row.getByText(copy('consoleUi', 'settings.state.overridden'), {
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 2: the switch is saved as closed and the row re-reads it', async () => {
      const row = settingRow(page)

      await valueField(page).fill(CLOSED)
      await saveButton(page).click()

      // The one feedback region under the table carries the success
      // confirmation, and the row re-reads as overridden with the written
      // value.
      await expect(
        page.getByText(copy('consoleUi', 'settings.saved'), { exact: true }),
      ).toBeVisible()
      await expect(
        row.getByText(copy('consoleUi', 'settings.state.overridden'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect
        .poll(() => valueField(page).inputValue(), {
          timeout: 10_000,
          message: 'the row did not re-read the written value',
        })
        .toBe(CLOSED)

      // A server re-read (the page's own refresh) shows the same state and
      // value: the write is stored, not local draft state.
      await page
        .getByRole('button', {
          name: copy('consoleUi', 'common.refresh'),
          exact: true,
        })
        .click()
      await expect
        .poll(() => valueField(page).inputValue(), {
          timeout: 10_000,
          message: 'the refreshed row lost the written value',
        })
        .toBe(CLOSED)
      await expect(
        row.getByText(copy('consoleUi', 'settings.state.overridden'), {
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 3: the anonymous surface keeps the register entry but drops the password form', async () => {
      // The operator's unlogged window: a fresh browser context with no
      // session. The context carries the tier's language and time zone pins,
      // which the config applies to the default context but not to a context
      // the spec creates by hand.
      visitor = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
      })
      const anonymous = await visitor.newPage()
      await anonymous.goto(`${server.url}/`)
      await expect(signInHeading(anonymous)).toBeVisible()

      // The sign-in surface still offers the register entry: first-login
      // signup is open and the assembled SMS and social methods can open an
      // account.
      await expect(registerEntry(anonymous)).toBeVisible()
      await registerEntry(anonymous).click()
      await expect(registerHeading(anonymous)).toBeVisible()

      // The password registration form is gone from register mode.
      for (const part of passwordFormParts(anonymous)) {
        await expect(part).toHaveCount(0)
      }

      // The remaining ways in are the account-opening methods: the social
      // block is still on the surface, and the way back to sign-in remains.
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
        page.getByText(copy('consoleUi', 'settings.saved'), { exact: true }),
      ).toBeVisible()
      await expect(
        row.getByText(copy('consoleUi', 'settings.state.overridden'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect
        .poll(() => valueField(page).inputValue(), {
          timeout: 10_000,
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

      // The second read happens after the second save: a reload re-reads the
      // discovery document, exactly as before the first change.
      await anonymous.reload()
      await expect(signInHeading(anonymous)).toBeVisible()
      await expect(registerEntry(anonymous)).toBeVisible()
      await registerEntry(anonymous).click()
      await expect(registerHeading(anonymous)).toBeVisible()

      // Register mode renders the password registration form again, beside the
      // account-opening methods.
      for (const part of passwordFormParts(anonymous)) {
        await expect(part).toBeVisible()
      }
    })

    await test.step('step 6: the audit query finds both writes in the time window', async () => {
      const since = datetimeBound(windowStart, -60_000)
      const until = datetimeBound(new Date(), 60_000)

      await consoleNav(page)
        .getByRole('link', {
          name: copy('consoleUi', 'audit.title'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('consoleUi', 'audit.title'),
          exact: true,
        }),
      ).toBeVisible()

      const table = page.getByRole('table')
      // The unfiltered list is read before the filters apply; waiting for a
      // row keeps the later "still unfiltered" check honest.
      await expect(table.locator('tbody tr').first()).toBeVisible()

      // Typing into the filter is not applying it: the unfiltered list still
      // holds a record whose action is not this journey's write.
      await page
        .getByLabel(copy('consoleUi', 'audit.filters.action'), { exact: true })
        .fill('settings.set')
      const otherAction = table
        .locator('tbody tr')
        .filter({ hasNot: page.getByRole('link', { name: 'settings.set' }) })
      await expect(otherAction.first()).toBeVisible()

      // The temporal bounds are applied too, not just the action. A window a
      // day after this run matches nothing, which the action filter alone
      // would not show.
      const day = 24 * 60 * 60 * 1000
      await page
        .getByLabel(copy('consoleUi', 'audit.filters.since'), { exact: true })
        .fill(datetimeBound(new Date(), day))
      await page
        .getByLabel(copy('consoleUi', 'audit.filters.until'), { exact: true })
        .fill(datetimeBound(new Date(), day + 60_000))
      await page
        .getByRole('button', {
          name: copy('consoleUi', 'audit.filters.apply'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 2,
          name: copy('consoleUi', 'audit.empty.title'),
          exact: true,
        }),
      ).toBeVisible()

      // Now the window that covers both saves.
      await page
        .getByLabel(copy('consoleUi', 'audit.filters.since'), { exact: true })
        .fill(since)
      await page
        .getByLabel(copy('consoleUi', 'audit.filters.until'), { exact: true })
        .fill(until)
      await page
        .getByRole('button', {
          name: copy('consoleUi', 'audit.filters.apply'),
          exact: true,
        })
        .click()

      // Only the two writes this journey made match the window and the action
      // filter. The read is eventually consistent, so the table assertion
      // retries rather than reading once.
      const rows = table.locator('tbody tr')
      await expect(rows).toHaveCount(2)
      for (const index of [0, 1]) {
        const row = rows.nth(index)
        await expect(
          row.getByRole('link', { name: 'settings.set', exact: true }),
        ).toBeVisible()
        await expect(row.getByText(SETTING_KEY, { exact: true })).toBeVisible()
        await expect(
          row.getByText(OPERATOR.displayName, { exact: true }),
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
          name: copy('consoleUi', 'auditDetail.title'),
          exact: true,
        }),
      ).toBeVisible()

      // The record's full field set: identifier, tenant, time, actor, source,
      // action, object, outcome and purpose, plus the detail section.
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
        await expect(term(page, copy('consoleUi', field))).toBeVisible()
      }
      await expect(
        page.getByRole('heading', {
          level: 3,
          name: copy('consoleUi', 'auditDetail.detailTitle'),
          exact: true,
        }),
      ).toBeVisible()

      // The values this write carries are the action, the setting key, the
      // outcome and the operator; the platform-level record has no tenant and
      // the write states no purpose, so those rows stand in as the placeholder.
      await expect(
        fieldValue(page, copy('consoleUi', 'auditDetail.field.action')),
      ).toHaveText('settings.set')
      await expect(
        fieldValue(page, copy('consoleUi', 'auditDetail.field.object')),
      ).toHaveText(SETTING_KEY)
      await expect(
        fieldValue(page, copy('consoleUi', 'auditDetail.field.outcome')),
      ).toHaveText('success')
      await expect(
        fieldValue(page, copy('consoleUi', 'auditDetail.field.actor')),
      ).toHaveText(OPERATOR.displayName)
      await expect(
        fieldValue(page, copy('consoleUi', 'auditDetail.field.tenant')),
      ).toHaveText(copy('consoleUi', 'common.emptyValue'))
      await expect(
        fieldValue(page, copy('consoleUi', 'auditDetail.field.purpose')),
      ).toHaveText(copy('consoleUi', 'common.emptyValue'))

      // The detail section records the setting key and the layer it was
      // written on, and carries no value: neither of the two values the
      // journey wrote appears, and no "value" key is listed.
      const detailHeading = page.getByRole('heading', {
        level: 3,
        name: copy('consoleUi', 'auditDetail.detailTitle'),
        exact: true,
      })
      const detailList = detailHeading.locator('xpath=following-sibling::dl[1]')
      await expect(
        detailList.getByText(SETTING_KEY, { exact: true }),
      ).toBeVisible()
      await expect(detailList.getByText('global', { exact: true })).toBeVisible()
      await expect(
        fieldValue(page, 'tenant'),
      ).toHaveText(copy('consoleUi', 'common.emptyValue'))
      await expect(
        fieldValue(page, 'action_id'),
      ).not.toHaveText(copy('consoleUi', 'common.emptyValue'))
      await expect(detailList.getByText(OPEN, { exact: true })).toHaveCount(0)
      await expect(detailList.getByText(CLOSED, { exact: true })).toHaveCount(0)
      await expect(page.locator('dt', { hasText: /^value$/ })).toHaveCount(0)

      // The detail carries its way back to the query.
      await page
        .getByRole('link', {
          name: copy('consoleUi', 'auditDetail.back'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('consoleUi', 'audit.title'),
          exact: true,
        }),
      ).toBeVisible()
    })
  } finally {
    // The anonymous context holds no session, but closing it here keeps a
    // failed run from leaving a second page open while the server drains.
    await visitor?.close().catch(() => undefined)
  }
})
