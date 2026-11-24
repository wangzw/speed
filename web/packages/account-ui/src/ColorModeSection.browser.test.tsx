/**
 * ColorModeSection in a real browser: the in-flight select stays focusable and read-only.
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
    await waitFor(() => {
      expect(
        select,
        `focus while the color-mode write is in flight; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
    expect(select).toHaveAttribute('aria-readonly', 'true')
    expect(select).toHaveTextContent(zhCN.appearance.colorMode.options.light)

    await userEvent.click(select)
    expect(screen.queryByRole('option')).toBeNull()
    expect(
      rig.calls.filter((call) => call.method === 'PATCH'),
    ).toHaveLength(1)

    patch.resolve(jsonResponse(200, {}))

    await waitFor(() => expect(onApplied).toHaveBeenCalledTimes(1))
    expect(onApplied).toHaveBeenCalledWith('dark')
    await waitFor(() =>
      expect(select).toHaveTextContent(
        zhCN.appearance.colorMode.options.dark,
      ),
    )
    expect(select).not.toHaveAttribute('aria-readonly', 'true')
  })

  it('returns focus to the select after a color mode is chosen', async () => {
    await page.viewport(1200, 900)
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
        return jsonResponse(200, {})
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRigWithoutSession(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <ColorModeSection effective="system" onApplied={vi.fn()} />,
    )

    const select = await screen.findByRole('combobox', {
      name: zhCN.appearance.colorMode.label,
    })
    await waitFor(() =>
      expect(select).toHaveTextContent(
        zhCN.appearance.colorMode.options.light,
      ),
    )

    select.focus()
    await userEvent.keyboard('{ArrowDown}')
    await screen.findByRole('listbox')
    await userEvent.click(
      screen.getByRole('option', {
        name: zhCN.appearance.colorMode.options.dark,
      }),
    )

    await waitFor(() =>
      expect(
        document.activeElement,
        `focus after choosing a color mode; document.activeElement is ${describeActiveElement()}`,
      ).toBe(select),
    )
  })
})
