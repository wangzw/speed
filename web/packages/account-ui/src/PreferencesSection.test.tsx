/**
 * PreferencesSection checks the language and timezone selects and their PATCH bodies.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { formControlClasses } from '@mui/material/FormControl'
import { inputLabelClasses } from '@mui/material/InputLabel'
import { languageDisplayName } from '@speed/i18n'
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

const PROFILE_PATH = '/api/v1/auth/me'

function languageLabel(tag: string, ui: string): string {
  return languageDisplayName(tag, ui, ['zh-CN', 'en-US'])
}

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

function chooseOption(combobox: HTMLElement, optionName: string): void {
  fireEvent.mouseDown(combobox)
  fireEvent.click(screen.getByRole('option', { name: optionName }))
}

function groupOf(combobox: HTMLElement): HTMLElement {
  const root = combobox.closest(`.${formControlClasses.root}`)
  if (root?.parentElement == null) {
    throw new Error('the select is not inside a control group')
  }
  return root.parentElement
}

function regionOf(group: HTMLElement): HTMLElement {
  return within(group).getByRole('status')
}

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

    expect(
      rig.calls.filter((call) => call.path === PROFILE_PATH),
    ).toHaveLength(1)

    await expectNoAxeViolations()
  })

  it('marks the page behind an open language menu inert', async () => {
    const { respond } = makeProfileResponder({})
    const { container } = mountSection(respond)

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    expect(container).not.toHaveAttribute('inert')

    fireEvent.mouseDown(language)
    expect(screen.getAllByRole('option').length).toBeGreaterThan(1)
    expect(container).toHaveAttribute('inert')
  })

  it('marks the page behind an open timezone menu inert', async () => {
    const { respond } = makeProfileResponder({})
    const { container } = mountSection(respond)

    const timezone = await screen.findByRole('combobox', {
      name: zhCN.preferences.timezone.label,
    })
    expect(container).not.toHaveAttribute('inert')

    fireEvent.mouseDown(timezone)
    expect(screen.getAllByRole('option').length).toBeGreaterThan(1)
    expect(container).toHaveAttribute('inert')
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
    for (const control of [language, timezone]) {
      const root = control.closest(`.${formControlClasses.root}`)
      expect(root).not.toBeNull()
      expect(root).toHaveStyle({ maxWidth: '384px' })
    }
  })

  it('names each label-less select by its row title above the not-chosen placeholder', async () => {
    const { respond } = makeProfileResponder({})
    mountSection(respond)

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    const timezone = screen.getByRole('combobox', {
      name: zhCN.preferences.timezone.label,
    })
    expect(language).toHaveTextContent(zhCN.preferences.language.notChosen)
    expect(timezone).toHaveTextContent(zhCN.preferences.timezone.notChosen)

    for (const [control, title] of [
      [language, zhCN.preferences.language.label],
      [timezone, zhCN.preferences.timezone.label],
    ] as const) {
      const titleElement = screen.getByText(title)
      expect(
        titleElement.compareDocumentPosition(control) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy()
      const root = control.closest(`.${formControlClasses.root}`)
      expect(root).not.toBeNull()
      expect(root?.querySelector(`.${inputLabelClasses.root}`)).toBeNull()
    }
  })

  it('PATCHes the language first, then switches the instance (manual slot and account move together)', async () => {
    const { respond, stored } = makeProfileResponder({})
    const { rig, i18n } = mountSection(respond)

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    expect(language).toHaveTextContent(zhCN.preferences.language.notChosen)

    chooseOption(language, languageLabel('en-US', 'zh-CN'))

    await waitFor(() => expect(i18n.language).toBe('en-US'))
    expect(stored.locale).toBe('en-US')
    const patch = rig.calls.find(
      (call) => call.method === 'PATCH' && call.path === PROFILE_PATH,
    )
    expect(patch).toBeDefined()
    expect(patch?.body).toEqual({ locale: 'en-US' })
    expect(
      await screen.findByRole('heading', { name: enUS.preferences.title }),
    ).toBeTruthy()
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

    expect(screen.queryByRole('alert')).toBeNull()
    expect(i18n.language).toBe('zh-CN')
    await waitFor(() =>
      expect(language).toHaveTextContent(zhCN.preferences.language.notChosen),
    )
    await waitFor(() =>
      expect(regionOf(groupOf(language))).toHaveTextContent(
        zhCN.preferences.language.followsDevice,
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
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    expect(groupOf(language)).toContainElement(alert)
    expect(
      language.compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    const timezone = screen.getByRole('combobox', {
      name: zhCN.preferences.timezone.label,
    })
    expect(regionOf(groupOf(timezone))).toHaveTextContent('')
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

    chooseOption(timezone, 'Europe/Berlin')
    await waitFor(() => expect(stored.time_zone).toBe('Europe/Berlin'))
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

  it('keeps the timezone select focused and read-only while its write is in flight', async () => {
    let releaseWrite: () => void = () => {}
    const writeGate = new Promise<void>((resolve) => {
      releaseWrite = resolve
    })
    const { respond, stored } = makeProfileResponder({
      time_zone: 'Asia/Tokyo',
    })
    const held: RealResponder = async (call) => {
      if (call.method === 'PATCH') {
        await writeGate
      }
      return respond(call)
    }
    const { rig } = mountSection(held)

    const timezone = await screen.findByRole('combobox', {
      name: zhCN.preferences.timezone.label,
    })
    chooseOption(timezone, 'Europe/Berlin')
    await waitFor(() =>
      expect(
        rig.calls.filter((call) => call.method === 'PATCH'),
      ).toHaveLength(1),
    )

    expect(document.activeElement).toBe(timezone)
    expect(timezone).toHaveAttribute('aria-readonly', 'true')
    expect(timezone).toHaveAttribute('tabindex', '0')
    expect(timezone).not.toHaveAttribute('aria-disabled', 'true')

    expect(timezone).toHaveTextContent('Asia/Tokyo')
    fireEvent.mouseDown(timezone)
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(
      rig.calls.filter((call) => call.method === 'PATCH'),
    ).toHaveLength(1)

    const language = screen.getByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    expect(language).toHaveAttribute('aria-readonly', 'true')
    expect(language).not.toHaveAttribute('aria-disabled', 'true')
    expect(language).not.toHaveAttribute('aria-busy')
    fireEvent.mouseDown(language)
    fireEvent.keyDown(language, { key: 'Enter' })
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(
      rig.calls.filter((call) => call.method === 'PATCH'),
    ).toHaveLength(1)

    await act(async () => {
      releaseWrite()
    })
    await waitFor(() => expect(stored.time_zone).toBe('Europe/Berlin'))
    await waitFor(() =>
      expect(timezone).not.toHaveAttribute('aria-readonly'),
    )
    expect(document.activeElement).toBe(timezone)
  })

  it('keeps the language select focused across its write and the language switch', async () => {
    let releaseWrite: () => void = () => {}
    const writeGate = new Promise<void>((resolve) => {
      releaseWrite = resolve
    })
    const { respond, stored } = makeProfileResponder({})
    const held: RealResponder = async (call) => {
      if (call.method === 'PATCH') {
        await writeGate
      }
      return respond(call)
    }
    const { rig, i18n } = mountSection(held)

    const language = await screen.findByRole('combobox', {
      name: zhCN.preferences.language.label,
    })
    chooseOption(language, languageLabel('en-US', 'zh-CN'))
    await waitFor(() =>
      expect(
        rig.calls.filter((call) => call.method === 'PATCH'),
      ).toHaveLength(1),
    )
    expect(document.activeElement).toBe(language)
    expect(language).toHaveAttribute('aria-readonly', 'true')

    await act(async () => {
      releaseWrite()
    })
    await waitFor(() => expect(stored.locale).toBe('en-US'))
    await waitFor(() => expect(i18n.language).toBe('en-US'))

    const switched = screen.getByRole('combobox', {
      name: enUS.preferences.language.label,
    })
    expect(switched).toBe(language)
    await waitFor(() => expect(switched).not.toHaveAttribute('aria-readonly'))
    expect(document.activeElement).toBe(switched)
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

    chooseOption(timezone, zhCN.preferences.timezone.notChosen)
    await waitFor(() => expect(stored.time_zone).toBe(''))
    await waitFor(() =>
      expect(regionOf(groupOf(timezone))).toHaveTextContent(
        zhCN.preferences.timezone.followsDevice,
      ),
    )
    const patches = rig.calls.filter((call) => call.method === 'PATCH')
    expect(patches).toHaveLength(1)
    expect(patches[0]?.body).toEqual({ time_zone: '' })
  })

  it('adds a stored timezone the engine list omits as its own option', async () => {
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

    const retry = screen.getByRole('button', { name: zhCN.preferences.retry })
    expect(retry).toHaveClass('MuiButton-contained')

    failLoad = false
    await user.click(retry)
    expect(
      await screen.findByRole('combobox', {
        name: zhCN.preferences.timezone.label,
      }),
    ).toHaveTextContent('Asia/Tokyo')
  })

  it('keeps the selects and marks them stale when a refresh fails', async () => {
    const user = userEvent.setup()
    let reads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        reads += 1
        return reads === 1
          ? jsonResponse(
              200,
              profileBody({ locale: 'en-US', time_zone: 'Asia/Tokyo' }),
            )
          : errorResponse(500, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { queryClient } = mountSection(respond)

    expect(
      await screen.findByRole('combobox', {
        name: zhCN.preferences.timezone.label,
      }),
    ).toHaveTextContent('Asia/Tokyo')

    await act(async () => {
      await queryClient.invalidateQueries()
    })

    expect(
      screen.getByRole('combobox', {
        name: zhCN.preferences.timezone.label,
      }),
    ).toHaveTextContent('Asia/Tokyo')
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(screen.queryByText(zhCN.preferences.error.title)).toBeNull()

    const readsBeforeRetry = reads
    await user.click(screen.getByRole('button', { name: zhCN.preferences.retry }))
    await waitFor(() => expect(reads).toBeGreaterThan(readsBeforeRetry))
  })
})
