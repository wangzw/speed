/**
 * ColorModeSection checks the three-valued color-mode select over the
 * profile self-write surface, pinning each PATCH body.
 */

import { describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
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
import type { AppThemeModePreference } from '@speed/ui-kit'
import { ColorModeSection } from './ColorModeSection.js'

const PROFILE_PATH = '/api/v1/auth/me'

/** GET answers the stored mode; PATCH merges the partial body and answers 200. */
function makeProfileResponder(initial: { color_mode?: string } = {}) {
  const stored: { color_mode?: string } = { ...initial }
  const respond: RealResponder = async (call: RealCall) => {
    if (call.path !== PROFILE_PATH) {
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    if (call.method === 'GET') {
      return jsonResponse(
        200,
        profileBody({ color_mode: stored.color_mode ?? '' }),
      )
    }
    if (call.method === 'PATCH') {
      const patch = call.body as { color_mode?: string }
      if (patch.color_mode !== undefined) {
        stored.color_mode = patch.color_mode
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

function mountSection(
  respond: RealResponder,
  effective: AppThemeModePreference,
  onApplied: (value: AppThemeModePreference) => void = vi.fn(),
) {
  const rig = makeRealClientRigWithoutSession(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(
    <ColorModeSection effective={effective} onApplied={onApplied} />,
  )
  return { rig, onApplied, ...rendered }
}

function modeSelect(): HTMLElement {
  return screen.getByRole('combobox', {
    name: zhCN.appearance.colorMode.label,
  })
}

describe('ColorModeSection', () => {
  it('renders the three options in order and marks the stored value selected', async () => {
    const { respond } = makeProfileResponder({ color_mode: 'light' })
    mountSection(respond, 'system')

    expect(
      await screen.findByRole('heading', {
        name: zhCN.appearance.colorMode.title,
      }),
    ).toBeTruthy()
    const select = modeSelect()
    expect(select).toHaveAccessibleDescription(
      zhCN.appearance.colorMode.description,
    )
    await waitFor(() =>
      expect(select).toHaveTextContent(
        zhCN.appearance.colorMode.options.light,
      ),
    )

    fireEvent.mouseDown(select)
    expect(screen.getAllByRole('option').map((option) => option.textContent)).toEqual([
      zhCN.appearance.colorMode.options.dark,
      zhCN.appearance.colorMode.options.light,
      zhCN.appearance.colorMode.options.system,
    ])
    expect(
      screen.getByRole('option', {
        name: zhCN.appearance.colorMode.options.light,
      }),
    ).toHaveAttribute('aria-selected', 'true')
    expect(
      screen.getByRole('option', {
        name: zhCN.appearance.colorMode.options.dark,
      }),
    ).toHaveAttribute('aria-selected', 'false')
    expect(
      screen.getByRole('option', {
        name: zhCN.appearance.colorMode.options.system,
      }),
    ).toHaveAttribute('aria-selected', 'false')
  })

  it('shows props.effective when the stored color mode is empty', async () => {
    const { respond } = makeProfileResponder()
    const { rig } = mountSection(respond, 'dark')

    const select = modeSelect()
    await waitFor(() =>
      expect(select).toHaveTextContent(zhCN.appearance.colorMode.options.dark),
    )
    await waitFor(() =>
      expect(
        rig.calls.filter(
          (call) => call.method === 'GET' && call.path === PROFILE_PATH,
        ),
      ).toHaveLength(1),
    )
    expect(select).toHaveTextContent(zhCN.appearance.colorMode.options.dark)
  })

  it('PATCHes { color_mode } once and calls onApplied after the write lands', async () => {
    let releaseWrite: (() => void) | undefined
    const writeGate = new Promise<void>((resolve) => {
      releaseWrite = resolve
    })
    const { respond, stored } = makeProfileResponder({
      color_mode: 'light',
    })
    const respondBehindGate: RealResponder = async (call: RealCall) => {
      if (call.method === 'PATCH') {
        await writeGate
      }
      return respond(call)
    }
    const onApplied = vi.fn()
    const { rig } = mountSection(respondBehindGate, 'system', onApplied)

    const select = modeSelect()
    await waitFor(() =>
      expect(select).toHaveTextContent(
        zhCN.appearance.colorMode.options.light,
      ),
    )

    chooseOption(select, zhCN.appearance.colorMode.options.dark)

    await waitFor(() =>
      expect(
        rig.calls.filter((call) => call.method === 'PATCH'),
      ).toHaveLength(1),
    )
    expect(onApplied).not.toHaveBeenCalled()

    releaseWrite?.()

    await waitFor(() => expect(onApplied).toHaveBeenCalledTimes(1))
    expect(onApplied).toHaveBeenCalledWith('dark')
    expect(stored.color_mode).toBe('dark')

    expect(
      screen.getByText(
        zhCN.appearance.colorMode.saved.replace(
          '{{value}}',
          zhCN.appearance.colorMode.options.dark,
        ),
      ),
    ).toBeInTheDocument()

    const patches = rig.calls.filter((call) => call.method === 'PATCH')
    expect(patches).toHaveLength(1)
    expect(patches[0]?.body).toEqual({ color_mode: 'dark' })

    await waitFor(() =>
      expect(
        rig.calls.filter(
          (call) => call.method === 'GET' && call.path === PROFILE_PATH,
        ),
      ).toHaveLength(2),
    )
  })

  it('confirms the unchosen mode with its own follow-the-device sentence', async () => {
    const { respond, stored } = makeProfileResponder({ color_mode: 'dark' })
    mountSection(respond, 'dark')

    const select = modeSelect()
    await waitFor(() =>
      expect(select).toHaveTextContent(zhCN.appearance.colorMode.options.dark),
    )
    chooseOption(select, zhCN.appearance.colorMode.options.system)

    await waitFor(() => expect(stored.color_mode).toBe('system'))
    expect(
      await screen.findByText(zhCN.appearance.colorMode.savedSystem),
    ).toBeInTheDocument()
  })

  it('leaves the shown value unchanged and renders the failure text when the write is refused', async () => {
    const respond: RealResponder = async (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ color_mode: 'light' }))
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        return errorResponse(400, 'authn.bad_request')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const onApplied = vi.fn()
    mountSection(respond, 'system', onApplied)

    const select = modeSelect()
    await waitFor(() =>
      expect(select).toHaveTextContent(
        zhCN.appearance.colorMode.options.light,
      ),
    )

    chooseOption(select, zhCN.appearance.colorMode.options.dark)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    expect(select).toHaveTextContent(zhCN.appearance.colorMode.options.light)
    expect(onApplied).not.toHaveBeenCalled()
  })

  it('passes an axe scan with the menu closed', async () => {
    const { respond } = makeProfileResponder({ color_mode: 'light' })
    mountSection(respond, 'system')

    await waitFor(() =>
      expect(modeSelect()).toHaveTextContent(
        zhCN.appearance.colorMode.options.light,
      ),
    )

    await expectNoAxeViolations()
  })
})
