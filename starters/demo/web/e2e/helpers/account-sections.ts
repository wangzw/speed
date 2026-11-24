import { expect, type Locator, type Page } from '@playwright/test'

import { copy, type CopyLocale } from './i18n'

export type AccountSection =
  | 'profile'
  | 'security'
  | 'sessions'
  | 'preferences'

const ACCOUNT_SECTION_PATHS: Readonly<Record<AccountSection, string>> = {
  profile: '/account/profile',
  security: '/account/security',
  sessions: '/account/sessions',
  preferences: '/account/preferences',
}

function paneHeading(
  page: Page,
  section: AccountSection,
  locale: CopyLocale,
): Locator {
  return page.getByRole('heading', {
    level: 1,
    name: copy('demo', `account.sections.${section}`, {}, locale),
    exact: true,
  })
}

// The preferences item reuses userMenu.preferences, which can differ from the pane heading.
const ACCOUNT_SECTION_MENU_KEYS: Readonly<Record<AccountSection, string>> = {
  profile: 'account.sections.profile',
  security: 'account.sections.security',
  sessions: 'account.sections.sessions',
  preferences: 'userMenu.preferences',
}

export async function selectAccountSection(
  page: Page,
  section: AccountSection,
  locale: CopyLocale = 'en-US',
): Promise<void> {
  const column = page.getByRole('complementary', {
    name: copy('layoutKit', 'appShell.sidebarRegionLabel', {}, locale),
    exact: true,
  })
  await column.getByTestId('account-entry-trigger').click()
  await page
    .getByRole('menuitem', {
      name: copy('demo', ACCOUNT_SECTION_MENU_KEYS[section], {}, locale),
      exact: true,
    })
    .click()
  await expect(paneHeading(page, section, locale)).toBeVisible()
}

// A card's body mounts only while expanded, so every read of such a body follows this click.
export async function expandAccountRows(
  page: Page,
  keys?: readonly string[],
  locale: CopyLocale = 'en-US',
): Promise<void> {
  await page.getByRole('heading', { level: 1 }).first().waitFor()
  // The pane's controls live in the main landmark; navigation disclosure toggles sit outside it.
  const main = page.getByRole('main')
  const actions: readonly Locator[] =
    keys === undefined
      ? await main.locator('button[aria-expanded]').all()
      : keys.map((key) => main.getByTestId(key))
  for (const action of actions) {
    if ((await action.getAttribute('aria-expanded')) !== 'false') {
      continue
    }
    await action.click()
    await expect(action).toHaveAttribute('aria-expanded', 'true')
  }
  // `locale` stays for call-site parity; these controls are addressed by state and test id.
  void locale
}

export async function openAccountSection(
  page: Page,
  section: AccountSection,
  locale: CopyLocale = 'en-US',
): Promise<void> {
  await page.goto(
    new URL(ACCOUNT_SECTION_PATHS[section], page.url()).toString(),
  )
  await expect(paneHeading(page, section, locale)).toBeVisible()
}
