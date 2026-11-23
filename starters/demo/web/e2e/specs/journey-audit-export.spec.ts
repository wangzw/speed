/**
 * An operator changes one setting often enough to fill more than one audit
 * page, is refused once, and exports the matching records from a page that is
 * not the first.
 *
 * The seed writes the platform administrator with an unproved email address,
 * and a settings write from an unproved account is refused, so the spec first
 * reaches the journey's precondition the way a person does: the standing
 * notice's verify entry, the code from the server's process log (the one
 * non-UI read this spec makes), and the verify action.
 *
 * PAGE SIZE. The spec must know how many saves exceed one audit page, and the
 * size belongs to the product, not to the test. The audit page asks its list
 * read for a page of its own size, so the spec listens to the page's own
 * request (a passive observation -- it neither answers nor rewrites it) and
 * takes the `limit` the rendered page asked for. The same number then decides
 * how many saves to make and is the upper bound of a rendered page; no page
 * size is written into an assertion.
 *
 * STEP 6 AND THE MINUTE BOUNDARY. The audit filter's two time fields are
 * `datetime-local` inputs, whose value carries whole minutes. The second
 * export must cover only the first few saves, so those saves have to lie in an
 * earlier minute than the rest. The spec makes the first few saves, then waits
 * for the next minute start, and only then makes the remaining saves and the
 * refused write. The wait is the product's own clock boundary, not a guess
 * about how long a click takes: every first-phase record is earlier than the
 * boundary and every later record is later than it. The second window's upper
 * bound is that boundary.
 *
 * WHAT THE TWO FILES PROVE. The list is paged by cursor and the export is not
 * a page: the spec walks every page the list offers, keeps each row's record
 * id (from the row's own detail link) together with the action, object,
 * outcome, the raw actor the list read answers for that id, and the time it
 * renders, and then requires the exported NDJSON lines to be the same set of
 * ids -- same count, same action, same object, same outcome and, per id, the
 * same raw actor the read answered -- and the second file to be exactly the
 * narrowed list's set and a strict subset of the first. Both sequences must
 * read newest first, and the downloaded name must name the export instant
 * inside the window around the click. Reading the downloaded file is a named
 * read of the file the browser saved; the spec writes nothing and opens no
 * other channel.
 *
 * THE ROW NAMES THE ACTOR, THE FILE KEEPS ITS KEY. The audit list renders an
 * actor as the account's display name and login username, never as the opaque
 * account id; the export is data and keeps the id. The raw actor is therefore
 * learned from the list response the page itself received, and each row is
 * also asserted not to render it.
 *
 * DISCLOSURE. The second half of REQ-platform-admin-112 -- a caller without
 * audit.read must be refused the export -- is not reachable through the UI: an
 * account without that permission does not see the audit page, so it cannot
 * press the export control. This spec drives only the positive path of a
 * holder; the refusal is covered by backend tests, not here.
 *
 * Expected copy (the settings page's save bar and feedback, the audit filters,
 * the pager and the export control, and the invalid-value message) is read
 * from the shipped en-US bundles through helpers/i18n.ts; the report's data
 * values (the setting key, the action and outcome names, the operator's
 * display name) are the product's own answers.
 */
// @journey journey-audit-export.md
// @covers REQ-platform-admin-029, REQ-platform-admin-111, REQ-platform-admin-112, REQ-platform-admin-113
import { readFile } from 'node:fs/promises'

import type { Locator, Page, Request } from '@playwright/test'

import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import { scaledTimeout } from '../fixtures/time-scale'
import {
  adminEntry,
  openAccountMenu,
  switchOrganization,
} from '../helpers/account-menu'
import { copy } from '../helpers/i18n'
import { expectNoAccountIds } from '../helpers/organization-admin'

// The settings table's type column and the audit table's least important
// columns render only from their container's larger widths. The desktop
// viewport keeps every column the journey's assertions read on screen.
test.use({ viewport: { width: 1920, height: 1080 } })

// The minute crossing in step 6 legitimately waits for the wall clock, so the
// file's bound is above the tier's default rather than the wait being cut off.
test.describe.configure({ timeout: scaledTimeout(300_000) })

/** The seeded platform administrator the e2e seed writes. */
const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

/** The one declared setting this journey writes, and its namespace. The two
 * legal values are the closed domain the authn account settings declare. */
const SETTING_KEY = 'authn.account.password-signup'
const SETTING_NAMESPACE = 'authn.account'
const OPEN = 'open'
const CLOSED = 'closed'

/** A value outside that closed domain: the refusal step 2 drives. */
const INVALID_VALUE = 'sometimes'

/** A declared namespace other than the written setting's, used to leave and
 * return so the page re-reads the stored value instead of keeping the refused
 * draft on screen. */
const OTHER_NAMESPACE = 'ratelimit'
const OTHER_SETTING_KEY = 'ratelimit.limit'

/** The action and outcome the settings management surface records. */
const SETTINGS_SET = 'settings.set'
const SUCCESS = 'success'
const FAILED = 'failed'

/** How many of this run's saves are made before the minute boundary, so the
 * narrowed window in step 6 covers exactly these. */
const FIRST_PHASE_SAVES = 3

const MINUTE_MS = 60_000

/**
 * How far the export file name may sit outside the click window. The name is
 * the export's own instant truncated to the whole second, so the truncation
 * alone can put it just under a second before the click the harness recorded.
 */
const EXPORT_NAME_TOLERANCE_MS = 1_000

/** The audit list's path, read from the page's own request. */
const AUDIT_LIST_PATH = '/api/v1/system/audit/records'

/** One record the rendered list shows, with the fields the export repeats. */
interface ListedRecord {
  readonly id: string
  readonly outcome: string
  /** The raw actor the list read answers for this id, which is what the
   * export must repeat. The row itself renders the readable identity. */
  readonly actor: string
  /** The row's rendered time parsed back to an instant. The render drops
   * sub-minute precision, so two rows of one minute compare equal. */
  readonly occurredAt: number
}

/** One line of the exported NDJSON file, in the shape the record view has. */
interface ExportedRecord {
  readonly id: string
  readonly action: string
  readonly object: string
  readonly outcome: string
  readonly actor: string
  readonly occurred_at: string
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

/** The home page's level-one heading. */
function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

/** The admin's platform navigation. */
function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

/** The settings page's namespace navigation. */
function namespaceNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'settings.nav.label'),
    exact: true,
  })
}

/** The settings table. */
function settingsTable(page: Page): Locator {
  return page.getByRole('table')
}

/** The item's value field. */
function valueField(page: Page): Locator {
  return page.getByRole('textbox', { name: SETTING_KEY, exact: true })
}

/** The save bar's one button. */
function saveButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'settings.saveBar.save'),
    exact: true,
  })
}

/** The save bar's pending-item count for a one-item batch. */
function pendingCount(page: Page): Locator {
  return page.getByText(
    copy('adminUi', 'settings.saveBar.pending', { count: 1 }),
    { exact: true },
  )
}

/** The audit page's action filter. */
function actionFilter(page: Page): Locator {
  return page.getByLabel(copy('adminUi', 'audit.filters.action'), {
    exact: true,
  })
}

/** The audit page's lower time bound. */
function sinceFilter(page: Page): Locator {
  return page.getByLabel(copy('adminUi', 'audit.filters.since'), {
    exact: true,
  })
}

/** The audit page's upper time bound. */
function untilFilter(page: Page): Locator {
  return page.getByLabel(copy('adminUi', 'audit.filters.until'), {
    exact: true,
  })
}

/** The audit page's apply button. */
function applyButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'audit.filters.apply'),
    exact: true,
  })
}

/** The audit page's next-page button. */
function nextPageButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'audit.nextPage'),
    exact: true,
  })
}

/** The audit page's previous-page button. */
function previousPageButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'audit.prevPage'),
    exact: true,
  })
}

/** The audit page's export control. */
function exportButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'audit.export.label'),
    exact: true,
  })
}

/** The audit list's body rows. */
function auditRows(page: Page): Locator {
  return page.getByRole('table').locator('tbody tr')
}

/**
 * A `datetime-local` value for an instant, floored to its whole minute. The
 * browser is pinned to UTC, so the string is the UTC wall clock the field
 * reads. The audit read's lower bound is inclusive and its upper bound is
 * exclusive; flooring a lower bound and extending an upper bound keeps every
 * write of the intended minute inside the intended window.
 */
function minuteValue(instant: Date): string {
  return instant.toISOString().slice(0, 16)
}

/** The start of the minute after the given instant. */
function nextMinuteStart(instant: Date): Date {
  return new Date(
    Math.floor(instant.getTime() / MINUTE_MS) * MINUTE_MS + MINUTE_MS,
  )
}

/** Wait until the given instant has passed. */
async function waitUntil(instant: Date): Promise<void> {
  const delay = instant.getTime() - Date.now()
  if (delay > 0) {
    await new Promise<void>((resolve) => setTimeout(resolve, delay))
  }
}

/** The zone the audit page renders every instant in (the page pins it to UTC). */
const RENDERED_TIME_ZONE = ' UTC'

/**
 * The instant a rendered time cell names, parsed back in UTC. The page renders
 * the record's `occurred_at` through Intl's medium-date/short-time en-US
 * formatter pinned to UTC, so the string carries no zone of its own and the
 * suffix supplies the one the render used. ICU may separate the day period
 * with a narrow no-break space, which the engine's date parser does not
 * accept, so those spaces are normalised first.
 */
function parseRenderedTime(text: string): number {
  const normalized = text.replace(/[\u00a0\u202f]/g, ' ').trim()
  const instant = Date.parse(`${normalized}${RENDERED_TIME_ZONE}`)
  if (Number.isNaN(instant)) {
    throw new Error(
      `the audit row's time ${JSON.stringify(text)} is not the en-US UTC instant the page renders`,
    )
  }
  return instant
}

/** The instant an exported record's `occurred_at` names (RFC 3339 in UTC). */
function parseExportedInstant(record: ExportedRecord): number {
  const instant = Date.parse(record.occurred_at)
  if (Number.isNaN(instant)) {
    throw new Error(
      `exported record ${record.id} has an unparseable occurred_at ${JSON.stringify(record.occurred_at)}`,
    )
  }
  return instant
}

/**
 * The body-cell indexes of the two columns this spec reads raw: the time
 * (rendered text, parsed back) and the actor (the DataTable full value). The
 * index comes from the rendered header, so a column added ahead of them keeps
 * the reads aligned instead of silently moving to the wrong cell.
 */
async function auditColumnIndexes(
  page: Page,
): Promise<{ readonly time: number; readonly actor: number }> {
  const headers = page.getByRole('table').getByRole('columnheader')
  const count = await headers.count()
  let time = -1
  let actor = -1
  for (let index = 0; index < count; index += 1) {
    const label = (await headers.nth(index).innerText()).trim()
    if (label === copy('adminUi', 'audit.column.time')) {
      time = index
    }
    if (label === copy('adminUi', 'audit.column.actor')) {
      actor = index
    }
  }
  if (time < 0 || actor < 0) {
    throw new Error('the audit table does not render its time and actor columns')
  }
  return { time, actor }
}

/** The record id an audit row's action link carries. */
async function rowRecordId(row: Locator): Promise<string> {
  const link = row.getByRole('link', { name: SETTINGS_SET, exact: true })
  if ((await link.count()) === 0) {
    throw new Error('an audit row carries no settings.set detail link')
  }
  const href = await link.getAttribute('href')
  if (href === null || href === '') {
    throw new Error('an audit row detail link carries no href')
  }
  return decodeURIComponent(href.slice(href.lastIndexOf('/') + 1))
}

/**
 * The raw actor each audit record id answers in the list read, learned from the
 * response the page itself receives. The console renders the readable identity
 * and never the id, so this is where the value the export repeats is learned.
 */
interface RawActors {
  readonly of: (id: string) => Promise<string>
}

/** Observe the audit page's own list reads and answer the raw actor per id. */
function observeRawActors(page: Page): RawActors {
  const actors = new Map<string, string>()
  page.on('response', (response) => {
    if (response.request().method() !== 'GET' || !response.ok()) {
      return
    }
    if (new URL(response.url()).pathname !== AUDIT_LIST_PATH) {
      return
    }
    response
      .json()
      .then(
        (body: {
          readonly entries: readonly {
            readonly id: string
            readonly actor: string
          }[]
        }) => {
          for (const entry of body.entries) actors.set(entry.id, entry.actor)
        },
      )
      .catch(() => undefined)
  })
  return {
    of: async (id: string) => {
      await expect
        .poll(() => actors.get(id) ?? '', {
          message: `the audit list read never answered the actor of record ${id}`,
        })
        .not.toBe('')
      return actors.get(id) ?? ''
    },
  }
}

/**
 * Walk every page the applied filter matches, asserting what each rendered row
 * says and returning the id, outcome, raw actor and time of every row. The
 * rows must read newest first across the pages, the actor cell must render the
 * readable identity, and the raw id the export repeats must appear nowhere in
 * that cell. The first page of a match longer than a page must be exactly the
 * page size the page asked for; the later pages carry the rest.
 */
async function collectListedRecords(
  page: Page,
  pageSize: number,
  rawActors: RawActors,
): Promise<readonly ListedRecord[]> {
  const listed: ListedRecord[] = []
  const seen = new Set<string>()
  let columns: { readonly time: number; readonly actor: number } | undefined
  let previousInstant: number | undefined

  for (;;) {
    const rows = auditRows(page)
    await expect(rows.first()).toBeVisible()
    // The header is read once the table is rendered: applying a filter can
    // leave the section in its loading placeholder for a moment, and a header
    // read then would find no table at all.
    columns ??= await auditColumnIndexes(page)
    const { time: timeColumn, actor: actorColumn } = columns
    const count = await rows.count()
    expect(count).toBeGreaterThan(0)
    expect(count).toBeLessThanOrEqual(pageSize)

    for (let index = 0; index < count; index += 1) {
      const row = rows.nth(index)
      const cells = row.getByRole('cell')
      const id = await rowRecordId(row)
      expect(id).not.toBe('')
      expect(seen.has(id)).toBe(false)
      seen.add(id)

      // The rendered row repeats the export's action, object, actor and
      // outcome: the action is the link's own name, the object is the key on
      // screen, the actor resolves to the operator's display name and login
      // username, and exactly one of the two outcome texts is present.
      await expect(
        row.getByText(SETTING_KEY, { exact: true }),
      ).toBeVisible()
      await expect(
        row.getByText(`${OPERATOR.displayName} (${OPERATOR.username})`, {
          exact: true,
        }),
      ).toBeVisible()
      const failed = await row
        .getByText(FAILED, { exact: true })
        .count()
      const success = await row
        .getByText(SUCCESS, { exact: true })
        .count()
      expect(failed + success).toBe(1)

      // The policy is newest first, and the list is read page by page, so the
      // order is asserted as the rows accumulate: no row may name a later
      // instant than the row before it, on this page or the one before.
      const occurredAt = parseRenderedTime(
        await cells.nth(timeColumn).innerText(),
      )
      if (previousInstant !== undefined) {
        expect(
          occurredAt,
          `audit row ${id} is newer than the row before it; the list must read newest first`,
        ).toBeLessThanOrEqual(previousInstant)
      }
      previousInstant = occurredAt

      // The actor cell renders the readable identity; the raw actor the
      // export carries is the list read's value for this id and must not
      // appear in the cell's text or any attribute.
      const actorCell = cells.nth(actorColumn)
      const actor = await rawActors.of(id)
      await expectNoAccountIds(actorCell, [actor])

      listed.push({
        id,
        outcome: failed === 1 ? FAILED : SUCCESS,
        actor,
        occurredAt,
      })
    }

    const next = nextPageButton(page)
    if (await next.isDisabled()) {
      break
    }

    const firstHref = await rows
      .first()
      .getByRole('link', { name: SETTINGS_SET, exact: true })
      .getAttribute('href')
    await next.click()
    // The next page replaces this one; wait for a different first row rather
    // than reading the page that is still on screen.
    await expect
      .poll(
        async () => {
          const firstRow = auditRows(page).first()
          if ((await firstRow.count()) === 0) {
            return null
          }
          const link = firstRow.getByRole('link', {
            name: SETTINGS_SET,
            exact: true,
          })
          if ((await link.count()) === 0) {
            return null
          }
          return link.getAttribute('href')
        },
        { message: 'the next audit page did not arrive' },
      )
      .not.toBe(firstHref)
    await expect(previousPageButton(page)).toBeEnabled()
  }

  return listed
}

/** Read and validate the NDJSON file a download saved. */
async function readExportedRecords(page: Page): Promise<readonly ExportedRecord[]> {
  // The export stamps the file name with the instant it starts, captured in
  // the page between these two reads of the harness clock, so the parsed name
  // has to fall inside this window (widened by the name's second truncation).
  const clickedAt = new Date()
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    exportButton(page).click(),
  ])
  const settledAt = new Date()

  const failure = await download.failure()
  expect(failure).toBeNull()

  const name = download.suggestedFilename()
  // The name is the export's own UTC instant, so a stale or fixed name would
  // fail here as well as the contents.
  expect(name).toMatch(/^audit-records-\d{8}T\d{6}Z\.ndjson$/)

  const stamped =
    /^audit-records-(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})Z\.ndjson$/.exec(
      name,
    )
  if (stamped === null) {
    throw new Error(
      `the export file name ${JSON.stringify(name)} is not audit-records-<UTC instant>.ndjson`,
    )
  }
  const namedInstant = Date.UTC(
    Number(stamped[1]),
    Number(stamped[2]) - 1,
    Number(stamped[3]),
    Number(stamped[4]),
    Number(stamped[5]),
    Number(stamped[6]),
  )
  expect(
    namedInstant,
    `the export file name ${JSON.stringify(name)} is earlier than the click that produced it`,
  ).toBeGreaterThanOrEqual(clickedAt.getTime() - EXPORT_NAME_TOLERANCE_MS)
  expect(
    namedInstant,
    `the export file name ${JSON.stringify(name)} is later than the download that carried it`,
  ).toBeLessThanOrEqual(settledAt.getTime() + EXPORT_NAME_TOLERANCE_MS)

  const filePath = await download.path()
  const body = await readFile(filePath, 'utf8')
  const lines = body.split('\n').filter((line) => line !== '')
  expect(lines.length).toBeGreaterThan(0)

  const records = lines.map((line, index) => {
    let parsed: unknown
    try {
      parsed = JSON.parse(line)
    } catch {
      throw new Error(`export line ${index + 1} is not JSON`)
    }
    if (typeof parsed !== 'object' || parsed === null) {
      throw new Error(`export line ${index + 1} is not one record object`)
    }
    const record = parsed as Partial<ExportedRecord>
    for (const field of [
      'id',
      'action',
      'object',
      'outcome',
      'actor',
      'occurred_at',
    ] as const) {
      if (typeof record[field] !== 'string') {
        throw new Error(`export line ${index + 1} has no string ${field}`)
      }
    }
    return record as ExportedRecord
  })

  // The export mirrors the list's newest-first order: every line's
  // `occurred_at` parses, and no line names a later instant than the one
  // before it.
  let previousInstant: number | undefined
  for (const record of records) {
    const instant = parseExportedInstant(record)
    if (previousInstant !== undefined) {
      expect(
        instant,
        `exported record ${record.id} is newer than the record before it; the export must read newest first`,
      ).toBeLessThanOrEqual(previousInstant)
    }
    previousInstant = instant
  }

  return records
}

/** The ids of a record set, sorted for comparison. */
function sortedIds(records: readonly { readonly id: string }[]): string[] {
  return records.map((record) => record.id).sort()
}

test('audit-export: the file carries the whole filtered set, and a narrowed filter narrows it', async ({
  demo,
}) => {
  const { page, server } = demo

  // The console renders an actor's readable identity, never the raw value the
  // export repeats; the page's own list reads are where the raw values are
  // learned.
  const rawActors = observeRawActors(page)

  // The page's requested page size, observed from the list request the audit
  // page issues. It is read before the saves so the run makes one page's worth
  // plus one, not a number copied from the product's source.
  let auditPageSize: number | undefined
  const observeAuditList = (event: Request): void => {
    const url = new URL(event.url())
    if (url.pathname !== AUDIT_LIST_PATH) {
      return
    }
    const limit = url.searchParams.get('limit')
    if (limit === null) {
      return
    }
    const parsed = Number.parseInt(limit, 10)
    if (Number.isInteger(parsed) && parsed > 0) {
      auditPageSize = parsed
    }
  }

  await test.step('precondition: the operator signs in, proves the email, and the audit page states its page size', async () => {
    // The journey's precondition is an operator whose email is proved. The
    // seed writes the address unproved, so the spec reaches the
    // precondition the way a person does: the standing notice's verify entry,
    // the code from the server's process log, and the verify action.
    await demo.accounts.signIn({
      identifier: OPERATOR.username,
      password: OPERATOR.password,
    })
    await expect(homeHeading(page)).toBeVisible()
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
    const codeField = section.getByLabel(copy('accountUi', 'verification.codeLabel'), {
      exact: true,
    })
    await expect(codeField).toBeFocused()

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
    await expect(verificationBanner(page, OPERATOR.identifier)).toHaveCount(0)

    // Reach the admin through the home page's own entries, as a person
    // does.
    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(homeHeading(page)).toBeVisible()
    // The admin entry is offered only while the platform's own organization
    // is current, and it lives in the account menu.
    await switchOrganization(page, 'system')
    await openAccountMenu(page)
    await expect(adminEntry(page)).toBeVisible()
    await adminEntry(page).click()
    await expect(adminNav(page)).toBeVisible()

    // Open the audit page once to read the size its list read asks for. The
    // listener is attached first; it observes the request and nothing else.
    // This is a harness read of the product's own number, not a journey step.
    page.on('request', observeAuditList)
    try {
      await adminNav(page)
        .getByRole('link', {
          name: copy('adminUi', 'audit.title'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('adminUi', 'audit.title'),
          exact: true,
        }),
      ).toBeVisible()
      await expect
        .poll(() => auditPageSize, {
          message: 'the audit page never issued a list request with a limit',
        })
        .toBeGreaterThan(0)
    } finally {
      page.off('request', observeAuditList)
    }
    if (auditPageSize === undefined) {
      throw new Error('the audit page size was never observed')
    }

    // The settings page is the journey's first surface.
    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'settings.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'settings.title'),
        exact: true,
      }),
    ).toBeVisible()
  })

  const pageSize = auditPageSize
  if (pageSize === undefined) {
    throw new Error('the audit page size is unknown')
  }
  expect(pageSize).toBeGreaterThan(FIRST_PHASE_SAVES)

  /** Save one legal value of the setting and wait for the write to land. */
  const saveValue = async (value: string): Promise<void> => {
    await valueField(page).fill(value)
    // A draft that survives the render is what the save bar counts; on a
    // landed write the draft is cleared and the count leaves the page.
    await expect(pendingCount(page)).toBeVisible()
    await saveButton(page).click()
    await expect(pendingCount(page)).toHaveCount(0)
    await expect
      .poll(() => valueField(page).inputValue(), {
        message: 'the row did not settle on the saved value',
      })
      .toBe(value)
  }

  let firstSaveAt: Date | undefined
  let lastSavedValue: string | undefined
  // The minute boundary between the first few saves and the rest. Step 6 uses
  // the same instant as its exclusive upper bound.
  let cutoff: Date | undefined

  await test.step('step 1: one setting is toggled between its two legal values until the writes exceed one audit page', async () => {
    await namespaceNav(page)
      .getByRole('button', { name: SETTING_NAMESPACE, exact: true })
      .click()
    await expect(valueField(page)).toBeVisible()

    // The first save's instant is the lower bound of the audit window.
    firstSaveAt = new Date()
    let value = await valueField(page).inputValue()
    expect([OPEN, CLOSED]).toContain(value)

    // The first phase stays before the minute boundary step 6 uses.
    for (let index = 0; index < FIRST_PHASE_SAVES; index += 1) {
      value = value === OPEN ? CLOSED : OPEN
      await saveValue(value)
    }

    // Cross the minute boundary the narrowed window in step 6 needs. Every
    // first-phase record is earlier than it; everything after this wait is
    // later. The margin leaves the boundary itself out of both phases.
    cutoff = nextMinuteStart(new Date())
    await waitUntil(new Date(cutoff.getTime() + 1_000))

    // One page's worth plus one save in total, so the filtered list has a
    // second page. The extra failed write in step 2 does not count towards
    // this requirement.
    for (
      let index = FIRST_PHASE_SAVES;
      index < pageSize + 1;
      index += 1
    ) {
      value = value === OPEN ? CLOSED : OPEN
      await saveValue(value)
    }
    lastSavedValue = value

    // The last save's own confirmation and a server re-read show the value is
    // stored rather than local draft state.
    await expect(
      page.getByText(copy('adminUi', 'settings.saved'), { exact: true }),
    ).toBeVisible()
    await page
      .getByRole('button', {
        name: copy('adminUi', 'common.refresh'),
        exact: true,
      })
      .click()
    await expect
      .poll(() => valueField(page).inputValue(), {
        message: 'the refreshed row lost the last saved value',
      })
      .toBe(value)
  })

  await test.step('step 2: an out-of-domain value is refused, the row keeps the old value, and the refusal is recorded', async () => {
    if (lastSavedValue === undefined) {
      throw new Error('the step-1 saves never ran')
    }
    const invalidCopy = copy('adminUi', 'errors.settings.invalid_value')

    await valueField(page).fill(INVALID_VALUE)
    await expect(pendingCount(page)).toBeVisible()
    await saveButton(page).click()

    // The failure is reported in the one feedback region and on the row
    // itself; the same text resolves both. The draft stays for correction.
    await expect(
      page.getByRole('alert').filter({ hasText: invalidCopy }),
    ).toBeVisible()
    const row = settingsTable(page)
      .getByRole('row')
      .filter({
        has: page.getByRole('textbox', { name: SETTING_KEY, exact: true }),
      })
    await expect(row.getByText(invalidCopy, { exact: true })).toBeVisible()
    await expect(valueField(page)).toHaveAttribute('aria-invalid', 'true')

    // Leaving the namespace clears the refused draft; returning re-reads the
    // stored value, which must still be the last legal one.
    await namespaceNav(page)
      .getByRole('button', { name: OTHER_NAMESPACE, exact: true })
      .click()
    await expect(
      page.getByRole('textbox', { name: OTHER_SETTING_KEY, exact: true }),
    ).toBeVisible()
    await namespaceNav(page)
      .getByRole('button', { name: SETTING_NAMESPACE, exact: true })
      .click()
    await expect
      .poll(() => valueField(page).inputValue(), {
        message: 'the refused write changed the stored value',
      })
      .toBe(lastSavedValue)
    await expect(valueField(page)).not.toHaveAttribute('aria-invalid', 'true')
  })

  let firstExport: readonly ExportedRecord[] = []
  let firstListed: readonly ListedRecord[] = []

  await test.step('step 3: the audit page filters to this run and its action, and the match is longer than a page', async () => {
    if (firstSaveAt === undefined) {
      throw new Error('the step-1 saves never ran')
    }
    const since = minuteValue(firstSaveAt)
    const until = minuteValue(new Date(Date.now() + MINUTE_MS))
    const day = 24 * 60 * MINUTE_MS

    await adminNav(page)
      .getByRole('link', {
        name: copy('adminUi', 'audit.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('adminUi', 'audit.title'),
        exact: true,
      }),
    ).toBeVisible()

    // The filters are applied on submit, not on a keystroke. A window a day
    // ahead matches nothing, which proves the temporal bounds are applied too
    // and not just the action.
    await sinceFilter(page).fill(minuteValue(new Date(Date.now() + day)))
    await untilFilter(page).fill(
      minuteValue(new Date(Date.now() + day + MINUTE_MS)),
    )
    await actionFilter(page).fill(SETTINGS_SET)
    await applyButton(page).click()
    await expect(
      page.getByRole('heading', {
        level: 2,
        name: copy('adminUi', 'audit.empty.title'),
        exact: true,
      }),
    ).toBeVisible()

    // The window that covers the whole run and the action filter leave this
    // run's writes and nothing else.
    await sinceFilter(page).fill(since)
    await untilFilter(page).fill(until)
    await applyButton(page).click()
    await expect(auditRows(page)).toHaveCount(pageSize)

    // The match is longer than one page, and the pager offers the next one.
    await expect(nextPageButton(page)).toBeEnabled()
    firstListed = await collectListedRecords(page, pageSize, rawActors)
    expect(firstListed.length).toBeGreaterThan(pageSize)
    expect(firstListed.filter((record) => record.outcome === FAILED)).toHaveLength(1)
  })

  await test.step('step 4: the list stays on a page that is not the first and the export is taken from there', async () => {
    // Walking every page left the list on its last page, which is not the
    // first.
    await expect(previousPageButton(page)).toBeEnabled()
    firstExport = await readExportedRecords(page)
  })

  await test.step('step 5: the file is the whole filtered set, the refused write included', async () => {
    expect(firstExport.length).toBe(firstListed.length)
    expect(firstExport.length).toBeGreaterThan(pageSize)
    expect(sortedIds(firstExport)).toEqual(sortedIds(firstListed))

    const listedById = new Map(firstListed.map((record) => [record.id, record]))
    for (const record of firstExport) {
      const listed = listedById.get(record.id)
      expect(listed).toBeDefined()
      expect(record.action).toBe(SETTINGS_SET)
      expect(record.object).toBe(SETTING_KEY)
      expect(record.outcome).toBe(listed?.outcome)
      // The export's actor is the raw value the list read answered for this
      // same id, whose row rendered the operator's display name and username --
      // so the file names the operator, not merely any one account.
      expect(record.actor).toBe(listed?.actor)
    }

    // Every record names one and the same non-empty actor: the operator who
    // made the writes the list rendered under that identity.
    const actors = new Set(firstExport.map((record) => record.actor))
    expect(actors.size).toBe(1)
    const [actor] = [...actors]
    expect(actor).toBeDefined()
    expect(actor).not.toBe('')

    // The one refused write is the failure step 2 left, and the file carries
    // it as a failed record.
    const failedListed = firstListed.filter((record) => record.outcome === FAILED)
    expect(failedListed).toHaveLength(1)
    const failedId = failedListed[0]?.id
    expect(failedId).toBeDefined()
    const failedExport = firstExport.filter((record) => record.id === failedId)
    expect(failedExport).toHaveLength(1)
    expect(failedExport[0]?.outcome).toBe(FAILED)
    expect(failedExport[0]?.action).toBe(SETTINGS_SET)
    expect(failedExport[0]?.object).toBe(SETTING_KEY)
  })

  await test.step('step 6: an earlier end time narrows the list and the next export is its exact set', async () => {
    if (cutoff === undefined) {
      throw new Error('the minute boundary between the save phases was never captured')
    }
    // The cutoff is the minute boundary the first phase was held behind. The
    // audit read's upper bound is exclusive, so every first-phase record is
    // inside the window and every later one is outside it.
    const narrowedUntil = minuteValue(cutoff)
    await untilFilter(page).fill(narrowedUntil)
    await applyButton(page).click()

    // Applying resets the list to its first page; the narrowed match fits on
    // that one page.
    await expect(previousPageButton(page)).toBeDisabled()
    const secondListed = await collectListedRecords(page, pageSize, rawActors)
    expect(secondListed).toHaveLength(FIRST_PHASE_SAVES)
    expect(secondListed.filter((record) => record.outcome === FAILED)).toHaveLength(0)
    await expect(nextPageButton(page)).toBeDisabled()

    const secondExport = await readExportedRecords(page)
    expect(secondExport.length).toBe(secondListed.length)
    expect(sortedIds(secondExport)).toEqual(sortedIds(secondListed))

    // The narrowed set is a strict subset of the first, and each line repeats
    // the narrowed list's action, object, outcome and actor.
    const firstIds = new Set(firstExport.map((record) => record.id))
    for (const id of secondExport.map((record) => record.id)) {
      expect(firstIds.has(id)).toBe(true)
    }
    expect(secondExport.length).toBeLessThan(firstExport.length)

    const listedById = new Map(secondListed.map((record) => [record.id, record]))
    for (const record of secondExport) {
      const listed = listedById.get(record.id)
      expect(listed).toBeDefined()
      expect(record.action).toBe(SETTINGS_SET)
      expect(record.object).toBe(SETTING_KEY)
      expect(record.outcome).toBe(SUCCESS)
      expect(record.actor).toBe(listed?.actor)
    }

    const actors = new Set(secondExport.map((record) => record.actor))
    expect(actors.size).toBe(1)
    expect([...actors][0]).toBe(firstExport[0]?.actor)
  })
})
