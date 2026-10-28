/**
 * J12 -- an operator invites an address, the invitee signs in through the
 * link and accepts, and the operator sees the member and the accepted
 * invitation.
 *
 * The operator is the seeded platform administrator. The review seed writes
 * its email address as an administrator's assertion, not as a proof of
 * ownership, and the invitation capability refuses a management write an
 * account with an unproved email makes. The spec therefore reaches the
 * journey's precondition the way a person does: the standing verification
 * notice, the code from the review process log (the one non-UI read this spec
 * makes), and the verify action. The operator then enters the console, opens
 * the invitation-and-members page, selects the seeded tenant and finds the
 * tenant's existing member.
 *
 * The operator invites an address the review configuration seeds as an account
 * with no membership (reviewInvitee in fixtures/config.ts). The issue is
 * confirmed, and the page's one-time panel hands out the acceptance link once:
 * its copy control and its explicit close are asserted, and the link is gone
 * from the page after the close. The invitation list then shows the pending
 * row with its address, status, inviter and expiry.
 *
 * The invitee opens the link in a second browser context with no session and
 * meets the sign-in surface, not the invitation summary. Signing in through
 * that surface leaves the URL on the invitation, and the accept screen then
 * shows the tenant, the invited address and the expiry the issue carried. The
 * acceptance lands on the tenant home with the membership confirmation, the
 * invited tenant as the current one, and its row in the tenant switcher. The
 * operator's refreshed page then shows the invitation accepted and the invitee
 * in the member table with the invitation source.
 *
 * The document's alternate path for a re-opened link is driven after the
 * acceptance: the invitee returns to the same link and meets the "invitation
 * no longer valid" state, with neither the summary nor an accept action, so a
 * second acceptance cannot be started from it. REQ-tenancy-013 is claimed for
 * that "cannot be redeemed" facet -- the missing-link facet and the 409
 * failure state are the component tier's -- and REQ-tenancy-020 for the
 * repeated-redemption observation: the consumed link's second open produces
 * no second membership, and the operator's refreshed member table still holds
 * the one joined row. REQ-tenancy-020's concurrent-redemption clause is not
 * driven by this spec.
 *
 * STEP 10's IN-APP HALF IS NOT REACHABLE FROM THE TENANT AREA. The document
 * asks for in-app navigation from the tenant area to a console path quietly
 * landing back on the tenant home. The tenant area offers a plain member no
 * console destination at all: the header entry and the start page's console
 * card are hidden by REQ-platform-admin-001, and the navigation lists no
 * console path. There is therefore no rendered control this step could click.
 * The spec drives the branch the document states for a full-page load instead:
 * a URL navigation to the console root and to a console subpath leaves the
 * page on the "checking console access" wait, renders no console content, and
 * does not fall back to the tenant home. REQ-platform-admin-036 is left out of
 * `@covers` for that reason: its in-app clause cannot be exercised here, and
 * the id as a whole would claim it.
 *
 * `@covers` is narrower than the document's list in other ways too.
 * REQ-platform-admin-002 (an anonymous viewer on a console path) is not driven:
 * this journey's anonymous surface is the invitation link. REQ-tenancy-017 (the
 * membership binds to the invited address even when another account accepts),
 * REQ-tenancy-018 (an address with no account gets one from the accept form)
 * and REQ-tenancy-019 (a pending self-registration is proved) each name a
 * branch this spec does not drive: the invitee here holds the invited address's
 * own account, so the acceptance binds the membership to that account.
 * REQ-tenancy-011's malformed-address refusal is not driven either.
 * REQ-platform-admin-032 is claimed narrowly: the issue carries an expiry and
 * both the list and the accept summary render that instant, but the
 * blank-expiry default is not driven. REQ-tenancy-008 is claimed narrowly too:
 * the console's member table lists the tenant's members, while its paging and
 * ascending order are not exercised by this spec.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts. The only non-UI read is the operator's email verification
 * code from the review process log (fixtures/log.ts).
 */
// @journey journey-invitation-to-tenant.md
// @covers REQ-platform-admin-001, REQ-platform-admin-032, REQ-platform-admin-033, REQ-signin-030, REQ-tenancy-008, REQ-tenancy-011, REQ-tenancy-013, REQ-tenancy-015, REQ-tenancy-016, REQ-tenancy-020, REQ-tenancy-021, REQ-tenancy-035, REQ-tenancy-043
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { reviewInvitee } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import { copy } from '../helpers/i18n'

// The console's invitation table has a low-priority expiry column, and the
// console sidebar takes part of the width, so the desktop viewport has to be
// wide enough for the container to render every column this journey reads.
test.use({ seedInvitee: true, viewport: { width: 1920, height: 1080 } })

/** The seeded platform administrator the review assembly writes. */
const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

/** The seeded tenant the invitation is addressed to. Its id is its alias. */
const TENANT = 'acme'

/** The member the tenant already holds before the journey starts. */
const EXISTING_MEMBER = 'Demo Member'

/**
 * The expiry the operator types into the issue form: two days out, truncated
 * to the minute. Two days is inside the server's lifetime domain and far from
 * the configured default, so an expiry the product ignored would render a
 * different day and fail the assertions below.
 */
const EXPIRY_INSTANT = (() => {
  const instant = new Date(Date.now() + 2 * 24 * 60 * 60 * 1000)
  instant.setUTCSeconds(0, 0)
  return instant
})()

/** The `datetime-local` value the browser (pinned to UTC) reads as the instant. */
const EXPIRY_INPUT = EXPIRY_INSTANT.toISOString().slice(0, 16)

/**
 * The instant as the product's own formatter renders it: ui-kit's default
 * tier is Intl's medium date and short time, and the invitation list pins the
 * zone to UTC.
 */
const EXPECTED_EXPIRY = new Intl.DateTimeFormat('en-US', {
  dateStyle: 'medium',
  timeStyle: 'short',
  timeZone: 'UTC',
}).format(EXPIRY_INSTANT)

/** Escape the characters a regular expression reads as syntax. */
function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/** The email-verification notice under the app bar. */
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

/** The header's console entry (a link, while the operator may reach it). */
function consoleEntry(page: Page): Locator {
  return page.getByRole('banner').getByRole('link', {
    name: copy('demo', 'nav.console'),
    exact: true,
  })
}

/** The home page's console card. */
function consoleCard(page: Page): Locator {
  return page
    .getByRole('heading', {
      level: 2,
      name: copy('demo', 'home.cards.console.title'),
      exact: true,
    })
    .locator('xpath=ancestor::a[1]')
}

/** The console's navigation landmark. */
function consoleNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('consoleUi', 'shell.navLabel'),
    exact: true,
  })
}

/** The tenant selector of a tenant-addressed console page. */
function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('consoleUi', 'common.tenant'),
    exact: true,
  })
}

/** The invitation section of the invitations-and-members page. */
function invitationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('consoleUi', 'invitations.section.title'),
    exact: true,
  })
}

/** The member section of the invitations-and-members page. */
function memberSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('consoleUi', 'members.title'),
    exact: true,
  })
}

/** The invitation list's row for the invited address. */
function invitationRow(page: Page): Locator {
  return invitationSection(page)
    .getByRole('table')
    .locator('tbody tr')
    .filter({ hasText: reviewInvitee.identifier })
}

/** The one-time panel that hands out the issued invitation's acceptance link. */
function invitationLinkPanel(page: Page, email: string): Locator {
  return page.getByRole('status').filter({
    hasText: copy('consoleUi', 'invitations.issued.link', { email }),
  })
}

/** The expiry line's copy prefix: the sentence with an empty value. */
const EXPIRY_PREFIX = copy('authUi', 'acceptInvitation.expires', { expires: '' })

test('invitation-to-tenant: an operator invites an address and the invitee joins the tenant', async ({
  demo,
  browser,
}) => {
  const { page, server, accounts } = demo
  let inviteeContext: BrowserContext | undefined
  let inviteePage: Page | undefined
  let acceptanceLink = ''

  try {
    await test.step('precondition: the operator signs in and proves the account email', async () => {
      // The journey's precondition is an operator whose email is proved. The
      // review seed writes the address unproved, so the spec reaches the
      // precondition the way a person does: the standing notice's verify entry,
      // the code from the review process log, and the verify action.
      await accounts.signIn({
        identifier: OPERATOR.username,
        password: OPERATOR.password,
      })
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

      const section = verificationSection(page)
      await expect(section).toBeVisible()
      const codeField = section.getByLabel(
        copy('accountUi', 'verification.codeLabel'),
        { exact: true },
      )
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
      await expect(banner).toHaveCount(0)
    })

    await test.step('step 1: the operator opens the invitation-and-members page and selects the tenant', async () => {
      // The account page the verification landed on is inside the tenant
      // shell; the start page is where the console entry and card are read.
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

      // REQ-platform-admin-001's operator facet: both derivations of the
      // console destination are offered to platform staff.
      await expect(consoleEntry(page)).toBeVisible()
      await expect(consoleCard(page)).toBeVisible()
      await consoleEntry(page).click()
      await expect(consoleNav(page)).toBeVisible()

      await consoleNav(page)
        .getByRole('link', {
          name: copy('consoleUi', 'invitations.title'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('consoleUi', 'invitations.title'),
          exact: true,
        }),
      ).toBeVisible()

      // The page reads nothing until a tenant is chosen; the roster names the
      // seeded tenant by its alias.
      await tenantSelector(page).click()
      await page.getByRole('option', { name: TENANT, exact: true }).click()

      // The tenant has no invitation yet, and its member list holds the
      // seeded member and the platform operator, each carrying the source the
      // seed wrote. The member's own row is what the journey names, so the
      // source is read inside that row.
      await expect(
        page.getByRole('heading', {
          name: copy('consoleUi', 'invitations.empty.title'),
          exact: true,
        }),
      ).toBeVisible()
      const members = memberSection(page).getByRole('table')
      const existing = members
        .locator('tbody tr')
        .filter({ hasText: EXISTING_MEMBER })
      await expect(existing).toHaveCount(1)
      await expect(
        existing.getByText(EXISTING_MEMBER, { exact: true }),
      ).toBeVisible()
      await expect(
        existing.getByText(copy('consoleUi', 'members.source.admin'), {
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 2: the operator invites the address with an expiry and sees the confirmation', async () => {
      await page
        .getByLabel(copy('consoleUi', 'invitations.issue.emailLabel'), {
          exact: true,
        })
        .fill(reviewInvitee.identifier)
      await page
        .getByLabel(copy('consoleUi', 'invitations.issue.expiresLabel'), {
          exact: true,
        })
        .fill(EXPIRY_INPUT)
      await page
        .getByRole('button', {
          name: copy('consoleUi', 'invitations.issue.submit'),
          exact: true,
        })
        .click()

      await expect(
        page.getByText(
          copy('consoleUi', 'invitations.issued.confirm', {
            email: reviewInvitee.identifier,
          }),
          { exact: true },
        ),
      ).toBeVisible()
    })

    await test.step('step 3: the one-time panel hands out the acceptance link until it is closed', async () => {
      const panel = invitationLinkPanel(page, reviewInvitee.identifier)
      await expect(panel).toBeVisible()
      await expect(
        panel.getByText(
          copy('consoleUi', 'invitations.issued.link', {
            email: reviewInvitee.identifier,
          }),
          { exact: true },
        ),
      ).toBeVisible()
      // The copy control is part of the handover; its clipboard write is not
      // read back here, the rendered value below is the link the operator
      // passes on.
      await expect(
        panel.getByRole('button', {
          name: copy('consoleUi', 'invitations.issued.copyLink'),
          exact: true,
        }),
      ).toBeVisible()

      const value = panel.getByText(
        new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
      )
      await expect(value).toBeVisible()
      acceptanceLink = ((await value.textContent()) ?? '').trim()
      expect(acceptanceLink).toMatch(
        new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
      )

      // Step 3's lifetime rule: the panel stays until the operator closes it,
      // and after the close the page offers the link no more.
      const close = panel.getByRole('button', {
        name: copy('consoleUi', 'invitations.issued.closeAria', {
          tenant: TENANT,
          email: reviewInvitee.identifier,
        }),
        exact: true,
      })
      await expect(close).toBeVisible()
      await close.click()
      await expect(panel).toHaveCount(0)
      await expect(page.getByText(acceptanceLink, { exact: true })).toHaveCount(
        0,
      )
    })

    await test.step('step 4: the invitation list shows the pending invitation with its address, inviter and expiry', async () => {
      const table = invitationSection(page).getByRole('table')
      await expect(table).toBeVisible()

      // The list's columns are the four the journey names, beside the actions.
      for (const header of [
        copy('consoleUi', 'invitations.column.email'),
        copy('consoleUi', 'invitations.column.status'),
        copy('consoleUi', 'invitations.column.invitedBy'),
        copy('consoleUi', 'invitations.column.expires'),
      ]) {
        await expect(
          table.getByRole('columnheader', { name: header, exact: true }),
        ).toBeVisible()
      }

      const row = invitationRow(page)
      await expect(row).toBeVisible()
      await expect(
        row.getByText(reviewInvitee.identifier, { exact: true }),
      ).toBeVisible()
      await expect(
        row.getByText(copy('consoleUi', 'invitations.status.pending'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        row.getByText(OPERATOR.displayName, { exact: true }),
      ).toBeVisible()
      // The typed expiry is the instant the list renders, not the default.
      await expect(row).toContainText(EXPECTED_EXPIRY)
    })

    await test.step('step 6: the invitee opens the link without a session and signs in through it', async () => {
      // The operator's channel hands the link to the invitee; the invitee is a
      // second browser context with no session. The tier's language, time zone
      // and viewport pins are repeated on a context the spec creates by hand.
      inviteeContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      inviteePage = await inviteeContext.newPage()
      const invitee = inviteePage
      const invitationPath = new URL(acceptanceLink).pathname

      await invitee.goto(acceptanceLink)
      await expect(
        invitee.getByRole('heading', {
          level: 1,
          name: copy('demo', 'signIn.heading'),
          exact: true,
        }),
      ).toBeVisible()
      // No invitation summary reaches an anonymous viewer.
      await expect(
        invitee.getByRole('heading', {
          name: copy('authUi', 'acceptInvitation.title'),
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.invited', { tenant: TENANT }),
          { exact: true },
        ),
      ).toHaveCount(0)
      expect(new URL(invitee.url()).pathname).toBe(invitationPath)

      // Signing in through the surface stays on the link: the host keeps the
      // landing for this flow, so the session flip re-renders the accept
      // screen at the same address.
      await invitee
        .getByRole('textbox', {
          name: copy('authUi', 'passwordSignIn.identifierLabel'),
          exact: true,
        })
        .fill(reviewInvitee.username)
      await invitee
        .getByRole('textbox', {
          name: copy('authUi', 'passwordSignIn.passwordLabel'),
          exact: true,
        })
        .fill(reviewInvitee.password)
      await invitee
        .getByRole('button', {
          name: copy('authUi', 'passwordSignIn.submit'),
          exact: true,
        })
        .click()

      await expect(
        invitee.getByRole('heading', {
          name: copy('authUi', 'acceptInvitation.title'),
          exact: true,
        }),
      ).toBeVisible({ timeout: 15_000 })
      expect(new URL(invitee.url()).pathname).toBe(invitationPath)
    })

    await test.step('step 7: the accept screen shows the tenant, the invited address and the expiry', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.invited', { tenant: TENANT }),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.email', {
            email: reviewInvitee.identifier,
          }),
          { exact: true },
        ),
      ).toBeVisible()
      // The same instant the operator's list rendered, in the display zone the
      // browser is pinned to.
      const expiryLine = invitee.getByText(
        new RegExp(`^${escapeRegExp(EXPIRY_PREFIX)}`),
      )
      await expect(expiryLine).toHaveText(`${EXPIRY_PREFIX}${EXPECTED_EXPIRY}`)
    })

    await test.step('step 8: the invitee accepts, lands on the tenant home and sees the confirmation', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage

      await invitee
        .getByRole('button', {
          name: copy('authUi', 'acceptInvitation.accept'),
          exact: true,
        })
        .click()

      // The acceptance navigates the landing to the tenant home; the home page
      // carries the confirmation the landing cannot keep.
      await expect(
        invitee.getByRole('heading', {
          level: 1,
          name: copy('demo', 'home.heading'),
          exact: true,
        }),
      ).toBeVisible({ timeout: 15_000 })
      expect(new URL(invitee.url()).pathname).toBe('/')

      const dismiss = invitee.getByRole('button', {
        name: copy('demo', 'home.membershipNotice.dismiss'),
        exact: true,
      })
      await expect(dismiss).toBeVisible()
      const confirmation = dismiss.locator(
        'xpath=ancestor::div[contains(@class, "MuiAlert-root")][1]',
      )
      await expect(
        confirmation.getByText(copy('demo', 'home.membershipNotice.title'), {
          exact: true,
        }),
      ).toBeVisible()

      // The current tenant is the invited one: the switcher's trigger names
      // it, and its row is the disabled current row in the open list.
      const switcher = invitee
        .getByRole('banner')
        .getByRole('button', { name: TENANT, exact: true })
      await expect(switcher).toBeVisible()
      await expect(switcher).toBeEnabled()
      await switcher.click()
      await expect(
        invitee.getByRole('menuitem', { name: TENANT, exact: true }),
      ).toBeDisabled()
      await invitee.keyboard.press('Escape')
      await expect(
        invitee.getByRole('menuitem', { name: TENANT, exact: true }),
      ).toBeHidden()
    })

    await test.step('step 9: a member has no console entry, and a full-page console load turns back home', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage

      // REQ-platform-admin-001's member facet, checked on the tenant home the
      // acceptance landed on: this step runs while the viewer is still there,
      // before the re-opened link below leaves them on the landing, so the
      // header and start-page assertions observe the member's real home.
      await expect(
        invitee
          .getByRole('banner')
          .getByRole('link', { name: copy('demo', 'nav.console'), exact: true }),
      ).toHaveCount(0)
      await expect(
        invitee.getByRole('heading', {
          level: 2,
          name: copy('demo', 'home.cards.console.title'),
          exact: true,
        }),
      ).toHaveCount(0)

      // The full-page-load branch: the member's own tenant list settles the
      // tenant, the platform permission set reads as a known empty set, and the
      // console area turns the viewer back to the tenant home. Nothing of the
      // console renders, and no request is spent being refused.
      for (const path of ['/console', '/console/settings']) {
        await invitee.goto(`${server.url}${path}`)
        await expect(
          invitee.getByRole('heading', {
            level: 1,
            name: copy('demo', 'home.heading'),
            exact: true,
          }),
        ).toBeVisible({ timeout: 15_000 })
        await expect(
          invitee.getByRole('navigation', {
            name: copy('consoleUi', 'shell.navLabel'),
            exact: true,
          }),
        ).toHaveCount(0)
        await expect(
          invitee.getByRole('progressbar', {
            name: copy('demo', 'console.accessPending'),
            exact: true,
          }),
        ).toHaveCount(0)
        expect(new URL(invitee.url()).pathname).toBe('/')
      }
    })

    await test.step('step 10: the invitee re-opens the accepted link and meets the invalid state', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage

      // The used link's read still returns the stored view; the screen judges
      // its status before showing anything, so the summary and the accept
      // action are gone and the link cannot be redeemed a second time.
      await invitee.goto(acceptanceLink)
      await expect(
        invitee.getByRole('heading', {
          name: copy('authUi', 'acceptInvitation.unavailable.title'),
          exact: true,
        }),
      ).toBeVisible({ timeout: 15_000 })
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.unavailable.description'),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.invited', { tenant: TENANT }),
          { exact: true },
        ),
      ).toHaveCount(0)
      await expect(
        invitee.getByRole('button', {
          name: copy('authUi', 'acceptInvitation.accept'),
          exact: true,
        }),
      ).toHaveCount(0)
    })

    await test.step('step 11: the operator sees the invitation accepted and the invitee in the member table', async () => {
      // The operator stayed on the page while the invitee accepted; the
      // page's own refresh re-reads both lists.
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('consoleUi', 'invitations.title'),
          exact: true,
        }),
      ).toBeVisible()
      await page
        .getByRole('button', {
          name: copy('consoleUi', 'common.refresh'),
          exact: true,
        })
        .click()

      // The invitation's terminal state is now the accepted one.
      await expect(
        invitationRow(page).getByText(
          copy('consoleUi', 'invitations.status.accepted'),
          { exact: true },
        ),
      ).toBeVisible({ timeout: 15_000 })

      // The member table gained exactly one row: the invited account, active,
      // and sourced from the invitation. The tenant's earlier members -- the
      // seeded target and the platform operator, who is acme's second
      // administrator -- are still there, so the table holds three rows.
      const members = memberSection(page).getByRole('table')
      await expect(members.locator('tbody tr')).toHaveCount(3)
      const invited = members
        .locator('tbody tr')
        .filter({ hasText: reviewInvitee.displayName })
      await expect(invited).toHaveCount(1)
      await expect(
        invited.getByText(copy('consoleUi', 'members.source.invite'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        invited.getByText(copy('consoleUi', 'members.activeYes'), {
          exact: true,
        }),
      ).toBeVisible()
    })
  } finally {
    // The invitee context holds its own cookie session; closing it keeps a
    // failed run from leaving a second browser context open while the server
    // drains.
    await inviteeContext?.close().catch(() => undefined)
  }
})
