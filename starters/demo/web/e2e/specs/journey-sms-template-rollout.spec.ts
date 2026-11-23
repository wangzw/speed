/**
 * J -- an operator rolls out one SMS template and turns it off again.
 *
 * The platform operator opens the admin's SMS page, reads the configuration
 * templates as read-only, registers a runtime template with a vendor code for
 * each of two languages, and observes the languages' review states as the log
 * carrier reports them: the configured English code reads approved, the Chinese
 * code has no configured verdict and stays pending. The template is disabled
 * while the states stand and enabled again, and neither move changes a review
 * state. A tenant member with a proved phone then lets the notification
 * fixture's periodic dispatch address the account: the notice is rendered in
 * the recipient's language (the platform default) and the approved English code
 * carries it to the log SMS carrier, which writes the message to the process
 * log. Disabling the template and waiting out the next dispatch windows is
 * refused locally: the carrier is never called and no successful external-send
 * record appears.
 *
 * Everything the person does is a mouse or keyboard action on the rendered
 * product: the account controls that prove the email and phone, the admin
 * navigation, and the register and enable/disable controls. Two reads are not
 * UI, and each is the harness's own:
 *
 *   - The email and SMS verification codes come from the process log through
 *     `waitForVerificationCode`, one of the three log reads the honesty rules
 *     allow.
 *   - The messages the log SMS carrier accepted and the delivery pipeline's
 *     external-send records come from their process-log lines through the
 *     fixture's `waitForCarrierMessage` and `waitForOutboundRecord`. The carrier
 *     reaches no vendor, so those lines are the evidence that a message left or
 *     did not leave the process.
 *
 * This spec does not claim to cover the delivery ledger. `notification_sends`
 * stays off the product's interface, and an end-to-end spec asserts rendered
 * behaviour rather than a table a fixture reads. The record that a locally
 * refused send is failed with its refusal reason is asserted by the Go case
 * `TestRefusedSMSTemplateIsRecordedFailedInTheLedger`
 * (`starters/demo/server/e2e_notification_ledger_test.go`), which queries
 * the ledger directly.
 *
 * The review verdict comes from the configuration's `sms.log.approvals` table,
 * not from a control the page drives: the log carrier answers the configured
 * verdict for each vendor code, so the English code reads approved from the
 * first list refresh and the Chinese code, with no entry, stays pending.
 *
 * The dispatch is the notification fixture's periodic one, and the recipient is
 * the seeded tenant member rather than the operator. The fixture addresses an
 * account by its proved phone or by any email identifier, and it prunes every
 * channel a dispatch cannot use from the recipient's effective set without ever
 * restoring one. The operator owns an email identifier from the seed, so a
 * dispatch that ran before the runtime template existed would fall back to email
 * and drop sms for good. The member holds no identifier until this journey proves
 * its phone, after the template is enabled, so the first window that can address
 * it chooses sms.
 *
 * The email and phone preconditions are produced through the account page, not
 * seeded. The operator's membership in the seeded tenant is part of this
 * server's seed: the fixture names it acme's second tenant administrator on
 * every server that does not shape the impersonation tenant.
 *
 * `@covers` is narrower than the journey document where an assertion cannot fail
 * on the requirement's whole claim:
 *
 *   - REQ-notifications-021 is claimed for the template binding and the
 *     parameter whitelist a successful external send proves, not for the
 *     "another supported language" comparison: this recipient has no stored
 *     language, so the dispatch resolves to the platform default and the spec
 *     never contrasts two languages.
 *   - REQ-platform-config-014 is claimed for the disabled template gate only:
 *     this spec never turns the deployment's sending switch off and never sends
 *     in a language the vendor has not approved (the Chinese code stays pending
 *     and is never used).
 *   - REQ-platform-config-015 is claimed for the configuration template's
 *     disable refusal; the modify, remove and same-name registration branches
 *     are not driven here.
 *   - REQ-platform-config-011 is not claimed: the main path registers a template
 *     whose vendor codes are already held, so the offer-for-review branch is not
 *     exercised.
 *
 * Expected copy is read from the shipped en-US bundles through helpers/i18n.ts;
 * the verification and verdict sentences follow the bundle when it changes.
 */
// @journey journey-sms-template-rollout.md
// @covers REQ-notifications-023, REQ-platform-config-010, REQ-platform-config-012, REQ-platform-config-013, REQ-platform-config-014, REQ-platform-config-015, REQ-platform-config-034, REQ-platform-config-036, REQ-presentation-027, REQ-presentation-028
import type { Locator, Page } from '@playwright/test'

import { e2eOrdinaryMember, smsRolloutOperator } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import { NOTIFICATION_INTERVAL_MS } from '../fixtures/notification-fixture'
import type { E2EServer } from '../fixtures/server'
import { scaledTimeout } from '../fixtures/time-scale'
import { createAccounts } from '../helpers/accounts'
import {
  adminEntry,
  closeAccountMenu,
  openAccountMenu,
} from '../helpers/account-menu'
import {
  expandAccountRows,
  selectAccountSection,
} from '../helpers/account-sections'
import { copy } from '../helpers/i18n'
import { expectClippedAnnouncement } from '../helpers/tenant-switch'

/** The runtime template the notification type's catalog names. */
const TEMPLATE = 'demo-notice'

/** The vendor codes the template is registered with, one per language. */
const EN_LOCALE = 'en-US'
const ZH_LOCALE = 'zh-CN'
const EN_CODE = 'demo-notice-en'
const ZH_CODE = 'demo-notice-zh'

/** The notification type the e2e assembly declares. */
const TYPE_KEY = 'demo.notice'

/** The phone the recipient proves; its region is admitted by the config. */
const PHONE_NATIONAL = '13800138000'
const PHONE_E164 = '+8613800138000'

/** The configuration template whose read-only row the page shows. */
const CONFIG_TEMPLATE = 'authn.code.login'

/** The profile pane's email card, which carries the verification surface. */
function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.email'),
    exact: true,
  })
}

/** The profile pane's phone card. */
function phoneSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.rows.label.phone'),
    exact: true,
  })
}

/** The admin's navigation landmark. */
function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

/** The SMS page's page header. */
function smsHeading(page: Page): Locator {
  return page.getByRole('heading', {
    name: copy('adminUi', 'sms.title'),
    exact: true,
  })
}

/** The SMS page's register section. */
function registerSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'sms.register.title'),
    exact: true,
  })
}

/** The page-header refresh control. */
function refreshButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'common.refresh'),
    exact: true,
  })
}

/** One template row, found by the logical name in its name column. */
function templateRow(page: Page, name: string): Locator {
  return page.getByRole('row').filter({ hasText: name })
}

/**
 * The platform's own organization. The admin entry is offered only while the
 * current organization is this one, and this operator also administers the
 * seeded tenant, so the journey switches here before opening the admin.
 */
const ADMIN_TENANT = 'system'

/** The account menu's organization row: the collapsed row that expands the
 * other organizations. Waiting for that shape skips the menu's later entries
 * while the caller tenant read is still settling. */
function currentOrganizationRow(page: Page): ReturnType<Page['getByRole']> {
  return page
    .getByRole('menu')
    .getByRole('menuitem', { expanded: false })
    .first()
}

/**
 * Move the tenant area to `target` through the account menu's organization
 * row. The operator belongs to more than one organization and the current one
 * is not known up front, so the row is expanded first; the target is one of
 * the other organizations the expansion lists. When the target is already
 * current, the expansion does not offer it and the menu is simply closed.
 */
async function switchToTenant(page: Page, target: string): Promise<void> {
  const menu = await openAccountMenu(page)
  const current = currentOrganizationRow(page)
  await expect(current).toBeVisible()
  await current.click()
  const others = menu.getByRole('group', {
    name: copy('demo', 'userMenu.organizations'),
    exact: true,
  })
  await expect(others).toBeVisible()
  const row = others.getByRole('menuitem', { name: target, exact: true })
  if ((await row.count()) === 0) {
    // The target is the current organization: the expansion lists only the
    // others, so there is nothing to switch to.
    await closeAccountMenu(page)
    return
  }
  await row.click()
  await expectClippedAnnouncement(
    page,
    copy('tenancyUi', 'tenantSwitcher.switchedTo', { tenant: target }),
  )
  // The pick closes the menu; wait for it to leave before reopening, so the
  // next open matches exactly one menu under strict mode.
  await closeAccountMenu(page)
  await openAccountMenu(page)
  await expect(
    page.getByRole('menu').getByRole('menuitem', { name: target, exact: true }),
  ).toBeVisible()
  await closeAccountMenu(page)
}

/** Open the admin from the tenant area and its SMS page from the navigation. */
async function openSmsPage(page: Page): Promise<void> {
  // The admin entry is offered only while the platform's own organization is
  // current, so the operator moves there before opening the account menu.
  await switchToTenant(page, ADMIN_TENANT)
  await openAccountMenu(page)
  await expect(adminEntry(page)).toBeVisible()
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
  await adminNav(page)
    .getByRole('link', { name: copy('adminUi', 'sms.title'), exact: true })
    .click()
  await expect(smsHeading(page)).toBeVisible()
}

/** One row's approval text for a language, as the page renders it. */
function approvalText(locale: string, stateKey: string): string {
  return `${locale}: ${copy('adminUi', `sms.approval.${stateKey}`)}`
}

/**
 * Refresh the list until the named row carries the wanted text. The registry
 * reads the vendor's verdict in a background pass, so the list needs a read
 * after the write; a refresh issued while the page's own read is still settling
 * is skipped rather than blocked on.
 */
async function refreshUntil(
  page: Page,
  name: string,
  wanted: string,
): Promise<void> {
  await expect
    .poll(
      async () => {
        const text = (await templateRow(page, name).textContent()) ?? ''
        if (text.includes(wanted)) return text
        const refresh = refreshButton(page)
        if (await refresh.isEnabled()) await refresh.click()
        return (await templateRow(page, name).textContent()) ?? ''
      },
      {
        message: `the ${name} row never showed ${wanted}`,
      },
    )
    .toContain(wanted)
}

/** Prove one account's email address from its own code flow. */
async function proveEmail(
  page: Page,
  server: E2EServer,
  email: string,
): Promise<void> {
  await selectAccountSection(page, 'profile')
  await expandAccountRows(page)
  const verification = verificationSection(page)
  const emailAfter = server.log.mark()
  await verification
    .getByRole('button', {
      name: copy('accountUi', 'verification.send'),
      exact: true,
    })
    .click()
  await expect(
    page.getByText(copy('accountUi', 'verification.sent', { email }), {
      exact: true,
    }),
  ).toBeVisible()
  const emailCode = await waitForVerificationCode(server.log, 'mail', email, {
    after: emailAfter,
  })
  await verification
    .getByRole('textbox', {
      name: copy('accountUi', 'verification.codeLabel'),
      exact: true,
    })
    .fill(emailCode.code)
  await verification
    .getByRole('button', {
      name: copy('accountUi', 'verification.verify'),
      exact: true,
    })
    .click()
  await expect(
    verification.getByText(email, { exact: true }),
  ).toBeVisible()
  await expect(
    verification.getByText(copy('accountUi', 'identifiers.verified'), {
      exact: true,
    }),
  ).toBeVisible()
}

/** Prove one account's phone number from its own code flow. */
async function provePhone(page: Page, server: E2EServer): Promise<void> {
  await selectAccountSection(page, 'profile')
  await expandAccountRows(page)
  const phone = phoneSection(page)
  await phone
    .getByRole('button', {
      name: copy('demo', 'account.action.add'),
      exact: true,
    })
    .click()
  await phone
    .getByRole('combobox', {
      name: copy('accountUi', 'phone.country.label'),
      exact: true,
    })
    .fill('86')
  await page.getByRole('option', { name: /\+86$/ }).click()
  await phone
    .getByRole('textbox', {
      name: copy('accountUi', 'phone.phone.label'),
      exact: true,
    })
    .fill(PHONE_NATIONAL)
  const phoneAfter = server.log.mark()
  await phone
    .getByRole('button', {
      name: copy('accountUi', 'phone.send'),
      exact: true,
    })
    .click()
  await expect(
    page.getByText(copy('accountUi', 'phone.sent', { phone: PHONE_E164 }), {
      exact: true,
    }),
  ).toBeVisible()
  const phoneCode = await waitForVerificationCode(server.log, 'sms', PHONE_E164, {
    after: phoneAfter,
  })
  await phone
    .getByRole('textbox', {
      name: copy('accountUi', 'phone.code.label'),
      exact: true,
    })
    .fill(phoneCode.code)
  await phone
    .getByRole('button', {
      name: copy('accountUi', 'phone.verify'),
      exact: true,
    })
    .click()
  await expect(
    page.getByText(copy('accountUi', 'phone.added', { phone: PHONE_E164 }), {
      exact: true,
    }),
  ).toBeVisible()
}

test('sms-template-rollout: an operator settles one language and a disabled template is refused locally', async ({
  demo,
  browser,
}) => {
  const { page, server, accounts } = demo

  await test.step('the operator signs in and proves the account email', async () => {
    await accounts.signIn({
      identifier: smsRolloutOperator.username,
      password: smsRolloutOperator.password,
    })
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()
    await proveEmail(page, server, smsRolloutOperator.identifier)
  })

  await test.step('the configuration template reads as read-only and refuses a write', async () => {
    await openSmsPage(page)

    // The configuration source's row names its source and carries no review
    // state, and its controls are still offered: the server, not the page, is
    // what refuses the write.
    const configRow = templateRow(page, CONFIG_TEMPLATE)
    await expect(configRow).toHaveCount(1)
    await expect(configRow).toContainText(
      copy('adminUi', 'sms.source.config'),
    )
    for (const state of ['pending', 'approved', 'rejected', 'withdrawn']) {
      await expect(configRow).not.toContainText(
        copy('adminUi', `sms.approval.${state}`),
      )
    }
    await configRow
      .getByRole('switch', {
        name: copy('adminUi', 'sms.disableAria', { name: CONFIG_TEMPLATE }),
        exact: true,
      })
      .click()
    // The refusal is rendered from the error code's own text, not from the
    // server's stored message.
    await expect(
      page
        .getByText(copy('adminUi', 'errors.sms.config_template'), {
          exact: true,
        })
        .first(),
    ).toBeVisible()
    await expect(configRow).toContainText(
      copy('adminUi', 'sms.source.config'),
    )
  })

  await test.step('the operator registers the runtime template and its languages read their configured verdicts', async () => {
    const register = registerSection(page)
    await register
      .getByRole('textbox', {
        name: copy('adminUi', 'sms.register.nameLabel'),
        exact: true,
      })
      .fill(TEMPLATE)
    await register
      .getByRole('textbox', {
        name: copy('adminUi', 'sms.register.paramsLabel'),
        exact: true,
      })
      .fill('to')
    await register
      .getByRole('textbox', {
        name: copy('adminUi', 'sms.register.codesLabel'),
        exact: true,
      })
      .fill(`${EN_LOCALE}:${EN_CODE}, ${ZH_LOCALE}:${ZH_CODE}`)
    await register
      .getByRole('button', {
        name: copy('adminUi', 'sms.register.submit'),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(copy('adminUi', 'sms.registered', { name: TEMPLATE }), {
        exact: true,
      }),
    ).toBeVisible()

    await refreshUntil(page, TEMPLATE, approvalText(EN_LOCALE, 'approved'))
    const row = templateRow(page, TEMPLATE)
    await expect(row).toHaveCount(1)
    await expect(row).toContainText(copy('adminUi', 'sms.source.runtime'))
    await expect(row).toContainText(copy('adminUi', 'sms.enabledYes'))
    await expect(row).toContainText(approvalText(EN_LOCALE, 'approved'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))
  })

  await test.step('disabling the template leaves the review states unchanged', async () => {
    const row = templateRow(page, TEMPLATE)
    await row
      .getByRole('switch', {
        name: copy('adminUi', 'sms.disableAria', { name: TEMPLATE }),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(
        copy('adminUi', 'sms.disabledToast', { name: TEMPLATE }),
        { exact: true },
      ),
    ).toBeVisible()
    await refreshUntil(page, TEMPLATE, copy('adminUi', 'sms.enabledNo'))
    await expect(row).toContainText(copy('adminUi', 'sms.enabledNo'))
    await expect(row).toContainText(approvalText(EN_LOCALE, 'approved'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))
  })

  await test.step('enabling the template leaves the review states unchanged', async () => {
    const row = templateRow(page, TEMPLATE)
    await row
      .getByRole('switch', {
        name: copy('adminUi', 'sms.enableAria', { name: TEMPLATE }),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(copy('adminUi', 'sms.enabledToast', { name: TEMPLATE }), {
        exact: true,
      }),
    ).toBeVisible()
    await refreshUntil(page, TEMPLATE, copy('adminUi', 'sms.enabledYes'))
    await expect(row).toContainText(copy('adminUi', 'sms.enabledYes'))
    await expect(row).toContainText(approvalText(EN_LOCALE, 'approved'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))
  })

  await test.step('the recipient proves the phone the dispatch needs', async () => {
    // The recipient is the seeded tenant member, signed in in its own context
    // so the operator's admin session stays where the disable step needs it.
    // The member holds no identifier until this proof, so no earlier dispatch
    // pruned its sms channel, and the first window that can address it chooses
    // sms now that the template is enabled.
    const recipientContext = await browser.newContext()
    try {
      const recipientPage = await recipientContext.newPage()
      const recipient = createAccounts({ page: recipientPage, server })
      await recipient.signIn({
        identifier: e2eOrdinaryMember.identifier,
        password: e2eOrdinaryMember.password,
      })
      await provePhone(recipientPage, server)
    } finally {
      await recipientContext.close()
    }
  })

  await test.step('the periodic dispatch sends the approved language to the SMS carrier', async () => {
    const logAfter = server.log.mark()

    // The message the carrier accepted: the template, the recipient's language
    // and the destination the dispatch resolved for this phone.
    const carrier = await server.waitForCarrierMessage({
      to: PHONE_E164,
      template: TEMPLATE,
      after: logAfter,
      timeoutMs: scaledTimeout(3 * NOTIFICATION_INTERVAL_MS),
    })
    expect(carrier.locale).toBe('en-US')

    // The external-send record the delivery pipeline writes when a message
    // leaves the process.
    const outbound = await server.waitForOutboundRecord({
      channel: 'sms',
      typeKey: TYPE_KEY,
      outcome: 'sent',
      after: logAfter,
      timeoutMs: scaledTimeout(3 * NOTIFICATION_INTERVAL_MS),
    })
    expect(outbound.providerID).not.toBe('')
    expect(outbound.durationMS).toBeGreaterThanOrEqual(0)
  })

  await test.step('a disabled template is refused locally and no message leaves', async () => {
    const row = templateRow(page, TEMPLATE)
    await row
      .getByRole('switch', {
        name: copy('adminUi', 'sms.disableAria', { name: TEMPLATE }),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(
        copy('adminUi', 'sms.disabledToast', { name: TEMPLATE }),
        { exact: true },
      ),
    ).toBeVisible()
    await refreshUntil(page, TEMPLATE, copy('adminUi', 'sms.enabledNo'))
    await expect(row).toContainText(copy('adminUi', 'sms.enabledNo'))
    await expect(row).toContainText(approvalText(EN_LOCALE, 'approved'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))

    const logAfter = server.log.mark()
    const windowMs = scaledTimeout(2 * NOTIFICATION_INTERVAL_MS)
    const startedAt = Date.now()

    // Nothing left the process across the next two dispatch windows: no
    // carrier line and no external-send record in any outcome. The dispatch
    // is still attempted, so the ledger records a failed send each window,
    // which the Go case named in this spec's header asserts; the browser tier
    // only observes that no message reached the carrier and none left the
    // process, whatever the record would have called its result.
    // The loop samples across the window and passes only once it has elapsed,
    // so a message that arrives late still fails the assertion.
    await expect(async () => {
      expect(
        server.countCarrierMessages({
          to: PHONE_E164,
          template: TEMPLATE,
          after: logAfter,
        }),
      ).toBe(0)
      expect(
        server.countOutboundRecords({
          channel: 'sms',
          typeKey: TYPE_KEY,
          after: logAfter,
        }),
      ).toBe(0)
      expect(Date.now() - startedAt).toBeGreaterThanOrEqual(windowMs)
    }).toPass({
      timeout: windowMs + scaledTimeout(NOTIFICATION_INTERVAL_MS),
      intervals: [1_000],
    })
  })
})
