/**
 * J12 -- an operator's first run: the seeded platform administrator signs in
 * with the account and password the seed wrote, and the account's email
 * identifier is on record but unproved. The standing notice under the app bar
 * names the address, offers the verify and change entries and carries no close
 * control. The operator enters the console, opens the settings page and finds
 * the declared items grouped by namespace, each with its key, type, current
 * value and, where this deployment has written no row, the "Default" state.
 *
 * The operator changes one item to another value inside its declared domain and
 * saves. The write is refused -- the surface answers with the code-pinned text
 * that says to verify the email on the account page first, not with the
 * server's message -- and the draft stays in the field while the row still
 * reads "Default" and the current value is still the declaration. The operator
 * returns to the tenant area, follows the notice's verify entry (which sends one
 * code on arrival), reads that code out of the review process log -- the one
 * non-UI read the tier allows -- and proves the address. The identifiers list
 * flips the same row to verified and the notice goes.
 *
 * Back on the settings page, the same item and the same value are written
 * again; this time the row reads "Overridden", the field shows the value the
 * operator entered, and the one feedback region carries "Setting saved" with
 * the earlier failure gone. A refresh re-reads the row from the server and it
 * still reads overridden -- the value is persisted, not local state. The audit
 * page lists the write newest-first; the action and outcome filters, applied
 * only on submit, narrow the list to that one record, and its detail shows the
 * action, object, outcome and actor with the tenant and purpose rows standing
 * in as placeholders; the back entry returns to the query.
 *
 * Everything is driven through the rendered product with mouse and keyboard:
 * headings, roles and accessible names, visible text, focus, disabled state and
 * the one feedback region. Expected copy is read from the shipped en-US bundles
 * through helpers/i18n.ts. The report's data values (the setting key, its type,
 * the action and outcome names) are the product's own answers, asserted as the
 * rendered data they are.
 *
 * Two claims in the journey document are narrower here than the document
 * states, and the reasons:
 *
 *  - The console sidebar's "Platform configuration" group heading does not
 *    render in the review assembly: that group has two declarations, the
 *    settings page and the SMS-template page, and the review binary serves no
 *    `sms.admin` feature, so the group folds to its one served entry. The spec
 *    therefore proves the grouped navigation through the "Identity & access"
 *    group (six entries, its heading rendered) and opens the settings page from
 *    the navigation entry itself. The grouping and the destination are the
 *    step's substance; the group's name is not observable here.
 *  - REQ-presentation-029 (one result at a time, the last settles) is left out
 *    of @covers. The journey's two writes sit on two different mounts of the
 *    settings page -- the account round trip between them unmounts it -- so a
 *    region that kept the first result would not look different from a fresh
 *    one, and this spec cannot discriminate the requirement. The success step
 *    still asserts that no failure text stands beside the confirmation.
 *
 * The type column is the table's lowest-priority column and only renders from
 * the container's `lg` width, so the spec opens a desktop viewport wide enough
 * to observe the key, type and value the step documents.
 *
 * REQ-signin-006 (an email identifier and a password sign in), REQ-account-010
 * (a correct code proves the held address in place), 015/016/017/019/020,
 * REQ-platform-admin-001 (staff see the console entry and the home card),
 * 017 (a write from an unproved account is refused with the verification
 * guidance), 029 (audit filters apply on submit) and 031 (the detail's full
 * field set), and REQ-platform-config-001/003/005 are the ids the assertions
 * can fail on.
 */
// @journey journey-operator-first-run.md
// @covers REQ-signin-006, REQ-account-010, REQ-account-015, REQ-account-016, REQ-account-017, REQ-account-019, REQ-account-020, REQ-platform-admin-001, REQ-platform-admin-017, REQ-platform-admin-029, REQ-platform-admin-031, REQ-platform-config-001, REQ-platform-config-003, REQ-platform-config-005, REQ-presentation-027, REQ-presentation-028
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import { copy, copyPattern } from '../helpers/i18n'

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

/** The one declared setting this journey writes, and its declared type. */
const SETTING_KEY = 'ratelimit.limit'
const SETTING_TYPE = 'int'

/** Two more items of the same namespace, so the narrowed table is the group. */
const SIBLING_KEYS = ['ratelimit.window', 'ratelimit.address-rate'] as const

/** A declared item outside that namespace, used to prove the narrowing. */
const FOREIGN_KEY = 'authn.account.signup'

/** The value this journey writes: a positive integer, inside the declared
 * domain of a setting whose declared default is 100. */
const NEW_VALUE = '150'

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

/** The account page's sign-in-identifiers section. */
function identifiersSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'identifiers.title'),
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

test('operator-first-run: the operator proves the email and lands the first settings write', async ({
  demo,
}) => {
  const { page, server, accounts } = demo

  await test.step('the operator signs in and the pending-email notice names the address', async () => {
    // The operator holds an email identifier and a password; the journey's
    // sign-in clause is that pair working on the password form.
    await accounts.signIn({
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

    // The tenant area's home is the address's standing notice: it names the
    // address, offers the verify and change entries, and carries no close
    // control (an Alert rendered without a close action).
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

  await test.step('the console opens from the header entry, and the settings page lists the declared items', async () => {
    // The home console card and the header entry are two derivations of the
    // same target; the step drives the header one and checks they agree.
    const consoleEntry = page
      .getByRole('banner')
      .getByRole('link', {
        name: copy('demo', 'nav.console'),
        exact: true,
      })
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

    // The console navigation is grouped: the identity group holds six served
    // pages, so its heading renders as a disclosure control. (The platform
    // group holds only the settings page in this assembly, so it folds to the
    // entry itself -- see the file header.)
    const nav = consoleNav(page)
    await expect(
      nav.getByRole('button', {
        name: copy('consoleUi', 'nav.groups.identity'),
        exact: true,
      }),
    ).toBeVisible()

    // The journey's platform-configuration group: the settings page is opened
    // from the console navigation.
    await nav
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

    // The item names its key, its declared type and its current value; no row
    // is stored at this layer, so its state is the declaration.
    const row = settingRow(page)
    await expect(row.getByText(SETTING_KEY, { exact: true })).toBeVisible()
    await expect(row.getByText(SETTING_TYPE, { exact: true })).toBeVisible()
    const declaredValue = await valueField(page).inputValue()
    await expect(
      row.getByText(
        copy('consoleUi', 'settings.field.defaultValue', {
          value: declaredValue,
        }),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(
      row.getByText(copy('consoleUi', 'settings.state.default'), {
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
        name: copy('consoleUi', 'settings.saveBar.save'),
        exact: true,
      })
      .click()

    // The refusal renders the code's own text -- not the server's message --
    // in the one feedback region under the table, and the row's field carries
    // the same message through its accessible description.
    const refusal = copy('consoleUi', 'errors.authn.contact_required')
    await expect(page.getByRole('alert').filter({ hasText: refusal }))
      .toBeVisible()
    await expect(valueField(page)).toHaveAccessibleDescription(refusal)
    await expect(valueField(page)).toHaveAttribute('aria-invalid', 'true')

    // The draft the operator typed stays in the field; no row was written, so
    // the item still reads as the declaration and the value it falls back to
    // is unchanged.
    await expect(valueField(page)).toHaveValue(NEW_VALUE)
    await expect(
      row.getByText(copy('consoleUi', 'settings.state.default'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      row.getByText(
        copy('consoleUi', 'settings.field.defaultValue', {
          value: declaredValue,
        }),
        { exact: true },
      ),
    ).toBeVisible()
  })

  await test.step('the verify entry sends a code and holds the resend control', async () => {
    // Back to the tenant area, where the standing notice is still there: it is
    // not a one-page notice, and the round trip through the console must not
    // clear it.
    await page
      .getByRole('link', {
        name: copy('demo', 'console.backToApp'),
        exact: true,
      })
      .click()
    const banner = verificationBanner(page, OPERATOR.identifier)
    await expect(banner).toBeVisible()

    // The cursor is taken before the entry is followed: the verification
    // section sends its one code after the arrival, and only that code may be
    // used.
    const beforeCode = server.log.mark()
    await banner
      .getByRole('link', {
        name: copy('demo', 'verificationBanner.verify'),
        exact: true,
      })
      .click()

    // The automatic send lands: the section names the destination, the code
    // field takes focus, and the send control is held for the server's
    // interval with the remaining seconds stated.
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

    // The one non-UI read: the code the review mail carrier wrote to the
    // process log for this address.
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

    // The completion state names the proved address, the standing notice goes,
    // and the account's own identifier list flips the same row to verified --
    // no second row is added for the address it already held.
    await expect(
      page.getByRole('region', {
        name: copy('accountUi', 'verification.verifiedTitle'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByText(
        copy('accountUi', 'verification.verifiedDescription', {
          email: OPERATOR.identifier,
        }),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(verificationBanner(page, OPERATOR.identifier)).toHaveCount(0)

    const identifiers = identifiersSection(page)
    const heldRow = identifiers
      .getByRole('listitem')
      .filter({ hasText: OPERATOR.identifier })
    await expect(heldRow).toHaveCount(1)
    await expect(
      heldRow.getByText(copy('accountUi', 'identifiers.verified'), {
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('the same write now lands and the item reads overridden', async () => {
    // Re-enter the console and the settings page; the page is a fresh mount, so
    // the item starts again from the declaration.
    await page
      .getByRole('banner')
      .getByRole('link', {
        name: copy('demo', 'nav.console'),
        exact: true,
      })
      .click()
    const nav = consoleNav(page)
    await nav
      .getByRole('link', {
        name: copy('consoleUi', 'settings.title'),
        exact: true,
      })
      .click()
    await namespaceNav(page)
      .getByRole('button', { name: 'ratelimit', exact: true })
      .click()
    await expect(valueField(page)).toBeVisible()

    const row = settingRow(page)
    // The same item and the same new value as the refused attempt.
    await valueField(page).fill(NEW_VALUE)
    await page
      .getByRole('button', {
        name: copy('consoleUi', 'settings.saveBar.save'),
        exact: true,
      })
      .click()

    // The one feedback region now carries the success confirmation, the row
    // re-reads as overridden with the written value, and the earlier failure's
    // text is not beside it.
    const saved = copy('consoleUi', 'settings.saved')
    await expect(page.getByText(saved, { exact: true })).toBeVisible()
    await expect(
      page.getByText(copy('consoleUi', 'errors.authn.contact_required'), {
        exact: true,
      }),
    ).toHaveCount(0)
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
      .toBe(NEW_VALUE)

    // A server re-read (the page's own refresh) shows the same state and value:
    // the write is stored, not local draft state.
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
      .toBe(NEW_VALUE)
    await expect(
      row.getByText(copy('consoleUi', 'settings.state.overridden'), {
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('the audit trail records the write, and its detail shows the fields', async () => {
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

    // The write is on the newest page, and its row carries the action the
    // settings module writes and the success outcome.
    const table = page.getByRole('table')
    const writeLink = table
      .getByRole('link')
      .filter({ hasText: /^settings\.set$/ })
    await expect(writeLink).toBeVisible()
    const writeRow = writeLink.locator('xpath=ancestor::tr[1]')
    const outcome = (
      (await writeRow.getByRole('cell').last().textContent()) ?? ''
    ).trim()
    expect(outcome).not.toBe('')

    // Typing the filters is not applying them: the unfiltered list still holds
    // more than the one record.
    await page
      .getByLabel(copy('consoleUi', 'audit.filters.action'), { exact: true })
      .fill('settings.set')
    await page
      .getByLabel(copy('consoleUi', 'audit.filters.outcome'), { exact: true })
      .fill(outcome)
    expect(await table.locator('tbody tr').count()).toBeGreaterThan(1)

    await page
      .getByRole('button', {
        name: copy('consoleUi', 'audit.filters.apply'),
        exact: true,
      })
      .click()

    // Only the write this journey made matches both filters.
    await expect(table.locator('tbody tr')).toHaveCount(1)
    await expect(writeLink).toBeVisible()
    await writeLink.click()

    // The record's detail shows every field the requirement names; the values
    // this write carries are the action, the item, the outcome and the actor,
    // and the tenant and purpose rows stand in as the empty-value placeholder.
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('consoleUi', 'auditDetail.title'),
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
      await expect(term(page, copy('consoleUi', field))).toBeVisible()
    }
    await expect(
      page.getByRole('heading', {
        level: 3,
        name: copy('consoleUi', 'auditDetail.detailTitle'),
        exact: true,
      }),
    ).toBeVisible()

    await expect(
      fieldValue(page, copy('consoleUi', 'auditDetail.field.action')),
    ).toHaveText('settings.set')
    await expect(
      fieldValue(page, copy('consoleUi', 'auditDetail.field.object')),
    ).toHaveText(SETTING_KEY)
    await expect(
      fieldValue(page, copy('consoleUi', 'auditDetail.field.outcome')),
    ).toHaveText(outcome)
    await expect(
      fieldValue(page, copy('consoleUi', 'auditDetail.field.actor')),
    ).toHaveText(OPERATOR.displayName)
    await expect(
      fieldValue(page, copy('consoleUi', 'auditDetail.field.tenant')),
    ).toHaveText(copy('consoleUi', 'common.emptyValue'))
    await expect(
      fieldValue(page, copy('consoleUi', 'auditDetail.field.purpose')),
    ).toHaveText(copy('consoleUi', 'common.emptyValue'))

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
})
