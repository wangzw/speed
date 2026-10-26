/**
 * The account-offboarding journey -- an operator disables a member, the
 * member's sessions end, the member is told to sign in again, and after the
 * operator restores the account the member signs back in on the page that was
 * refused.
 *
 * THE SEEDED OPERATOR HAS FACTORS, SO THE STEP-UP BRANCH IS NOT DRIVEN. The
 * status write is one of the privileged writes that ask for a fresh local
 * multi-factor proof (REQ-platform-admin-114). The review seed's opt-in
 * impersonation factors give the operator an authenticator and an SMS factor
 * and prove the operator's address in the same seed. The operator signs in
 * with the password and the authenticator code, so the session already holds
 * a fresh local multi-factor proof and the save lands directly. The journey's
 * step-up paragraph -- the dialog and its one replay -- is therefore not
 * driven, and REQ-platform-admin-018 and REQ-security-014 stay out of
 * `@covers`. The no-factor half of that dialog is not reachable either: an
 * operator with no factor cannot make the privileged write at all.
 * REQ-security-013 stays out for the same class of reason: it names the
 * freshness rule of the self-service writes (password, identifiers, factors,
 * bindings, profile), and this journey's only privileged write is the
 * operator's status change. REQ-platform-admin-014 (display name, attributes
 * and the protected flag) is not driven by the journey's steps either.
 *
 * TWO MEMBER SESSIONS IN TWO CONTEXTS. The member signs in on two browser
 * contexts. Context A stays on the account page: before the disable its
 * session list holds two live rows with exactly one marked current. Context B
 * is the device the journey refuses: opening another in-app page after the
 * disable is refused, the session ends, and the frame is replaced by the
 * session-ended surface. The member then tries to sign in during the disable;
 * a wrong password and the account's own correct credentials both render the
 * one collapsed password failure, and the account-unavailable text never
 * appears.
 *
 * THE LIST IS RE-FILTERED AFTER EACH STATUS CHANGE. Step 2 narrows the list by
 * the member's login-identifier prefix and the enabled status, and step 3 says
 * the list then shows the account as disabled. The list read filters on the
 * server and the status write invalidates it, so the disabled row leaves the
 * enabled-filtered page. The spec re-submits the same prefix with the disabled
 * status to observe the row, and returns the filter to enabled after the
 * restore. Paging is not driven: the review seed holds two accounts on one
 * page. The filter's "applies only on submit" clause is observed before the
 * first submission, where the list still shows both accounts.
 *
 * THE RESTORE PATH IS DRIVEN. After the operator restores the account the
 * member reloads the protected page, meets the sign-in surface, signs in with
 * the same credentials and lands back on that page, which is REQ-signin-030's
 * "continue in place". The account's session list then holds one row, the
 * current session: both pre-disable sessions (context A's and context B's
 * earlier one) are gone, so the count is what discriminates a revocation from
 * a stale list. REQ-security-022 is claimed for the fields this journey
 * drives: the rows, the current-session marker, the sign-in line and the
 * expiry, which each live row names. A last-active line appears only once a
 * coalesced touch moves it past the creation instant, so that clause stays
 * undriven. This surface therefore fails for a product that omits the expiry;
 * the registry keeps the document's full list.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts.
 */
// @journey journey-account-offboarding.md
// @covers REQ-platform-admin-012, REQ-presentation-027, REQ-security-022, REQ-security-025, REQ-signin-006, REQ-signin-007, REQ-signin-030, REQ-tenancy-022
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { createAccounts } from '../helpers/accounts'
import { expect, test } from '../fixtures/demo'
import { copy, copyPattern } from '../helpers/i18n'
import { totpCode } from '../helpers/totp'

// The operator needs its two seeded factors and a proved address; the console
// tables carry low-priority columns, so the viewport is wide enough for the
// status column and the row controls.
test.use({ seedImpersonation: true, viewport: { width: 1920, height: 1080 } })

/** The seeded platform administrator the review assembly writes. */
const OPERATOR = {
  identifier: 'operator@demo.test',
  password: 'operator-dev-secret',
} as const

/** The seeded member of acme; it holds a username and no second factor. */
const MEMBER = {
  identifier: 'member',
  password: 'member-dev-secret',
  displayName: 'Demo Member',
} as const

/** The organization the member belongs to; its alias is the identifier. */
const TENANT = 'acme'

/**
 * The operator's authenticator seed, fixed by the review fixture in
 * starters/demo/server/review_seed.go. A review fixture, not a credential: it
 * is the value a person imports into an authenticator app.
 */
const TOTP_SEED = 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP'

/** A password that matches no account, used for the comparison attempt. */
const WRONG_PASSWORD = 'not-the-member-password'

/** The account page's path, which is the protected page the member was on. */
const ACCOUNT_PATH = '/account'

/** The header's console entry (a link, while the operator may reach it). */
function consoleEntry(page: Page): Locator {
  return page.getByRole('banner').getByRole('link', {
    name: copy('demo', 'nav.console'),
    exact: true,
  })
}

/** The console's navigation landmark. */
function consoleNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('consoleUi', 'shell.navLabel'),
    exact: true,
  })
}

/** The accounts table of the console page. */
function accountsTable(page: Page): Locator {
  return page.getByRole('table', {
    name: copy('uiKit', 'dataTable.ariaLabel'),
    exact: true,
  })
}

/** The accounts list's row for the member. */
function memberRow(page: Page): Locator {
  return accountsTable(page)
    .locator('tbody tr')
    .filter({ hasText: MEMBER.displayName })
}

/** The manage-control label prefix of an account row. */
function managePrefix(): string {
  return (
    copy('consoleUi', 'accounts.manageAria', { id: '__ACCOUNT_ID__' }).split(
      '__ACCOUNT_ID__',
    )[0] ?? ''
  )
}

/** The full identifier the row's manage control carries in its name. */
async function accountIdOf(row: Locator): Promise<string> {
  const prefix = managePrefix()
  const label = await row
    .getByRole('button', { name: prefix })
    .getAttribute('aria-label')
  if (label === null || !label.startsWith(prefix)) {
    throw new Error('the account row carries no identifier in its manage control')
  }
  const id = label.slice(prefix.length).trim()
  if (id === '') {
    throw new Error('the account row carries an empty identifier')
  }
  return id
}

/** The detail panel's accessible name for one account. */
function detailTitle(id: string): string {
  return copy('consoleUi', 'accounts.detail.title', { id })
}

/**
 * The settled status write's success notice.
 *
 * The console names an account in a success notice by its display name and
 * first login identifier and never by the opaque account id, so the expected
 * sentence is the bundle's `statusSaved` filled with the reference the same
 * bundle's `common.accountReference.nameWithIdentifier` renders. The seeded
 * member resolves both, so the named form is the one the product renders; an
 * implementation that fell back to the neutral sentence or inserted the id
 * would fail this assertion.
 */
function statusSavedNotice(): string {
  return copy('consoleUi', 'accounts.statusSaved', {
    account: copy('consoleUi', 'common.accountReference.nameWithIdentifier', {
      name: MEMBER.displayName,
      identifier: MEMBER.identifier,
    }),
  })
}

/** The detail panel's status line for one status label. */
function statusLine(panel: Locator, status: string): Locator {
  return panel.getByText(
    copy('consoleUi', 'accounts.detail.statusLine', { value: status }),
    { exact: true },
  )
}

/** The status filter's select on the accounts page. */
function statusFilter(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('consoleUi', 'accounts.filter.status'),
    exact: true,
  })
}

/** The identifier-kind select on the accounts page. */
function identifierKind(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('consoleUi', 'accounts.identifierKindLabel'),
    exact: true,
  })
}

/** The login-identifier prefix field on the accounts page. */
function identifierPrefixField(page: Page): Locator {
  return page.getByRole('textbox', {
    name: copy('consoleUi', 'accounts.identifierLabel'),
    exact: true,
  })
}

/** The accounts page's search control. */
function searchButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('consoleUi', 'accounts.search'),
    exact: true,
  })
}

/** The accounts page's empty state, shown when the applied filter matches none. */
function emptyState(page: Page): Locator {
  return page.getByRole('heading', {
    name: copy('consoleUi', 'accounts.empty.title'),
    exact: true,
  })
}

/** The session-ended surface. */
function sessionEndedHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('authUi', 'sessionEnded.title'),
    exact: true,
  })
}

/** The account page's heading (the protected page the member was refused on). */
function accountHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'account.title'),
    exact: true,
  })
}

/** The account page's sessions section. */
function sessionsSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'sessions.title'),
    exact: true,
  })
}

/** The tenant switcher's trigger, naming the current organization. */
function tenantSwitcher(page: Page): Locator {
  return page
    .getByRole('banner')
    .getByRole('button', { name: TENANT, exact: true })
}

/** The password sign-in surface's fields and submit control. */
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

test('account-offboarding: an operator disables a member and the member returns after the restore', async ({
  demo,
  browser,
}) => {
  const { page, server, accounts } = demo
  let memberAContext: BrowserContext | undefined
  let memberBContext: BrowserContext | undefined
  let memberAPage: Page | undefined
  let memberBPage: Page | undefined
  let memberId = ''

  try {
    await test.step('precondition: the operator signs in with a factor, and the member holds two sessions', async () => {
      // The operator has the seeded authenticator factor, so the password
      // step parks the login at its second factor.
      await accounts.signIn({
        identifier: OPERATOR.identifier,
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

      // The member's two devices are two browser contexts with no shared
      // cookie jar. Their language, zone and viewport repeat the tier's pins.
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

      // The account page lists this device's own sessions: the two rows are
      // the two signed-in contexts, and exactly one of them is current.
      await memberAPage
        .getByRole('link', { name: copy('demo', 'nav.account'), exact: true })
        .click()
      await expect(accountHeading(memberAPage)).toBeVisible()
      const sessions = sessionsSection(memberAPage)
      await expect(sessions.getByRole('listitem')).toHaveCount(2)
      await expect(
        sessions.getByText(copy('accountUi', 'sessions.current'), {
          exact: true,
        }),
      ).toHaveCount(1)
      await expect(
        sessions
          .getByText(
            copyPattern('accountUi', 'sessions.signedIn', { time: '.+' }),
          )
          .first(),
      ).toBeVisible()
      // Both live rows name their session's absolute expiry.
      await expect(
        sessions.getByText(
          copyPattern('accountUi', 'sessions.expires', { time: '.+' }),
        ),
      ).toHaveCount(2)
    })

    await test.step('step 1: the operator enters the console and opens the accounts page', async () => {
      await expect(consoleEntry(page)).toBeVisible()
      await consoleEntry(page).click()
      await expect(consoleNav(page)).toBeVisible()
      await consoleNav(page)
        .getByRole('link', {
          name: copy('consoleUi', 'accounts.title'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('consoleUi', 'accounts.title'),
          exact: true,
        }),
      ).toBeVisible()

      // The unfiltered list holds the operator and the member.
      await expect(accountsTable(page).locator('tbody tr')).toHaveCount(2)
    })

    await test.step("step 2: the operator filters by the member's identifier and enabled status and opens the detail", async () => {
      await identifierKind(page).click()
      await page
        .getByRole('option', {
          name: copy('consoleUi', 'accounts.identifierKind.username'),
          exact: true,
        })
        .click()
      await identifierPrefixField(page).fill(MEMBER.identifier)
      await statusFilter(page).click()
      await page
        .getByRole('option', {
          name: copy('consoleUi', 'accounts.status.active'),
          exact: true,
        })
        .click()

      // The draft filters apply nothing: the list still holds both accounts.
      await expect(accountsTable(page).locator('tbody tr')).toHaveCount(2)

      await searchButton(page).click()
      await expect(accountsTable(page).locator('tbody tr')).toHaveCount(1)
      const row = memberRow(page)
      await expect(row).toHaveCount(1)
      await expect(
        row.getByText(copy('consoleUi', 'accounts.status.active'), {
          exact: true,
        }),
      ).toBeVisible()

      memberId = await accountIdOf(row)
      await row.getByRole('button', { name: managePrefix() }).click()
      const dialog = page.getByRole('dialog', { name: detailTitle(memberId) })
      await expect(dialog).toBeVisible()
      await expect(
        statusLine(dialog, copy('consoleUi', 'accounts.status.active')),
      ).toBeVisible()
    })

    await test.step('step 3: the operator saves the account as disabled, and the detail and the list show it', async () => {
      const dialog = page.getByRole('dialog', { name: detailTitle(memberId) })
      await dialog
        .getByRole('combobox', {
          name: copy('consoleUi', 'accounts.status.label'),
          exact: true,
        })
        .click()
      await page
        .getByRole('option', {
          name: copy('consoleUi', 'accounts.status.disabled'),
          exact: true,
        })
        .click()
      await dialog
        .getByRole('button', {
          name: copy('consoleUi', 'accounts.status.submit'),
          exact: true,
        })
        .click()

      // The settled write confirms with a message that names the account by
      // its display name and login identifier, and the detail re-reads the
      // account's own status.
      await expect(
        dialog.getByText(statusSavedNotice(), { exact: true }),
      ).toBeVisible()
      await expect(
        statusLine(dialog, copy('consoleUi', 'accounts.status.disabled')),
      ).toBeVisible()

      await dialog
        .getByRole('button', {
          name: copy('uiKit', 'sidePanel.closeLabel'),
          exact: true,
        })
        .click()
      await expect(dialog).toHaveCount(0)

      // The enabled-filtered list no longer matches the account, and the
      // disabled filter lists it with the new status.
      await expect(emptyState(page)).toBeVisible()
      await statusFilter(page).click()
      await page
        .getByRole('option', {
          name: copy('consoleUi', 'accounts.status.disabled'),
          exact: true,
        })
        .click()
      await searchButton(page).click()
      const row = memberRow(page)
      await expect(row).toHaveCount(1)
      await expect(
        row.getByText(copy('consoleUi', 'accounts.status.disabled'), {
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

      // The revocation can also end the frame through the live notification
      // stream. When it has not, one in-app page open is the request the step
      // describes; the state-based wait below is the assertion either way.
      if (!(await ended.isVisible())) {
        const accountLink = memberB.getByRole('link', {
          name: copy('demo', 'nav.account'),
          exact: true,
        })
        if ((await accountLink.count()) > 0) {
          await accountLink.click().catch(() => undefined)
        }
      }

      await expect(ended).toBeVisible({ timeout: 15_000 })
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

      // Nothing of the refused page renders: neither the account page nor the
      // tenant home heading is the page's heading any more.
      await expect(accountHeading(memberB)).toHaveCount(0)
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

      // A wrong password is the reference answer ...
      await identifierField(memberB).fill(MEMBER.identifier)
      await passwordField(memberB).fill(WRONG_PASSWORD)
      await signInButton(memberB).click()
      await expect(failure).toBeVisible({ timeout: 15_000 })

      // ... and the account's own correct credentials are answered the same
      // way while the account is disabled. The account-unavailable text never
      // replaces the collapsed failure.
      await passwordField(memberB).fill(MEMBER.password)
      await signInButton(memberB).click()
      await expect(failure).toBeVisible({ timeout: 15_000 })
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

      // The disabled-filtered list still holds the member; its detail is
      // opened again and the status moved back.
      const row = memberRow(page)
      await expect(row).toHaveCount(1)
      await row.getByRole('button', { name: managePrefix() }).click()
      const dialog = page.getByRole('dialog', { name: detailTitle(memberId) })
      await expect(dialog).toBeVisible()
      await expect(
        statusLine(dialog, copy('consoleUi', 'accounts.status.disabled')),
      ).toBeVisible()
      await dialog
        .getByRole('combobox', {
          name: copy('consoleUi', 'accounts.status.label'),
          exact: true,
        })
        .click()
      await page
        .getByRole('option', {
          name: copy('consoleUi', 'accounts.status.active'),
          exact: true,
        })
        .click()
      await dialog
        .getByRole('button', {
          name: copy('consoleUi', 'accounts.status.submit'),
          exact: true,
        })
        .click()
      await expect(
        dialog.getByText(statusSavedNotice(), { exact: true }),
      ).toBeVisible()
      await expect(
        statusLine(dialog, copy('consoleUi', 'accounts.status.active')),
      ).toBeVisible()
      await dialog
        .getByRole('button', {
          name: copy('uiKit', 'sidePanel.closeLabel'),
          exact: true,
        })
        .click()
      await expect(dialog).toHaveCount(0)

      // The disabled filter no longer matches the restored account, and the
      // enabled filter lists it as enabled again.
      await expect(emptyState(page)).toBeVisible()
      await statusFilter(page).click()
      await page
        .getByRole('option', {
          name: copy('consoleUi', 'accounts.status.active'),
          exact: true,
        })
        .click()
      await searchButton(page).click()
      await expect(memberRow(page)).toHaveCount(1)
      await expect(
        memberRow(page).getByText(copy('consoleUi', 'accounts.status.active'), {
          exact: true,
        }),
      ).toBeVisible()

      // The member reloads the protected page: the restore revived no old
      // session, so the page starts at the sign-in surface and continues on
      // the same address after the login.
      await memberB.reload()
      await expect(signInHeading(memberB)).toBeVisible()
      await identifierField(memberB).fill(MEMBER.identifier)
      await passwordField(memberB).fill(MEMBER.password)
      await signInButton(memberB).click()
      await expect(accountHeading(memberB)).toBeVisible({ timeout: 15_000 })
      expect(new URL(memberB.url()).pathname).toBe(ACCOUNT_PATH)
    })

    await test.step('alternate path: the session list holds only the new device, and the switcher names the organization', async () => {
      if (memberBPage === undefined) {
        throw new Error('the member page was never opened')
      }
      const memberB = memberBPage

      // The two pre-disable sessions are gone; the re-login created this one,
      // and it is the current one.
      const sessions = sessionsSection(memberB)
      await expect(sessions.getByRole('listitem')).toHaveCount(1, {
        timeout: 15_000,
      })
      await expect(
        sessions.getByText(copy('accountUi', 'sessions.current'), {
          exact: true,
        }),
      ).toHaveCount(1)

      // The tenant area still names the member's own organization as current,
      // and its row is the disabled current row of the switcher.
      await expect(tenantSwitcher(memberB)).toBeVisible({ timeout: 15_000 })
      await tenantSwitcher(memberB).click()
      await expect(
        memberB.getByRole('menuitem', { name: TENANT, exact: true }),
      ).toBeDisabled()
      await memberB.keyboard.press('Escape')
      await expect(
        memberB.getByRole('menuitem', { name: TENANT, exact: true }),
      ).toBeHidden()
    })
  } finally {
    // The member contexts hold cookie sessions of their own; closing them
    // keeps a failed run from leaving them open while the server drains.
    await memberAContext?.close().catch(() => undefined)
    await memberBContext?.close().catch(() => undefined)
  }
})
