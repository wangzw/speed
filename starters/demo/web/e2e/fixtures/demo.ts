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
   * Seed the member-workspace journey's account and membership. A spec sets
   * `test.use({ seedMember: true })` when it signs that member in; every other
   * spec starts from the template's own seed.
   */
  seedMember: boolean
}>({
  seedMember: [false, { option: true }],
  server: async ({ seedMember }, use, testInfo) => {
    const server = await startReviewServer({ seedMember })
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
