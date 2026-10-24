/**
 * J6 -- a signed-in account holder personalizes the interface's language and
 * time zone, and the choices survive a reload.
 *
 * The account is the review assembly's seeded member: an ordinary active
 * member with no platform permission, whose profile holds no language and no
 * time zone yet. The browser context is pinned to en-US and UTC by the
 * Playwright config, so the starting interface is English and the starting
 * clock zone is the device's UTC; the journey then chooses a different
 * language (zh-CN) and a different zone (Asia/Tokyo) and observes the change.
 *
 * What the browser must show, step by step: both preference controls start on
 * their explicit "not chosen" choice, and the time zone list keeps that choice
 * beside the available zones; choosing zh-CN writes the account first and
 * switches the interface at once -- page copy, navigation, the browser tab
 * title and `<html lang>`, with no reload -- and the confirmation beneath the
 * control names the language in the language just chosen; choosing
 * Asia/Tokyo confirms by name and the control holds the zone; the session
 * list's current row then renders its sign-in time in Asia/Tokyo and in
 * zh-CN's own date and time format; a full page reload keeps the zh-CN
 * interface even though the browser prefers en-US, shows the account's two
 * stored values in the controls (read back from the server, not from the
 * device slot), and still renders the session time in the chosen zone.
 *
 * The session time is the one value whose text the spec cannot read from a
 * bundle: it is an instant the server stamps when the session is created during
 * this test. The spec therefore captures the wall-clock window around sign-in
 * and derives the set of strings Intl.DateTimeFormat produces for those
 * instants in zh-CN and Asia/Tokyo -- the same call, language and zone the
 * SessionsSection makes -- then asserts the rendered line is one of them. A
 * renderer that ignored the stored zone would fall nine hours outside the set,
 * and one that ignored the interface language would not match zh-CN's format
 * at all. The "last active" line shares the same formatter, but the section
 * renders it only when it differs from the sign-in time; the session's
 * last-seen is its creation instant until the first coalesced touch (the
 * review configuration leaves the default one-minute interval), so this
 * journey observes the sign-in line alone.
 *
 * REQ-account-004, REQ-account-034, REQ-account-058, REQ-presentation-005 and
 * REQ-presentation-006 are listed by the journey document but deliberately
 * absent from `@covers`: they describe a refused write, a cleared preference,
 * a step-up replay and the write-then-switch ordering, none of which the main
 * success path can observe. The registry keeps the document's full list.
 * REQ-security-022 is claimed only as far as this surface renders it: the
 * session list, its current-session marker and the sign-in time. The same
 * requirement's expiry field is not on this page, and the device summary's
 * text is derived from the user agent, so neither is asserted here.
 */
// @journey journey-personalize-interface.md
// @covers REQ-account-003, REQ-account-033, REQ-presentation-003, REQ-presentation-004, REQ-presentation-007, REQ-presentation-008, REQ-presentation-010, REQ-presentation-030, REQ-security-022
import type { Locator, Page } from '@playwright/test'

import { reviewMember } from '../fixtures/config'
import { expect, test } from '../fixtures/demo'
import { copy, copyPattern, type CopyLocale } from '../helpers/i18n'

// The seeded member holds no platform permission and starts with no stored
// language or time zone: the template's seed carries the operator alone, so
// this journey asks the fixture to seed the member.
test.use({ seedMember: true })

/** The interface language chosen by the journey; the browser starts en-US. */
const CHOSEN_LOCALE: CopyLocale = 'zh-CN'

/** The time zone chosen by the journey; the browser's device zone is UTC. */
const CHOSEN_ZONE = 'Asia/Tokyo'

/**
 * Slack around the sign-in window for a clock that is a second or two off
 * between this process and the server. The width only widens the set of
 * accepted renderings; it cannot make a wrong zone match, because the set is
 * still computed in the chosen zone.
 */
const CLOCK_SKEW_MS = 2_000

/** The language's own name in a UI language, exactly as the product asks Intl. */
function languageName(tag: string, uiLanguage: string): string {
  return new Intl.DisplayNames([uiLanguage], { type: 'language' }).of(tag) ?? tag
}

/** The account page's preferences section, named in the given UI language. */
function preferencesSection(page: Page, locale: CopyLocale): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'preferences.title', {}, locale),
    exact: true,
  })
}

/** The language select of the preferences section. */
function languageSelect(page: Page, locale: CopyLocale): Locator {
  return preferencesSection(page, locale).getByRole('combobox', {
    name: copy('accountUi', 'preferences.language.label', {}, locale),
    exact: true,
  })
}

/** The time zone select of the preferences section. */
function timeZoneSelect(page: Page, locale: CopyLocale): Locator {
  return preferencesSection(page, locale).getByRole('combobox', {
    name: copy('accountUi', 'preferences.timezone.label', {}, locale),
    exact: true,
  })
}

/** The account page's sessions section, named in the given UI language. */
function sessionsSection(page: Page, locale: CopyLocale): Locator {
  return page.getByRole('region', {
    name: copy('accountUi', 'sessions.title', {}, locale),
    exact: true,
  })
}

/** The matcher for the current row's sign-in line in the given UI language. */
function signedInLinePattern(locale: CopyLocale): RegExp {
  // The pattern carries its own capture group: `copyPattern` splices the
  // caller's pattern into the bundle's sentence verbatim, so the group that
  // yields the time is the caller's to write.
  return copyPattern('accountUi', 'sessions.signedIn', { time: '(.+)' }, locale)
}

/**
 * The instant strings the product would render for any wall-clock instant in
 * [from, to], in `language` and `timeZone`.
 *
 * The product formats through Intl.DateTimeFormat with the default medium
 * style (medium date, short time). This mirrors that call rather than
 * assembling the text by hand, so the assertion is about which instant and
 * which zone the surface rendered, not about a literal the test invented.
 */
function renderingsIn(
  language: string,
  timeZone: string,
  from: number,
  to: number,
): ReadonlySet<string> {
  const formatter = new Intl.DateTimeFormat(language, {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone,
  })
  const seen = new Set<string>()
  for (let instant = from; instant <= to; instant += 1_000) {
    seen.add(formatter.format(new Date(instant)))
  }
  return seen
}

/**
 * The rendered sign-in time of the current session row, read from the line the
 * section renders. The row is located by its current-session marker so a
 * change in row order cannot point this at another device.
 */
async function currentSessionSignedInTime(
  page: Page,
  locale: CopyLocale,
): Promise<string> {
  const section = sessionsSection(page, locale)
  await section.scrollIntoViewIfNeeded()
  const current = section
    .getByRole('listitem')
    .filter({
      has: page.getByText(copy('accountUi', 'sessions.current', {}, locale), {
        exact: true,
      }),
    })
  await expect(current).toHaveCount(1)
  const line = current.getByText(signedInLinePattern(locale))
  await expect(line).toHaveCount(1)
  const text = ((await line.textContent()) ?? '').trim()
  const match = signedInLinePattern(locale).exec(text)
  if (match?.[1] === undefined) {
    throw new Error(`the sign-in line read no time: ${JSON.stringify(text)}`)
  }
  return match[1]
}

test('personalize-interface: the chosen language and time zone take effect and survive a reload', async ({
  demo,
}) => {
  const { page, accounts } = demo

  // The window the session's creation instant falls in: taken around the one
  // sign-in this journey performs, and used later to recognize the rendered
  // time as this session's own instant in the chosen zone.
  let signInFrom = 0
  let signInTo = 0

  await test.step('the member signs in and opens the account settings page', async () => {
    // The precondition is an enabled, signed-in member with no stored
    // preference. The browser context is en-US/UTC, so the starting interface
    // and device zone are the ones the journey moves away from.
    signInFrom = Date.now() - CLOCK_SKEW_MS
    await accounts.signIn({
      identifier: reviewMember.identifier,
      password: reviewMember.password,
    })
    signInTo = Date.now() + CLOCK_SKEW_MS
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'home.heading'),
        exact: true,
      }),
    ).toBeVisible()

    await page
      .getByRole('link', {
        name: copy('demo', 'nav.account'),
        exact: true,
      })
      .click()
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'account.title'),
        exact: true,
      }),
    ).toBeVisible()
  })

  await test.step('step 1: both preference controls start on the explicit "not chosen" choice', async () => {
    const section = preferencesSection(page, 'en-US')
    await section.scrollIntoViewIfNeeded()
    await expect(section).toBeVisible()

    // An account with no stored preference renders the empty-string choice,
    // which the controls display as their own "not chosen" label.
    await expect(languageSelect(page, 'en-US')).toHaveText(
      copy('accountUi', 'preferences.language.notChosen'),
    )
    await expect(timeZoneSelect(page, 'en-US')).toHaveText(
      copy('accountUi', 'preferences.timezone.notChosen'),
    )

    // The time zone list carries the engine's available zones and always keeps
    // the explicit "not chosen" choice beside them.
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
    // The option is named in the language the interface is currently in, which
    // is what the control renders before the switch.
    await languageSelect(page, 'en-US').click()
    await page
      .getByRole('option', {
        name: languageName(CHOSEN_LOCALE, 'en-US'),
        exact: true,
      })
      .click()

    // The confirmation beneath the control names the preference and is
    // rendered in the language just chosen, because the write's success text
    // is translated against the target language.
    const section = preferencesSection(page, CHOSEN_LOCALE)
    await expect(
      section.getByText(
        copy(
          'accountUi',
          'preferences.language.saved',
          { value: languageName(CHOSEN_LOCALE, CHOSEN_LOCALE) },
          CHOSEN_LOCALE,
        ),
        { exact: true },
      ),
    ).toBeVisible()

    // No reload: the page heading, navigation, browser tab title and document
    // language all follow the switch in place.
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'account.title', {}, CHOSEN_LOCALE),
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByRole('link', {
        name: copy('demo', 'nav.account', {}, CHOSEN_LOCALE),
        exact: true,
      }),
    ).toBeVisible()
    await expect(page).toHaveTitle(
      copy('demo', 'app.title', {}, CHOSEN_LOCALE),
    )
    await expect(page.locator('html')).toHaveAttribute('lang', CHOSEN_LOCALE)

    // The control holds the stored value, named in the new interface language.
    await expect(languageSelect(page, CHOSEN_LOCALE)).toHaveText(
      languageName(CHOSEN_LOCALE, CHOSEN_LOCALE),
    )
  })

  await test.step('step 3: choosing a time zone saves it and confirms the zone by name', async () => {
    const section = preferencesSection(page, CHOSEN_LOCALE)
    const timeZone = timeZoneSelect(page, CHOSEN_LOCALE)
    await expect(timeZone).toHaveText(
      copy('accountUi', 'preferences.timezone.notChosen', {}, CHOSEN_LOCALE),
    )

    await timeZone.click()
    await page
      .getByRole('option', { name: CHOSEN_ZONE, exact: true })
      .click()

    // The success text names the zone and sits beneath the control.
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

    // The explicit "not chosen" choice is still offered after a value is
    // stored, alongside the available zones.
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
    const section = sessionsSection(page, CHOSEN_LOCALE)
    await section.scrollIntoViewIfNeeded()
    await expect(section).toBeVisible()
    await expect(
      section.getByText(copy('accountUi', 'sessions.current', {}, CHOSEN_LOCALE), {
        exact: true,
      }),
    ).toBeVisible()

    const rendered = await currentSessionSignedInTime(page, CHOSEN_LOCALE)
    const expected = renderingsIn(
      CHOSEN_LOCALE,
      CHOSEN_ZONE,
      signInFrom,
      signInTo,
    )
    // The interface is zh-CN, so the line's label and the instant's format are
    // zh-CN's; the instant is the session's creation, inside the sign-in
    // window; the zone is the one just stored. A renderer that used the
    // device's UTC would fall outside this set.
    expect(
      expected.has(rendered),
      `the session time ${JSON.stringify(rendered)} is not ${CHOSEN_ZONE} ` +
        `in ${CHOSEN_LOCALE}: expected one of ${JSON.stringify([...expected])}`,
    ).toBe(true)
  })

  await test.step('step 5: a reload keeps the language, the stored preferences and the zone-rendered time', async () => {
    await page.reload()

    // The interface is still the manually chosen language, not the browser's
    // preferred en-US: the device slot outranks the navigator tier on reload.
    await expect(
      page.getByRole('heading', {
        level: 1,
        name: copy('demo', 'account.title', {}, CHOSEN_LOCALE),
        exact: true,
      }),
    ).toBeVisible()
    await expect(page).toHaveTitle(
      copy('demo', 'app.title', {}, CHOSEN_LOCALE),
    )
    await expect(page.locator('html')).toHaveAttribute('lang', CHOSEN_LOCALE)
    await expect(
      page.getByRole('link', {
        name: copy('demo', 'nav.account', {}, CHOSEN_LOCALE),
        exact: true,
      }),
    ).toBeVisible()

    // The controls read the account's stored values back from the server: the
    // language select shows the chosen language (not the "not chosen" choice a
    // failed write would leave), and the time zone select shows the zone.
    await expect(languageSelect(page, CHOSEN_LOCALE)).toHaveText(
      languageName(CHOSEN_LOCALE, CHOSEN_LOCALE),
    )
    await expect(timeZoneSelect(page, CHOSEN_LOCALE)).toHaveText(
      CHOSEN_ZONE,
    )

    // The session row's sign-in time still renders in the stored zone and the
    // interface language; the session outlived the page's reload.
    const rendered = await currentSessionSignedInTime(page, CHOSEN_LOCALE)
    expect(
      renderingsIn(CHOSEN_LOCALE, CHOSEN_ZONE, signInFrom, signInTo).has(
        rendered,
      ),
      `after reload the session time ${JSON.stringify(rendered)} left ${CHOSEN_ZONE}`,
    ).toBe(true)
  })
})
