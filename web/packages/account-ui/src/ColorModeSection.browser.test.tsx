/**
 * ColorModeSection in a real browser: the color-mode select is the control
 * that starts its own immediate write, so while the PATCH is in flight it
 * keeps focus and stays focusable but unchangeable (readOnly) instead of
 * turning natively disabled and dropping focus to the document. jsdom never
 * moves focus when a focused control turns natively disabled, so only the
 * browser tier can tell the two behaviours apart.
 */
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { page, userEvent } from 'vitest/browser'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { profileBody } from '../test-utils/fixtures.js'
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { ColorModeSection } from './ColorModeSection.js'

const PROFILE_PATH = '/api/v1/auth/me'

function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
} {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

/** What currently holds focus, for a failure message. */
function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const parts = [active.tagName.toLowerCase()]
  const role = active.getAttribute('role')
  const label = active.getAttribute('aria-label')
  const text = (active.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 80)
  if (role !== null) {
    parts.push(`role="${role}"`)
  }
  if (label !== null) {
    parts.push(`aria-label="${label}"`)
  }
  if (text !== '') {
    parts.push(`text="${text}"`)
  }
  return parts.join(' ')
}

describe('ColorModeSection write focus in Chromium', () => {
  it('keeps focus on the select and its value fixed while the write is in flight', async () => {
    await page.viewport(1200, 900)
    const patch = deferred<Response>()
    const stored = { color_mode: 'light' }
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ color_mode: stored.color_mode }))
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        const body = call.body as { color_mode?: string }
        if (body.color_mode !== undefined) {
          stored.color_mode = body.color_mode
        }
        return patch.promise
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRigWithoutSession(respond)
    rig.store.set('access-1')
    const onApplied = vi.fn()
    renderWithProviders(
      <ColorModeSection effective="system" onApplied={onApplied} />,
    )

    const select = await screen.findByRole('combobox', {
      name: zhCN.appearance.colorMode.label,
    })
    await waitFor(() =>
      expect(select).toHaveTextContent(
        zhCN.appearance.colorMode.options.light,
      ),
    )

    // The operator opens the select and picks a mode; MUI hands focus back
    // to the select when the menu closes, and the change starts the write.
    select.focus()
    await userEvent.click(select)
    await userEvent.click(
      await screen.findByRole('option', {
        name: zhCN.appearance.colorMode.options.dark,
      }),
    )

    await waitFor(() =>
      expect(
        rig.calls.filter((call) => call.method === 'PATCH'),
      ).toHaveLength(1),
    )
    // The menu closes and MUI hands focus back to the select; keep waiting
    // for it, because the closing transition finishes after the write is
    // already on the wire.
    await waitFor(() => {
      expect(
        select,
        `focus while the color-mode write is in flight; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
    // Focusable but unchangeable: readOnly, not natively disabled.
    expect(select).toHaveAttribute('aria-readonly', 'true')
    // The chosen value stays the stored one until the server accepts it.
    expect(select).toHaveTextContent(zhCN.appearance.colorMode.options.light)

    // A second activation cannot open the menu or start another write.
    await userEvent.click(select)
    expect(screen.queryByRole('option')).toBeNull()
    expect(
      rig.calls.filter((call) => call.method === 'PATCH'),
    ).toHaveLength(1)

    patch.resolve(jsonResponse(200, {}))

    await waitFor(() => expect(onApplied).toHaveBeenCalledTimes(1))
    expect(onApplied).toHaveBeenCalledWith('dark')
    // Settled: the control reads the stored value and is writable again.
    await waitFor(() =>
      expect(select).toHaveTextContent(
        zhCN.appearance.colorMode.options.dark,
      ),
    )
    expect(select).not.toHaveAttribute('aria-readonly', 'true')
  })
})
