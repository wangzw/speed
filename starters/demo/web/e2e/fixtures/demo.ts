import {
  test as base,
  expect,
  type Page,
  type TestInfo,
} from '@playwright/test'

import { createAccounts, type Accounts } from '../helpers/accounts'
import { startE2EServer, type E2EServer } from './server'

export interface DemoTarget {
  readonly server: E2EServer
  readonly page: Page
  readonly accounts: Accounts
}

function unexpected(testInfo: TestInfo): boolean {
  return testInfo.status !== testInfo.expectedStatus
}

async function attachFailure(
  testInfo: TestInfo,
  server: E2EServer,
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
  server: E2EServer
  demo: DemoTarget
  seedMember: boolean
  seedImpersonation: boolean
  seedInvitee: boolean
  invitationMail: boolean
  seedSocialAccount: boolean
  socialIdentityProvider: boolean
  enterpriseSSO: boolean
  samlIdentityProvider: boolean
  tenantSSO: boolean
  seedTenantAdmin: boolean
  seedUnprovedTenantAdmin: boolean
  seedSystemTenantAdmin: boolean
  platformRequireMFA: boolean
  tenantRequireMFA: boolean
  tenantAdmins: Readonly<Record<string, readonly string[]>> | undefined
  recentWindow: string | undefined
  mfaRequired: boolean
  identityQuota: string | undefined
}>({
  seedMember: [false, { option: true }],
  seedImpersonation: [false, { option: true }],
  seedInvitee: [false, { option: true }],
  invitationMail: [true, { option: true }],
  seedSocialAccount: [false, { option: true }],
  socialIdentityProvider: [false, { option: true }],
  enterpriseSSO: [false, { option: true }],
  samlIdentityProvider: [false, { option: true }],
  tenantSSO: [false, { option: true }],
  seedTenantAdmin: [false, { option: true }],
  seedUnprovedTenantAdmin: [false, { option: true }],
  seedSystemTenantAdmin: [false, { option: true }],
  platformRequireMFA: [false, { option: true }],
  tenantRequireMFA: [false, { option: true }],
  tenantAdmins: [undefined, { option: true }],
  recentWindow: [undefined, { option: true }],
  mfaRequired: [false, { option: true }],
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
      samlIdentityProvider,
      tenantSSO,
      seedTenantAdmin,
      seedUnprovedTenantAdmin,
      seedSystemTenantAdmin,
      platformRequireMFA,
      tenantRequireMFA,
      tenantAdmins,
      recentWindow,
      mfaRequired,
      identityQuota,
    },
    use,
    testInfo,
  ) => {
    const server = await startE2EServer({
      seedMember,
      seedImpersonation,
      seedInvitee,
      invitationMail,
      seedSocialAccount,
      socialIdentityProvider,
      enterpriseSSO,
      samlIdentityProvider,
      tenantSSO,
      seedTenantAdmin,
      seedUnprovedTenantAdmin,
      seedSystemTenantAdmin,
      platformRequireMFA,
      tenantRequireMFA,
      tenantAdmins,
      recentWindow,
      mfaRequired,
      identityQuota,
    })
    try {
      await use(server)
    } finally {
      const failed = unexpected(testInfo)
      // Attaching evidence is best effort; stopping the server and removing its temporary directory is not.
      try {
        if (failed) await attachFailure(testInfo, server)
      } finally {
        await server.stop({ keep: failed })
      }
    }
  },
  demo: async ({ server, page }, use) => {
    // Close the browser context before SIGTERM so the notification stream's connection goes away first.
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
