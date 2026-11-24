// @journey journey-personalize-interface.md
// @covers REQ-account-003, REQ-account-033, REQ-presentation-003, REQ-presentation-004, REQ-presentation-007, REQ-presentation-008, REQ-presentation-009, REQ-presentation-010, REQ-presentation-030, REQ-security-022
import type { Locator, Page } from '@playwright/test'

import { e2eMember, smsRolloutOperator } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import { waitForVerificationCode } from '../fixtures/log'
import {
  adminEntry,
  openAccountMenu,
  switchOrganization,
} from '../helpers/account-menu'
import {
  expandAccountRows,
  openAccountSection,
  selectAccountSection,
} from '../helpers/account-sections'
import { copy, type CopyLocale } from '../helpers/i18n'
import { chooseSelectOption } from '../helpers/select-option'

test.use({ seedMember: true })

const CHOSEN_LOCALE: CopyLocale = 'zh-CN'

const CHOSEN_ZONE = 'Asia/Tokyo'

const CLOCK_SKEW_MS = 2_000

function preferencesSection(page: Page, locale: CopyLocale): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.blocks.appearance', {}, locale),
    exact: true,
  })
}

function languageSelect(page: Page, locale: CopyLocale): Locator {
  return preferencesSection(page, locale).getByRole('combobox', {
    name: copy('accountUi', 'preferences.language.label', {}, locale),
    exact: true,
  })
}

function timeZoneSelect(page: Page, locale: CopyLocale): Locator {
  return preferencesSection(page, locale).getByRole('combobox', {
    name: copy('accountUi', 'preferences.timezone.label', {}, locale),
    exact: true,
  })
}

function colorModeSelect(page: Page, locale: CopyLocale): Locator {
  return preferencesSection(page, locale).getByRole('combobox', {
    name: copy('accountUi', 'appearance.colorMode.label', {}, locale),
    exact: true,
  })
}

function colorModeOptionLabel(
  mode: 'dark' | 'light' | 'system',
  locale: CopyLocale,
): string {
  return copy('accountUi', `appearance.colorMode.options.${mode}`, {}, locale)
}

const LIGHT_PAGE_BACKGROUND = 'rgb(248, 250, 252)'
const DARK_PAGE_BACKGROUND = 'rgb(15, 23, 42)'

function sessionsSection(page: Page, locale: CopyLocale): Locator {
  return page.getByRole('region', {
    name: copy('demo', 'account.blocks.devices', {}, locale),
    exact: true,
  })
}

function currentSessionRow(page: Page, locale: CopyLocale): Locator {
  return sessionsSection(page, locale)
    .getByRole('listitem')
    .filter({
      has: page.getByText(copy('accountUi', 'sessions.current', {}, locale), {
        exact: true,
      }),
    })
}

function currentSessionActivity(page: Page, locale: CopyLocale): Locator {
  const prefix = copy('accountUi', 'sessions.rowActivity', { time: '' }, locale)
  return currentSessionRow(page, locale).getByText(prefix, { exact: false })
}

function timeOfDetail(text: string, locale: CopyLocale): string {
  const prefix = copy('accountUi', 'sessions.rowActivity', { time: '' }, locale)
  const at = text.lastIndexOf(prefix)
  return at < 0 ? text : text.slice(at + prefix.length)
}

async function languageOptionLabel(
  page: Page,
  locale: CopyLocale,
  tag: string,
): Promise<string> {
  await languageSelect(page, locale).click()
  const option = page.locator(`[role="option"][data-value="${tag}"]`)
  await expect(option).toBeVisible()
  const label = ((await option.textContent()) ?? '').trim()
  await page.keyboard.press('Escape')
  if (label === '') {
    throw new Error(`the language option ${tag} rendered no label`)
  }
  return label
}

function numericFieldKey(
  formatter: Intl.DateTimeFormat,
  instant: number,
): string {
  return formatter
    .formatToParts(new Date(instant))
    .filter(
      (part) =>
        part.type === 'year' ||
        part.type === 'month' ||
        part.type === 'day' ||
        part.type === 'hour' ||
        part.type === 'minute',
    )
    .map((part) => String(Number(part.value)))
    .join(',')
}

function renderedFieldKey(rendered: string): string {
  return (rendered.match(/\d+/g) ?? [])
    .map((value) => String(Number(value)))
    .join(',')
}

// The product formats through Intl.DateTimeFormat with medium date and short time styles; mirror that call and compare the fields it yields, within the recorded window.
function renderedInstantIn(
  rendered: string,
  language: string,
  timeZone: string,
  from: number,
  to: number,
): boolean {
  const formatter = new Intl.DateTimeFormat(language, {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone,
  })
  const key = renderedFieldKey(rendered)
  for (let instant = from; instant <= to; instant += 1_000) {
    if (numericFieldKey(formatter, instant) === key) {
      return true
    }
  }
  return false
}

function renderedTimeIn(
  rendered: string,
  language: string,
  timeZone: string,
  from: number,
  to: number,
): boolean {
  const formatter = new Intl.DateTimeFormat(language, {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone,
  })
  for (let instant = from; instant <= to; instant += 1_000) {
    if (formatter.format(instant) === rendered) {
      return true
    }
  }
  return false
}

async function currentSessionSignedInTime(
  page: Page,
  locale: CopyLocale,
): Promise<string> {
  await sessionsSection(page, locale).scrollIntoViewIfNeeded()
  const current = currentSessionRow(page, locale)
  await expect(current).toHaveCount(1)
  const activity = currentSessionActivity(page, locale)
  const text = ((await activity.textContent()) ?? '').trim()
  if (text === '') {
    throw new Error('the row detail read no instant')
  }
  return timeOfDetail(text, locale)
}

test('personalize-interface: the chosen language and time zone take effect and survive a reload', async ({
  demo,
}) => {
  const { page, accounts } = demo

  let signInFrom = 0
  let signInTo = 0
  let chosenLanguageLabel = ''

  await test.step('the member signs in and opens the account settings page', async () => {
    signInFrom = Date.now() - CLOCK_SKEW_MS
    await accounts.signIn({
      identifier: e2eMember.username,
      password: e2eMember.password,
    })
    signInTo = Date.now() + CLOCK_SKEW_MS
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()

    await openAccountSection(page, 'preferences')
    await expandAccountRows(page)
  })

  await test.step('step 1: both preference controls start on the explicit "not chosen" choice', async () => {
    const section = preferencesSection(page, 'en-US')
    await section.scrollIntoViewIfNeeded()
    await expect(section).toBeVisible()

    await expect(languageSelect(page, 'en-US')).toHaveText(
      copy('accountUi', 'preferences.language.notChosen'),
    )
    await expect(timeZoneSelect(page, 'en-US')).toHaveText(
      copy('accountUi', 'preferences.timezone.notChosen'),
    )

    await timeZoneSelect(page, 'en-US').click()
    await expect(
      page.getByRole('option', {
        name: copy('accountUi', 'preferences.timezone.notChosen'),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByRole('option', { name: CHOSEN_ZONE, exact: true }),
    ).toBeVisible()
    await page.keyboard.press('Escape')
  })

  await test.step('step 2: choosing a language saves it and switches the interface at once', async () => {
    await chooseSelectOption(
      languageSelect(page, 'en-US'),
      page.locator('[role="option"][data-value="zh-CN"]'),
      null,
    )

    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'account.sections.preferences', {}, CHOSEN_LOCALE),
        exact: true,
      }),
    ).toBeVisible()

    chosenLanguageLabel = await languageOptionLabel(
      page,
      CHOSEN_LOCALE,
      CHOSEN_LOCALE,
    )
    const section = preferencesSection(page, CHOSEN_LOCALE)
    await expect(
      section.getByText(
        copy(
          'accountUi',
          'preferences.language.saved',
          { value: chosenLanguageLabel },
          CHOSEN_LOCALE,
        ),
        { exact: true },
      ),
    ).toBeVisible()

    await expect(
      page.getByRole('link', {
        name: copy('demo', 'nav.home', {}, CHOSEN_LOCALE),
        exact: true,
      }),
    ).toBeVisible()
    await expect(page).toHaveTitle(
      copy('demo', 'app.title', {}, CHOSEN_LOCALE),
    )
    await expect(page.locator('html')).toHaveAttribute('lang', CHOSEN_LOCALE)

    await expect(languageSelect(page, CHOSEN_LOCALE)).toHaveText(
      chosenLanguageLabel,
    )
  })

  await test.step('step 3: choosing a time zone saves it and confirms the zone by name', async () => {
    const section = preferencesSection(page, CHOSEN_LOCALE)
    const timeZone = timeZoneSelect(page, CHOSEN_LOCALE)
    await expect(timeZone).toHaveText(
      copy('accountUi', 'preferences.timezone.notChosen', {}, CHOSEN_LOCALE),
    )

    await chooseSelectOption(
      timeZone,
      page.getByRole('option', { name: CHOSEN_ZONE, exact: true }),
    )

    await expect(
      section.getByText(
        copy(
          'accountUi',
          'preferences.timezone.saved',
          { value: CHOSEN_ZONE },
          CHOSEN_LOCALE,
        ),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(timeZone).toHaveText(CHOSEN_ZONE)

    await timeZone.click()
    await expect(
      page.getByRole('option', {
        name: copy('accountUi', 'preferences.timezone.notChosen', {}, CHOSEN_LOCALE),
        exact: true,
      }),
    ).toBeVisible()
    await page.keyboard.press('Escape')
  })

  await test.step('step 4: the current session time renders in the chosen zone and interface language', async () => {
    await selectAccountSection(page, 'sessions', CHOSEN_LOCALE)
    const section = sessionsSection(page, CHOSEN_LOCALE)
    await section.scrollIntoViewIfNeeded()
    await expect(section).toBeVisible()
    await expect(
      section.getByText(copy('accountUi', 'sessions.current', {}, CHOSEN_LOCALE), {
        exact: true,
      }),
    ).toBeVisible()
    await expect(currentSessionActivity(page, CHOSEN_LOCALE)).toHaveText(/\d/)

    const rendered = await currentSessionSignedInTime(page, CHOSEN_LOCALE)
    expect(
      renderedInstantIn(
        rendered,
        CHOSEN_LOCALE,
        CHOSEN_ZONE,
        signInFrom,
        signInTo,
      ),
      `the session time ${JSON.stringify(rendered)} is not an instant in ` +
        `${CHOSEN_ZONE} rendered in ${CHOSEN_LOCALE} within the sign-in window`,
    ).toBe(true)
  })

  await test.step('step 5: a reload keeps the language, the stored preferences and the zone-rendered time', async () => {
    await page.reload()

    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'account.sections.sessions', {}, CHOSEN_LOCALE),
        exact: true,
      }),
    ).toBeVisible()
    await expect(page).toHaveTitle(
      copy('demo', 'app.title', {}, CHOSEN_LOCALE),
    )
    await expect(page.locator('html')).toHaveAttribute('lang', CHOSEN_LOCALE)
    await expect(
      page.getByRole('link', {
        name: copy('demo', 'nav.home', {}, CHOSEN_LOCALE),
        exact: true,
      }),
    ).toBeVisible()

    await selectAccountSection(page, 'preferences', CHOSEN_LOCALE)
    await expandAccountRows(page, undefined, CHOSEN_LOCALE)
    await expect(languageSelect(page, CHOSEN_LOCALE)).toHaveText(
      chosenLanguageLabel,
    )
    await expect(timeZoneSelect(page, CHOSEN_LOCALE)).toHaveText(
      CHOSEN_ZONE,
    )

    await selectAccountSection(page, 'sessions', CHOSEN_LOCALE)
    await expect(currentSessionActivity(page, CHOSEN_LOCALE)).toHaveText(/\d/)

    const rendered = await currentSessionSignedInTime(page, CHOSEN_LOCALE)
    expect(
      renderedInstantIn(
        rendered,
        CHOSEN_LOCALE,
        CHOSEN_ZONE,
        signInFrom,
        signInTo,
      ),
      `after reload the session time ${JSON.stringify(rendered)} left ` +
        `${CHOSEN_ZONE} in ${CHOSEN_LOCALE}`,
    ).toBe(true)
  })
})

test('personalize-interface: the color mode applies at once, follows the device and survives a reload', async ({
  demo,
}) => {
  const { page, accounts } = demo

  await test.step('the member signs in and opens the account preferences pane', async () => {
    await accounts.signIn({
      identifier: e2eMember.username,
      password: e2eMember.password,
    })
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()
    await openAccountSection(page, 'preferences')
    await expandAccountRows(page)
  })

  const body = page.locator('body')
  await expect(body).toHaveCSS('background-color', LIGHT_PAGE_BACKGROUND)

  let loads = 0
  const countLoad = (): void => {
    loads += 1
  }
  page.on('load', countLoad)

  await test.step('choosing dark repaints the page at once, without a reload', async () => {
    const select = colorModeSelect(page, 'en-US')
    await expect(select).toHaveText(colorModeOptionLabel('system', 'en-US'))
    loads = 0
    await chooseSelectOption(
      select,
      page.getByRole('option', {
        name: colorModeOptionLabel('dark', 'en-US'),
        exact: true,
      }),
    )
    await expect(body).toHaveCSS('background-color', DARK_PAGE_BACKGROUND)
    expect(loads, 'the dark mode applies without a reload').toBe(0)
  })

  await test.step('follow-device repaints with the system scheme both ways', async () => {
    const select = colorModeSelect(page, 'en-US')
    await expect(select).toBeEnabled()
    await chooseSelectOption(
      select,
      page.getByRole('option', {
        name: colorModeOptionLabel('system', 'en-US'),
        exact: true,
      }),
    )
    await expect(body).toHaveCSS('background-color', LIGHT_PAGE_BACKGROUND)

    await page.emulateMedia({ colorScheme: 'dark' })
    await expect(body).toHaveCSS('background-color', DARK_PAGE_BACKGROUND)

    await page.emulateMedia({ colorScheme: 'light' })
    await expect(body).toHaveCSS('background-color', LIGHT_PAGE_BACKGROUND)
  })

  page.off('load', countLoad)

  await test.step('a reload keeps the stored follow-device choice', async () => {
    await page.reload()
    await expandAccountRows(page)
    await expect(colorModeSelect(page, 'en-US')).toHaveText(
      colorModeOptionLabel('system', 'en-US'),
    )
  })
})

function adminNav(page: Page): Locator {
  return page.getByRole('navigation', {
    name: copy('adminUi', 'shell.navLabel'),
    exact: true,
  })
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

test('personalize-interface: the chosen time zone drives times on every surface', async ({
  demo,
}) => {
  const { page, server, accounts } = demo

  let signInFrom = 0
  let signInTo = 0

  await test.step('the operator signs in, proves the account email and stores Asia/Tokyo', async () => {
    signInFrom = Date.now() - CLOCK_SKEW_MS
    await accounts.signIn({
      identifier: smsRolloutOperator.username,
      password: smsRolloutOperator.password,
    })
    signInTo = Date.now() + CLOCK_SKEW_MS

    const banner = verificationBanner(page, smsRolloutOperator.identifier)
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
    const codeField = section.getByLabel(
      copy('accountUi', 'verification.codeLabel'),
      { exact: true },
    )
    const { code } = await waitForVerificationCode(
      server.log,
      'mail',
      smsRolloutOperator.identifier,
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
    await expect(banner).toHaveCount(0)

    await openAccountSection(page, 'preferences')
    await expandAccountRows(page)
    const preferences = preferencesSection(page, 'en-US')
    const timeZone = timeZoneSelect(page, 'en-US')
    await chooseSelectOption(
      timeZone,
      page.getByRole('option', { name: CHOSEN_ZONE, exact: true }),
    )
    await expect(
      preferences.getByText(
        copy(
          'accountUi',
          'preferences.timezone.saved',
          { value: CHOSEN_ZONE },
          'en-US',
        ),
        { exact: true },
      ),
    ).toBeVisible()
    await expect(timeZone).toHaveText(CHOSEN_ZONE)
  })

  await test.step('the audit page renders the recorded sign-in in Asia/Tokyo', async () => {
    await switchOrganization(page, 'system')
    await openAccountMenu(page)
    await adminEntry(page).click()
    await expect(adminNav(page)).toBeVisible()

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

    await page
      .getByLabel(copy('adminUi', 'audit.filters.action'), { exact: true })
      .fill('authn.login')
    await page
      .getByRole('button', {
        name: copy('adminUi', 'audit.filters.apply'),
        exact: true,
      })
      .click()

    const table = page.getByRole('table')
    const loginRow = table
      .getByRole('link', { name: 'authn.login', exact: true })
      .first()
      .locator('xpath=ancestor::tr[1]')
    await expect(loginRow).toBeVisible()
    const timeCell = loginRow.getByRole('cell').first()
    await expect(timeCell).toHaveText(/\d/)
    const rendered = ((await timeCell.textContent()) ?? '').trim()

    expect(
      renderedTimeIn(rendered, 'en-US', CHOSEN_ZONE, signInFrom, signInTo),
      `the audit time ${JSON.stringify(rendered)} is not the sign-in ` +
        `instant in ${CHOSEN_ZONE}`,
    ).toBe(true)
  })
})
