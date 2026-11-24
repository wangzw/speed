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
      // Attaching evidence is best effort; stopping the server and removing its temporary directory is not.
      try {
        if (failed) await attachFailure(testInfo, server)
      } finally {
        await server.stop({ keep: failed })
      }
    }
  },
  deploy: async ({ deployServer, page }, use) => {
    // Close the browser context before SIGTERM so the notification stream's connection goes away first.
    deployServer.beforeStop(async () => {
      await page.context().close().catch(() => undefined)
    })
    await use({ server: deployServer, page })
  },
})

export { expect }
