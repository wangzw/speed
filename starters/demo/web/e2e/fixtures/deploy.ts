/**
 * The Playwright test the deploy journey is written against.
 *
 * The deployment is not the review assembly: this test gets the default binary
 * -- every capability compiled in, no review controls -- on a temporary volume,
 * seeded and served by fixtures/deploy-server.ts.
 * The built-in page fixture is created after the server (the deploy fixture
 * names it second), so teardown closes the browser context before SIGTERM and
 * the process can drain.
 *
 * A failed test keeps the volume and attaches the process log, so the rendered
 * configuration, the sqlite file and the first-run signing key can be read
 * without rerunning it. A passing test removes the whole temporary directory.
 */
import {
  test as base,
  expect,
  type Page,
  type TestInfo,
} from '@playwright/test'

import { startDeployServer, type DeployServer } from './deploy-server'

export interface DeployTarget {
  readonly server: DeployServer
  readonly page: Page
}

function unexpected(testInfo: TestInfo): boolean {
  return testInfo.status !== testInfo.expectedStatus
}

async function attachFailure(
  testInfo: TestInfo,
  server: DeployServer,
): Promise<void> {
  await testInfo.attach('deploy.log', {
    body: server.log.text,
    contentType: 'text/plain',
  })
  if (server.stderr !== '') {
    await testInfo.attach('deploy.stderr', {
      body: server.stderr,
      contentType: 'text/plain',
    })
  }
  await testInfo.attach('deploy.seed', {
    body: server.seedOutput,
    contentType: 'text/plain',
  })
  await testInfo.attach('deploy.volume', {
    body: server.volume,
    contentType: 'text/plain',
  })
}

export const test = base.extend<{
  deployServer: DeployServer
  deploy: DeployTarget
}>({
  deployServer: async ({}, use, testInfo) => {
    const server = await startDeployServer()
    try {
      await use(server)
    } finally {
      const failed = unexpected(testInfo)
      // Attaching the evidence is best effort; stopping the server and
      // removing its temporary directory is not.
      try {
        if (failed) await attachFailure(testInfo, server)
      } finally {
        await server.stop({ keep: failed })
      }
    }
  },
  deploy: async ({ deployServer, page }, use) => {
    // Closing the context before SIGTERM is what lets the notification stream
    // go away first; the hook runs ahead of the signal in server.stop.
    deployServer.beforeStop(async () => {
      await page.context().close().catch(() => undefined)
    })
    await use({ server: deployServer, page })
  },
})

export { expect }
