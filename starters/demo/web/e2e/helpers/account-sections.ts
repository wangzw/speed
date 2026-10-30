/**
 * Navigating the account settings pages.
 *
 * The account settings is four pane pages, one per route: the profile, the
 * security pane, the sessions pane and the preferences pane. The tenant
 * navigation nests their links under the account entry, so a journey opens a
 * pane either by its address or through that entry, and a pane is on screen
 * once its own heading (the page's h1) is.
 *
 * Inside a pane the facts stand in cards headed by an h2: the identity, the
 * email address and the phone number on the profile pane; the password, the
 * sign-in ways, the second step and the social accounts on the security pane;
 * the devices on the sessions pane; the appearance and preferences on the
 * preferences pane. Only the password card is collapsible -- its body mounts
 * after the row's action is clicked -- so a journey that reads the password
 * surface first expands it. `expandAccountRows` does that by clicking the
 * collapsed control's own action, never by writing the rendered state.
 *
 * The pane paths mirror the host's own route constants (src/paths.ts's
 * ACCOUNT_SECTION_PATHS). They are kept here, beside the helper, because the
 * e2e tier runs outside the app's build and must not import its sources.
 */
import { expect, type Locator, type Page } from '@playwright/test'

import { copy, type CopyLocale } from './i18n'

/** The panes the account settings offers, one page each, in its order. */
export type AccountSection =
  | 'profile'
  | 'security'
  | 'sessions'
  | 'preferences'

/** Each pane's page path, mirroring the host's ACCOUNT_SECTION_PATHS. */
const ACCOUNT_SECTION_PATHS: Readonly<Record<AccountSection, string>> = {
  profile: '/account/profile',
  security: '/account/security',
  sessions: '/account/sessions',
  preferences: '/account/preferences',
}

/** The pane's own heading, the page's one h1. */
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

/**
 * Select one pane on the account page already on screen: click its entry in
 * the tenant navigation and wait for the pane's own heading to arrive. The
 * account entry's disclosure is open on arrival; a closed one keeps its child
 * links in the document but hidden, so the entry is opened first when the
 * target link is not visible.
 */
export async function selectAccountSection(
  page: Page,
  section: AccountSection,
  locale: CopyLocale = 'en-US',
): Promise<void> {
  const nav = page.getByRole('navigation', {
    name: copy('layoutKit', 'appShell.navLabel', {}, locale),
    exact: true,
  })
  const entry = nav.getByRole('link', {
    name: copy('demo', `account.sections.${section}`, {}, locale),
    exact: true,
  })
  if (!(await entry.isVisible())) {
    await nav
      .getByRole('button', {
        name: copy('demo', 'nav.account', {}, locale),
        exact: true,
      })
      .click()
  }
  await entry.click()
  await expect(paneHeading(page, section, locale)).toBeVisible()
}

/**
 * Expand the collapsible cards whose bodies a journey is about to read or
 * drive.
 *
 * The account page mounts a card's body only while the card is expanded, so
 * every read of such a body is preceded by this click. With no keys every
 * collapsed control in the pane is used, which is enough when a step reads the
 * whole pane; with keys only those test ids are opened, when a step must leave
 * the others closed. The one collapsible card today is the password card
 * (`security-row-password`); the other cards mount their bodies with the pane.
 */
export async function expandAccountRows(
  page: Page,
  keys?: readonly string[],
  locale: CopyLocale = 'en-US',
): Promise<void> {
  // A page opened by URL renders after the navigation returned; wait for the
  // pane's heading before counting its controls, so a call right after a
  // reload or a direct visit does not read an empty document.
  await page.getByRole('heading', { level: 1 }).first().waitFor()
  // The pane's controls live in the main landmark; the navigation's own
  // disclosure toggles sit outside it and are never opened here.
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
  // `locale` stays in the signature for call-site parity; the controls above
  // are addressed by state and test id, which the interface's language does
  // not rename.
  void locale
}

/**
 * Open the account settings at one pane, by the pane's own address. The page
 * is reached the way a bookmark or a shared link reaches it, outside the
 * tenant navigation, and the pane's heading is the arrival proof.
 */
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
