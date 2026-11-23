/**
 * J12 -- an operator invites an address, the recipient registers, proves the
 * invited address through the email code, accepts, and the operator sees the
 * member and the accepted invitation.
 *
 * The operator is the seeded platform administrator. The seed writes
 * its email address as an administrator's assertion, not as a proof of
 * ownership, and the invitation capability refuses a management write an
 * account with an unproved email makes. The spec therefore reaches the
 * journey's precondition the way a person does: the standing verification
 * notice, the code from the server's process log (one of the two non-UI reads
 * this spec makes), and the verify action. The operator then enters the admin,
 * opens the invitation-and-members page, selects the seeded tenant and finds
 * the tenant's existing member.
 *
 * The operator invites an address no account holds (e2eInvitee in
 * fixtures/config.ts). This server is started with the invitation mail
 * unconfigured (`invitationMail: false`): the issue answer then carries the
 * one-time token, and the page's panel hands out the acceptance link. With the
 * notice sent the answer withholds the token -- the recipient holds the link
 * already -- and the server's process log masks every `spd_inv_` token, so a
 * test that must open the link itself cannot read it from the notice. The
 * panel's copy control and explicit close are asserted, and the link is gone
 * from the page after the close. Issuing to the same address again is refused
 * as `invitation-pending`: the address already holds an unexpired pending
 * invitation, so no second row or link appears and the operator keeps the one
 * already handed out. A resend instead acts on the existing row through
 * `/invitations/{id}/resend`, replacing the token without adding a row; this
 * journey leaves resend to the tenant-admin journey, which drives it. The
 * invitation list then shows the pending row with its address, status,
 * inviter and expiry.
 *
 * The recipient opens the link in a second browser context with no session and
 * meets the invitation landing: the inviter's display name, the organization,
 * the masked invited address and the deadline, with sign-in and create-account
 * actions. The token grants joining; it does not prove the invited address, so
 * the recipient registers an account with that address through the landing's
 * create-account action. A password registration lands on the account page's
 * verification section and sends one code to the address; the code comes from
 * the process log (the second non-UI read). Verifying the address returns the
 * visitor to the remembered invitation link, where the accept screen shows the
 * inviter, the tenant, the masked invited address and the expiry the issue
 * carried. Accepting then lands on the tenant home with the membership
 * confirmation, the invited tenant as the current one, and its row in the
 * tenant switcher. The operator's refreshed page then shows the invitation
 * accepted and the invitee in the member table with the invitation source.
 *
 * A second case drives the inviter-held-token path: another visitor registers
 * on the same link with a different address, proves that address through its
 * own email code, and meets the proof step on arrival because the eligibility
 * read names the mismatch. The retry is refused the same way and writes no
 * membership; the operator's page keeps the invitation pending and the member
 * table unchanged.
 *
 * The document's alternate path for a re-opened link is driven after the
 * acceptance: the invitee returns to the same link and meets the member
 * terminal state, which names the organization the viewer already belongs to
 * and offers the way in, with neither the summary nor an accept action, so a
 * second acceptance cannot be started from it. REQ-tenancy-013 is that read
 * path, and REQ-tenancy-020 is claimed for the repeated-redemption
 * observation: the consumed link's second open produces no second membership,
 * and the operator's refreshed member table still holds the one joined row.
 * REQ-tenancy-020's concurrent-redemption clause is not driven by this spec.
 *
 * STEP 10's IN-APP HALF IS NOT REACHABLE FROM THE TENANT AREA. The document
 * asks for in-app navigation from the tenant area to a admin path quietly
 * landing back on the tenant home. The tenant area offers a plain member no
 * admin destination at all: the account menu's admin entry and the start
 * page's admin card are hidden by REQ-platform-admin-001, and the navigation
 * lists no admin path. There is therefore no rendered control this step could
 * click.
 * The spec drives the branch the document states for a full-page load instead:
 * a URL navigation to the admin root and to a admin subpath leaves the
 * page on the "checking admin access" wait, renders no admin content, and
 * does not fall back to the tenant home. REQ-platform-admin-036 is left out of
 * `@covers` for that reason: its in-app clause cannot be exercised here, and
 * the id as a whole would claim it.
 *
 * `@covers` is narrower than the document's list in other ways too.
 * REQ-platform-admin-002 (an anonymous viewer on a admin path) is not driven:
 * this journey's anonymous surface is the invitation link. REQ-tenancy-017 is
 * driven by the second case: an account that proves a different address is
 * refused and writes no membership. REQ-tenancy-018 is claimed narrowly: the
 * acceptance never opens an account, and the recipient registers before
 * accepting, while the case where the acceptance itself would have opened one
 * is gone. REQ-tenancy-019 is driven: the invited address is proved through an
 * ordinary email code and is not chosen as a sign-in identifier (the account
 * signs in with its username). REQ-tenancy-011's malformed-address refusal is
 * not driven. REQ-tenancy-057 is claimed narrowly for the not-configured
 * delivery: this server sends no invitation notice, so the answer reports it
 * and carries the token; the sent and failed branches are not driven here.
 * REQ-platform-admin-032 is claimed narrowly: the issue carries an expiry and
 * both the list and the accept summary render that instant, but the
 * blank-expiry default is not driven. REQ-tenancy-008 is claimed narrowly too:
 * the admin's member table lists the tenant's members, while its paging and
 * ascending order are not exercised by this spec.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts. The operator's email verification code comes from the
 * server's process log (fixtures/log.ts). The page's own member and invitation
 * reads are also observed: their account identifiers must never appear on the
 * rendered page, so the test learns the values from the responses the page
 * itself received and asserts neither section carries one.
 */
// @journey journey-invitation-to-tenant.md
// @covers REQ-platform-admin-001, REQ-platform-admin-032, REQ-platform-admin-033, REQ-signin-030, REQ-tenancy-008, REQ-tenancy-011, REQ-tenancy-013, REQ-tenancy-015, REQ-tenancy-016, REQ-tenancy-017, REQ-tenancy-018, REQ-tenancy-019, REQ-tenancy-020, REQ-tenancy-021, REQ-tenancy-035, REQ-tenancy-043, REQ-tenancy-057, REQ-tenancy-078
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { e2eInvitee } from '../fixtures/config'
import { expect, test, type DemoTarget } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import {
  adminEntry,
  closeAccountMenu,
  openAccountMenu,
  organizationRow,
} from '../helpers/account-menu'
import { copy } from '../helpers/i18n'
import {
  expiryChoices,
  expectOneOfText,
} from '../helpers/invitation-expiry'
import {
  collectTenantAccountIds,
  expectNoAccountIds,
} from '../helpers/organization-admin'
import { chooseSelectOption } from '../helpers/select-option'
import { switchToTenant } from '../helpers/switch-tenant'

// The admin's invitation table has a low-priority expiry column, and the
// admin sidebar takes part of the width, so the desktop viewport has to be
// wide enough for the container to render every column this journey reads.
// This journey registers the recipient, so no invitee account is seeded. The
// server sends no invitation notice, which is what leaves the one-time token
// in the issue answer: the server's process log masks an `spd_inv_`
// token, and a sent notice withholds it from the answer, so the link the
// recipient must open is readable only from the panel of an unsent notice.
test.use({
  seedInvitee: false,
  invitationMail: false,
  viewport: { width: 1920, height: 1080 },
})

/** The e2e server one test runs against. */
type DemoServer = DemoTarget['server']

/** The seeded platform administrator the e2e seed writes. */
const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

/** The seeded tenant the invitation is addressed to. Its id is its name. */
const TENANT = 'acme'

/** The invited address as the public read renders it: first character and domain. */
const MASKED_INVITEE = 'i***@demo.test'

/** The member the tenant already holds before the journey starts. */
const EXISTING_MEMBER = 'Demo Member'

/** The existing member's login username, as the seed writes it. */
const EXISTING_MEMBER_USERNAME = 'member'

/**
 * The visitor the second case signs in a different account with. The address
 * is not the invited one; it is proved through its own email code, so the
 * refusal is the address mismatch and not a missing proof.
 */
const OUTSIDER = {
  username: 'outsider-e2e',
  email: 'outsider@demo.test',
  password: 'outsider-e2e-secret',
  displayName: 'Invitation Outsider',
} as const

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

/** The home page's admin card. */
function adminCard(page: Page): Locator {
  return page
    .getByRole('heading', {
      level: 2,
      name: copy('demo', 'home.cards.admin.title'),
      exact: true,
    })
    .locator('xpath=ancestor::a[1]')
}

/** The admin's navigation landmark. */
function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

/** The platform's own organization. The account menu offers the admin entry
 * only while this is the current organization; platform administration is not
 * a tenant member's surface. */
const PLATFORM_TENANT = 'system'

/**
 * Open the admin from the tenant area. The way in is the account menu's admin
 * entry, offered only while the platform's own organization is current, so
 * the journey moves there first.
 */
async function openConsole(page: Page): Promise<void> {
  await switchToTenant(page, PLATFORM_TENANT)
  await openAccountMenu(page)
  await expect(adminEntry(page)).toBeVisible()
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
}

/** The tenant selector of a tenant-addressed admin page. */
function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'common.tenant'),
    exact: true,
  })
}

/** The invitation section of the invitations-and-members page. */
function invitationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'invitations.section.title'),
    exact: true,
  })
}

/** The member section of the invitations-and-members page. */
function memberSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'members.title'),
    exact: true,
  })
}

/**
 * The index of the column headed `header` in a table. The header copy comes
 * from the shipped bundle, so a renamed column fails here rather than matching
 * a stale literal.
 */
async function columnIndex(table: Locator, header: string): Promise<number> {
  const headers = table.locator('thead th')
  const count = await headers.count()
  for (let index = 0; index < count; index += 1) {
    if ((await headers.nth(index).innerText()).trim() === header) {
      return index
    }
  }
  throw new Error(`the table has no column headed ${header}`)
}

/**
 * The invitation list's row for the invited address. The match is an exact
 * address cell, so a longer address that contains it does not answer.
 */
function invitationRow(page: Page): Locator {
  return invitationSection(page)
    .getByRole('table')
    .locator('tbody tr')
    .filter({ has: page.getByText(e2eInvitee.identifier, { exact: true }) })
}

/** The one-time panel that hands out the issued invitation's acceptance link. */
function invitationLinkPanel(page: Page, email: string): Locator {
  return page.getByRole('status').filter({
    hasText: copy('adminUi', 'invitations.issued.link', { email }),
  })
}

/**
 * Sign the seeded operator in and prove the account's email, the journey's
 * precondition: the invitation capability refuses a management write an
 * account with an unproved email makes. The code comes from the server's
 * process log, the standing notice's own verify entry asks for it.
 */
async function proveOperatorEmail(demo: DemoTarget): Promise<void> {
  const { page, server, accounts } = demo
  await accounts.signIn({
    identifier: OPERATOR.username,
    password: OPERATOR.password,
  })
  const banner = verificationBanner(page, OPERATOR.identifier)
  await expect(banner).toBeVisible()

  // The cursor is taken before the entry is followed: the verification section
  // sends its one code after the arrival, and only that code may be used.
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
}

/**
 * Open the invitation-and-members page from wherever the viewer is and select
 * the seeded tenant. The page reads nothing until a tenant is chosen.
 */
async function openTenantInvitations(page: Page): Promise<void> {
  // The account page the verification landed on is inside the tenant shell;
  // the start page is where the admin entry and card are read.
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

  // REQ-platform-admin-001's operator facet: while the platform's own
  // organization is current, both derivations of the admin destination are
  // offered to platform staff -- the account menu's entry and the start
  // page's card.
  await switchToTenant(page, PLATFORM_TENANT)
  await expect(adminCard(page)).toBeVisible()
  await openConsole(page)

  await adminNav(page)
    .getByRole('link', {
      name: copy('adminUi', 'invitations.title'),
      exact: true,
    })
    .click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('adminUi', 'invitations.title'),
      exact: true,
    }),
  ).toBeVisible()

  await chooseSelectOption(
    tenantSelector(page),
    page.getByRole('option', { name: TENANT, exact: true }),
  )
}

/** Fill the issue form and submit it, without waiting for the answer. */
async function submitInvitation(page: Page, email: string): Promise<void> {
  await page
    .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
      exact: true,
    })
    .fill(email)
  // REQ-platform-admin-032: the expiry is a fixed lifetime choice, not a
  // free-form time. Thirty days is far from the seven-day default, so a
  // lifetime the product ignored would render a different deadline.
  await chooseSelectOption(
    page.getByRole('combobox', {
      name: copy('adminUi', 'invitations.issue.quickLabel'),
      exact: true,
    }),
    page.getByRole('option', {
      name: copy('adminUi', 'invitations.issue.thirtyDays'),
      exact: true,
    }),
  )
  await page
    .getByRole('button', {
      name: copy('adminUi', 'invitations.issue.submit'),
      exact: true,
    })
    .click()
}

/** Fill the issue form and wait for the confirmation naming the address. */
async function issueInvitation(page: Page, email: string): Promise<void> {
  await submitInvitation(page, email)
  await expect(
    page.getByText(
      copy('adminUi', 'invitations.issued.confirm', { email }),
      { exact: true },
    ),
  ).toBeVisible()
}

/**
 * Read the acceptance link from the one-time panel and close it. This server's
 * invitation mail is not configured, so the answer carries the token and the
 * panel reports the delivery as not configured beside the link it hands over.
 */
async function takeAcceptanceLink(
  page: Page,
  server: DemoServer,
  email: string,
): Promise<string> {
  const panel = invitationLinkPanel(page, email)
  await expect(panel).toBeVisible()
  // The message carries the delivery outcome beside the hand-out line, so it
  // is matched as a substring of the panel's text rather than as an exact node.
  await expect(
    panel.getByText(
      copy('adminUi', 'invitations.issued.link', { email }),
    ),
  ).toBeVisible()
  await expect(
    panel.getByText(
      copy('adminUi', 'invitations.issued.delivery.not_configured'),
      { exact: true },
    ),
  ).toBeVisible()
  // The copy control is part of the handover; its clipboard write is not read
  // back here, the rendered value below is the link the operator passes on.
  await expect(
    panel.getByRole('button', {
      name: copy('adminUi', 'invitations.issued.copyLink'),
      exact: true,
    }),
  ).toBeVisible()

  const value = panel.getByText(
    new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
  )
  await expect(value).toBeVisible()
  const link = ((await value.textContent()) ?? '').trim()
  expect(link).toMatch(
    new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
  )

  // The panel stays until the operator closes it. Closing drops the sole copy,
  // so it asks first; the link is gone only once the close is confirmed.
  await panel
    .getByRole('button', {
      name: copy('adminUi', 'invitations.issued.closeAria', {
        tenant: TENANT,
        email,
      }),
      exact: true,
    })
    .click()
  const closeDialog = page.getByRole('dialog', {
    name: copy('adminUi', 'invitations.issued.closeTitle'),
    exact: true,
  })
  await expect(closeDialog).toBeVisible()
  await closeDialog
    .getByRole('button', {
      name: copy('adminUi', 'invitations.issued.close'),
      exact: true,
    })
    .click()
  await expect(panel).toHaveCount(0)
  await expect(page.getByText(link, { exact: true })).toHaveCount(0)
  return link
}

/**
 * Open the invitation link in a fresh context, register through the sign-in
 * surface, prove the registered address with the code the account page sends,
 * and wait for the automatic return to the link. The registration lands on the
 * account page's verification section, which is where a password registration
 * proves its address; the remembered landing brings the viewer back here.
 */
async function registerAndProve(
  context: BrowserContext,
  link: string,
  identity: {
    readonly username: string
    readonly email: string
    readonly password: string
    readonly displayName: string
  },
  server: DemoServer,
): Promise<Page> {
  const page = await context.newPage()
  const invitationPath = new URL(link).pathname

  await page.goto(link)
  // The anonymous viewer meets the invitation landing, not a generic sign-in
  // page: the inviter, the masked address and the two account actions.
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('authUi', 'acceptInvitation.title'),
      exact: true,
    }),
  ).toBeVisible()
  await expect(
    page.getByText(
      copy('authUi', 'acceptInvitation.email', { email: MASKED_INVITEE }),
      { exact: true },
    ),
  ).toBeVisible()
  expect(new URL(page.url()).pathname).toBe(invitationPath)

  // The create-account action opens the host's registration surface in
  // place, so the token stays in the URL across the session flip.
  await page
    .getByRole('button', {
      name: copy('authUi', 'acceptInvitation.createAccount'),
      exact: true,
    })
    .click()
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('demo', 'signIn.registerHeading'),
      exact: true,
    }),
  ).toBeVisible()

  await page
    .getByLabel(copy('authUi', 'register.usernameLabel'), { exact: true })
    .fill(identity.username)
  await page
    .getByLabel(copy('authUi', 'register.displayNameLabel'), { exact: true })
    .fill(identity.displayName)
  await page
    .getByLabel(copy('authUi', 'register.emailLabel'), { exact: true })
    .fill(identity.email)
  await page
    .getByLabel(copy('authUi', 'register.passwordLabel'), { exact: true })
    .fill(identity.password)
  await page
    .getByLabel(copy('authUi', 'register.confirmPasswordLabel'), {
      exact: true,
    })
    .fill(identity.password)

  // Registration sends no mail; the one code that follows is the account
  // page's verification send, so the cursor before the submit is enough.
  const beforeCode = server.log.mark()
  await page
    .getByRole('button', { name: copy('authUi', 'register.submit'), exact: true })
    .click()

  // The password registration's product landing is the account page's email
  // verification section, which sends one code on arrival.
  await expect(
    page.getByRole('region', {
      name: copy('accountUi', 'verification.title'),
      exact: true,
    }),
  ).toBeVisible()
  // The account route lands on the profile pane, which hosts the email card.
  expect(new URL(page.url()).pathname).toBe('/account/profile')

  const section = verificationSection(page)
  const { code } = await waitForVerificationCode(
    server.log,
    'mail',
    identity.email,
    { after: beforeCode },
  )
  await section
    .getByLabel(copy('accountUi', 'verification.codeLabel'), { exact: true })
    .fill(code)
  await section
    .getByRole('button', {
      name: copy('accountUi', 'verification.verify'),
      exact: true,
    })
    .click()

  // The remembered landing brings the visitor back to the link they started
  // from, now signed in and with the address proved. The return is itself the
  // proof that settled: the account page navigates only once the profile reads
  // verified, and it unmounts before a verified banner on that page could be
  // observed.
  await expect
    .poll(() => new URL(page.url()).pathname)
    .toBe(invitationPath)
  await expect(
    page.getByRole('heading', {
      level: 1,
      name: copy('authUi', 'acceptInvitation.title'),
      exact: true,
    }),
  ).toBeVisible()
  return page
}

/** One invitation row in a given status, for the address the journey invites. */
function invitationRowInStatus(page: Page, statusKey: string): Locator {
  return invitationRow(page).filter({
    hasText: copy('adminUi', statusKey),
  })
}

/** The expiry line's copy prefix: the sentence with an empty value. */
const EXPIRY_PREFIX = copy('authUi', 'acceptInvitation.expires', { expires: '' })

test('invitation-to-tenant: an operator invites an address and the invitee joins the tenant', async ({
  demo,
  browser,
}) => {
  const { page, server } = demo
  // The account ids the invitations page reads must never reach the rendered
  // page; this collects them from the product's own reads.
  const accountIds = collectTenantAccountIds(page, TENANT)
  let inviteeContext: BrowserContext | undefined
  let inviteePage: Page | undefined
  let acceptanceLink = ''
  // The instants around the issue, the origin of the chosen lifetime.
  let issuedAt = 0
  let issuedAtConfirmed = 0
  // The expiry strings the chosen lifetime can render.
  let expectedExpiry: string[] = []

  try {
    await test.step('precondition: the operator signs in and proves the account email', async () => {
      // The journey's precondition is an operator whose email is proved. The
      // seed writes the address unproved, so the spec reaches the
      // precondition the way a person does: the standing notice's verify entry,
      // the code from the server's process log, and the verify action.
      await proveOperatorEmail(demo)
    })

    await test.step('step 1: the operator opens the invitation-and-members page and selects the tenant', async () => {
      // REQ-platform-admin-001's operator facet is driven by the way in: the
      // start page's card and the account menu's entry are both offered while
      // the platform's own organization is current.
      await openTenantInvitations(page)

      // The page opens on pending invitations, and the tenant holds none, so
      // the list renders its filtered empty state. Its member list holds the
      // seeded member and the platform operator, each carrying the source the
      // seed wrote. The member's own row is what the journey names, so the
      // source is read inside that row.
      await expect(
        invitationSection(page).getByRole('heading', {
          name: copy('adminUi', 'invitations.empty.filteredTitle'),
          exact: true,
        }),
      ).toBeVisible()
      const members = memberSection(page).getByRole('table')
      const nameColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.displayName'),
      )
      const usernameColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.username'),
      )
      const sourceColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.source'),
      )
      const existing = members
        .locator('tbody tr')
        .filter({ hasText: EXISTING_MEMBER })
      await expect(existing).toHaveCount(1)
      // The member read supplies both columns: the display name under its own
      // header and the login username under the username header. The internal
      // account id the read carries is not what either column shows.
      await expect(existing.locator('td').nth(nameColumn)).toHaveText(
        EXISTING_MEMBER,
      )
      await expect(existing.locator('td').nth(usernameColumn)).toHaveText(
        EXISTING_MEMBER_USERNAME,
      )
      await expect(existing.locator('td').nth(sourceColumn)).toHaveText(
        copy('adminUi', 'members.source.admin'),
      )

      // Both sections are read from the same tenant read that answers the ids;
      // neither may render one of them in its text or any attribute.
      const ids = await accountIds.settled()
      await expectNoAccountIds(invitationSection(page), ids)
      await expectNoAccountIds(memberSection(page), ids)
    })

    await test.step('step 2: the operator invites the address with a chosen lifetime and sees the confirmation', async () => {
      // The deadline the product must write is measured from this instant, so
      // a chosen lifetime the implementation ignored cannot pass on both the
      // list and the accept screen agreeing with each other.
      issuedAt = Date.now()
      await issueInvitation(page, e2eInvitee.identifier)
      issuedAtConfirmed = Date.now()
    })

    await test.step('step 3: the one-time panel hands out the acceptance link until it is closed', async () => {
      acceptanceLink = await takeAcceptanceLink(
        page,
        server,
        e2eInvitee.identifier,
      )
    })

    await test.step('step 4: issuing to the same address again is refused as invitation-pending', async () => {
      // The address already holds an unexpired pending invitation, so the
      // second issue is a conflict rather than a replacement. The response is
      // observed directly: the panel's error text is the shared fallback, while
      // the problem type is what names this refusal.
      const refused = page.waitForResponse(
        (response) =>
          response.request().method() === 'POST' &&
          new URL(response.url()).pathname ===
            `/api/v1/tenants/${TENANT}/invitations`,
      )
      await submitInvitation(page, e2eInvitee.identifier)
      const answer = await refused
      expect(answer.status()).toBe(409)
      const problem = (await answer.json()) as { type?: string }
      expect(problem.type).toBe('urn:speed:authn:invitation-pending')
      await expect(invitationSection(page).getByRole('alert')).toBeVisible()
      // The one invitation and the link already handed out are unchanged.
      await expect(
        invitationRowInStatus(page, 'invitations.status.pending'),
      ).toHaveCount(1)
    })

    await test.step('step 5: the list shows the one pending invitation', async () => {
      const table = invitationSection(page).getByRole('table')
      await expect(table).toBeVisible()

      // The list's columns are the four the journey names, beside the actions.
      for (const header of [
        copy('adminUi', 'invitations.column.email'),
        copy('adminUi', 'invitations.column.status'),
        copy('adminUi', 'invitations.column.invitedBy'),
        copy('adminUi', 'invitations.column.expires'),
      ]) {
        await expect(
          table.getByRole('columnheader', { name: header, exact: true }),
        ).toBeVisible()
      }

      // The refused second issue created no row: the list holds the one
      // pending invitation. A superseded old row would read expired rather
      // than revoked, so this flow produces no revoked row either.
      const pending = invitationRowInStatus(page, 'invitations.status.pending')
      await expect(pending).toHaveCount(1)
      await expect(
        pending.getByText(e2eInvitee.identifier, { exact: true }),
      ).toBeVisible()
      // The inviter cell shows the display name alone; the complete
      // "display name (username)" is its hover title.
      const invitedByColumn = await columnIndex(
        table,
        copy('adminUi', 'invitations.column.invitedBy'),
      )
      const invitedByCell = pending.getByRole('cell').nth(invitedByColumn)
      await expect(invitedByCell).toHaveText(OPERATOR.displayName)
      // The complete label is the cell content's own hover title, so the
      // element carrying it inside the cell is what has to show it.
      await expect(
        invitedByCell.getByTitle(
          `${OPERATOR.displayName} (${OPERATOR.username})`,
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        pending.getByText(`${OPERATOR.displayName} (${OPERATOR.username})`, {
          exact: true,
        }),
      ).toHaveCount(0)

      // The deadline the list renders must lie thirty days from the issue,
      // not merely agree between the list and the accept screen. The expected
      // strings are computed from the chosen lifetime and the instants around
      // the issue, so a lifetime the product ignored fails here.
      expectedExpiry = expiryChoices(issuedAt, issuedAtConfirmed)
      const expiryColumn = await columnIndex(
        table,
        copy('adminUi', 'invitations.column.expires'),
      )
      await expectOneOfText(
        pending.getByRole('cell').nth(expiryColumn),
        expectedExpiry,
      )
      await expect(
        invitationRowInStatus(page, 'invitations.status.revoked'),
      ).toHaveCount(0)
    })

    await test.step('step 6: the invitee registers through the link and proves the invited address', async () => {
      // The operator's channel hands the link to the invitee; the invitee is a
      // second browser context with no session. The tier's language, time zone
      // and viewport pins are repeated on a context the spec creates by hand.
      inviteeContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      inviteePage = await registerAndProve(
        inviteeContext,
        acceptanceLink,
        {
          username: e2eInvitee.username,
          email: e2eInvitee.identifier,
          password: e2eInvitee.password,
          displayName: e2eInvitee.displayName,
        },
        server,
      )
      // The registration returned to the one link already handed out; the
      // refused second issue produced no alternative to compare against.
    })

    await test.step('step 7: the accept screen shows the tenant, the masked invited address and the expiry', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.invitedBy', {
            inviter: OPERATOR.displayName,
            tenant: TENANT,
          }),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.email', { email: MASKED_INVITEE }),
          { exact: true },
        ),
      ).toBeVisible()
      // The public read masks the address on the server, so the full address
      // never reaches the page.
      await expect(
        invitee.getByText(e2eInvitee.identifier, { exact: true }),
      ).toHaveCount(0)
      // The deadline the test computed from the chosen thirty-day lifetime,
      // in the display zone the browser is pinned to -- not a value the
      // operator page rendered.
      const expiryLine = invitee.getByText(
        new RegExp(`^${escapeRegExp(EXPIRY_PREFIX)}`),
      )
      await expectOneOfText(
        expiryLine,
        expectedExpiry.map((text) => `${EXPIRY_PREFIX}${text}`),
      )
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
      ).toBeVisible()
      expect(new URL(invitee.url()).pathname).toBe('/')

      const dismiss = invitee.getByRole('button', {
        name: copy('demo', 'home.notice.dismiss'),
        exact: true,
      })
      await expect(dismiss).toBeVisible()
      const confirmation = dismiss.locator(
        'xpath=ancestor::div[contains(@class, "MuiAlert-root")][1]',
      )
      await expect(
        confirmation.getByText(copy('demo', 'home.notice.membershipTitle'), {
          exact: true,
        }),
      ).toBeVisible()

      // The current organization is the invited one: the account menu's first
      // entry names it, and its own row expands the other organizations
      // instead of being a pick, so the expansion never lists it again.
      const menu = await openAccountMenu(invitee)
      await expect(menu.getByText(TENANT, { exact: true })).toBeVisible()
      if ((await organizationRow(invitee, TENANT).count()) > 0) {
        await organizationRow(invitee, TENANT).click()
        await expect(
          menu
            .getByRole('group', {
              name: copy('demo', 'userMenu.organizations'),
              exact: true,
            })
            .getByRole('menuitem', { name: TENANT, exact: true }),
        ).toHaveCount(0)
      }
      await closeAccountMenu(invitee)
      await expect(organizationRow(invitee, TENANT)).toHaveCount(0)
    })

    await test.step('step 9: a member has no admin entry, and a full-page admin load turns back home', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage

      // REQ-platform-admin-001's member facet, checked on the tenant home the
      // acceptance landed on: this step runs while the viewer is still there,
      // before the re-opened link below leaves them on the landing, so the
      // menu and start-page assertions observe the member's real home. The bar
      // carries no admin link, and the account menu offers no admin entry
      // because the current organization is the invited tenant rather than the
      // platform's own.
      await expect(
        invitee
          .getByRole('banner')
          .getByRole('link', { name: copy('demo', 'nav.admin'), exact: true }),
      ).toHaveCount(0)
      await openAccountMenu(invitee)
      await expect(adminEntry(invitee)).toHaveCount(0)
      await closeAccountMenu(invitee)
      await expect(
        invitee.getByRole('heading', {
          level: 2,
          name: copy('demo', 'home.cards.admin.title'),
          exact: true,
        }),
      ).toHaveCount(0)

      // The full-page-load branch: the member's own tenant list settles the
      // tenant, the platform permission set reads as a known empty set, and the
      // admin area turns the viewer back to the tenant home. Nothing of the
      // admin renders, and no request is spent being refused.
      for (const path of ['/admin', '/admin/settings']) {
        await invitee.goto(`${server.url}${path}`)
        await expect(
          invitee.getByRole('heading', {
            level: 1,
            name: copy('demo', 'home.heading'),
            exact: true,
          }),
        ).toBeVisible()
        await expect(
          invitee.getByRole('navigation', {
            name: copy('adminUi', 'shell.navLabel'),
            exact: true,
          }),
        ).toHaveCount(0)
        await expect(
          invitee.getByRole('progressbar', {
            name: copy('demo', 'admin.accessPending'),
            exact: true,
          }),
        ).toHaveCount(0)
        expect(new URL(invitee.url()).pathname).toBe('/')
      }
    })

    await test.step('step 10: the invitee re-opens the accepted link and meets the member state', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage

      // The accepted link names the organization the viewer already belongs
      // to and offers the way in; the summary and the accept action are gone,
      // so the link cannot be redeemed a second time.
      await invitee.goto(acceptanceLink)
      await expect(
        invitee.getByRole('heading', {
          name: copy('authUi', 'acceptInvitation.member.title', {
            tenant: TENANT,
          }),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        invitee.getByRole('button', {
          name: copy('authUi', 'acceptInvitation.member.open'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        invitee.getByRole('button', {
          name: copy('authUi', 'acceptInvitation.accept'),
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.email', { email: MASKED_INVITEE }),
          { exact: true },
        ),
      ).toHaveCount(0)
    })

    await test.step('step 11: the operator sees the invitation accepted and the invitee in the member table', async () => {
      // The operator stayed on the page while the invitee accepted; the
      // page's own refresh re-reads both lists.
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'invitations.title'),
          exact: true,
        }),
      ).toBeVisible()
      // The list opens on the pending state, so the accepted invitation is
      // read under its own filter. The refresh re-reads the list the filter
      // narrows.
      await chooseSelectOption(
        page.getByRole('combobox', {
          name: copy('adminUi', 'invitations.filter.label'),
          exact: true,
        }),
        page.getByRole('option', {
          name: copy('adminUi', 'invitations.status.accepted'),
          exact: true,
        }),
      )
      await page
        .getByRole('button', {
          name: copy('adminUi', 'common.refresh'),
          exact: true,
        })
        .click()

      // The one invitation's terminal state is now the accepted one; the
      // refused second issue never created a superseded row.
      await expect(
        invitationRowInStatus(page, 'invitations.status.accepted'),
      ).toHaveCount(1)
      await expect(
        invitationRowInStatus(page, 'invitations.status.revoked'),
      ).toHaveCount(0)

      // The member table gained exactly one row: the invited account, active,
      // and sourced from the invitation. The tenant's earlier members -- the
      // seeded target and the platform operator, who is acme's second
      // administrator -- are still there, so the table holds three rows.
      const members = memberSection(page).getByRole('table')
      await expect(members.locator('tbody tr')).toHaveCount(3)
      const nameColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.displayName'),
      )
      const usernameColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.username'),
      )
      const sourceColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.source'),
      )
      const activeColumn = await columnIndex(
        members,
        copy('adminUi', 'members.column.active'),
      )
      const invited = members
        .locator('tbody tr')
        .filter({ hasText: e2eInvitee.displayName })
      await expect(invited).toHaveCount(1)
      await expect(invited.locator('td').nth(nameColumn)).toHaveText(
        e2eInvitee.displayName,
      )
      // The Username column carries the member read's login username, not
      // the composed identity: the header names exactly that value.
      await expect(invited.locator('td').nth(usernameColumn)).toHaveText(
        e2eInvitee.username,
      )
      await expect(invited.locator('td').nth(sourceColumn)).toHaveText(
        copy('adminUi', 'members.source.invite'),
      )
      await expect(invited.locator('td').nth(activeColumn)).toHaveText(
        copy('adminUi', 'members.activeYes'),
      )

      // The refreshed reads answered the new member's account id as well; the
      // page still renders none of them.
      const ids = await accountIds.settled()
      await expectNoAccountIds(invitationSection(page), ids)
      await expectNoAccountIds(memberSection(page), ids)
    })
  } finally {
    // The invitee context holds its own cookie session; closing it keeps a
    // failed run from leaving a second browser context open while the server
    // drains.
    await inviteeContext?.close().catch(() => undefined)
  }
})

test('invitation-to-tenant: an account that proves a different address cannot accept the invitation', async ({
  demo,
  browser,
}) => {
  const { page, server } = demo
  let outsiderContext: BrowserContext | undefined
  let acceptanceLink = ''

  try {
    await test.step('precondition: the operator signs in, proves the account email and opens the invitations page', async () => {
      await proveOperatorEmail(demo)
      await openTenantInvitations(page)
    })

    await test.step('step 1: the operator issues an invitation and the panel hands out the link', async () => {
      await issueInvitation(page, e2eInvitee.identifier)
      acceptanceLink = await takeAcceptanceLink(
        page,
        server,
        e2eInvitee.identifier,
      )
      outsiderContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      // The outsider proves its own, different address through the same
      // registration-and-code path, so the refusal below is the address
      // mismatch and not a missing proof.
      expect(acceptanceLink).toMatch(
        new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
      )
    })

    await test.step('step 2: the outsider registers, proves its own address and is refused', async () => {
      if (outsiderContext === undefined) {
        throw new Error('the outsider context was never opened')
      }
      const outsider = await registerAndProve(
        outsiderContext,
        acceptanceLink,
        OUTSIDER,
        server,
      )

      // The token grants joining; it does not prove the invited address. The
      // account proved a different one, so the eligibility read shows the
      // proof step on arrival: no plain accept action is offered. The
      // refusal the retry reaches is the same unproved-email refusal, and no
      // membership is written.
      await expect(
        outsider.getByRole('heading', {
          name: copy('authUi', 'acceptInvitation.unproved.title'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        outsider.getByText(
          copy('authUi', 'acceptInvitation.unproved.description', {
            email: MASKED_INVITEE,
          }),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        outsider.getByRole('button', {
          name: copy('authUi', 'acceptInvitation.accept'),
          exact: true,
        }),
      ).toHaveCount(0)
      // Wait for the acceptance answer itself: the proof step is already on
      // screen before the click, so re-asserting it would pass without the
      // request having been made at all.
      const inviteToken = new URL(acceptanceLink).pathname.split('/').pop()
      const refused = outsider.waitForResponse(
        (response) =>
          response.request().method() === 'POST' &&
          new URL(response.url()).pathname ===
            `/api/v1/auth/invitations/${inviteToken ?? ''}`,
      )
      await outsider
        .getByRole('button', {
          name: copy('authUi', 'acceptInvitation.unproved.retry'),
          exact: true,
        })
        .click()
      const answer = await refused
      expect(answer.status()).toBe(403)
      const problem = (await answer.json()) as { type?: string }
      expect(problem.type).toBe('urn:speed:authn:invitation-email-unproved')
      await expect(
        outsider.getByRole('heading', {
          name: copy('authUi', 'acceptInvitation.unproved.title'),
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 3: the invitation stays pending and no membership is written', async () => {
      // The operator's page re-reads both lists: the invitation was not
      // consumed and the member table did not gain the outsider.
      await page
        .getByRole('button', {
          name: copy('adminUi', 'common.refresh'),
          exact: true,
        })
        .click()
      await expect(
        invitationRowInStatus(page, 'invitations.status.pending'),
      ).toHaveCount(1)
      await expect(
        invitationRowInStatus(page, 'invitations.status.accepted'),
      ).toHaveCount(0)

      const members = memberSection(page).getByRole('table')
      await expect(members.locator('tbody tr')).toHaveCount(2)
      await expect(
        members
          .locator('tbody tr')
          .filter({ hasText: OUTSIDER.displayName }),
      ).toHaveCount(0)
      await expect(
        members
          .locator('tbody tr')
          .filter({ hasText: OUTSIDER.username }),
      ).toHaveCount(0)
    })
  } finally {
    await outsiderContext?.close().catch(() => undefined)
  }
})
