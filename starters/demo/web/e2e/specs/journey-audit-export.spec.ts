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

// The settings and audit tables render their low-priority columns only at desktop widths; keep the viewport wide enough for the columns this spec reads.
test.use({ viewport: { width: 1920, height: 1080 } })

// The minute crossing in step 6 waits for the wall clock, so keep this bound above the tier's default.
test.describe.configure({ timeout: scaledTimeout(300_000) })

const OPERATOR = {
  identifier: 'operator@demo.test',
  username: 'operator',
  password: 'operator-dev-secret',
  displayName: 'Demo Operator',
} as const

const SETTING_KEY = 'authn.account.password-signup'
const SETTING_NAMESPACE = 'authn.account'
const OPEN = 'open'
const CLOSED = 'closed'

const INVALID_VALUE = 'sometimes'

const OTHER_NAMESPACE = 'ratelimit'
const OTHER_SETTING_KEY = 'ratelimit.limit'

const SETTINGS_SET = 'settings.set'
const SUCCESS = 'success'
const FAILED = 'failed'

const FIRST_PHASE_SAVES = 3

const MINUTE_MS = 60_000

const EXPORT_NAME_TOLERANCE_MS = 1_000

const AUDIT_LIST_PATH = '/api/v1/system/audit/records'

interface ListedRecord {
  readonly id: string
  readonly outcome: string
  readonly actor: string
  readonly occurredAt: number
}

interface ExportedRecord {
  readonly id: string
  readonly action: string
  readonly object: string
  readonly outcome: string
  readonly actor: string
  readonly occurred_at: string
}

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

function verificationSection(page: Page): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'verification.title'),
    exact: true,
  })
}

function homeHeading(page: Page): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', 'home.heading'),
    exact: true,
  })
}

function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
}

function namespaceNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'settings.nav.label'),
    exact: true,
  })
}

function settingsTable(page: Page): Locator {
  return page.getByRole('table')
}

function valueField(page: Page): Locator {
  return page.getByRole('textbox', { name: SETTING_KEY, exact: true })
}

function saveButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'settings.saveBar.save'),
    exact: true,
  })
}

function pendingCount(page: Page): Locator {
  return page.getByText(
    copy('adminUi', 'settings.saveBar.pending', { count: 1 }),
    { exact: true },
  )
}

function actionFilter(page: Page): Locator {
  return page.getByLabel(copy('adminUi', 'audit.filters.action'), {
    exact: true,
  })
}

function sinceFilter(page: Page): Locator {
  return page.getByLabel(copy('adminUi', 'audit.filters.since'), {
    exact: true,
  })
}

function untilFilter(page: Page): Locator {
  return page.getByLabel(copy('adminUi', 'audit.filters.until'), {
    exact: true,
  })
}

function applyButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'audit.filters.apply'),
    exact: true,
  })
}

function nextPageButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'audit.nextPage'),
    exact: true,
  })
}

function previousPageButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'audit.prevPage'),
    exact: true,
  })
}

function exportButton(page: Page): Locator {
  return page.getByRole('button', {
    name: copy('adminUi', 'audit.export.label'),
    exact: true,
  })
}

function auditRows(page: Page): Locator {
  return page.getByRole('table').locator('tbody tr')
}

// A datetime-local value is floored to its whole minute; the read's lower bound is inclusive and its upper bound exclusive, so flooring and extending keep every write inside the intended window.
function minuteValue(instant: Date): string {
  return instant.toISOString().slice(0, 16)
}

function nextMinuteStart(instant: Date): Date {
  return new Date(
    Math.floor(instant.getTime() / MINUTE_MS) * MINUTE_MS + MINUTE_MS,
  )
}

async function waitUntil(instant: Date): Promise<void> {
  const delay = instant.getTime() - Date.now()
  if (delay > 0) {
    await new Promise<void>((resolve) => setTimeout(resolve, delay))
  }
}

const RENDERED_TIME_ZONE = ' UTC'

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

function parseExportedInstant(record: ExportedRecord): number {
  const instant = Date.parse(record.occurred_at)
  if (Number.isNaN(instant)) {
    throw new Error(
      `exported record ${record.id} has an unparseable occurred_at ${JSON.stringify(record.occurred_at)}`,
    )
  }
  return instant
}

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

interface RawActors {
  readonly of: (id: string) => Promise<string>
}

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

      // Newest first and paged: assert the order as rows accumulate, so no row names a later instant than the one before.
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

async function readExportedRecords(page: Page): Promise<readonly ExportedRecord[]> {
  const clickedAt = new Date()
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    exportButton(page).click(),
  ])
  const settledAt = new Date()

  const failure = await download.failure()
  expect(failure).toBeNull()

  const name = download.suggestedFilename()
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

function sortedIds(records: readonly { readonly id: string }[]): string[] {
  return records.map((record) => record.id).sort()
}

test('audit-export: the file carries the whole filtered set, and a narrowed filter narrows it', async ({
  demo,
}) => {
  const { page, server } = demo

  const rawActors = observeRawActors(page)

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

    await page
      .getByRole('link', { name: copy('demo', 'nav.home'), exact: true })
      .click()
    await expect(homeHeading(page)).toBeVisible()
    await switchOrganization(page, 'system')
    await openAccountMenu(page)
    await expect(adminEntry(page)).toBeVisible()
    await adminEntry(page).click()
    await expect(adminNav(page)).toBeVisible()

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

  const saveValue = async (value: string): Promise<void> => {
    await valueField(page).fill(value)
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
  let cutoff: Date | undefined

  await test.step('step 1: one setting is toggled between its two legal values until the writes exceed one audit page', async () => {
    await namespaceNav(page)
      .getByRole('button', { name: SETTING_NAMESPACE, exact: true })
      .click()
    await expect(valueField(page)).toBeVisible()

    firstSaveAt = new Date()
    let value = await valueField(page).inputValue()
    expect([OPEN, CLOSED]).toContain(value)

    for (let index = 0; index < FIRST_PHASE_SAVES; index += 1) {
      value = value === OPEN ? CLOSED : OPEN
      await saveValue(value)
    }

    cutoff = nextMinuteStart(new Date())
    await waitUntil(new Date(cutoff.getTime() + 1_000))

    for (
      let index = FIRST_PHASE_SAVES;
      index < pageSize + 1;
      index += 1
    ) {
      value = value === OPEN ? CLOSED : OPEN
      await saveValue(value)
    }
    lastSavedValue = value

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

    await sinceFilter(page).fill(since)
    await untilFilter(page).fill(until)
    await applyButton(page).click()
    await expect(auditRows(page)).toHaveCount(pageSize)

    await expect(nextPageButton(page)).toBeEnabled()
    firstListed = await collectListedRecords(page, pageSize, rawActors)
    expect(firstListed.length).toBeGreaterThan(pageSize)
    expect(firstListed.filter((record) => record.outcome === FAILED)).toHaveLength(1)
  })

  await test.step('step 4: the list stays on a page that is not the first and the export is taken from there', async () => {
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
      expect(record.actor).toBe(listed?.actor)
    }

    const actors = new Set(firstExport.map((record) => record.actor))
    expect(actors.size).toBe(1)
    const [actor] = [...actors]
    expect(actor).toBeDefined()
    expect(actor).not.toBe('')

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
    const narrowedUntil = minuteValue(cutoff)
    await untilFilter(page).fill(narrowedUntil)
    await applyButton(page).click()

    await expect(previousPageButton(page)).toBeDisabled()
    const secondListed = await collectListedRecords(page, pageSize, rawActors)
    expect(secondListed).toHaveLength(FIRST_PHASE_SAVES)
    expect(secondListed.filter((record) => record.outcome === FAILED)).toHaveLength(0)
    await expect(nextPageButton(page)).toBeDisabled()

    const secondExport = await readExportedRecords(page)
    expect(secondExport.length).toBe(secondListed.length)
    expect(sortedIds(secondExport)).toEqual(sortedIds(secondListed))

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
