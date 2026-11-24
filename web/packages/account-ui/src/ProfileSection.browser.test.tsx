/**
 * ProfileSection in a real browser: the in-flight control and field keep focus,
 * which a native disable would drop.
 */

import { describe, expect, it } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { profileBody } from '../test-utils/fixtures.js'
import {
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { ProfileSection } from './ProfileSection.js'

const PROFILE_PATH = '/api/v1/auth/me'

/** A promise the case settles by hand, so one request is held in flight
 * across frames. */
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

/** Mounts the section over the real client and holds the PATCH open until
 * the case settles it. */
function mountHeldWrite(inline: boolean) {
  const held = deferred<Response>()
  let stored = 'Ada'
  const respond: RealResponder = (call: RealCall) => {
    if (call.method === 'GET' && call.path === PROFILE_PATH) {
      return jsonResponse(200, profileBody({ display_name: stored }))
    }
    if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
      stored = (call.body as { display_name: string }).display_name
      return held.promise
    }
    return jsonResponse(404, { code: 'authn.bad_request' })
  }
  const rig = makeRealClientRigWithoutSession(respond)
  rig.store.set('access-1')
  renderWithProviders(<ProfileSection inline={inline} />)
  return { held }
}

describe('ProfileSection writes in Chromium', () => {
  it('keeps focus on the save button through the write and its landing', async () => {
    await page.viewport(900, 900)
    const { held } = mountHeldWrite(false)
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.clear(field)
    await user.type(field, 'Grace')
    const save = screen.getByRole('button', { name: zhCN.profile.save })

    await user.click(save)
    // The control that started the write keeps focus through it. A native
    // `disabled` would have moved it to the document body already.
    await waitFor(() => expect(save).toHaveFocus())
    expect(save).toHaveAttribute('aria-busy', 'true')
    expect(save).toHaveAttribute('aria-disabled', 'true')
    // The field is locked but stays focusable and unchangeable.
    expect(field).toHaveAttribute('readonly')
    expect(field).not.toHaveAttribute('disabled')

    await act(async () => {
      held.resolve(jsonResponse(200, {}))
    })
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(zhCN.profile.saved),
    )

    // The landing makes the field match the stored name, so the save turns
    // unavailable with its reason -- and keeps the focus it never lost.
    await waitFor(() => expect(save).not.toHaveAttribute('aria-busy'))
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).toHaveAccessibleDescription(zhCN.profile.saveUnavailable)
    expect(save).toHaveFocus()
    expect(document.activeElement).not.toBe(document.body)
  })

  it('keeps focus in the inline field through an Enter-started write', async () => {
    await page.viewport(900, 900)
    const { held } = mountHeldWrite(true)
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.clear(field)
    await user.type(field, 'Grace')
    field.focus()
    await user.keyboard('{Enter}')

    // The field started the write: it stays focused and read-only, where a
    // native `disabled` would have dropped focus to the body.
    await waitFor(() => expect(field).toHaveAttribute('readonly'))
    expect(field).toHaveFocus()
    expect(field).not.toHaveAttribute('disabled')

    await act(async () => {
      held.resolve(jsonResponse(200, {}))
    })
    await waitFor(() => expect(field).not.toHaveAttribute('readonly'))
    expect(field).toHaveFocus()
    expect(field).toHaveValue('Grace')
  })

  it('leaves focus with the operator when they move it during the write', async () => {
    await page.viewport(900, 900)
    const { held } = mountHeldWrite(false)
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.clear(field)
    await user.type(field, 'Grace')
    const save = screen.getByRole('button', { name: zhCN.profile.save })
    await user.click(save)
    await waitFor(() => expect(save).toHaveAttribute('aria-busy', 'true'))

    // The operator deliberately moves into the locked field mid-write.
    field.focus()
    expect(field).toHaveFocus()

    await act(async () => {
      held.resolve(jsonResponse(200, {}))
    })
    await waitFor(() => expect(save).not.toHaveAttribute('aria-busy'))

    // The landing does not pull focus back to the save button.
    expect(field).toHaveFocus()
    expect(save).not.toHaveFocus()
  })
})
