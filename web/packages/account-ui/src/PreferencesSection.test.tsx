/**
 * PreferencesSection behaviour: the language and timezone selects over
 * the profile self-write surface, GET/PATCH /auth/me.
 *
 * What this suite pins, per story: the stored profile values render into
 * the controls (including a stored timezone the engine's own list omits,
 * which must be added as its own option rather than silently displaying
 * the wrong one); a language change PATCHes the account FIRST and only a
 * confirmed write switches the instance (the manual slot and the account
 * move together), while a language CLEAR PATCHes the empty string,
 * leaves the device language where it was -- the empty string is not a
 * language to switch to -- and still refetches; a refused write renders
 * the two-line failure alert (save line plus the server's code text)
 * with the instance left on the old language; a timezone change and a
 * timezone CLEAR (the "not chosen" choice, whose value is the empty
 * string) PATCH the `time_zone` field; and a settled load that failed
 * renders the EmptyState error variant with a retry that refetches.
 * Every write's request body is pinned, so the PATCH contract (partial
 * update, empty string clears) is asserted from the wire up.
 */

import { describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { profileBody } from '../test-utils/fixtures.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { PreferencesSection } from './PreferencesSection.js'

const PROFILE_PATH = '/auth/me'

/** The language label the component computes for `tag` under `ui`, using
 * the same Intl.DisplayNames call (with the same raw-tag fallback), so
 * the assertion tracks the engine's own naming. */
function languageLabel(tag: string, ui: string): string {
  try {
    return new Intl.DisplayNames([ui], { type: 'language' }).of(tag) ?? tag
  } catch {
    return tag
  }
}

/** A stateful profile responder: GET answers the stored pair, PATCH merges
 * the partial body into it (absent field unchanged, empty string clears
 * -- the endpoint's contract) and answers 200. */
function makeProfileResponder(initial: { locale?: string; time_zone?: string }) {
  const stored: { locale?: string; time_zone?: string } = { ...initial }
  const respond: RealResponder = async (call: RealCall) => {
    if (call.path !== PROFILE_PATH) {
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    if (call.method === 'GET') {
      return jsonResponse(
        200,
        profileBody({ locale: stored.locale ?? '', time_zone: stored.time_zone ?? '' }),
      )
    }
    if (call.method === 'PATCH') {
      const patch = call.body as { locale?: string; time_zone?: string }
      if (patch.locale !== undefined) {
        stored.locale = patch.locale
      }
      if (patch.time_zone !== undefined) {
        stored.time_zone = patch.time_zone
      }
      return jsonResponse(200, {})
    }
    throw new Error(`unexpected ${call.method} ${call.path}`)
  }
  return { respond, stored }
}

/** Opens a MUI select and picks the option whose accessible name is
 * `optionName`, through the dispatched events the DataTable-pattern
 * suites use (mousedown opens the menu, the click on the rendered option
 * commits the choice). */
function chooseOption(combobox: HTMLElement, optionName: string): void {
  fireEvent.mouseDown(combobox)
  fireEvent.click(screen.getByRole('option', { name: optionName }))
}

/** Mounts the section over a session-free rig that plants the bearer the
 * profile read rides. */
function mountSection(respond: RealResponder) {
  const rig = makeRealClientRigWithoutSession(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(<PreferencesSection />)
  return { rig, ...rendered }
}

describe('PreferencesSection', () => {
  it('renders the stored profile values into the controls and passes axe', async () => {
    const { respond } = makeProfileResponder({
      locale: 'en-US',
      time_zone: 'Asia/Tokyo',
    })
    const { rig } = mountSection(respond)

    expect(
      await screen.findByRole('heading', { name: zhCN.preferences.title }),
    ).toBeTruthy()

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    expect(language).toHaveTextContent(languageLabel('en-US', 'zh-CN'))
    const timezone = screen.getByRole('combobox', {
      name: zhCN.preferences.timezone.label,
    })
    expect(timezone).toHaveTextContent('Asia/Tokyo')

    // Both controls' values come from the one profile read.
    expect(
      rig.calls.filter((call) => call.path === PROFILE_PATH),
    ).toHaveLength(1)

    await expectNoAxeViolations()
  })

  it('caps the preference selects so they do not span the whole content width', async () => {
    const { respond } = makeProfileResponder({})
    mountSection(respond)

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    const timezone = screen.getByRole('combobox', {
      name: zhCN.preferences.timezone.label,
    })
    // The width cap sits on the field root (the combobox is the inner
    // input); both controls are limited to a comfortable select width
    // (24rem = 384px) rather than stretching across the page.
    for (const control of [language, timezone]) {
      const root = control.closest('.MuiFormControl-root')
      expect(root).not.toBeNull()
      expect(root).toHaveStyle({ maxWidth: '384px' })
    }
  })

  it('floats both select labels above the not-chosen placeholder', async () => {
    // A fresh account holds no preference, so both selects render the "not
    // chosen" placeholder -- the state in which MUI leaves the InputLabel
    // unshrunk and it overlaps the placeholder text. Both labels must be
    // shrunk (floated) so the label and the value never occupy one line.
    const { respond } = makeProfileResponder({})
    mountSection(respond)

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    const timezone = screen.getByRole('combobox', {
      name: zhCN.preferences.timezone.label,
    })
    // The placeholder really is showing: this is the overlap-prone state.
    expect(language).toHaveTextContent(zhCN.preferences.language.notChosen)
    expect(timezone).toHaveTextContent(zhCN.preferences.timezone.notChosen)

    for (const control of [language, timezone]) {
      // The InputLabel is the element the combobox's aria-labelledby points
      // at; it must carry the shrink class so it floats above the value.
      const labelId = control.getAttribute('aria-labelledby')
      expect(labelId).toBeTruthy()
      const label = document.getElementById(labelId as string)
      expect(label).toHaveClass('MuiInputLabel-root')
      expect(label).toHaveClass('MuiInputLabel-shrink')
    }
  })

  it('PATCHes the language first, then switches the instance (manual slot and account move together)', async () => {
    const { respond, stored } = makeProfileResponder({})
    const { rig, i18n } = mountSection(respond)

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    // A fresh account's empty locale reads as the "not chosen" choice.
    expect(language).toHaveTextContent(zhCN.preferences.language.notChosen)

    chooseOption(language, languageLabel('en-US', 'zh-CN'))

    await waitFor(() => expect(i18n.language).toBe('en-US'))
    expect(stored.locale).toBe('en-US')
    const patch = rig.calls.find(
      (call) => call.method === 'PATCH' && call.path === PROFILE_PATH,
    )
    expect(patch).toBeDefined()
    // The body is the partial update the endpoint documents: only the
    // changed field, under its wire name.
    expect(patch?.body).toEqual({ locale: 'en-US' })
    // The UI switched with the account: the heading now speaks en-US.
    expect(
      await screen.findByRole('heading', { name: enUS.preferences.title }),
    ).toBeTruthy()
    // The landed write confirms itself in the status region (resolved in the
    // now-current language), rather than succeeding silently.
    expect(await screen.findByRole('status')).toHaveTextContent(
      enUS.preferences.saved,
    )
  })

  it('clears the language preference without switching the device language', async () => {
    const { respond, stored } = makeProfileResponder({ locale: 'en-US' })
    const { rig, i18n } = mountSection(respond)

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    expect(language).toHaveTextContent(languageLabel('en-US', 'zh-CN'))

    chooseOption(language, zhCN.preferences.language.notChosen)

    // The clear reaches the account as the empty string, and the
    // confirmed write still refetches the stored profile.
    await waitFor(() => expect(stored.locale).toBe(''))
    const patch = rig.calls.find(
      (call) => call.method === 'PATCH' && call.path === PROFILE_PATH,
    )
    expect(patch?.body).toEqual({ locale: '' })
    await waitFor(() =>
      expect(
        rig.calls.filter(
          (call) => call.method === 'GET' && call.path === PROFILE_PATH,
        ),
      ).toHaveLength(2),
    )

    // No failure alert renders and the device keeps its current
    // language: the empty string is not a language to switch to, so a
    // successful clear must not be reported as a failed save.
    expect(screen.queryByRole('alert')).toBeNull()
    expect(i18n.language).toBe('zh-CN')
    await waitFor(() =>
      expect(language).toHaveTextContent(zhCN.preferences.language.notChosen),
    )
  })

  it('renders the two-line failure alert and does not switch when the language write is refused', async () => {
    const respond: RealResponder = async (call) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody())
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        return errorResponse(400, 'authn.bad_request')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { i18n } = mountSection(respond)

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    chooseOption(language, languageLabel('en-US', 'zh-CN'))

    const alert = await screen.findByRole('alert')
    // Line one says what happened; line two is the server's own code text.
    expect(alert).toHaveTextContent(zhCN.preferences.saveFailed)
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    // A refused write must not move the device language.
    expect(i18n.language).toBe('zh-CN')
  })

  // This test's cost is the engine's own timezone list: ~418 zones
  // rendered into the open select menu twice (the pick and the clear) and
  // two option queries by accessible name over the whole list; the
  // explicit 20s budget covers the loaded-runner cost of that
  // render-heavy class with headroom.
  it('PATCHes a timezone choice and the cleared empty string under time_zone', async () => {
    const { respond, stored } = makeProfileResponder({
      time_zone: 'Asia/Tokyo',
    })
    const { rig } = mountSection(respond)

    const timezone = await screen.findByRole('combobox', {
      name: zhCN.preferences.timezone.label,
    })
    expect(timezone).toHaveTextContent('Asia/Tokyo')

    // A new zone: the PATCH carries it alone, under the profile's own
    // field name.
    chooseOption(timezone, 'Europe/Berlin')
    await waitFor(() => expect(stored.time_zone).toBe('Europe/Berlin'))
    let patches = rig.calls.filter((call) => call.method === 'PATCH')
    expect(patches[0]?.body).toEqual({ time_zone: 'Europe/Berlin' })

    // Back to "not chosen": the clearing value is the empty string.
    chooseOption(
      screen.getByRole('combobox', {
        name: zhCN.preferences.timezone.label,
      }),
      zhCN.preferences.timezone.notChosen,
    )
    await waitFor(() => expect(stored.time_zone).toBe(''))
    patches = rig.calls.filter((call) => call.method === 'PATCH')
    expect(patches[1]?.body).toEqual({ time_zone: '' })
  }, 20000)

  it('adds a stored timezone the engine list omits as its own option', async () => {
    // The engine's list is narrowed for this test: the stored value is
    // not in it, and the control must still offer (and show) it -- a
    // select whose value matches no option would silently display
    // another zone.
    const supportedValues = vi
      .spyOn(Intl, 'supportedValuesOf')
      .mockReturnValue(['UTC'])
    try {
      const { respond } = makeProfileResponder({ time_zone: 'Legacy/Zone' })
      mountSection(respond)

      const timezone = await screen.findByRole('combobox', {
        name: zhCN.preferences.timezone.label,
      })
      expect(timezone).toHaveTextContent('Legacy/Zone')

      fireEvent.mouseDown(timezone)
      expect(screen.getByRole('option', { name: 'Legacy/Zone' })).toBeTruthy()
      expect(screen.getByRole('option', { name: 'UTC' })).toBeTruthy()
    } finally {
      supportedValues.mockRestore()
    }
  })

  it('renders the error state with a retry that refetches when the load fails', async () => {
    const user = userEvent.setup()
    let failLoad = true
    const respond: RealResponder = async (call) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        if (failLoad) {
          return errorResponse(503, 'client.network')
        }
        return jsonResponse(200, profileBody({ time_zone: 'Asia/Tokyo' }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)

    expect(
      await screen.findByText(zhCN.preferences.error.title),
    ).toBeTruthy()
    expect(
      screen.queryByRole('combobox', {
        name: zhCN.preferences.language.label,
      }),
    ).toBeNull()

    failLoad = false
    await user.click(screen.getByRole('button', { name: zhCN.preferences.retry }))
    expect(
      await screen.findByRole('combobox', {
        name: zhCN.preferences.timezone.label,
      }),
    ).toHaveTextContent('Asia/Tokyo')
  })
})
