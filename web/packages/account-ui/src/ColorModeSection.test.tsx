/**
 * ColorModeSection behaviour: the three-valued color-mode select over the
 * profile self-write surface, GET/PATCH /api/v1/auth/me.
 *
 * What this suite pins: the three options render in their fixed order
 * (dark, light, system) and the account's stored color_mode is the
 * selected one; an account that has never saved a preference shows the
 * host's `effective` value; a choice PATCHes exactly `{ color_mode }` and
 * reports the written value through `onApplied` only after the write has
 * landed; a refused write leaves the control on the stored value and
 * renders the resolved code text in the section's own feedback region;
 * and the closed control passes an axe scan. Every write's request body
 * is pinned, so the PATCH contract is asserted from the wire up.
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

/** A stateful profile responder: GET answers the stored color mode, PATCH
 * merges the partial body into it (absent field unchanged) and answers
 * 200. */
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

/** Opens the MUI select and picks the option whose accessible name is
 * `optionName`. */
function chooseOption(combobox: HTMLElement, optionName: string): void {
  fireEvent.mouseDown(combobox)
  fireEvent.click(screen.getByRole('option', { name: optionName }))
}

/** Mounts the section over a session-free rig that plants the bearer the
 * profile read rides. */
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
    // The description is the control's own helper, under the select.
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
    // The fixed order is the section's own: dark, light, system.
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
    // The value came from `effective`, not from an empty stored field.
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

    // The write is in flight: the request is on the wire, and the host has
    // not been told the preference was applied.
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

    // The accepted write renders a confirmation naming the chosen mode, in
    // the same region a refusal would use.
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
    // The body is the partial update the endpoint documents: only the
    // changed field, under its wire name.
    expect(patches[0]?.body).toEqual({ color_mode: 'dark' })

    // The confirmed write invalidates the profile read, so the control
    // converges on the stored value.
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

    // The stored value stays `system`; the copy reads as a behaviour.
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
    // The server's own code text, in the section's feedback region.
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    // A refused write leaves the control on the stored value, and the
    // host is not told a preference was applied.
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
