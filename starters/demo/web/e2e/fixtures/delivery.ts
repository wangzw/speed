/**
 * The delivery ledger, read from the review server's own sqlite file.
 *
 * notification.delivery records one row per channel attempt in
 * `notification_sends`. The table is the module's own and no product surface
 * reads it: the design calls it the operations view of what went out, not a
 * contract face. A browser tier therefore has no rendered surface to observe
 * the channel's final state through, and the review assembly writes no log
 * line for a refusal that never reached a carrier. This module is the harness's
 * sanctioned read of that ledger, and the one place a journey learns a channel's
 * recorded status and failure reason.
 *
 * The connection is read-only and opened against the file the running server
 * wrote; the fixture never writes a row. The cursor is the table's rowid, which
 * is monotonic, so a wait that starts after an action cannot match a row an
 * earlier action wrote even when the two share a millisecond.
 */
import { DatabaseSync } from 'node:sqlite'

/** One channel's recorded terminal state. */
export interface DeliveryRecord {
  /** The row's position, the cursor a later wait continues from. */
  readonly rowID: number
  /** succeeded, failed or skipped. */
  readonly status: string
  /** The producer's reason for a failed attempt; null when it recorded none. */
  readonly error: string | null
  /** The carrier's receipt identifier; null when the send never reached one. */
  readonly providerReceiptID: string | null
}

/** Which channel's ledger a read names. */
export interface DeliveryFilter {
  readonly typeKey: string
  readonly channel: string
}

/** Open the ledger read-only, with a timeout long enough to ride a write. */
function open(dsn: string): DatabaseSync {
  const database = new DatabaseSync(dsn, { readOnly: true, timeout: 5_000 })
  database.exec('PRAGMA busy_timeout = 5000')
  return database
}

/** Every row a filter names after a cursor, oldest first. */
function rowsAfter(
  dsn: string,
  filter: DeliveryFilter,
  after: number,
): DeliveryRecord[] {
  const database = open(dsn)
  try {
    const rows = database
      .prepare(
        'SELECT rowid AS rowID, status, error, provider_receipt_id AS providerReceiptID ' +
          'FROM notification_sends WHERE type_key = ? AND channel = ? AND rowid > ? ' +
          'ORDER BY rowid ASC',
      )
      .all(filter.typeKey, filter.channel, after) as Array<{
      rowID: number
      status: string
      error: string | null
      providerReceiptID: string | null
    }>
    return rows.map((row) => ({
      rowID: row.rowID,
      status: row.status,
      error: row.error,
      providerReceiptID: row.providerReceiptID,
    }))
  } finally {
    database.close()
  }
}

/**
 * The rowid of the newest row a filter names, or zero when it names none. A
 * caller takes it before an action and passes it as `after`, so a wait sees
 * only the rows that action wrote.
 */
export function deliveryMark(dsn: string, filter: DeliveryFilter): number {
  const rows = rowsAfter(dsn, filter, 0)
  const last = rows[rows.length - 1]
  return last === undefined ? 0 : last.rowID
}

/**
 * Wait for a row the filter names, after `after`, whose status is `status` when
 * one is given. The poll is a read of the running server's file; it never
 * writes. A refusal that never reached a carrier still records a row, so this is
 * where such an attempt's reason is observed.
 */
export async function waitForDeliveryRecord(
  dsn: string,
  options: DeliveryFilter & {
    readonly status?: string
    readonly after?: number
    readonly timeoutMs?: number
  },
): Promise<DeliveryRecord> {
  const after = Math.max(0, options.after ?? 0)
  const deadline = Date.now() + (options.timeoutMs ?? 30_000)
  let seen: DeliveryRecord[] = []
  let lastError: unknown
  for (;;) {
    try {
      seen = rowsAfter(dsn, options, after)
      lastError = undefined
      const match = seen.find(
        (row) => options.status === undefined || row.status === options.status,
      )
      if (match !== undefined) return match
    } catch (error) {
      // A concurrent write can hold the file for a moment; the next poll
      // retries it. A database that has gone away is reported at the deadline.
      lastError = error
    }
    if (Date.now() >= deadline) {
      const cause = lastError === undefined ? '' : `\nlast error: ${String(lastError)}`
      throw new Error(
        `timed out waiting for a ${options.status ?? 'recorded'} ${options.channel} ` +
          `delivery row for ${options.typeKey} after rowid ${after}` +
          (seen.length === 0 ? '' : `\n--- rows seen ---\n${JSON.stringify(seen)}`) +
          cause,
      )
    }
    await sleep(100)
  }
}

/** Resolve after a bounded pause. */
function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}
