/**
 * J13 -- an operator onboards a tenant from the admin: register it, open its
 * detail page, invite its first member, and see the member land in it.
 *
 * The operator is the seeded platform administrator. The review seed writes its
 * email address as an administrator's assertion, not as a proof of ownership,
 * and every management write this journey makes -- the roster, the
 * authorization surface and the invitation surface -- refuses an account whose
 * email address is unproved. The spec therefore reaches the journey's
 * precondition the way a person does: the standing verification notice, the
 * code from the review process log (the one non-UI read this spec makes), and
 * the verify action.
 *
 * OPENING A TENANT BINDS ITS CREATOR AS ITS FIRST TENANT-ADMIN. The roster's
 * registration route provisions the domain's tenant-admin role, its grants and
 * the creator's membership and binding, so the operator can address the new
 * tenant's management pages at once and the page's member list holds the
 * operator from the start. The permissions page is still driven in step 6: it
 * creates a second role, grants it `authn.membership.manage` through the
 * high-risk permission's two-step confirmation, and binds the operator to it.
 * That exercises the authorization surface without being the precondition the
 * invitation page needs.
 *
 * The seeded roster already carries acme; the second tenant is registered
 * through the standalone registration page, opened from the roster's header
 * link, and a successful registration lands on the new tenant's detail page.
 * Both collision branches the document names are driven before the successful
 * registration -- an identifier acme holds, and an alias acme holds -- and the
 * roster is then asserted to have gained only the one row. The new tenant is
 * registered with an alias that differs from its identifier, so the accept
 * summary (which names the tenant by identifier) and the account menu's
 * organization row (which names it by alias) cannot be confused. An alias is
 * immutable after creation, so the detail page shows it read-only and offers
 * no rename control.
 *
 * THE DOCUMENT'S STEP-10 RE-OPENED LINK IS NOT RENDERED BY THE PRODUCT, so this
 * spec leaves it undriven and reports the gap. The accept-view read returns an
 * invitation's stored state even after acceptance, and the accept screen
 * ignores that state: a link opened again renders the invitation summary with
 * an Accept action rather than the "no longer valid" result the document
 * requires. A second acceptance click is refused, but only with the generic
 * authn-conflict text, not the terminal accepted result. REQ-tenancy-013 is
 * exactly that read path and REQ-tenancy-020's second-redemption observation
 * depends on it, so both stay out of `@covers`. The same divergence is already
 * reported by the invitation-to-tenant spec.
 *
 * `@covers` is narrower than the document's list in other ways.
 * REQ-tenancy-006 (the roster's read decided in the platform domain) is not
 * driven: this journey only reads it as a platform administrator, and the
 * refusal branch needs a caller authorized inside a tenant but not in the
 * platform domain, which the admin cannot present. REQ-tenancy-012 (a second
 * issue for the same address voids the first) is not a step of this journey.
 * REQ-tenancy-017's unrelated-session clause, REQ-tenancy-018 (an address with
 * no account) and REQ-tenancy-019 (an unproved reservation) are alternate
 * branches: the invitee here holds the invited address's own account, so the
 * acceptance binds the membership to that account. REQ-tenancy-023 is not
 * claimed: the invitee's account owns a personal tenant as well, so the
 * organization list has a second row available, but this spec only observes the
 * current row and leaves the switch to the journeys that drive it. REQ-tenancy-005 is
 * claimed narrowly: the roster is read with the seeded tenants and asserted in
 * identifier order, while the empty-roster clause needs a deployment that seeds
 * no tenant. REQ-platform-admin-058 (the detail page's ownership controls) is
 * not claimed: this spec reads the detail page's summary and its missing
 * rename control, while the transfer and delete controls it drives are the
 * roster's, and the PRD marks that requirement's end-to-end case unimplemented.
 * REQ-tenancy-008 is claimed narrowly too: the creator's member list
 * and the list after the acceptance are driven, while paging is not.
 * REQ-tenancy-011's malformed-address refusal and REQ-platform-admin-032's
 * blank-expiry default are not driven; the typed expiry is what both the issuer
 * and the invitee read. REQ-platform-admin-006 is claimed narrowly to the
 * grouped navigation this assembly renders: the shell renders a group holding a
 * single entry as that entry alone, and this deployment serves one page in the
 * overview, tenant and audit sections (the SMS administration module is not
 * mounted), so the grouping is asserted through the multi-entry identity group
 * and the ordered entries the group table produces.
 *
 * Expected copy is read from the shipped en-US bundles through
 * helpers/i18n.ts. The only non-UI read is the operator's email verification
 * code from the review process log (fixtures/log.ts).
 */
// @journey journey-operator-onboards-tenant.md
// @covers REQ-platform-admin-001, REQ-platform-admin-006, REQ-platform-admin-010, REQ-platform-admin-011, REQ-platform-admin-032, REQ-platform-admin-033, REQ-platform-admin-050, REQ-platform-admin-051, REQ-platform-admin-055, REQ-platform-admin-118, REQ-tenancy-001, REQ-tenancy-002, REQ-tenancy-005, REQ-tenancy-008, REQ-tenancy-011, REQ-tenancy-016, REQ-tenancy-021, REQ-tenancy-022, REQ-tenancy-061, REQ-tenancy-062, REQ-tenancy-064
import type { BrowserContext, Locator, Page } from '@playwright/test'

import { reviewInvitee, reviewOrdinaryMember } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import type { ReviewServer } from '../fixtures/server'
import {
  adminEntry,
  backToApp,
  closeAccountMenu,
  openAccountMenu,
  organizationRow,
  switchOrganization,
} from '../helpers/account-menu'
import type { Accounts } from '../helpers/accounts'
import { copy, copyPattern } from '../helpers/i18n'

// The admin tables carry low-priority columns and the admin sidebar takes
// part of the width, so the desktop viewport has to be wide enough for the
// container to render every column this journey reads.
test.use({ seedInvitee: true, viewport: { width: 1920, height: 1080 } })

/** The seeded platform administrator the review assembly writes. */
const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

/** The tenant the seed registers; its identifier and alias are the same. */
const SEEDED_TENANT = 'acme'

/** The identifier of the tenant this journey registers. */
const TENANT_ID = 'globex'

/** The alias the tenant is registered with; it differs from the identifier. */
const TENANT_ALIAS = 'globex-labs'

/** The display name the registration form requires. */
const TENANT_DISPLAY_NAME = 'Globex Labs'

/**
 * The role the operator creates in the new tenant and binds itself to. The
 * grant the role carries is the membership-management permission; the code is
 * the platform's own vocabulary, not user-facing copy.
 */
const ROLE = 'tenant-inviter'
const PERMISSION = 'authn.membership.manage'

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
 * tier is Intl's medium date and short time, and both the invitation list and
 * the accept summary pin the zone to UTC.
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

/**
 * The bind write's success notice.
 *
 * The admin names an account in a success notice by its display name and
 * first login identifier and never by the opaque account id, so the expected
 * sentence is the bundle's `bound` filled with the reference the same bundle's
 * `common.accountReference.nameWithIdentifier` renders. The seeded operator
 * resolves both, so the named form is the one the product renders; an
 * implementation that fell back to the neutral sentence or inserted the id
 * would fail this assertion.
 */
function boundNotice(): string {
  return copy('adminUi', 'permissions.bindings.bound', {
    role: ROLE,
    account: copy('adminUi', 'common.accountReference.nameWithIdentifier', {
      name: OPERATOR.displayName,
      identifier: OPERATOR.username,
    }),
  })
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

/** The home page's admin card, the link the card's heading sits in. */
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

/** One admin navigation link by its page title. */
function adminLink(page: Page, label: string): Locator {
  return adminNav(page).getByRole('link', { name: label, exact: true })
}

/** The tenant selector of a tenant-addressed admin page. */
function tenantSelector(page: Page): Locator {
  return page.getByRole('combobox', {
    name: copy('adminUi', 'common.tenant'),
    exact: true,
  })
}

/** The roster's create form, as the section named by its title. */
function createSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'tenants.create.title'),
    exact: true,
  })
}

/** The roster table. */
function rosterTable(page: Page): Locator {
  return page.getByRole('table', {
    name: copy('uiKit', 'dataTable.ariaLabel'),
    exact: true,
  })
}

/** One roster row, by the tenant identifier it shows. */
function rosterRow(page: Page, id: string): Locator {
  return rosterTable(page)
    .locator('tbody tr')
    .filter({ hasText: id })
}

/**
 * The identifier cell of a roster row. The identifier is the row's link to the
 * tenant's detail page; the alias cell beside it may carry the same text (a
 * tenant registered with alias equal to identifier), so the two are addressed
 * by role and by title respectively rather than by text.
 */
function rosterIdentifier(row: Locator, id: string): Locator {
  return row.getByRole('link', { name: id, exact: true })
}

/**
 * The alias cell of a roster row. The shared table puts the same value on the
 * cell's `title` and on the paragraph inside it, so the paragraph is addressed
 * by the title it carries; the aliases this spec uses are plain tokens.
 */
function rosterAlias(row: Locator, alias: string): Locator {
  return row.locator(`p[title="${alias}"]`)
}

/** The tenant detail page's summary section. */
function summarySection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('adminUi', 'tenantDetail.summary'),
    exact: true,
  })
}

/** One value of the summary's definition list, by its field position. */
function summaryValue(page: Page, index: number): Locator {
  return summarySection(page).locator('dd').nth(index)
}

/** The permissions page's named section. */
function permissionsSection(page: Page, title: string): Locator {
  return page.getByRole('region', { name: title, exact: true })
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
    hasText: copy('adminUi', 'invitations.issued.link', { email }),
  })
}

/** The expiry line's copy prefix: the sentence with an empty value. */
const EXPIRY_PREFIX = copy('authUi', 'acceptInvitation.expires', { expires: '' })

test('operator-onboards-tenant: an operator registers a tenant, invites its first member, and the member joins', async ({
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

    await test.step('step 1: the operator enters the admin from the header entry and sees the grouped navigation', async () => {
      // The account page the verification landed on is inside the tenant
      // shell; the start page is where the admin entry and card are read.
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
      // admin destination are offered to platform staff, and only while the
      // platform's own organization is current. The account menu's admin
      // entry is read with the menu open; the card is read before it,
      // because an open menu's backdrop hides the page from role queries.
      await switchOrganization(page, 'system')
      await expect(adminCard(page)).toBeVisible()
      await openAccountMenu(page)
      await expect(adminEntry(page)).toBeVisible()
      await adminEntry(page).click()
      const nav = adminNav(page)
      await expect(nav).toBeVisible()

      // REQ-platform-admin-006: the admin groups its pages by capability
      // domain. This deployment serves a single page in the overview, tenant
      // and audit sections, and the shell renders a single-entry group as that
      // entry rather than a heading over one link; the multi-entry identity
      // group keeps its heading, and the entries of all five sections render in
      // the group table's order.
      await expect(
        nav.getByRole('button', {
          name: copy('adminUi', 'nav.groups.identity'),
          exact: true,
        }),
      ).toBeVisible()
      for (const label of [
        copy('adminUi', 'dashboard.title'),
        copy('adminUi', 'tenants.title'),
        copy('adminUi', 'settings.title'),
        copy('adminUi', 'audit.title'),
      ]) {
        await expect(adminLink(page, label)).toBeVisible()
      }
    })

    await test.step('step 2: the operator opens the tenant roster and reads the seeded tenant row', async () => {
      await adminLink(page, copy('adminUi', 'tenants.title')).click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'tenants.title'),
          exact: true,
        }),
      ).toBeVisible()

      // REQ-platform-admin-010: every row carries the identifier, the alias and
      // the status. The roster is every tenant the process serves: the system
      // tenant, the seeded business tenant acme, and the personal tenant the
      // account-creation hook gives each of this server's three seeded
      // accounts. REQ-tenancy-005: the roster is read in identifier order (the
      // new tenant registered in step 3 follows acme).
      const rows = rosterTable(page).locator('tbody tr')
      await expect(rows).toHaveCount(5)
      const seeded = rosterRow(page, SEEDED_TENANT)
      await expect(seeded).toHaveCount(1)
      await expect(rosterIdentifier(seeded, SEEDED_TENANT)).toBeVisible()
      await expect(rosterAlias(seeded, SEEDED_TENANT)).toBeVisible()
      await expect(
        seeded.getByText(copy('adminUi', 'tenants.status.active'), {
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 3: the operator registers a new tenant from its own page, and the roster grows by one active row', async () => {
      // The roster no longer carries an inline form. Its header links to the
      // standalone registration page.
      const rows = rosterTable(page).locator('tbody tr')
      const before = await rows.count()

      await page
        .getByRole('link', {
          name: copy('adminUi', 'tenants.register'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'tenants.create.title'),
          exact: true,
        }),
      ).toBeVisible()

      const form = createSection(page)
      const idField = form.getByLabel(
        copy('orgCreateUi', 'fields.id'),
        { exact: true },
      )
      const aliasField = form.getByLabel(
        copy('orgCreateUi', 'fields.alias'),
        { exact: true },
      )
      const displayNameField = form.getByLabel(
        copy('orgCreateUi', 'fields.displayName'),
        { exact: true },
      )
      const submit = form.getByRole('button', {
        name: copy('adminUi', 'tenants.create.submit'),
        exact: true,
      })
      const conflict = copy('adminUi', 'errors.tenant.conflict')

      // REQ-tenancy-001's first collision branch: an identifier another tenant
      // holds is refused, and the page stays on the registration form.
      await idField.fill(SEEDED_TENANT)
      await aliasField.fill('acme-alias-clash')
      await displayNameField.fill('Acme Alias Clash')
      await submit.click()
      await expect(page.getByText(conflict, { exact: true })).toBeVisible()

      // Its second branch: an alias another tenant holds is refused the same
      // way, with a fresh identifier.
      await idField.fill(`${TENANT_ID}-spare`)
      await aliasField.fill(SEEDED_TENANT)
      await displayNameField.fill('Acme Spare')
      await submit.click()
      await expect(page.getByText(conflict, { exact: true })).toBeVisible()

      // The successful registration navigates to the new tenant's detail
      // page. The alias differs from the identifier, so the later accept
      // summary and account-menu assertions can tell the two names apart.
      await idField.fill(TENANT_ID)
      await aliasField.fill(TENANT_ALIAS)
      await displayNameField.fill(TENANT_DISPLAY_NAME)
      await submit.click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'tenantDetail.title', { id: TENANT_ID }),
          exact: true,
        }),
      ).toBeVisible()

      // Back on the roster the two refused attempts left no row: only the
      // successful registration grew the table, and its row is active with
      // the values typed.
      await page
        .getByRole('link', {
          name: copy('adminUi', 'tenants.title'),
          exact: true,
        })
        .first()
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'tenants.title'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(rows).toHaveCount(before + 1)
      const created = rosterRow(page, TENANT_ID)
      await expect(created).toHaveCount(1)
      await expect(rosterIdentifier(created, TENANT_ID)).toBeVisible()
      await expect(rosterAlias(created, TENANT_ALIAS)).toBeVisible()
      await expect(
        created.getByText(copy('adminUi', 'tenants.status.active'), {
          exact: true,
        }),
      ).toBeVisible()

      // The roster is read in identifier order: acme first, the new tenant
      // after it.
      await expect(rows.nth(0)).toContainText(SEEDED_TENANT)
      await expect(rows.nth(1)).toContainText(TENANT_ID)
    })

    await test.step("step 4: the detail page shows the tenant's identifier, alias and status", async () => {
      // REQ-platform-admin-011: the detail page is reached from the roster row
      // and shows the identifier, the alias and the status.
      await rosterRow(page, TENANT_ID)
        .getByRole('link', { name: TENANT_ID, exact: true })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'tenantDetail.title', { id: TENANT_ID }),
          exact: true,
        }),
      ).toBeVisible()

      // Summary fields, in the page's own order: identifier, alias, display
      // name, status. An alias is immutable after creation: the cell is
      // read-only and the page offers no rename control.
      await expect(summaryValue(page, 0)).toHaveText(TENANT_ID)
      await expect(summaryValue(page, 1)).toHaveText(TENANT_ALIAS)
      await expect(summaryValue(page, 2)).toHaveText(TENANT_DISPLAY_NAME)
      await expect(summaryValue(page, 3)).toHaveText(
        copy('adminUi', 'tenants.status.active'),
      )
      await expect(
        page.getByLabel(
          copy('adminUi', 'tenantDetail.actions.renameLabel'),
          { exact: true },
        ),
      ).toHaveCount(0)

      // The roster still shows the alias chosen at registration.
      await page
        .getByRole('link', {
          name: copy('adminUi', 'tenantDetail.back'),
          exact: true,
        })
        .click()
      await expect(rosterRow(page, TENANT_ID)).toHaveCount(1)
      await expect(
        rosterAlias(rosterRow(page, TENANT_ID), TENANT_ALIAS),
      ).toBeVisible()
    })

    await test.step('the operator manages the new tenant through the permissions page', async () => {
      // Opening the tenant bound the operator as its first tenant-admin. This
      // step still drives the authorization surface: it creates a second role
      // in the tenant, grants it the membership-management permission and
      // binds the operator to it. The tenant-addressed page decides in the
      // tenant's own domain, which the created binding exercises.
      await adminLink(page, copy('adminUi', 'permissions.title')).click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'permissions.title'),
          exact: true,
        }),
      ).toBeVisible()

      await tenantSelector(page).click()
      await page.getByRole('option', { name: TENANT_ALIAS, exact: true }).click()

      // The role catalog, then the role.
      const roles = permissionsSection(
        page,
        copy('adminUi', 'permissions.roles.title'),
      )
      const roleField = roles.getByLabel(
        copy('adminUi', 'permissions.roles.create.roleLabel'),
        { exact: true },
      )
      await roleField.fill(ROLE)
      await roles
        .getByRole('button', {
          name: copy('adminUi', 'permissions.roles.create.submit'),
          exact: true,
        })
        .click()
      await expect(
        page.getByText(
          copy('adminUi', 'permissions.roles.saved', { role: ROLE }),
          { exact: true },
        ),
      ).toBeVisible()
      const roleRow = roles
        .getByRole('table')
        .locator('tbody tr')
        .filter({ hasText: ROLE })
      await expect(roleRow).toHaveCount(1)

      // Its grants: select the role, then grant the membership permission.
      await roleRow
        .getByRole('button', {
          name: copy('adminUi', 'permissions.roles.manageGrantsAria', {
            role: ROLE,
          }),
          exact: true,
        })
        .click()
      const grants = permissionsSection(
        page,
        copy('adminUi', 'permissions.grants.title'),
      )
      await expect(
        grants.getByText(
          copy('adminUi', 'permissions.grants.forRole', { role: ROLE }),
          { exact: true },
        ),
      ).toBeVisible()

      await grants
        .getByRole('combobox', {
          name: copy('adminUi', 'permissions.grants.permissionLabel'),
          exact: true,
        })
        .click()
      await page
        .getByRole('option', { name: new RegExp(escapeRegExp(PERMISSION)) })
        .click()
      await grants
        .getByRole('button', {
          name: copy('adminUi', 'permissions.grants.add'),
          exact: true,
        })
        .click()
      // The membership permission is dangerous, so the platform page asks for
      // the same deliberate second click the organization page does.
      const dangerousDialog = page.getByRole('dialog', {
        name: copy('adminUi', 'permissions.grants.dangerousTitle'),
        exact: true,
      })
      await expect(dangerousDialog).toBeVisible()
      await dangerousDialog
        .getByRole('button', {
          name: copy('adminUi', 'permissions.grants.dangerousConfirm'),
          exact: true,
        })
        .click()
      const armAgain = dangerousDialog.getByRole('button', {
        name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
        exact: true,
      })
      await expect(armAgain).toBeEnabled({ timeout: 5_000 })
      await armAgain.click()
      await expect(
        page.getByText(
          copy('adminUi', 'permissions.grants.granted', {
            permission: PERMISSION,
            role: ROLE,
          }),
          { exact: true },
        ),
      ).toBeVisible()
      await expect(
        grants.getByRole('table').getByText(PERMISSION, { exact: true }),
      ).toBeVisible()

      // The binding: the operator's own account holds the role in the tenant.
      const bindings = permissionsSection(
        page,
        copy('adminUi', 'permissions.bindings.title'),
      )
      const accountField = bindings.getByRole('combobox', {
        name: copy('adminUi', 'permissions.bindings.accountLabel'),
        exact: true,
      })
      await accountField.click()
      await accountField.fill(OPERATOR.identifier)
      await page
        .getByRole('option', { name: new RegExp(OPERATOR.displayName) })
        .click()
      await bindings
        .getByLabel(copy('adminUi', 'permissions.bindings.roleLabel'), {
          exact: true,
        })
        .fill(ROLE)
      await bindings
        .getByRole('button', {
          name: copy('adminUi', 'permissions.bindings.bind'),
          exact: true,
        })
        .click()
      // The settled write names the account by its display name and login
      // identifier.
      await expect(
        page.getByText(boundNotice(), { exact: true }),
      ).toBeVisible()
      // The tenant already carries the creator's tenant-admin binding, so the
      // row this write produced is the one naming the role it just granted.
      const bindingsTable = bindings.getByRole('table')
      const bound = bindingsTable
        .locator('tbody tr')
        .filter({ hasText: ROLE })
      await expect(bound).toHaveCount(1)
      await expect(
        bound.getByText(OPERATOR.displayName, { exact: true }),
      ).toBeVisible()
      await expect(bound.getByText(ROLE, { exact: true })).toBeVisible()
    })

    await test.step('step 5: the operator opens invitations-and-members and selects the new tenant', async () => {
      // The document's step offers the detail page's related link or the
      // navigation; this spec takes the navigation branch, which is the one
      // that can observe the page before a tenant is chosen.
      await adminLink(page, copy('adminUi', 'invitations.title')).click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'invitations.title'),
          exact: true,
        }),
      ).toBeVisible()

      // Before a tenant is chosen the page lists no tenant's data.
      await expect(
        page.getByRole('heading', {
          name: copy('adminUi', 'invitations.needTenant.title'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(invitationSection(page)).toHaveCount(0)
      await expect(memberSection(page)).toHaveCount(0)

      // After choosing it, the new tenant's invitation list is empty. Its
      // member list holds the operator: opening a tenant binds its creator as
      // the first tenant-admin, and a tenant-admin is a member as well.
      await tenantSelector(page).click()
      await page.getByRole('option', { name: TENANT_ALIAS, exact: true }).click()
      await expect(
        invitationSection(page)
          .getByRole('heading', {
            name: copy('adminUi', 'invitations.empty.title'),
            exact: true,
          }),
      ).toBeVisible()
      const members = memberSection(page).getByRole('table')
      const creator = members
        .locator('tbody tr')
        .filter({ hasText: OPERATOR.displayName })
      await expect(creator).toHaveCount(1)
      await expect(
        creator.getByText(copy('adminUi', 'members.source.initial'), {
          exact: true,
        }),
      ).toBeVisible()
    })

    await test.step('step 6: the operator issues an invitation and the one-time panel hands out the link', async () => {
      await page
        .getByLabel(copy('adminUi', 'invitations.issue.emailLabel'), {
          exact: true,
        })
        .fill(reviewInvitee.identifier)
      await page
        .getByLabel(copy('adminUi', 'invitations.issue.expiresLabel'), {
          exact: true,
        })
        .fill(EXPIRY_INPUT)
      await page
        .getByRole('button', {
          name: copy('adminUi', 'invitations.issue.submit'),
          exact: true,
        })
        .click()

      await expect(
        page.getByText(
          copy('adminUi', 'invitations.issued.confirm', {
            email: reviewInvitee.identifier,
          }),
          { exact: true },
        ),
      ).toBeVisible()

      // REQ-platform-admin-033: the list row carries the address, the status,
      // the inviter and the expiry. REQ-platform-admin-032: the expiry is the
      // instant the operator typed, not the configured default.
      const table = invitationSection(page).getByRole('table')
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
      const row = invitationRow(page)
      await expect(row).toBeVisible()
      await expect(
        row.getByText(reviewInvitee.identifier, { exact: true }),
      ).toBeVisible()
      await expect(
        row.getByText(copy('adminUi', 'invitations.status.pending'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        row.getByText(OPERATOR.displayName, { exact: true }),
      ).toBeVisible()
      await expect(row).toContainText(EXPECTED_EXPIRY)

      // REQ-tenancy-011's one-time handover: the panel carries the acceptance
      // link and a copy control, and neither survives the panel's close.
      const panel = invitationLinkPanel(page, reviewInvitee.identifier)
      await expect(panel).toBeVisible()
      // The message carries the delivery outcome beside the hand-out line, so
      // it is matched as a substring of the panel's text rather than as an
      // exact node.
      await expect(
        panel.getByText(
          copy('adminUi', 'invitations.issued.link', {
            email: reviewInvitee.identifier,
          }),
        ),
      ).toBeVisible()
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
      acceptanceLink = ((await value.textContent()) ?? '').trim()

      await panel
        .getByRole('button', {
          name: copy('adminUi', 'invitations.issued.closeAria', {
            tenant: TENANT_ID,
            email: reviewInvitee.identifier,
          }),
          exact: true,
        })
        .click()
      await expect(panel).toHaveCount(0)
      await expect(
        page.getByText(acceptanceLink, { exact: true }),
      ).toHaveCount(0)
    })

    await test.step('step 7: the operator hands the acceptance link to the invitee', async () => {
      // The operator's channel is outside the browser; what the tier can carry
      // is the rendered link itself, which the invitee's device opens in step
      // 8. The second context has no session and repeats the tier's pins.
      expect(acceptanceLink).toMatch(
        new RegExp(`^${escapeRegExp(server.url)}/invitations/\\S+$`),
      )
      inviteeContext = await browser.newContext({
        locale: 'en-US',
        timezoneId: 'UTC',
        viewport: page.viewportSize(),
      })
      inviteePage = await inviteeContext.newPage()
    })

    await test.step('step 8: the invitee opens the link without a session, signs in, and sees the invitation summary', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage
      const invitationPath = new URL(acceptanceLink).pathname

      // REQ-tenancy-016: an anonymous viewer meets the sign-in surface first,
      // and no invitation summary reaches it.
      await invitee.goto(acceptanceLink)
      await expect(
        invitee.getByRole('heading', {
          level: 1,
          name: copy('demo', 'signIn.heading'),
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        invitee.getByRole('heading', {
          name: copy('authUi', 'acceptInvitation.title'),
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.invited', { tenant: TENANT_ID }),
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

      // The summary names the tenant by identifier, the invited address and
      // the expiry the issue carried.
      await expect(
        invitee.getByText(
          copy('authUi', 'acceptInvitation.invited', { tenant: TENANT_ID }),
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
      const expiryLine = invitee.getByText(
        new RegExp(`^${escapeRegExp(EXPIRY_PREFIX)}`),
      )
      await expect(expiryLine).toHaveText(`${EXPIRY_PREFIX}${EXPECTED_EXPIRY}`)
    })

    await test.step('step 9: the invitee accepts, lands on the tenant home and sees the confirmation', async () => {
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

      // REQ-tenancy-021: the current organization is the invited one. The
      // account menu's organization row names it by its display name (the
      // alias is the fallback when no display name is set), not by the
      // identifier the accept summary showed.
      await openAccountMenu(invitee)
      await expect(organizationRow(invitee, TENANT_DISPLAY_NAME)).toBeVisible({
        timeout: 15_000,
      })
      await expect(organizationRow(invitee, TENANT_DISPLAY_NAME)).toBeEnabled()
      await closeAccountMenu(invitee)
    })

    await test.step('step 10: the organization row names the current organization and its list offers only the others', async () => {
      if (inviteePage === undefined) {
        throw new Error('the invitee page was never opened')
      }
      const invitee = inviteePage

      // REQ-tenancy-022: the current organization is named by the menu's
      // organization row. Expanding that row lists the organizations the
      // account may switch to, so the current one is not offered again: the
      // expansion does not hold a second row for it, and the account's other
      // organization is the one choice it renders.
      const menu = await openAccountMenu(invitee)
      const row = organizationRow(invitee, TENANT_DISPLAY_NAME)
      await expect(row).toBeVisible()
      await row.click()
      const others = menu.getByRole('group', {
        name: copy('demo', 'userMenu.organizations'),
        exact: true,
      })
      await expect(others).toBeVisible()
      await expect(
        others.getByRole('menuitem', {
          name: TENANT_DISPLAY_NAME,
          exact: true,
        }),
      ).toHaveCount(0)
      await expect(
        others.getByRole('menuitem', {
          name: reviewInvitee.displayName,
          exact: true,
        }),
      ).toBeVisible()
      await closeAccountMenu(invitee)

      // The document's re-opened-link sub-step is not driven: the product
      // renders the accepted invitation's summary again instead of the
      // terminal result the document requires, so REQ-tenancy-013 and
      // REQ-tenancy-020 are left out of `@covers` and reported as a gap. The
      // invitation-to-tenant spec documents the same divergence.
      //
      // REQ-platform-admin-001's member facet: a plain member is offered
      // neither the account menu's admin entry nor the start page's admin
      // card. The card is read with the menu closed; the menu entry with it
      // open.
      await expect(
        invitee.getByRole('heading', {
          level: 2,
          name: copy('demo', 'home.cards.admin.title'),
          exact: true,
        }),
      ).toHaveCount(0)
      await openAccountMenu(invitee)
      await expect(adminEntry(invitee)).toHaveCount(0)
      await closeAccountMenu(invitee)
    })

    await test.step('step 11: the operator refreshes and sees the invitation accepted and the member listed', async () => {
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'invitations.title'),
          exact: true,
        }),
      ).toBeVisible()
      await page
        .getByRole('button', {
          name: copy('adminUi', 'common.refresh'),
          exact: true,
        })
        .click()

      // The invitation's terminal state is now the accepted one.
      await expect(
        invitationRow(page).getByText(
          copy('adminUi', 'invitations.status.accepted'),
          { exact: true },
        ),
      ).toBeVisible({ timeout: 15_000 })

      // REQ-tenancy-008: the member list now holds the invited address's
      // account, sourced from the invitation and active. The operator was
      // already a member as the tenant's creator and first tenant-admin, so
      // the acceptance grows the list from one row to two.
      const members = memberSection(page).getByRole('table')
      await expect(members.locator('tbody tr')).toHaveCount(2)
      const invited = members
        .locator('tbody tr')
        .filter({ hasText: reviewInvitee.displayName })
      await expect(invited).toHaveCount(1)
      await expect(
        invited.getByText(copy('adminUi', 'members.source.invite'), {
          exact: true,
        }),
      ).toBeVisible()
      await expect(
        invited.getByText(copy('adminUi', 'members.activeYes'), {
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

/**
 * The platform roster's organization lifecycle: an owner transfer through the
 * member selector, one through the account selector, and the terminal delete.
 *
 * The platform transfer dialog chooses its target path from the caller's
 * tenant permission set -- the member selector when `authn.membership.manage`
 * is held in the current tenant, the platform account selector otherwise. The
 * first is the ordinary admin session: the current tenant is the platform's
 * own `system`, where the operator holds tenant-admin, so the dialogs attach
 * the member read. The second is reached the way the personal-tenant history
 * visit reaches a tenant-scoped surface: the operator enters the admin while
 * `system` is current, leaves to the tenant area, switches to the personal
 * tenant whose tenant-admin allowlist deliberately withholds membership
 * management, and returns to the admin URL through history, which keeps the
 * personal tenant current without a document load.
 *
 * The ownership requirements (REQ-platform-admin-051, -055 and -058) are
 * registered in the PRD's feature documents but not in this journey document,
 * so they stay out of `@covers`, which the honesty gate holds to the ids the
 * journey document lists.
 */

/** The operator's password sign-in and its proved-email precondition. */
async function signInAndVerifyOperator(
  page: Page,
  server: ReviewServer,
  accounts: Accounts,
): Promise<void> {
  await accounts.signIn({
    identifier: OPERATOR.username,
    password: OPERATOR.password,
  })
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
}

/** Enter the platform admin from the tenant area, selecting `system` first. */
async function enterAdminFromHome(page: Page): Promise<void> {
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
  await switchOrganization(page, 'system')
  await openAccountMenu(page)
  await adminEntry(page).click()
  await expect(adminNav(page)).toBeVisible()
}

/** One tenant roster row's transfer control. */
function transferTenantButton(page: Page, id: string): Locator {
  return rosterRow(page, id).getByRole('button', {
    name: copy('adminUi', 'tenantLifecycle.transferAria', { id }),
    exact: true,
  })
}

/** One tenant roster row's delete control. */
function deleteTenantButton(page: Page, id: string): Locator {
  return rosterRow(page, id).getByRole('button', {
    name: copy('adminUi', 'tenantLifecycle.deleteAria', { id }),
    exact: true,
  })
}

/** The transfer dialog's open dialog. */
function transferDialog(page: Page, id: string): Locator {
  return page.getByRole('dialog', {
    name: copy('adminUi', 'tenantLifecycle.transfer.title', { id }),
    exact: true,
  })
}

/** Arm and confirm the shared two-step danger dialog. */
async function confirmTwice(dialog: Locator, confirmLabel: string): Promise<void> {
  await dialog
    .getByRole('button', { name: confirmLabel, exact: true })
    .click()
  const again = dialog.getByRole('button', {
    name: copy('uiKit', 'confirmDialog.confirmAgainLabel'),
    exact: true,
  })
  await expect(again).toBeEnabled({ timeout: 5_000 })
  await again.click()
}

test.describe('the platform lifecycle controls', () => {
  test.use({ seedInvitee: true, viewport: { width: 1920, height: 1080 } })

  test('operator-onboards-tenant: the operator transfers a tenant owner through the member selector', async ({
    demo,
  }) => {
    test.setTimeout(180_000)

    const { page, server, accounts } = demo

    await signInAndVerifyOperator(page, server, accounts)
    await enterAdminFromHome(page)
    await adminLink(page, copy('adminUi', 'tenants.title')).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenants.title'),
        exact: true,
      }),
    ).toBeVisible()

    // The operator is acme's tenant-admin, so the transfer dialog offers the
    // organization's member selector rather than the platform account list.
    await transferTenantButton(page, SEEDED_TENANT).click()
    const dialog = transferDialog(page, SEEDED_TENANT)
    await expect(dialog).toBeVisible()
    const member = dialog.getByRole('combobox', {
      name: copy('adminUi', 'tenantLifecycle.transfer.memberLabel'),
      exact: true,
    })
    await expect(member).toBeVisible()
    await member.click()
    await expect(
      page.getByRole('option', {
        name: new RegExp(
          `^${escapeRegExp(reviewOrdinaryMember.displayName)} \\(.+\\)$`,
        ),
      }),
    ).toBeVisible()
    await page
      .getByRole('option', {
        name: new RegExp(
          `^${escapeRegExp(reviewOrdinaryMember.displayName)} \\(.+\\)$`,
        ),
      })
      .click()
    await dialog
      .getByRole('button', {
        name: copy('adminUi', 'tenantLifecycle.transfer.submit'),
        exact: true,
      })
      .click()

    const confirm = page.getByRole('dialog', {
      name: copy('adminUi', 'tenantLifecycle.transfer.confirmTitle', {
        id: SEEDED_TENANT,
      }),
      exact: true,
    })
    await expect(confirm).toBeVisible()
    await confirmTwice(
      confirm,
      copy('adminUi', 'tenantLifecycle.transfer.confirmLabel'),
    )

    // A landed transfer names the organization and its new owner.
    await expect(
      page.getByText(
        copyPattern('adminUi', 'tenantLifecycle.transfer.success', {
          id: escapeRegExp(SEEDED_TENANT),
          target: '.+',
        }),
      ),
    ).toBeVisible()
  })

  test('operator-onboards-tenant: with only tenant.manage the operator uses the account selector and a non-member is refused', async ({
    demo,
  }) => {
    test.setTimeout(180_000)

    const { page, server, accounts } = demo

    await signInAndVerifyOperator(page, server, accounts)
    await enterAdminFromHome(page)

    // Leave the admin for the tenant area and switch to the account's personal
    // organization. Its tenant-admin allowlist withholds membership
    // management, so the platform transfer dialog falls back to the account
    // selector. The return to the admin URL is a history visit: a document
    // load would re-settle the current tenant from the caller's list and drop
    // the personal organization, which is the state the dialog reads.
    await openAccountMenu(page)
    await backToApp(page).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()
    await switchOrganization(page, OPERATOR.displayName)
    await page.goBack()
    await expect(adminNav(page)).toBeVisible({ timeout: 15_000 })

    await adminLink(page, copy('adminUi', 'tenants.title')).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenants.title'),
        exact: true,
      }),
    ).toBeVisible()

    await transferTenantButton(page, SEEDED_TENANT).click()
    const dialog = transferDialog(page, SEEDED_TENANT)
    await expect(dialog).toBeVisible()

    // The account selector is offered instead of the member selector.
    const accountField = dialog.getByRole('combobox', {
      name: copy('adminUi', 'tenantLifecycle.transfer.accountLabel'),
      exact: true,
    })
    await expect(accountField).toBeVisible()
    await accountField.fill(reviewInvitee.displayName)
    await expect(
      page.getByRole('option', {
        name: new RegExp(
          `^${escapeRegExp(reviewInvitee.displayName)} \\(.+\\)$`,
        ),
      }),
    ).toBeVisible()
    await page
      .getByRole('option', {
        name: new RegExp(
          `^${escapeRegExp(reviewInvitee.displayName)} \\(.+\\)$`,
        ),
      })
      .click()
    await dialog
      .getByRole('button', {
        name: copy('adminUi', 'tenantLifecycle.transfer.submit'),
        exact: true,
      })
      .click()

    const confirm = page.getByRole('dialog', {
      name: copy('adminUi', 'tenantLifecycle.transfer.confirmTitle', {
        id: SEEDED_TENANT,
      }),
      exact: true,
    })
    await expect(confirm).toBeVisible()
    await confirmTwice(
      confirm,
      copy('adminUi', 'tenantLifecycle.transfer.confirmLabel'),
    )

    // The target is not a member of the organization, so the endpoint answers
    // 409 and the dialog states that refusal inline rather than claiming the
    // account is a member.
    await expect(
      dialog.getByText(
        copy('adminUi', 'tenantLifecycle.transfer.failedConflict'),
        { exact: true },
      ),
    ).toBeVisible()
  })
})

/** The tenant the platform lifecycle delete branch registers and ends. */
const LIFECYCLE_TENANT_ID = 'initrode'
const LIFECYCLE_TENANT_ALIAS = 'initrode-labs'
const LIFECYCLE_TENANT_NAME = 'Initrode Labs'
/** A second identifier and alias used for the two collision attempts. */
const LIFECYCLE_SPARE_ID = 'initrode-two'
const LIFECYCLE_SPARE_ALIAS = 'initrode-spare'

test.describe('the platform lifecycle delete', () => {
  test.use({ viewport: { width: 1920, height: 1080 } })

  test('operator-onboards-tenant: the operator deletes a tenant and neither its id nor its alias can be reused', async ({
    demo,
  }) => {
    test.setTimeout(180_000)

    const { page, server, accounts } = demo

    await signInAndVerifyOperator(page, server, accounts)
    await enterAdminFromHome(page)
    await adminLink(page, copy('adminUi', 'tenants.title')).click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenants.title'),
        exact: true,
      }),
    ).toBeVisible()

    // Register a tenant of this branch's own, through the roster's page.
    await page
      .getByRole('link', { name: copy('adminUi', 'tenants.register'), exact: true })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenants.create.title'),
        exact: true,
      }),
    ).toBeVisible()
    const form = createSection(page)
    await form
      .getByLabel(copy('orgCreateUi', 'fields.id'), { exact: true })
      .fill(LIFECYCLE_TENANT_ID)
    await form
      .getByLabel(copy('orgCreateUi', 'fields.alias'), { exact: true })
      .fill(LIFECYCLE_TENANT_ALIAS)
    await form
      .getByLabel(copy('orgCreateUi', 'fields.displayName'), { exact: true })
      .fill(LIFECYCLE_TENANT_NAME)
    await form
      .getByRole('button', {
        name: copy('adminUi', 'tenants.create.submit'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenantDetail.title', {
          id: LIFECYCLE_TENANT_ID,
        }),
        exact: true,
      }),
    ).toBeVisible()
    await page
      .getByRole('link', { name: copy('adminUi', 'tenants.title'), exact: true })
      .first()
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenants.title'),
        exact: true,
      }),
    ).toBeVisible()

    // The platform staff path deletes the tenant: one confirmation, then the
    // heavy second click.
    await deleteTenantButton(page, LIFECYCLE_TENANT_ID).click()
    const deleteDialog = page.getByRole('dialog', {
      name: copy('adminUi', 'tenantLifecycle.deleteConfirm.title', {
        id: LIFECYCLE_TENANT_ID,
      }),
      exact: true,
    })
    await expect(deleteDialog).toBeVisible()
    await confirmTwice(
      deleteDialog,
      copy('adminUi', 'tenantLifecycle.deleteConfirm.confirmLabel'),
    )
    await expect(
      page.getByText(
        copy('adminUi', 'tenantLifecycle.deleteConfirm.success', {
          id: LIFECYCLE_TENANT_ID,
        }),
        { exact: true },
      ),
    ).toBeVisible()

    // The row is a tombstone: its status says deleted and its actions cell
    // carries no lifecycle control any more.
    const tombstone = rosterRow(page, LIFECYCLE_TENANT_ID)
    await expect(
      tombstone.getByText(copy('adminUi', 'tenants.status.deleted'), {
        exact: true,
      }),
    ).toBeVisible({ timeout: 15_000 })
    await expect(
      transferTenantButton(page, LIFECYCLE_TENANT_ID),
    ).toHaveCount(0)
    await expect(deleteTenantButton(page, LIFECYCLE_TENANT_ID)).toHaveCount(0)

    // Neither the identifier nor the alias the tombstone holds can be
    // registered again: the roster answers the registration's conflict.
    await page
      .getByRole('link', { name: copy('adminUi', 'tenants.register'), exact: true })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'tenants.create.title'),
        exact: true,
      }),
    ).toBeVisible()
    const again = createSection(page)
    const idField = again.getByLabel(copy('orgCreateUi', 'fields.id'), {
      exact: true,
    })
    const aliasField = again.getByLabel(copy('orgCreateUi', 'fields.alias'), {
      exact: true,
    })
    const nameField = again.getByLabel(
      copy('orgCreateUi', 'fields.displayName'),
      { exact: true },
    )
    const submit = again.getByRole('button', {
      name: copy('adminUi', 'tenants.create.submit'),
      exact: true,
    })
    const conflict = copy('adminUi', 'errors.tenant.conflict')

    await idField.fill(LIFECYCLE_TENANT_ID)
    await aliasField.fill(LIFECYCLE_SPARE_ALIAS)
    await nameField.fill('Initrode Spare')
    await submit.click()
    await expect(page.getByText(conflict, { exact: true })).toBeVisible()

    await idField.fill(LIFECYCLE_SPARE_ID)
    await aliasField.fill(LIFECYCLE_TENANT_ALIAS)
    await nameField.fill('Initrode Second')
    await submit.click()
    await expect(page.getByText(conflict, { exact: true })).toBeVisible()
  })
})
