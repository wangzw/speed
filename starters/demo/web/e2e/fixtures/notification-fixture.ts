/**
 * The notification fixture's dispatch window, in milliseconds.
 *
 * `starters/demo/server/notification_fixture.go` declares it once as
 * `demoNotificationInterval`, and a TypeScript fixture cannot import a Go
 * constant. This copy is held to the Go source by notification-fixture.test.ts:
 * the rollout journey sizes its waits to several windows and its reverse
 * assertion watches two, so a drifted copy would wait against a schedule the
 * fixture no longer runs.
 */
export const NOTIFICATION_INTERVAL_MS = 10_000
