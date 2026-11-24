import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { NOTIFICATION_INTERVAL_MS } from './notification-fixture'
import { SERVER_DIR } from './paths'

describe('NOTIFICATION_INTERVAL_MS', () => {
  it("matches the Go fixture's declared dispatch window", () => {
    const source = readFileSync(
      join(SERVER_DIR, 'notification_fixture.go'),
      'utf8',
    )
    const declared = /const demoNotificationInterval = (\d+) \* time\.Second\b/.exec(
      source,
    )
    if (declared === null) {
      throw new Error(
        'notification_fixture.go no longer declares demoNotificationInterval as "<n> * time.Second"; ' +
          'update this test and the copy together',
      )
    }
    expect(NOTIFICATION_INTERVAL_MS).toBe(Number(declared[1]) * 1000)
  })
})
