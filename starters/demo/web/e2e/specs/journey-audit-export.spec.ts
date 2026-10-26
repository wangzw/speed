/**
 * An operator changes one setting often enough to fill more than one audit
 * page, is refused once, and exports the matching records from a page that is
 * not the first.
 *
 * The seed writes the platform administrator with an unproved email address,
 * and a settings write from an unproved account is refused, so the spec first
 * reaches the journey's precondition the way a person does: the standing
 * notice's verify entry, the code from the review process log (the one non-UI
 * read the tier allows), and the verify action.
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
 * outcome and actor the row renders, and then requires the exported NDJSON
 * lines to be the same set of ids -- same count, same action, same object,
 * same outcome per id, the one refused write included -- and the second file
 * to be exactly the narrowed list's set and a strict subset of the first.
 * Reading the downloaded file is a named read of the file the browser saved;
 * the spec writes nothing and opens no other channel.
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
import { copy } from '../helpers/i18n'

// The settings table's type column and the audit table's least important
// columns render only from their container's larger widths. The desktop
// viewport keeps every column the journey's assertions read on screen.
test.use({ viewport: { width: 1920, height: 1080 } })

// The minute crossing in step 6 legitimately waits for the wall clock, so the
// file's bound is above the tier's default rather than the wait being cut off.
test.describe.configure({ timeout: 300_000 })

/** The seeded platform administrator the review assembly writes. */
const OPERATOR = {
  identifier: 'operator@demo.test',
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

/** The audit list's path, read from the page's own request. */
const AUDIT_LIST_PATH = '/api/v1/system/audit/records'

/** One record the rendered list shows, with the fields the export repeats. */
interface ListedRecord {
  readonly id: string
  readonly outcome: string
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

/** The console's platform navigation. */
function consoleNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('consoleUi', 'shell.navLabel'),
    exact: true,
  })
}

/** The settings page's namespace navigation. */
function namespaceNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('consoleUi', 'settings.nav.label'),
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
    name: copy('consoleUi', 'settings.saveBar.save'),
    exact: true,
  })
}

/** The save bar's pending-item count for a one-item batch. */
function pendingCount(page: Page): Locator {
  return page.getByText(
    copy('consoleUi', 'settings.saveBar.pending', { count: 1 }),
    { exact: true },
  )
}

/** The audit page's action filter. */
function actionFilter(page: Page): Locator {
  return page.getByLabel(copy('consoleUi', 'audit.filters.action'), {
    exact: true,
  })
}

/** The audit page's lower time bound. */
function sinceFilter(page: Page): Locator {
  return page.getByLabel(copy('consoleUi', 'audit.filters.since'), {
    exact: true,
  })
}

/** The audit page's upper time bound. */
function untilFilter(page: Page): Locator {
  return page.getByLabel(copy('consoleUi', 'audit.filters.until'), {
    exact: true,
  })
}

/** The audit page's apply button. */
function applyButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('consoleUi', 'audit.filters.apply'),
    exact: true,
  })
}

/** The audit page's next-page button. */
function nextPageButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('consoleUi', 'audit.nextPage'),
    exact: true,
  })
}

/** The audit page's previous-page button. */
function previousPageButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('consoleUi', 'audit.prevPage'),
    exact: true,
  })
}

/** The audit page's export control. */
function exportButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('consoleUi', 'audit.export.label'),
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
 * Walk every page the applied filter matches, asserting what each rendered row
 * says and returning the id and outcome of every row. The first page of a
 * match longer than a page must be exactly the page size the page asked for;
 * the later pages carry the rest.
 */
async function collectListedRecords(
  page: Page,
  pageSize: number,
): Promise<readonly ListedRecord[]> {
  const listed: ListedRecord[] = []
  const seen = new Set<string>()

  for (;;) {
    const rows = auditRows(page)
    await expect(rows.first()).toBeVisible({ timeout: 15_000 })
    const count = await rows.count()
    expect(count).toBeGreaterThan(0)
    expect(count).toBeLessThanOrEqual(pageSize)

    for (let index = 0; index < count; index += 1) {
      const row = rows.nth(index)
      const id = await rowRecordId(row)
      expect(id).not.toBe('')
      expect(seen.has(id)).toBe(false)
      seen.add(id)

      // The rendered row repeats the export's action, object, actor and
      // outcome: the action is the link's own name, the object is the key on
      // screen, the actor resolves to the operator's display name, and exactly
      // one of the two outcome texts is present.
      await expect(
        row.getByText(SETTING_KEY, { exact: true }),
      ).toBeVisible()
      await expect(
        row.getByText(OPERATOR.displayName, { exact: true }),
      ).toBeVisible()
      const failed = await row
        .getByText(FAILED, { exact: true })
        .count()
      const success = await row
        .getByText(SUCCESS, { exact: true })
        .count()
      expect(failed + success).toBe(1)
      listed.push({ id, outcome: failed === 1 ? FAILED : SUCCESS })
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
        { timeout: 15_000, message: 'the next audit page did not arrive' },
      )
      .not.toBe(firstHref)
    await expect(previousPageButton(page)).toBeEnabled()
  }

  return listed
}

/** Read and validate the NDJSON file a download saved. */
async function readExportedRecords(page: Page): Promise<readonly ExportedRecord[]> {
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    exportButton(page).click(),
  ])

  const failure = await download.failure()
  expect(failure).toBeNull()

  // The name is the export's own UTC instant, so a stale or fixed name would
  // fail here as well as the contents.
  expect(download.suggestedFilename()).toMatch(
    /^audit-records-\d{8}T\d{6}Z\.ndjson$/,
  )

  const filePath = await download.path()
  const body = await readFile(filePath, 'utf8')
  const lines = body.split('\n').filter((line) => line !== '')
  expect(lines.length).toBeGreaterThan(0)

  return lines.map((line, index) => {
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
}

/** The ids of a record set, sorted for comparison. */
function sortedIds(records: readonly { readonly id: string }[]): string[] {
  return records.map((record) => record.id).sort()
}

test('audit-export: the file carries the whole filtered set, and a narrowed filter narrows it', async ({
  demo,
}) => {
  const { page, server } = demo

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
    // review seed writes the address unproved, so the spec reaches the
    // precondition the way a person does: the standing notice's verify entry,
    // the code from the review process log, and the verify action.
    await demo.accounts.signIn({
      identifier: OPERATOR.identifier,
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

    // Reach the console through the home page's own entries, as a person
    // does.
    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(homeHeading(page)).toBeVisible()
    await page
      .getByRole('banner')
      .getByRole('link', { name: copy('demo', 'nav.console'), exact: true })
      .click()
    await expect(consoleNav(page)).toBeVisible()

    // Open the audit page once to read the size its list read asks for. The
    // listener is attached first; it observes the request and nothing else.
    // This is a harness read of the product's own number, not a journey step.
    page.on('request', observeAuditList)
    try {
      await consoleNav(page)
        .getByRole('link', {
          name: copy('consoleUi', 'audit.title'),
          exact: true,
        })
        .click()
      await expect(
        page.getByRole('heading', {
          level: 1,
          name: copy('consoleUi', 'audit.title'),
          exact: true,
        }),
      ).toBeVisible()
      await expect
        .poll(() => auditPageSize, {
          timeout: 15_000,
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
    await consoleNav(page)
      .getByRole('link', {
        name: copy('consoleUi', 'settings.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('consoleUi', 'settings.title'),
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
    await expect(pendingCount(page)).toHaveCount(0, { timeout: 15_000 })
    await expect
      .poll(() => valueField(page).inputValue(), {
        timeout: 15_000,
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
      page.getByText(copy('consoleUi', 'settings.saved'), { exact: true }),
    ).toBeVisible()
    await page
      .getByRole('button', {
        name: copy('consoleUi', 'common.refresh'),
        exact: true,
      })
      .click()
    await expect
      .poll(() => valueField(page).inputValue(), {
        timeout: 15_000,
        message: 'the refreshed row lost the last saved value',
      })
      .toBe(value)
  })

  await test.step('step 2: an out-of-domain value is refused, the row keeps the old value, and the refusal is recorded', async () => {
    if (lastSavedValue === undefined) {
      throw new Error('the step-1 saves never ran')
    }
    const invalidCopy = copy('consoleUi', 'errors.settings.invalid_value')

    await valueField(page).fill(INVALID_VALUE)
    await expect(pendingCount(page)).toBeVisible()
    await saveButton(page).click()

    // The failure is reported in the one feedback region and on the row
    // itself; the same text resolves both. The draft stays for correction.
    await expect(
      page.getByRole('alert').filter({ hasText: invalidCopy }),
    ).toBeVisible({ timeout: 15_000 })
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
        timeout: 15_000,
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

    await consoleNav(page)
      .getByRole('link', {
        name: copy('consoleUi', 'audit.title'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('consoleUi', 'audit.title'),
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
        name: copy('consoleUi', 'audit.empty.title'),
        exact: true,
      }),
    ).toBeVisible({ timeout: 15_000 })

    // The window that covers the whole run and the action filter leave this
    // run's writes and nothing else.
    await sinceFilter(page).fill(since)
    await untilFilter(page).fill(until)
    await applyButton(page).click()
    await expect(auditRows(page)).toHaveCount(pageSize, { timeout: 15_000 })

    // The match is longer than one page, and the pager offers the next one.
    await expect(nextPageButton(page)).toBeEnabled()
    firstListed = await collectListedRecords(page, pageSize)
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
    }

    // Every record names one and the same non-empty actor: the operator who
    // made the writes the list rendered under that display name.
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
    await expect(previousPageButton(page)).toBeDisabled({ timeout: 15_000 })
    const secondListed = await collectListedRecords(page, pageSize)
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
    }

    const actors = new Set(secondExport.map((record) => record.actor))
    expect(actors.size).toBe(1)
    expect([...actors][0]).toBe(firstExport[0]?.actor)
  })
})
