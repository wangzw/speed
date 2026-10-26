/**
 * J -- an operator rolls out one SMS template and turns it off again.
 *
 * The platform operator opens the console's SMS page, reads the configuration
 * templates as read-only, registers a runtime template with a vendor code for
 * each of two languages, and observes both languages start pending on a carrier
 * that reviews each template. The review-only control settles the English code
 * alone, which leaves the Chinese code pending. The template is disabled while
 * pending and enabled after approval, so the review state and the sending switch
 * are observed to be independent. The operator then enters the seeded tenant as
 * a member and clicks the review notice control: the notice is rendered in the
 * recipient's language (the platform default) and the approved English code
 * carries it to the review SMS carrier, which writes the message to the process
 * log. Disabling the template and repeating the click is refused locally: the
 * carrier is never called and no external-send record appears.
 *
 * Everything the person does is a mouse or keyboard action on the rendered
 * product: the account controls that prove the email and phone the dispatch
 * needs, the console navigation, the register and enable/disable controls, the
 * review-only verdict control, the tenant switcher and the notice control. Two
 * reads are not UI, and each is the harness's own:
 *
 *   - The email and SMS verification codes come from the process log through
 *     `waitForVerificationCode`, one of the three log reads the honesty rules
 *     allow.
 *   - The messages the review SMS carrier accepted and the delivery pipeline's
 *     external-send records come from their process-log lines through the
 *     fixture's `waitForCarrierMessage` and `waitForOutboundRecord`. The carrier
 *     reaches no vendor, so those lines are the evidence that a message left or
 *     did not leave the process.
 *
 * This spec does not claim to cover the delivery ledger. `notification_sends`
 * stays off the product's interface, and an end-to-end spec asserts rendered
 * behaviour rather than a table a fixture reads. The record that a locally
 * refused send is failed with its refusal reason is asserted by the
 * review-tagged Go case `TestReviewRefusedSMSTemplateIsRecordedFailedInTheLedger`
 * (`starters/demo/server/review_notification_ledger_test.go`), which queries
 * the ledger directly.
 *
 * The review verdict is driven by a control the review assembly alone compiles
 * and mounts; the page renders it only while the review build publishes the
 * feature, and the component labels itself as review-only. A baseline deployment
 * carries neither the route nor the feature.
 *
 * The periodic notification worker is turned off for this spec's server
 * (`schedulerTick: '0s'`). It would otherwise address the same tenant's members
 * on its own window, so a notice observed after the click could have come from
 * the worker instead of the action. The control's dispatch travels the job
 * queue, which polls independently of the scheduler.
 *
 * The email and phone preconditions are produced through the account page, not
 * seeded: the operator's email is proved from a `mail.review` code and its phone
 * from an `sms.review` code, both read through the allowed code reader. The
 * operator's membership in the seeded tenant is part of every review server's
 * seed: the fixture names it acme's second tenant administrator.
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

import { smsRolloutOperator } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import { copy } from '../helpers/i18n'

// This journey's actor is the platform operator as an administrator of the
// seeded tenant, and its only intended sends are its own control clicks.
test.use({ schedulerTick: '0s' })

/** The seeded tenant the operator joins. */
const TENANT = smsRolloutOperator.tenant

/** The runtime template the review notice type's catalog names. */
const TEMPLATE = 'review-notice'

/** The vendor codes the template is registered with, one per language. */
const EN_LOCALE = 'en-US'
const ZH_LOCALE = 'zh-CN'
const EN_CODE = 'review-notice-en'
const ZH_CODE = 'review-notice-zh'

/** The notification type the review build declares. */
const TYPE_KEY = 'review.notice'

/** The phone the operator proves; its region is admitted by the review config. */
const PHONE_NATIONAL = '13800138000'
const PHONE_E164 = '+8613800138000'

/** The configuration template whose read-only row the page shows. */
const CONFIG_TEMPLATE = 'authn.code.login'

/** The account page's email-verification section. */
function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
    exact: true,
  })
}

/** The account page's phone section. */
function phoneSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'phone.title'),
    exact: true,
  })
}

/** The header's console entry (a link while the operator may reach it). */
function consoleEntry(page: Page): Locator {
  return page
    .getByRole('banner')
    .getByRole('link', { name: copy('demo', 'nav.console'), exact: true })
}

/** The console's navigation landmark. */
function consoleNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('consoleUi', 'shell.navLabel'),
    exact: true,
  })
}

/** The console frame's link back to the tenant area. */
function backToApp(page: Page): Locator {
  return page.getByRole('link', {
    name: copy('demo', 'console.backToApp'),
    exact: true,
  })
}

/** The SMS page's page header. */
function smsHeading(page: Page): Locator {
  return page.getByRole('heading', {
    name: copy('consoleUi', 'sms.title'),
    exact: true,
  })
}

/** The SMS page's register section. */
function registerSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('consoleUi', 'sms.register.title'),
    exact: true,
  })
}

/** The SMS page's review-only verdict control. */
function verdictControl(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'smsReview.title'),
    exact: true,
  })
}

/** The page-header refresh control. */
function refreshButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('consoleUi', 'common.refresh'),
    exact: true,
  })
}

/** One template row, found by the logical name in its name column. */
function templateRow(page: Page, name: string): Locator {
  return page.getByRole('row').filter({ hasText: name })
}

/** The tenant switcher's trigger: the personal workspace or the seeded tenant. */
function tenantTrigger(page: Page): Locator {
  return page
    .getByRole('banner')
    .getByRole('button', { name: TENANT, exact: true })
    .or(
      page
        .getByRole('banner')
        .getByRole('button', { name: copy('demo', 'workspace.personal'), exact: true }),
    )
}

/**
 * Switch the tenant area to the seeded tenant. The operator may hold a personal
 * workspace too, so the trigger's current name is not known; the seeded tenant's
 * row is disabled when it is already current, which is the only case the helper
 * leaves the menu without a click.
 */
async function switchToTenant(page: Page): Promise<void> {
  const trigger = tenantTrigger(page)
  await expect(trigger).toBeVisible()
  await trigger.click()
  const row = page.getByRole('menuitem', { name: TENANT, exact: true })
  await expect(row).toBeVisible()
  if (await row.isEnabled()) {
    await row.click()
    await expect(
      page.getByText(
        copy('tenancyUi', 'tenantSwitcher.switchedTo', { tenant: TENANT }),
        { exact: true },
      ),
    ).toBeVisible()
  } else {
    await page.keyboard.press('Escape')
  }
  await expect(
    page.getByRole('banner').getByRole('button', { name: TENANT, exact: true }),
  ).toBeVisible()
}

/** Open the console from the tenant area and its SMS page from the navigation. */
async function openSmsPage(page: Page): Promise<void> {
  await expect(consoleEntry(page)).toBeVisible()
  await consoleEntry(page).click()
  await expect(consoleNav(page)).toBeVisible()
  await consoleNav(page)
    .getByRole('link', { name: copy('consoleUi', 'sms.title'), exact: true })
    .click()
  await expect(smsHeading(page)).toBeVisible()
}

/** One row's approval text for a language, as the page renders it. */
function approvalText(locale: string, stateKey: string): string {
  return `${locale}: ${copy('consoleUi', `sms.approval.${stateKey}`)}`
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
        timeout: 20_000,
        message: `the ${name} row never showed ${wanted}`,
      },
    )
    .toContain(wanted)
}

test('sms-template-rollout: an operator settles one language and a disabled template is refused locally', async ({
  demo,
}) => {
  const { page, server, accounts } = demo

  await test.step('the operator signs in and proves the email and phone the dispatch needs', async () => {
    await accounts.signIn({
      identifier: smsRolloutOperator.identifier,
      password: smsRolloutOperator.password,
    })
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()

    // The account page: the two preconditions the journey names, produced by the
    // product's own verification flows rather than written into the database.
    await page
      .getByRole('banner')
      .getByTestId('user-menu-trigger')
      .click()
    await page
      .getByRole('menuitem', {
        name: copy('demo', 'userMenu.accountSettings'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'account.title'),
        exact: true,
      }),
    ).toBeVisible()

    // The email address: the account page offers the send action for the
    // unproved address; the code arrives in the review mail carrier's line.
    const verification = verificationSection(page)
    const emailAfter = server.log.mark()
    await verification
      .getByRole('button', {
        name: copy('accountUi', 'verification.send'),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(
        copy('accountUi', 'verification.sent', {
          email: smsRolloutOperator.identifier,
        }),
        { exact: true },
      ),
    ).toBeVisible()
    const emailCode = await waitForVerificationCode(
      server.log,
      'mail',
      smsRolloutOperator.identifier,
      { after: emailAfter },
    )
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
      page.getByText(
        copy('accountUi', 'verification.verifiedDescription', {
          email: smsRolloutOperator.identifier,
        }),
        { exact: true },
      ),
    ).toBeVisible()

    // The phone number: the two-part control, then the code from the review SMS
    // carrier's line. The number's region is one the review config admits.
    const phone = phoneSection(page)
    const country = phone.getByRole('combobox', {
      name: copy('accountUi', 'phone.country.label'),
      exact: true,
    })
    await country.fill('86')
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
    const phoneCode = await waitForVerificationCode(
      server.log,
      'sms',
      PHONE_E164,
      { after: phoneAfter },
    )
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
  })

  await test.step('the configuration template reads as read-only and refuses a write', async () => {
    await openSmsPage(page)

    // The configuration source's row names its source and carries no review
    // state, and its controls are still offered: the server, not the page, is
    // what refuses the write.
    const configRow = templateRow(page, CONFIG_TEMPLATE)
    await expect(configRow).toHaveCount(1)
    await expect(configRow).toContainText(
      copy('consoleUi', 'sms.source.config'),
    )
    for (const state of ['pending', 'approved', 'rejected', 'withdrawn']) {
      await expect(configRow).not.toContainText(
        copy('consoleUi', `sms.approval.${state}`),
      )
    }
    await configRow
      .getByRole('button', {
        name: copy('consoleUi', 'sms.disableAria', { name: CONFIG_TEMPLATE }),
        exact: true,
      })
      .click()
    // The refusal is rendered from the error code's own text, not from the
    // server's stored message.
    await expect(
      page
        .getByText(copy('consoleUi', 'errors.sms.config_template'), {
          exact: true,
        })
        .first(),
    ).toBeVisible()
    await expect(configRow).toContainText(
      copy('consoleUi', 'sms.source.config'),
    )
  })

  await test.step('the operator registers the runtime template and both languages start pending', async () => {
    const register = registerSection(page)
    await register
      .getByRole('textbox', {
        name: copy('consoleUi', 'sms.register.nameLabel'),
        exact: true,
      })
      .fill(TEMPLATE)
    await register
      .getByRole('textbox', {
        name: copy('consoleUi', 'sms.register.paramsLabel'),
        exact: true,
      })
      .fill('to')
    await register
      .getByRole('textbox', {
        name: copy('consoleUi', 'sms.register.codesLabel'),
        exact: true,
      })
      .fill(`${EN_LOCALE}:${EN_CODE}, ${ZH_LOCALE}:${ZH_CODE}`)
    await register
      .getByRole('button', {
        name: copy('consoleUi', 'sms.register.submit'),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(copy('consoleUi', 'sms.registered', { name: TEMPLATE }), {
        exact: true,
      }),
    ).toBeVisible()

    await refreshUntil(page, TEMPLATE, approvalText(EN_LOCALE, 'pending'))
    const row = templateRow(page, TEMPLATE)
    await expect(row).toHaveCount(1)
    await expect(row).toContainText(copy('consoleUi', 'sms.source.runtime'))
    await expect(row).toContainText(copy('consoleUi', 'sms.enabledYes'))
    await expect(row).toContainText(approvalText(EN_LOCALE, 'pending'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))
  })

  await test.step('disabling the template leaves both review states pending', async () => {
    const row = templateRow(page, TEMPLATE)
    await row
      .getByRole('button', {
        name: copy('consoleUi', 'sms.disableAria', { name: TEMPLATE }),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(
        copy('consoleUi', 'sms.disabledToast', { name: TEMPLATE }),
        { exact: true },
      ),
    ).toBeVisible()
    await refreshUntil(page, TEMPLATE, copy('consoleUi', 'sms.enabledNo'))
    await expect(row).toContainText(copy('consoleUi', 'sms.enabledNo'))
    await expect(row).toContainText(approvalText(EN_LOCALE, 'pending'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))
  })

  await test.step('the review-only control settles the English language and leaves Chinese pending', async () => {
    const control = verdictControl(page)
    await expect(control).toBeVisible()
    await expect(
      control.getByText(copy('demo', 'smsReview.description'), { exact: true }),
    ).toBeVisible()
    await control
      .getByRole('textbox', {
        name: copy('demo', 'smsReview.codeLabel'),
        exact: true,
      })
      .fill(EN_CODE)
    await control
      .getByRole('combobox', {
        name: copy('demo', 'smsReview.stateLabel'),
        exact: true,
      })
      .selectOption('approved')
    await control
      .getByRole('button', {
        name: copy('demo', 'smsReview.submit'),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(
        copy('demo', 'smsReview.set', {
          code: EN_CODE,
          state: copy('demo', 'smsReview.state.approved'),
        }),
        { exact: true },
      ),
    ).toBeVisible()

    await refreshUntil(
      page,
      TEMPLATE,
      approvalText(EN_LOCALE, 'approved'),
    )
    const row = templateRow(page, TEMPLATE)
    await expect(row).toContainText(approvalText(EN_LOCALE, 'approved'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))
    // The template is still disabled: the verdict changed the review state and
    // nothing else.
    await expect(row).toContainText(copy('consoleUi', 'sms.enabledNo'))
  })

  await test.step('enabling the template sends and leaves the review states unchanged', async () => {
    const row = templateRow(page, TEMPLATE)
    await row
      .getByRole('button', {
        name: copy('consoleUi', 'sms.enableAria', { name: TEMPLATE }),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(copy('consoleUi', 'sms.enabledToast', { name: TEMPLATE }), {
        exact: true,
      }),
    ).toBeVisible()
    await refreshUntil(page, TEMPLATE, copy('consoleUi', 'sms.enabledYes'))
    await expect(row).toContainText(copy('consoleUi', 'sms.enabledYes'))
    await expect(row).toContainText(approvalText(EN_LOCALE, 'approved'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))
  })

  await test.step('the member dispatches a notice and the approved language reaches the SMS carrier', async () => {
    await backToApp(page).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()
    await switchToTenant(page)

    const notice = page.getByRole('region', {
      name: copy('demo', 'home.reviewNotice.title'),
      exact: true,
    })
    await expect(notice).toBeVisible()
    await expect(
      notice.getByText(copy('demo', 'home.reviewNotice.description'), {
        exact: true,
      }),
    ).toBeVisible()

    const logAfter = server.log.mark()
    await notice
      .getByRole('button', {
        name: copy('demo', 'home.reviewNotice.action'),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(copy('demo', 'home.reviewNotice.sent'), { exact: true }),
    ).toBeVisible()

    // The message the carrier accepted: the template, the recipient's language
    // and the destination the dispatch resolved for this phone.
    const carrier = await server.waitForCarrierMessage({
      to: PHONE_E164,
      template: TEMPLATE,
      after: logAfter,
    })
    expect(carrier.locale).toBe('en-US')

    // The external-send record the delivery pipeline writes when a message
    // leaves the process.
    const outbound = await server.waitForOutboundRecord({
      channel: 'sms',
      typeKey: TYPE_KEY,
      outcome: 'sent',
      after: logAfter,
    })
    expect(outbound.providerID).not.toBe('')
    expect(outbound.durationMS).toBeGreaterThanOrEqual(0)
  })

  await test.step('a disabled template is refused locally and no message leaves', async () => {
    await openSmsPage(page)
    const row = templateRow(page, TEMPLATE)
    await row
      .getByRole('button', {
        name: copy('consoleUi', 'sms.disableAria', { name: TEMPLATE }),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(
        copy('consoleUi', 'sms.disabledToast', { name: TEMPLATE }),
        { exact: true },
      ),
    ).toBeVisible()
    await refreshUntil(page, TEMPLATE, copy('consoleUi', 'sms.enabledNo'))
    await expect(row).toContainText(copy('consoleUi', 'sms.enabledNo'))
    await expect(row).toContainText(approvalText(EN_LOCALE, 'approved'))
    await expect(row).toContainText(approvalText(ZH_LOCALE, 'pending'))

    await backToApp(page).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()
    await switchToTenant(page)

    const logAfter = server.log.mark()
    await page
      .getByRole('button', {
        name: copy('demo', 'home.reviewNotice.action'),
        exact: true,
      })
      .click()
    await expect(
      page.getByText(copy('demo', 'home.reviewNotice.sent'), { exact: true }),
    ).toBeVisible()

    // Nothing left the process: no carrier line and no external-send record
    // after the click. The SMS page above still renders the template as
    // disabled with its review states unchanged, which is the refusal the
    // participant observes. The ledger row that records the refusal is covered
    // by the review-tagged Go case named in this spec's header, not here.
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
  })
})
