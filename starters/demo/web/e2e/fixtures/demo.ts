/**
 * The Playwright test the journeys are written against.
 *
 * Each test gets one review server of its own and one browser page. The server
 * fixture is set up before the page (the demo fixture names it first), and its
 * first shutdown hook closes the browser context: the notification stream keeps
 * a connection open, and a context still holding it would keep serve from
 * draining on SIGTERM.
 *
 * A failed test keeps its server directory and attaches the process log, so the
 * failure can be read without rerunning it. A passing test removes both.
 */
import {
  test as base,
  expect,
  type Page,
  type TestInfo,
} from '@playwright/test'

import { createAccounts, type Accounts } from '../helpers/accounts'
import { startReviewServer, type ReviewServer } from './server'

export interface DemoTarget {
  readonly server: ReviewServer
  readonly page: Page
  readonly accounts: Accounts
}

function unexpected(testInfo: TestInfo): boolean {
  return testInfo.status !== testInfo.expectedStatus
}

async function attachFailure(
  testInfo: TestInfo,
  server: ReviewServer,
): Promise<void> {
  await testInfo.attach('server.log', {
    body: server.log.text,
    contentType: 'text/plain',
  })
  if (server.stderr !== '') {
    await testInfo.attach('server.stderr', {
      body: server.stderr,
      contentType: 'text/plain',
    })
  }
  await testInfo.attach('server.dir', {
    body: server.dir,
    contentType: 'text/plain',
  })
}

export const test = base.extend<{
  server: ReviewServer
  demo: DemoTarget
  /**
   * Seed the member-organization journey's account and membership. A spec sets
   * `test.use({ seedMember: true })` when it signs that member in; every other
   * spec starts from the template's own seed.
   */
  seedMember: boolean
  /**
   * Seed the impersonation journey's second factors on the operator. A spec sets
   * `test.use({ seedImpersonation: true })` when it impersonates; every other
   * spec keeps the operator signing in with one factor.
   */
  seedImpersonation: boolean
  /**
   * Seed the invitation journey's invitee account (an email login name with no
   * membership). A spec sets `test.use({ seedInvitee: true })` when it invites
   * and signs that account in; every other spec starts from the template's own
   * seed.
   */
  seedInvitee: boolean
  /**
   * Send invitation notices through the review mail carrier. A journey that
   * has to open the acceptance link itself turns it off, so the issue answer
   * carries the token (`not_configured`): the process log masks an `spd_inv_`
   * token, and a sent notice withholds the token from the answer. Every other
   * spec keeps the template's carrier.
   */
  invitationMail: boolean
  /**
   * Seed the social journey's account that already holds the identity
   * provider's address. A spec sets `test.use({ seedSocialAccount: true })` for
   * the branch where the provider's verified address belongs to an account;
   * every other spec starts from the template's own seed.
   */
  seedSocialAccount: boolean
  /**
   * Start the local OpenID Connect provider the social methods are pointed at.
   * A spec that drives a social method sets `test.use({ socialIdentityProvider:
   * true })`; every other spec's server runs without openssl and without the
   * provider's two extra loopback ports.
   */
  socialIdentityProvider: boolean
  /**
   * Start the enterprise journey's fixtures: a second OpenID Connect provider
   * and the UDP DNS stub that answers the test domain's TXT record. A spec that
   * drives the enterprise journey sets `test.use({ enterpriseSSO: true })`;
   * every other spec's server keeps both `authn.sso` seams empty, which is the
   * deployment default. This is separate from `socialIdentityProvider`.
   */
  enterpriseSSO: boolean
  /**
   * Start the enterprise fixtures and seed the tenant-SSO journey's state: the
   * enterprise user as the target tenant's administrator and that tenant's own
   * `tenant.admission.require-sso` switched on. A spec that drives the tenant
   * SSO re-authentication sets `test.use({ tenantSSO: true })`; every other
   * spec starts from the template's own seed and without the requirement.
   */
  tenantSSO: boolean
  /**
   * Seed the tenant-admin journey's proved-email business administrator and
   * make it acme's only tenant administrator. A spec sets
   * `test.use({ seedTenantAdmin: true })` when it drives the organization admin
   * surface; every other spec starts from the template's own seed.
   */
  seedTenantAdmin: boolean
  /**
   * Seed the tenant-admin journey's unproved-email administrator. A spec that
   * drives the refused-write branch sets it; every other spec starts without
   * that account.
   */
  seedUnprovedTenantAdmin: boolean
  /**
   * Seed a system-tenant administrator outside the platform role, so a spec can
   * prove the system tenant is not manageable through the organization admin
   * surface.
   */
  seedSystemTenantAdmin: boolean
  /**
   * Seed the review build's protected business tenant and its proved
   * administrator, so a spec can prove the settings surface's read-only state.
   */
  seedProtectedTenant: boolean
  /**
   * Write the admission policy `tenant.admission.require-mfa` into the global
   * layer, so a spec observes the platform-enforced, read-only switch.
   */
  platformRequireMFA: boolean
  /**
   * Hold the tenant-layer admission policy `tenant.admission.require-mfa` on
   * for the seeded tenant, so a member of that tenant who has no second factor
   * must enrol one at login. Every other spec keeps the template's own seed.
   */
  tenantRequireMFA: boolean
  /**
   * Replace the administrators of the named tenants. A spec that needs a tenant
   * with exactly one tenant-admin binding sets this; every other spec keeps the
   * default administrator list.
   */
  tenantAdmins: Readonly<Record<string, readonly string[]>> | undefined
  /**
   * Lower the account self-service freshness window for this spec's server.
   * A journey that observes the window passing sets it to the setting's
   * domain floor; every other spec keeps the template's declared window.
   */
  recentWindow: string | undefined
  /**
   * Require every account to register a second factor. A journey whose session
   * must post-date the factor it enrolled sets this, so the login flow
   * registers the factor before the session exists; every other spec keeps the
   * template's declared switch.
   */
  mfaRequired: boolean
  /**
   * Replace the scheduler tick for this spec's server. A journey whose only
   * sends are its own control clicks turns the periodic trigger off with `0s`;
   * every other spec keeps the template's two-second tick.
   */
  schedulerTick: string | undefined
  /**
   * Raise `ratelimit.limit` for this spec's server. A journey whose steps make
   * more than the declared hundred requests from one identity inside one
   * counting window sets a higher positive integer; every other spec keeps the
   * declared default, which the settings surface then still reports as the
   * declaration.
   */
  identityQuota: string | undefined
}>({
  seedMember: [false, { option: true }],
  seedImpersonation: [false, { option: true }],
  seedInvitee: [false, { option: true }],
  invitationMail: [true, { option: true }],
  seedSocialAccount: [false, { option: true }],
  socialIdentityProvider: [false, { option: true }],
  enterpriseSSO: [false, { option: true }],
  tenantSSO: [false, { option: true }],
  seedTenantAdmin: [false, { option: true }],
  seedUnprovedTenantAdmin: [false, { option: true }],
  seedSystemTenantAdmin: [false, { option: true }],
  seedProtectedTenant: [false, { option: true }],
  platformRequireMFA: [false, { option: true }],
  tenantRequireMFA: [false, { option: true }],
  tenantAdmins: [undefined, { option: true }],
  recentWindow: [undefined, { option: true }],
  mfaRequired: [false, { option: true }],
  schedulerTick: [undefined, { option: true }],
  identityQuota: [undefined, { option: true }],
  server: async (
    {
      seedMember,
      seedImpersonation,
      seedInvitee,
      invitationMail,
      seedSocialAccount,
      socialIdentityProvider,
      enterpriseSSO,
      tenantSSO,
      seedTenantAdmin,
      seedUnprovedTenantAdmin,
      seedSystemTenantAdmin,
      seedProtectedTenant,
      platformRequireMFA,
      tenantRequireMFA,
      tenantAdmins,
      recentWindow,
      mfaRequired,
      schedulerTick,
      identityQuota,
    },
    use,
    testInfo,
  ) => {
    const server = await startReviewServer({
      seedMember,
      seedImpersonation,
      seedInvitee,
      invitationMail,
      seedSocialAccount,
      socialIdentityProvider,
      enterpriseSSO,
      tenantSSO,
      seedTenantAdmin,
      seedUnprovedTenantAdmin,
      seedSystemTenantAdmin,
      seedProtectedTenant,
      platformRequireMFA,
      tenantRequireMFA,
      tenantAdmins,
      recentWindow,
      mfaRequired,
      schedulerTick,
      identityQuota,
    })
    try {
      await use(server)
    } finally {
      const failed = unexpected(testInfo)
      // Attaching the evidence is best effort; stopping the server and
      // removing its temporary directory is not. A reporting failure must
      // not leak the child process and its sqlite file.
      try {
        if (failed) await attachFailure(testInfo, server)
      } finally {
        await server.stop({ keep: failed })
      }
    }
  },
  demo: async ({ server, page }, use) => {
    // Closing the context before SIGTERM is what lets the notification stream
    // go away first; the hook runs ahead of the signal in server.stop.
    server.beforeStop(async () => {
      await page.context().close().catch(() => undefined)
    })
    await use({
      server,
      page,
      accounts: createAccounts({ page, server }),
    })
  },
})

export { expect }
