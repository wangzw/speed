/**
 * J13 -- a platform operator impersonates a member for support, works as that
 * member, ends the impersonation and finds the record and the audit trail.
 *
 * The seeded operator is not a member of the target tenant (acme) and holds no
 * permission in it: the requirement is an initiator outside the organization.
 * Impersonation is a platform capability decided in the platform domain, so the
 * operator's platform-administrator role is what admits the page and the start;
 * the tenant in the path is the data the record is confined to. The operator
 * holds two two-step methods: an authenticator factor aged past the factor-age
 * requirement, and an SMS factor on a proven phone. The target is an ordinary
 * member of the tenant, not an administrator. The spec signs in with the
 * password and an authenticator code computed from the review seed's fixed
 * base32 secret (helpers/totp.ts, the same secret a person imports into an
 * authenticator app), enters the admin, opens the impersonation page and
 * reads the target account's identifier from the admin's own accounts list.
 *
 * THE FRESHNESS WAIT, AND WHY IT IS NOT A SLEEP. The start requires a local
 * multi-factor authentication no older than authn.impersonation.max-auth-age;
 * the review assembly lowers that to its one-minute floor. The spec therefore
 * has to let the window pass before the main-path start is refused and the
 * step-up branch is reached. It observes the window through the product: while
 * the session is still fresh, submitting the start with an over-long reason is
 * refused with the restricted-target/parameter text (the freshness check, the
 * target check and then the parameter check are the server's order); once the
 * window has passed the same submission is refused for authentication strength
 * and the admin opens its step-up dialog. The spec repeats that submission
 * until the dialog appears -- a state transition of the rendered product, never
 * a fixed delay. The long reason is discarded: the dialog is cancelled, the
 * reason is corrected, and the start is submitted again, stepped up with SMS,
 * and replayed by the admin's own gate.
 *
 * The first submission is not assumed to land inside the window. The probe
 * classifies the product's answer instead: the restricted text is the
 * in-window refusal, and the step-up dialog is the same rule seen after the
 * window has elapsed (a slow setup can spend the minute before the first
 * click). When the dialog arrives first, the in-window refusal is not asserted
 * and the run continues through the step-up branch; the transition from the
 * restricted text to the dialog is observed only on a run that starts inside
 * the window. Correctness therefore never depends on setup finishing inside
 * the one-minute wall clock.
 *
 * The target member is seeded without an email address on purpose (the review
 * notification worker addresses a notice only to a member with a mailbox, and
 * an extra mailbox would change the counts another review fixture asserts), so
 * no notice is addressed to the target. Step 5 therefore proves the inbox
 * opens as the impersonated identity inside the grant's tenant -- its normal
 * empty state rather than the tenant-unavailable state an identity without a
 * membership in acme would meet -- and leaves the "a row shows only the
 * recipient's messages with every field" facet of REQ-notifications-001 and the
 * admission-refusal facet of REQ-notifications-025 out of `@covers`.
 *
 * Several ids the journey document lists are claimed narrowly:
 *
 *  - REQ-platform-admin-022: the spec proves the submit is unavailable until a
 *    tenant and target are chosen and that leaving the TTL empty takes the
 *    server's default (the banner states a planned expiry). It does not drive
 *    the server's empty-reason refusal.
 *  - REQ-platform-admin-023 / REQ-security-015: the spec drives the freshness
 *    refusal, the step-up with a pre-enabled SMS factor (a different channel
 *    from the authenticator login) and the replayed start. The no-factor and
 *    freshly-enrolled-factor clauses are not driven.
 *  - REQ-platform-admin-024: the over-long reason is the parameter refusal the
 *    freshness wait turns on; the spec asserts the refusal's multi-clause text
 *    while the session is fresh. The non-member, disabled and protected target
 *    cases are not driven.
 *  - REQ-platform-admin-084: only the explicit end reason is observed.
 *  - REQ-platform-admin-109: the spec observes that the initiator's platform
 *    permissions do not apply during the impersonation (the admin is
 *    unreachable); the target-holds-a-permission positive case is not driven.
 *  - REQ-platform-admin-110: the end audit row's actor, target account, tenant
 *    and session are asserted; the "same action after the end carries no such
 *    detail" counter-case is not driven.
 *  - REQ-platform-admin-029: the action, outcome and actor filters are applied
 *    on submit; the time filters are not driven.
 *  - REQ-security-014: the replayed start landing is asserted. The
 *    cancel-leaves-it-unexecuted half is not driven.
 *
 * REQ-platform-admin-107/108/018 (the target's platform permissions, the
 * disabled/protected/self target refusals and the seasoned-factor refusal at
 * another age) and REQ-security-013 (the freshness rule for the account writes
 * it enumerates) are left out of `@covers`: this journey exercises the valid
 * path or a different refusal, so those assertions could not fail on them.
 *
 * Expected copy is read from the shipped en-US locale bundles through
 * helpers/i18n.ts. The SMS code the review carrier writes to the process log
 * (fixtures/log.ts) is read as on every journey that verifies a code. The
 * accounts page's own roster read is observed for the operator's account id,
 * which the audit actor filter still takes and the page never renders; the
 * target is chosen through the account picker by a login name instead.
 */
// @journey journey-impersonation-for-support.md
// @covers REQ-platform-admin-001, REQ-platform-admin-003, REQ-platform-admin-021, REQ-platform-admin-022, REQ-platform-admin-023, REQ-platform-admin-024, REQ-platform-admin-027, REQ-platform-admin-029, REQ-platform-admin-078, REQ-platform-admin-081, REQ-platform-admin-082, REQ-platform-admin-084, REQ-platform-admin-109, REQ-platform-admin-110, REQ-security-014, REQ-security-015, REQ-tenancy-024, REQ-tenancy-038
import type { Locator, Page } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import { scaledTimeout } from '../fixtures/time-scale'
import {
  adminEntry,
  closeAccountMenu,
  openAccountMenu,
  organizationRow,
} from '../helpers/account-menu'
import { copy, copyPattern } from '../helpers/i18n'
import { totpCode } from '../helpers/totp'

// The impersonation factors change how the seeded operator signs in, so only
// this journey's server turns them on. The admin tables carry low-priority
// columns (the impersonation record's tenant, the settings-style type columns),
// so the desktop viewport is wide enough for every column this journey reads.
test.use({ seedImpersonation: true, viewport: { width: 1920, height: 1080 } })

/** The seeded platform administrator the review assembly writes. */
const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

/**
 * The seeded target: an enabled, unprotected member of acme, not the operator.
 * Its login username is the seed's `member` identifier; the console renders
 * the composed `Demo Member (member)` label.
 */
const TARGET = { displayName: 'Demo Member', username: 'member' } as const

/** The tenant the impersonation is confined to. */
const TENANT = 'acme'

/**
 * The tenant the operator's own membership list settles: the platform
 * administrator belongs to the system tenant and is deliberately not an acme
 * member (the impersonation requirement is an operator outside the tenant).
 * The current tenant settles from that list, not from the platform roster.
 */
const OPERATOR_TENANT = 'system'

/** The phone the review fixture proves and binds the operator's SMS factor to. */
const OPERATOR_PHONE = '+15551234567'

/**
 * The operator's authenticator seed, fixed by the review fixture in
 * starters/demo/server/review_seed.go. A review fixture and not a credential:
 * it is the value a person imports into an authenticator app.
 */
const TOTP_SEED = 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP'

/** The reason recorded on the impersonation and its start audit row. */
const REASON = 'Support ticket 4711: reproduce the reported inbox problem.'

/**
 * A reason the server refuses: one character past the 500-rune bound. It is
 * the probe the freshness wait uses, never the reason of the journey's start.
 */
const OVERLONG_REASON = 'x'.repeat(501)

/** The journey's own start and end audit actions. */
const ACTION_START = 'authn.impersonation.start'
const ACTION_END = 'authn.impersonation.end'

/** The tenant home's heading (the tenant area's landing page). */
function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

/** The account menu's trigger on the sidebar's account row.
 *
 * Its accessible name carries the signed-in identity (with the visible
 * initials in front of it), so asserting the identity is present is what says
 * the menu names the acting account. */
async function expectAccountMenuName(
  trigger: Locator,
  identity: string,
): Promise<void> {
  await expect(trigger).toHaveAccessibleName(new RegExp(escapeRegExp(identity)))
}

/** The home page's admin card, which must agree with the entry's visibility. */
function adminCard(page: Page): Locator {
  return page.getByRole('heading', {
    level: 2,
    name: copy('demo', 'home.cards.admin.title'),
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

/** The impersonation banner's visible region. */
function banner(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('authUi', 'impersonation.regionLabel'),
    exact: true,
  })
}

/** The notification surface's preference matrix. */
function preferencesMatrix(page: Page): Locator {
  return page.getByRole('table', {
    name: copy('notificationUi', 'preferences.title'),
    exact: true,
  })
}

/** The step-up dialog. */
function stepUpDialog(page: Page): Locator {
  return page.getByRole('dialog', {
    name: copy('adminUi', 'stepUp.title'),
    exact: true,
  })
}

/** The tenant selector of a tenant-addressed admin page. */
function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'common.tenant'),
    exact: true,
  })
}

/** The impersonation page's start form controls. */
function targetField(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'impersonations.start.accountLabel'),
    exact: true,
  })
}
function reasonField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('adminUi', 'impersonations.start.reasonLabel'),
    exact: true,
  })
}
function ttlField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('adminUi', 'impersonations.start.ttlLabel'),
    exact: true,
  })
}
function startButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'impersonations.start.submit'),
    exact: true,
  })
}

/**
 * The target account's rendered identity label: the display name with the
 * login username beside it, which is how every console surface names it.
 */
function targetLabel(): string {
  return `${TARGET.displayName} (${TARGET.username})`
}

/**
 * Choose the impersonation target through the account picker. The operator
 * identifies an account by a login name it can read; the picker searches the
 * roster and hands the chosen row's internal id to the request. The caller
 * re-runs this after a status read remounts the form, because a remount drops
 * the selection with the draft.
 */
async function chooseTarget(page: Page): Promise<void> {
  const field = targetField(page)
  await field.click()
  await field.fill(TARGET.username)
  await page
    .getByRole('option', { name: new RegExp(escapeRegExp(TARGET.displayName)) })
    .click()
}

/**
 * Wait for the accounts page's own roster read and answer the account id of
 * the row carrying `displayName`. The page never renders that id -- it names
 * accounts by their display name and username -- but the audit actor filter
 * still takes one, so the test learns the value from the response the page
 * itself received.
 */
async function accountIdFromRoster(
  page: Page,
  displayName: string,
): Promise<string> {
  const response = await page.waitForResponse(
    (candidate) =>
      candidate.request().method() === 'GET' &&
      new URL(candidate.url()).pathname === '/api/v1/system/auth/accounts',
  )
  expect(response.ok()).toBe(true)
  const body = (await response.json()) as {
    readonly accounts: readonly {
      readonly display_name: string
      readonly id: string
    }[]
  }
  const row = body.accounts.find((account) => account.display_name === displayName)
  if (row === undefined) {
    throw new Error(`the account roster carries no row named ${displayName}`)
  }
  return row.id
}

/** Escape the characters a regular expression reads as syntax. */
function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/** One definition term of an audit detail, found by its exact text. */
function term(page: Page, label: string): Locator {
  return page.locator('dt', {
    hasText: new RegExp(`^${escapeRegExp(label)}$`),
  })
}

/** The value cell beside one definition term. */
function fieldValue(page: Page, label: string): Locator {
  return term(page, label).locator('xpath=following-sibling::dd[1]')
}

test('impersonation-for-support: the operator acts as a member and the record and audit trail survive the end', async ({
  demo,
}) => {
  // The freshness window is a real backend minute, and the journey around it
  // (two-factor sign-in, admin navigation, the impersonation, the end, the
  // record and the audit reads) does not fit the tier's default bound.
  test.setTimeout(scaledTimeout(180_000))

  const { page, server, accounts } = demo
  let operatorAccountId = ''

  await test.step('precondition: the operator signs in with the password and the authenticator code', async () => {
    // The password step parks the login on its second factor; the operator has
    // two of them, and the seeded authenticator one is the strong, seasoned
    // factor the impersonation gate requires.
    await accounts.signIn({
      identifier: OPERATOR.username,
      password: OPERATOR.password,
    })
    // The step belongs to the password channel, which renders in its own
    // panel. The enterprise entry sits on the same sign-in page and carries
    // its own "Continue", so every control of the step is addressed within
    // that panel.
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
    const codeField = passwordPanel.getByRole('textbox', {
      name: copy('authUi', 'smsSignIn.codeLabel'),
      exact: true,
    })
    await expect(codeField).toBeVisible()
    // The code is computed at the moment it is typed, from the fixed review
    // seed; the thirty-second step is the one the server verifies.
    await codeField.fill(totpCode(TOTP_SEED))
    await passwordPanel
      .getByRole('button', {
        name: copy('authUi', 'signInFlow.submit'),
        exact: true,
      })
      .click()
    await expect(homeHeading(page)).toBeVisible()

    // The operator is platform staff: the account menu's admin entry and the
    // home card both exist before any impersonation. The card is read with
    // the menu closed; the entry with it open.
    await expect(adminCard(page)).toBeVisible()
    await openAccountMenu(page)
    await expect(adminEntry(page)).toBeVisible()
    await closeAccountMenu(page)
  })

  await test.step('step 1: the operator opens the impersonation page and it reports no impersonation', async () => {
    await openAccountMenu(page)
    await adminEntry(page).click()
    await expect(adminNav(page)).toBeVisible()
    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'impersonations.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'impersonations.title'),
        exact: true,
      }),
    ).toBeVisible()

    // The current-impersonation section is a settled "none": no failure
    // placeholder, no retry control and no stale marker beside it.
    await expect(
      page.getByText(copy('adminUi', 'impersonations.current.none'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByText(copy('adminUi', 'impersonations.current.error.title'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(
      page.getByRole('button', {
        name: copy('adminUi', 'impersonations.retry'),
        exact: true,
      }),
    ).toHaveCount(0)

    // The start area explains its factor-age precondition and, with no tenant
    // chosen, says so and keeps the submit unavailable.
    await expect(
      page.getByText(copy('adminUi', 'impersonations.start.precondition'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByText(copy('adminUi', 'impersonations.start.tenantHint'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(startButton(page)).toBeDisabled()
  })

  await test.step('step 2: the target organization and account are chosen, and the start is submitted', async () => {
    // The target is chosen through the account picker, by a login name the
    // operator can read. The audit actor filter still takes an account id, so
    // the operator's id is learned from the roster read the accounts page
    // makes on the way to the impersonation page; the id is never rendered.
    const operatorIdRead = accountIdFromRoster(page, OPERATOR.displayName)
    const targetIdRead = accountIdFromRoster(page, TARGET.displayName)
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
    operatorAccountId = await operatorIdRead
    // The target is a different account from the operator, which is the
    // requirement the start form's roster check leaves to the server.
    expect(await targetIdRead).not.toBe(operatorAccountId)

    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'impersonations.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'impersonations.title'),
        exact: true,
      }),
    ).toBeVisible()

    await tenantSelector(page).click()
    await page.getByRole('option', { name: TENANT, exact: true }).click()
    await chooseTarget(page)
    await expect(startButton(page)).toBeEnabled()

    // The TTL is left empty on purpose: the server's default lifetime is what
    // the banner's planned expiry then states.
    await expect(ttlField(page)).toHaveValue('')

    // The freshness window is a backend fact (authn.impersonation.max-auth-age,
    // one minute here). The probe below is a start the server can only refuse
    // on the reason's length: while the session is fresh it answers with the
    // restricted target/parameter text, and once the window has passed the
    // same submission is refused for authentication strength and raises the
    // step-up dialog. The first answer is classified rather than presumed, and
    // the transition is observed where it can be, so nothing here waits on a
    // guessed duration and nothing presumes the setup fitted the window.
    await reasonField(page).fill(OVERLONG_REASON)
    await startButton(page).click()

    const restricted = page.getByText(
      copy('adminUi', 'errors.authn.impersonation_restricted'),
      { exact: true },
    )
    const dialog = stepUpDialog(page)

    /** Re-enter the probe; the status re-read may have remounted the form. */
    const submitProbe = async (): Promise<void> => {
      await chooseTarget(page)
      await reasonField(page).fill(OVERLONG_REASON)
      await expect(startButton(page)).toBeEnabled()
      await startButton(page).click()
    }

    // Wait for whichever answer the server gives first. The restricted text is
    // the in-window refusal; the step-up dialog is the same rule seen after the
    // window elapsed, which a slow setup can reach before the first click.
    // `waitFor` resolves on the visible state and rejects on its timeout, and
    // both are caught, so the race settles as soon as either appears.
    await Promise.race([
      restricted
        .waitFor({ state: 'visible', timeout: scaledTimeout(20_000) })
        .catch(() => undefined),
      dialog
        .waitFor({ state: 'visible', timeout: scaledTimeout(20_000) })
        .catch(() => undefined),
    ])

    if (!(await dialog.isVisible()) && !(await restricted.isVisible())) {
      // Neither answer landed: the form was remounted while a status read
      // settled. Re-enter the probe and wait for one of the two answers, so
      // the observation is about the window, not about a draft surviving a
      // read a person cannot control.
      await expect(async () => {
        if (await dialog.isVisible()) return
        if (await restricted.isVisible()) return
        await submitProbe()
        throw new Error('the start has answered neither outcome yet')
      }).toPass({ timeout: scaledTimeout(120_000), intervals: [2_000] })
    }

    if (await restricted.isVisible()) {
      // The over-long reason was refused while the session was still fresh.
      await expect(restricted).toBeVisible()
      // The same submission is refused for authentication strength once the
      // window passes; observe that transition rather than waiting a fixed
      // time. The length is what keeps the attempt unable to start while the
      // session is still young.
      await expect(async () => {
        if (await dialog.isVisible()) return
        await submitProbe()
        throw new Error('the step-up dialog has not opened yet')
      }).toPass({ timeout: scaledTimeout(120_000), intervals: [2_000] })
    } else {
      // The window had already elapsed before the first submission, so the
      // in-window refusal is not observable on this run. The step-up branch
      // below is still driven; the annotation records the skipped assertion.
      test.info().annotations.push({
        type: 'freshness-window',
        description:
          'the impersonation max-auth-age window had already elapsed before ' +
          'the first start; the in-window refusal was not asserted',
      })
    }

    // The step-up was raised for the probe, not for the journey's start. A
    // cancelled dialog replays nothing, so the corrected start is submitted
    // afresh below.
    await dialog
      .getByRole('button', {
        name: copy('adminUi', 'common.cancel'),
        exact: true,
      })
      .click()
    await expect(dialog).toHaveCount(0)
  })

  await test.step('step 3: the start is refused for freshness and the step-up replays it after SMS', async () => {
    // The admin hands the refused start to its elevation gate. The flow
    // offers a factor kind the signing-in session has not proved: the operator
    // authenticated with the authenticator, so the SMS factor is the one on
    // offer.
    const dialog = stepUpDialog(page)
    await expect(async () => {
      if (await dialog.isVisible()) {
        return
      }
      // The form is re-entered for the same reason as in the wait above: the
      // status read may have remounted it. The target and the corrected
      // reason are the body the gate must replay.
      await chooseTarget(page)
      await reasonField(page).fill(REASON)
      await expect(startButton(page)).toBeEnabled()
      await startButton(page).click({ timeout: scaledTimeout(3_000) })
      await expect(dialog).toBeVisible({ timeout: scaledTimeout(8_000) })
    }).toPass({ timeout: scaledTimeout(30_000), intervals: [1_000] })
    const beforeSmsCode = server.log.mark()
    await dialog
      .getByRole('button', {
        name: copy('adminUi', 'stepUp.factorKind.sms'),
        exact: true,
      })
      .click()

    // The challenge field's label is the server-supplied field name (`code`),
    // which FlowStep renders as the input's label; it is protocol data, not
    // interface copy, so there is no locale bundle key behind it and the
    // literal is the field's own name. The review carrier writes the code to
    // the process log, this spec's one non-UI read.
    const codeField = dialog.getByRole('textbox', {
      name: 'code',
      exact: true,
    })
    await expect(codeField).toBeVisible()
    const { code } = await waitForVerificationCode(
      server.log,
      'sms',
      OPERATOR_PHONE,
      { after: beforeSmsCode },
    )
    await codeField.fill(code)
    const verify = dialog.getByRole('button', {
      name: copy('adminUi', 'stepUp.submit'),
      exact: true,
    })
    await verify.click()
    // The code answer settles the challenge step; the admin's step-up
    // driver answers the flow's ready confirmation on its own, so the dialog
    // closes without a second click on the same control.
    await expect(codeField).toHaveCount(0)
    await expect(dialog).toHaveCount(0)

    // The won challenge replays the refused start once; it lands, and the host
    // takes the operator to the tenant area as the target member.
    await expect(homeHeading(page)).toBeVisible()
    expect(new URL(page.url()).pathname).toBe('/')
  })

  await test.step('step 4: the impersonation lands on the tenant home with its banner and indicators', async () => {
    const notice = banner(page)
    await expect(notice).toBeVisible()

    // The banner names the target by display name and username together, the
    // grant's tenant, the planned end, and the management restriction; its end
    // control is the way out.
    await expect(notice).toContainText(
      copy('authUi', 'impersonation.actingAs', {
        subject: targetLabel(),
      }),
    )
    await expect(notice).toContainText(
      copy('authUi', 'impersonation.tenant', { tenant: TENANT }),
    )
    await expect(notice).toContainText(
      copy('authUi', 'impersonation.managementUnavailable'),
    )
    await expect(
      notice.getByText(
        copyPattern('authUi', 'impersonation.expiresAt', {
          expiresAt: '.+',
        }),
      ),
    ).toBeVisible()
    await expect(
      notice.getByRole('button', {
        name: copy('authUi', 'impersonation.end'),
        exact: true,
      }),
    ).toBeVisible()

    // The chrome carries the persistent impersonating indicator -- in the
    // app bar's user slot, or in the sidebar's bottom row when the shell hides
    // the bar -- and the account menu's first entry is the same end action.
    await expect(
      page.getByText(copy('authUi', 'impersonation.impersonating'), {
        exact: true,
      }),
    ).toBeVisible()
    const menuTrigger = page.getByTestId('account-entry-trigger')
    await expectAccountMenuName(menuTrigger, TARGET.displayName)
    await menuTrigger.click()
    await expect(
      page.getByRole('menuitem', {
        name: copy('authUi', 'impersonation.end'),
        exact: true,
      }),
    ).toBeVisible()

    // The admin entry and the organization row are gone from the menu: an
    // impersonation is confined to one organization and the target holds no
    // platform permission. Both are asserted with the menu open; the home
    // card is read after it closes, because the backdrop hides the page.
    await expect(adminEntry(page)).toHaveCount(0)
    await expect(organizationRow(page, TENANT)).toHaveCount(0)
    await closeAccountMenu(page, 'sidebar')
    await expect(adminCard(page)).toHaveCount(0)

    // A admin path entered by a full document load is silently brought back
    // to the tenant home: the admin area is decided by the effective
    // account's platform permission set, and a target never holds one. The
    // banner remains the operator's explanation. The load also
    // keeps the grant's tenant named -- the host reads the impersonated
    // account's own membership list, which the server narrows to the grant --
    // so the tenant-scoped surfaces the next steps read keep working across a
    // reload.
    await page.goto(
      new URL('/admin/impersonations?tenant=acme', page.url()).toString(),
    )
    await expect(homeHeading(page)).toBeVisible()
    expect(new URL(page.url()).pathname).toBe('/')
    await expect(banner(page)).toBeVisible()
    await expect(adminNav(page)).toHaveCount(0)
  })

  await test.step('step 5: the notification inbox opens under the impersonated identity', async () => {
    // The bar carries no bell any more: the inbox is the account menu's
    // notification-centre row. The target has no mailbox and no notice is
    // addressed to it, so the row carries no count.
    await openAccountMenu(page)
    await page
      .getByRole('menu')
      .getByRole('menuitem', {
        name: copy('demo', 'userMenu.messages'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        name: copy('notificationUi', 'inbox.title'),
        exact: true,
      }),
    ).toBeVisible()

    // The inbox is the member's, inside the grant's tenant: it renders its
    // normal empty state. The tenant-unavailable state would be the answer for
    // an identity without a membership in acme, which is what the operator is
    // -- so seeing the empty inbox is the rendered proof that the effective
    // identity is the target member.
    await expect(
      page.getByText(copy('notificationUi', 'inbox.empty.title'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByText(copy('notificationUi', 'inbox.empty.description'), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByText(copy('notificationUi', 'tenantUnavailable.title'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(banner(page)).toBeVisible()
  })

  await test.step('step 5b: a full page load keeps the notification surface inside the grant tenant', async () => {
    // The support operator's job is done in the member's own surfaces, so a
    // reload has to keep them inside the grant's tenant. A full document load
    // re-mints every module-level cell; if the grant's tenant could no longer
    // be named, the tenant-scoped reads would be refused 404 and this page
    // would show the organization-unavailable state instead of its content.
    await page.reload()
    await expect(
      page.getByRole('heading', {
        name: copy('notificationUi', 'inbox.title'),
        exact: true,
      }),
    ).toBeVisible()

    // The preference matrix is a tenant-scoped read of its own. The normal
    // matrix is the proof the reload kept announcing the tenant; the refusal
    // state is the answer for an identity with no membership there.
    await expect(preferencesMatrix(page)).toBeVisible()
    await expect(
      page.getByText(copy('notificationUi', 'tenantUnavailable.title'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(
      page.getByText(copy('notificationUi', 'tenantUnavailable.description'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expect(banner(page)).toBeVisible()
  })

  await test.step("step 6: the banner ends the impersonation and the operator's own identity returns", async () => {
    await banner(page)
      .getByRole('button', {
        name: copy('authUi', 'impersonation.end'),
        exact: true,
      })
      .click()

    // The banner withdraws at the write's settlement and the one live region
    // announces the end.
    await expect(banner(page)).toHaveCount(0)
    await expect(
      page.getByText(copy('authUi', 'impersonation.ended'), { exact: true }),
    ).toBeVisible()

    // The indicator goes, the account menu names the operator again, and the
    // admin entry and the organization row come back.
    await expect(
      page.getByText(copy('authUi', 'impersonation.impersonating'), {
        exact: true,
      }),
    ).toHaveCount(0)
    await expectAccountMenuName(
      page.getByTestId('account-entry-trigger'),
      OPERATOR.displayName,
    )
    await openAccountMenu(page)
    await expect(adminEntry(page)).toBeVisible()
    // The organization row names the organization the operator's own
    // membership list settles (the system organization); the grant's
    // organization was never the operator's own.
    await expect(organizationRow(page, OPERATOR_TENANT)).toBeVisible()
    await closeAccountMenu(page)

    // No re-login happened: the ended-session surface never appears.
    await expect(
      page.getByRole('heading', {
        name: copy('authUi', 'sessionEnded.title'),
        exact: true,
      }),
    ).toHaveCount(0)
  })

  await test.step('step 7: the impersonation record and the audit trail carry the start and the end', async () => {
    await openAccountMenu(page)
    await adminEntry(page).click()
    await expect(adminNav(page)).toBeVisible()
    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'impersonations.title'),
        exact: true,
      })
      .click()

    // The current section is again a settled "none", not the finished grant.
    await expect(
      page.getByText(copy('adminUi', 'impersonations.current.none'), {
        exact: true,
      }),
    ).toBeVisible()

    await tenantSelector(page).click()
    await page.getByRole('option', { name: TENANT, exact: true }).click()

    // One record: the operator, the target, the tenant, the planned start and
    // end instants, and the ended state with its reason.
    const table = page.getByRole('table')
    const rows = table.locator('tbody tr')
    await expect(rows).toHaveCount(1)
    const record = rows.first()
    // The actor and target cells name the accounts by display name and login
    // username together, never by the recorded account id.
    await expect(
      record.getByText(`${OPERATOR.displayName} (${OPERATOR.username})`, {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      record.getByText(targetLabel(), { exact: true }),
    ).toBeVisible()
    await expect(record.getByText(TENANT, { exact: true })).toBeVisible()
    await expect(
      record.getByText(
        copy('adminUi', 'impersonations.ended', {
          reason: copy('adminUi', 'impersonations.endReason.explicit'),
        }),
        { exact: true },
      ),
    ).toBeVisible()
    const cells = record.getByRole('cell')
    await expect(cells.nth(3)).toHaveText(/\d/u)
    await expect(cells.nth(4)).toHaveText(/\d/u)
    await expect(
      record.getByRole('button', {
        name: copy('adminUi', 'impersonations.terminate'),
        exact: true,
      }),
    ).toBeDisabled()

    // The audit trail: the end row is found by action, outcome and actor.
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

    const auditTable = page.getByRole('table')
    await expect(auditTable.locator('tbody tr').first()).toBeVisible()
    await page
      .getByLabel(copy('adminUi', 'audit.filters.action'), { exact: true })
      .fill(ACTION_END)
    await page
      .getByLabel(copy('adminUi', 'audit.filters.outcome'), { exact: true })
      .fill('success')
    await page
      .getByLabel(copy('adminUi', 'audit.filters.actor'), { exact: true })
      .fill(operatorAccountId)
    // Typing alone applies nothing: the unfiltered list still holds records.
    expect(await auditTable.locator('tbody tr').count()).toBeGreaterThan(1)
    await page
      .getByRole('button', {
        name: copy('adminUi', 'audit.filters.apply'),
        exact: true,
      })
      .click()
    await expect(auditTable.locator('tbody tr')).toHaveCount(1)

    await auditTable
      .getByRole('link', { name: ACTION_END, exact: true })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'auditDetail.title'),
        exact: true,
      }),
    ).toBeVisible()

    // The end row names the operator as the actor and the target account as
    // the object, both by display name and login username; the account ids the
    // record carries never reach the page.
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.action')),
    ).toHaveText(ACTION_END)
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.outcome')),
    ).toHaveText('success')
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.actor')),
    ).toHaveText(`${OPERATOR.displayName} (${OPERATOR.username})`)
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.object')),
    ).toHaveText(targetLabel())
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.purpose')),
    ).toHaveText(copy('adminUi', 'common.emptyValue'))

    // The detail section carries the impersonation's own keys: the grant, the
    // tenant it was confined to, and the session it ran on.
    await expect(term(page, 'grant_id')).toBeVisible()
    await expect(term(page, 'tenant')).toBeVisible()
    await expect(term(page, 'sid')).toBeVisible()
    await expect(fieldValue(page, 'tenant')).toHaveText(TENANT)
    await expect(fieldValue(page, 'sid')).not.toHaveText('')
    const endGrantId =
      (await fieldValue(page, 'grant_id').textContent())?.trim() ?? ''
    expect(endGrantId).not.toBe('')

    // The start row is found the same way: its own action and its granted
    // outcome, by the same actor.
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
    await page
      .getByLabel(copy('adminUi', 'audit.filters.action'), { exact: true })
      .fill(ACTION_START)
    await page
      .getByLabel(copy('adminUi', 'audit.filters.outcome'), { exact: true })
      .fill('granted')
    await page
      .getByLabel(copy('adminUi', 'audit.filters.actor'), { exact: true })
      .fill(operatorAccountId)
    await page
      .getByRole('button', {
        name: copy('adminUi', 'audit.filters.apply'),
        exact: true,
      })
      .click()
    await expect(auditTable.locator('tbody tr')).toHaveCount(1)
    const startRow = auditTable.locator('tbody tr').first()
    await expect(
      startRow.getByText(`${OPERATOR.displayName} (${OPERATOR.username})`, {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      startRow.getByText(targetLabel(), { exact: true }),
    ).toBeVisible()
    await expect(
      startRow.getByText('granted', { exact: true }),
    ).toBeVisible()

    // The start's detail links to the same grant the end names, and records
    // the settled lifetime and the stated purpose.
    await auditTable
      .getByRole('link', { name: ACTION_START, exact: true })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'auditDetail.title'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.action')),
    ).toHaveText(ACTION_START)
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.outcome')),
    ).toHaveText('granted')
    await expect(
      fieldValue(page, copy('adminUi', 'auditDetail.field.purpose')),
    ).toHaveText(REASON)
    await expect(fieldValue(page, 'grant_id')).toHaveText(endGrantId)
    await expect(term(page, 'ttl_seconds')).toBeVisible()
    await expect(fieldValue(page, 'sid')).not.toHaveText('')
    await expect(fieldValue(page, 'tenant')).toHaveText(TENANT)
  })
})
