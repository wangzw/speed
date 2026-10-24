/**
 * PreferencesSection behaviour: the language and timezone selects over
 * the profile self-write surface, GET/PATCH /api/v1/auth/me.
 *
 * What this suite pins, per story: the stored profile values render into
 * the controls (including a stored timezone the engine's own list omits,
 * which must be added as its own option rather than silently displaying
 * the wrong one); a language change PATCHes the account FIRST and only a
 * confirmed write switches the instance (the manual slot and the account
 * move together), while a language CLEAR PATCHes the empty string,
 * leaves the device language where it was -- the empty string is not a
 * language to switch to -- and still refetches; each select owns a
 * feedback scope whose region sits beneath that select, so a refusal
 * renders there and one control's settled outcome never clears the
 * other's; a timezone change and a timezone CLEAR (the "not chosen"
 * choice, whose value is the empty string) PATCH the `time_zone` field;
 * and a settled load that failed renders the EmptyState error variant
 * with a retry that refetches. Every write's request body is pinned, so
 * the PATCH contract (partial update, empty string clears) is asserted
 * from the wire up.
 */

import { describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { formControlClasses } from '@mui/material/FormControl'
import { inputLabelClasses } from '@mui/material/InputLabel'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { profileBody } from '../test-utils/fixtures.js'
import { renderWithProviders } from '../test-utils/render.js'
import { captureRefreshWarning } from '../test-utils/refresh-warning.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { PreferencesSection } from './PreferencesSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const FACTORS_PATH = '/api/v1/auth/factors'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

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

/** The control group of one select: the field root plus its own feedback
 * region, which renders directly beneath the field. */
function groupOf(combobox: HTMLElement): HTMLElement {
  const root = combobox.closest(`.${formControlClasses.root}`)
  if (root?.parentElement == null) {
    throw new Error('the select is not inside a control group')
  }
  return root.parentElement
}

/** The standing polite live region of one control group's scope. */
function regionOf(group: HTMLElement): HTMLElement {
  return within(group).getByRole('status')
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
      const root = control.closest(`.${formControlClasses.root}`)
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
      expect(label).toHaveClass(inputLabelClasses.root)
      expect(label).toHaveClass(inputLabelClasses.shrink)
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
    // The landed write confirms itself in the language scope's region below
    // the select, naming the control and the chosen value; the message was
    // translated after the instance switched.
    const currentLanguage = screen.getByRole('combobox', {
      name: enUS.preferences.language.label,
    })
    const languageStatus = regionOf(groupOf(currentLanguage))
    await waitFor(() =>
      expect(languageStatus).toHaveTextContent(
        enUS.preferences.language.saved.replace(
          '{{value}}',
          languageLabel('en-US', 'en-US'),
        ),
      ),
    )
    expect(enUS.preferences.language.saved).toContain(
      enUS.preferences.language.label,
    )
    expect(
      currentLanguage.compareDocumentPosition(languageStatus) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('keeps one control scope outcome when the other control settles later', async () => {
    const { respond } = makeProfileResponder({})
    mountSection(respond)

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    chooseOption(language, languageLabel('en-US', 'zh-CN'))

    // The language write switches the instance, so its confirmation and the
    // page now speak en-US.
    const languageSaved = enUS.preferences.language.saved.replace(
      '{{value}}',
      languageLabel('en-US', 'en-US'),
    )
    await waitFor(() =>
      expect(
        regionOf(
          groupOf(
            screen.getByRole('combobox', {
              name: enUS.preferences.language.label,
            }),
          ),
        ),
      ).toHaveTextContent(languageSaved),
    )

    // A later timezone write settles in its own scope; the language scope's
    // outcome stays exactly as it was.
    const timezone = screen.getByRole('combobox', {
      name: enUS.preferences.timezone.label,
    })
    chooseOption(timezone, 'Europe/Berlin')
    await waitFor(() =>
      expect(regionOf(groupOf(timezone))).toHaveTextContent(
        enUS.preferences.timezone.saved.replace('{{value}}', 'Europe/Berlin'),
      ),
    )
    expect(
      regionOf(
        groupOf(
          screen.getByRole('combobox', {
            name: enUS.preferences.language.label,
          }),
        ),
      ),
    ).toHaveTextContent(languageSaved)
    expect(screen.queryByRole('alert')).toBeNull()
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
    // The clearing write confirms itself too, naming the control and the
    // "not chosen" value.
    await waitFor(() =>
      expect(regionOf(groupOf(language))).toHaveTextContent(
        zhCN.preferences.language.saved.replace(
          '{{value}}',
          zhCN.preferences.language.notChosen,
        ),
      ),
    )
  })

  it('renders the refusal in the language scope and leaves the other scope untouched', async () => {
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
    // The server's own code text, in the same place a success lands: the
    // language scope's region beneath the select.
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    expect(groupOf(language)).toContainElement(alert)
    expect(
      language.compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    // The timezone control's own scope stays untouched.
    const timezone = screen.getByRole('combobox', {
      name: zhCN.preferences.timezone.label,
    })
    expect(regionOf(groupOf(timezone))).toHaveTextContent('')
    // A refused write must not move the device language.
    expect(i18n.language).toBe('zh-CN')
  })

  it('PATCHes a timezone choice under time_zone', async () => {
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
    // The timezone scope confirms beneath its own select, naming the control
    // and the chosen zone.
    await waitFor(() =>
      expect(regionOf(groupOf(timezone))).toHaveTextContent(
        zhCN.preferences.timezone.saved.replace('{{value}}', 'Europe/Berlin'),
      ),
    )
    expect(zhCN.preferences.timezone.saved).toContain(
      zhCN.preferences.timezone.label,
    )
    const patches = rig.calls.filter((call) => call.method === 'PATCH')
    expect(patches).toHaveLength(1)
    expect(patches[0]?.body).toEqual({ time_zone: 'Europe/Berlin' })
  })

  it('PATCHes the cleared empty string under time_zone', async () => {
    const { respond, stored } = makeProfileResponder({
      time_zone: 'Asia/Tokyo',
    })
    const { rig } = mountSection(respond)

    const timezone = await screen.findByRole('combobox', {
      name: zhCN.preferences.timezone.label,
    })
    expect(timezone).toHaveTextContent('Asia/Tokyo')

    // Back to "not chosen": the clearing value is the empty string.
    chooseOption(timezone, zhCN.preferences.timezone.notChosen)
    await waitFor(() => expect(stored.time_zone).toBe(''))
    // The clearing write confirms in the timezone scope, naming the control
    // and the "not chosen" value.
    await waitFor(() =>
      expect(regionOf(groupOf(timezone))).toHaveTextContent(
        zhCN.preferences.timezone.saved.replace(
          '{{value}}',
          zhCN.preferences.timezone.notChosen,
        ),
      ),
    )
    const patches = rig.calls.filter((call) => call.method === 'PATCH')
    expect(patches).toHaveLength(1)
    expect(patches[0]?.body).toEqual({ time_zone: '' })
  })

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

  it('gates a step_up_required write behind the shared dialog and replays it once', async () => {
    captureRefreshWarning()
    let elevated = false
    let storedLocale = ''
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ locale: storedLocale }))
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        storedLocale = (call.body as { locale?: string }).locale ?? ''
        return jsonResponse(200, {})
      }
      if (call.method === 'GET' && call.path === FACTORS_PATH) {
        return jsonResponse(200, {
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'App',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number }
        if (body.seq === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'second',
            next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
          })
        }
        elevated = true
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    const { i18n } = renderWithProviders(
      <PreferencesSection session={rig.session} />,
    )
    const user = userEvent.setup()

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    chooseOption(language, languageLabel('en-US', 'zh-CN'))

    // The 401 opens the shared dialog; winning it replays the refused
    // write exactly once.
    await user.click(await screen.findByRole('button', { name: 'App' }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() => expect(storedLocale).toBe('en-US'))
    expect(i18n.language).toBe('en-US')
    const patches = rig.calls.filter(
      (call) => call.method === 'PATCH' && call.path === PROFILE_PATH,
    )
    expect(patches).toHaveLength(2)
    expect(patches[1]?.body).toEqual({ locale: 'en-US' })
  })

  it('keeps the failure banner when a step-up refusal arrives without a session', async () => {
    // The section cannot open a dialog it has no session to drive: the
    // refusal renders the code's text and the host decides how to elevate.
    const respond: RealResponder = async (call) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody())
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        return errorResponse(401, 'authn.insufficient_user_authentication')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    chooseOption(language, languageLabel('en-US', 'zh-CN'))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(
      zhCN.errors.authn.insufficient_user_authentication,
    )
    expect(groupOf(language)).toContainElement(alert)
    expect(screen.queryByRole('dialog')).toBeNull()
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
