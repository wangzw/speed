/**
 * ProfileSection behaviour: the display-name surface over the profile
 * read (GET /api/v1/auth/me) and the profile write (PATCH /api/v1/auth/me).
 *
 * What this suite pins: the stored display name renders into the field; a
 * save PATCHes a one-field body under `display_name`, fills the success
 * live region and refetches the profile; a refused save renders its code
 * text in the banner (the write needs no fresh authentication, so no
 * step-up dialog opens); the inline mode writes on blur and on Enter; and a
 * load that failed renders the error state with a retry that refetches.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { profileBody } from '../test-utils/fixtures.js'
import {
  errorResponse,
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { ProfileSection } from './ProfileSection.js'

const PROFILE_PATH = '/api/v1/auth/me'

/** Mounts the section over a session-free rig that plants the bearer the
 * profile read and the PATCH ride: the section takes no session, so a raw
 * refusal surfaces directly. */
function mount(respond: RealResponder) {
  const rig = makeRealClientRigWithoutSession(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(<ProfileSection />)
  return { rig, ...rendered }
}

describe('ProfileSection', () => {
  it('renders the stored display name and passes axe', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: 'Grace Hopper' }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(
      await screen.findByRole('heading', { name: zhCN.profile.title }),
    ).toBeTruthy()
    expect(
      await screen.findByRole('textbox', { name: zhCN.profile.displayName.label }),
    ).toHaveValue('Grace Hopper')

    await expectNoAxeViolations()
  })

  it('PATCHes the display name, reports it and refetches the profile', async () => {
    let stored = 'Ada'
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: stored }))
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        stored = (call.body as { display_name: string }).display_name
        return jsonResponse(200, {})
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.clear(field)
    await user.type(field, 'Grace')
    await user.click(screen.getByRole('button', { name: zhCN.profile.save }))

    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(zhCN.profile.saved),
    )
    // The confirmation names the changed object (the display name) and sits
    // below the control group it confirms -- the field and its save button.
    expect(zhCN.profile.saved).toContain(zhCN.profile.displayName.label)
    const saveButton = screen.getByRole('button', { name: zhCN.profile.save })
    expect(
      saveButton.compareDocumentPosition(screen.getByRole('status')) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
    const patch = rig.calls.find((call) => call.method === 'PATCH')
    expect(patch?.body).toEqual({ display_name: 'Grace' })
    // The read converges on the account's own answer.
    expect(
      rig.calls.filter(
        (call) => call.method === 'GET' && call.path === PROFILE_PATH,
      ).length,
    ).toBeGreaterThanOrEqual(2)
  })

  it('reports the name the server stored to the onDisplayNameSaved callback', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: 'Ada' }))
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        // The write answers with the name as stored, which may differ from
        // the body it accepted; the callback reports that answer, not the
        // typed value.
        return jsonResponse(200, { display_name: 'Grace Hopper' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const onDisplayNameSaved = vi.fn()
    const rig = makeRealClientRigWithoutSession(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <ProfileSection onDisplayNameSaved={onDisplayNameSaved} />,
    )
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.clear(field)
    await user.type(field, 'Grace')
    await user.click(screen.getByRole('button', { name: zhCN.profile.save }))

    await waitFor(() =>
      expect(onDisplayNameSaved).toHaveBeenCalledWith('Grace Hopper'),
    )
    expect(onDisplayNameSaved).toHaveBeenCalledTimes(1)
  })

  it('keeps the field disabled until the refetch that follows the save settles', async () => {
    let patchDone = false
    let releaseRefetch: (() => void) | null = null
    const respond: RealResponder = async (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        if (patchDone && releaseRefetch === null) {
          await new Promise<void>((resolve) => {
            releaseRefetch = resolve
          })
        }
        return jsonResponse(
          200,
          profileBody({ display_name: patchDone ? 'Grace' : 'Ada' }),
        )
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        patchDone = true
        return jsonResponse(200, {})
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.clear(field)
    await user.type(field, 'Grace')
    await user.click(screen.getByRole('button', { name: zhCN.profile.save }))

    // The PATCH settled, but the scope's attempt is still open on the
    // refetch, so the field stays disabled until the whole attempt settles.
    await waitFor(() => expect(releaseRefetch).not.toBeNull())
    expect(field).toBeDisabled()

    releaseRefetch!()
    await waitFor(() => expect(field).toBeEnabled())
  })

  it('renders a refused save as its code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody())
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        return errorResponse(400, 'authn.bad_request')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.clear(field)
    await user.type(field, 'Grace')
    await user.click(screen.getByRole('button', { name: zhCN.profile.save }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    // The failure renders in the same place as a success: below the control
    // group, in the scope's region.
    const saveButton = screen.getByRole('button', { name: zhCN.profile.save })
    expect(
      saveButton.compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('disables the save button while the field still holds the account name', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: 'Ada' }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const save = await screen.findByRole('button', { name: zhCN.profile.save })
    // Nothing changed yet: the save is inert, so a stray click cannot fire a
    // no-op PATCH.
    expect(save).toBeDisabled()

    const field = screen.getByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.type(field, '!')
    expect(save).toBeEnabled()

    // Restoring the account's own name disables it again.
    await user.clear(field)
    await user.type(field, 'Ada')
    expect(save).toBeDisabled()
  })

  it('writes the inline field on blur and on Enter, with no save button', async () => {
    let stored = 'Ada'
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: stored }))
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        stored = (call.body as { display_name: string }).display_name
        return jsonResponse(200, {})
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRigWithoutSession(respond)
    rig.store.set('access-1')
    renderWithProviders(<ProfileSection inline />)
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    // The inline mode carries no submit button: the field commits itself.
    expect(screen.queryByRole('button', { name: zhCN.profile.save })).toBeNull()

    // A changed name lands when the field loses focus.
    await user.clear(field)
    await user.type(field, 'Grace')
    await user.tab()
    await waitFor(() => expect(stored).toBe('Grace'))
    const afterBlur = rig.calls.filter((call) => call.method === 'PATCH')
    expect(afterBlur).toHaveLength(1)
    expect(afterBlur[0]?.body).toEqual({ display_name: 'Grace' })

    // The refetch that follows the save settles before the next edit.
    await waitFor(() => expect(field).toBeEnabled())
    expect(field).toHaveValue('Grace')

    // Enter commits the next changed name through the form's submit.
    await user.clear(field)
    await user.type(field, 'Grace Hopper{Enter}')
    await waitFor(() => expect(stored).toBe('Grace Hopper'))
    const afterEnter = rig.calls.filter((call) => call.method === 'PATCH')
    expect(afterEnter).toHaveLength(2)
    expect(afterEnter[1]?.body).toEqual({ display_name: 'Grace Hopper' })
  })

  it('does not write the inline field on Enter when the value did not change', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: 'Ada' }))
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        return jsonResponse(200, {})
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRigWithoutSession(respond)
    rig.store.set('access-1')
    renderWithProviders(<ProfileSection inline />)
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.click(field)
    await user.keyboard('{Enter}')

    // Enter on the account's own name is no write: no PATCH goes out and the
    // region reports nothing, matching the non-inline button's disabled rule.
    expect(rig.calls.filter((call) => call.method === 'PATCH')).toHaveLength(0)
    expect(screen.getByRole('status').textContent).toBe('')
  })

  it('renders a 401 refusal in the feedback region without a step-up dialog', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: 'Ada' }))
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        return errorResponse(401, 'authn.sso_required')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const field = await screen.findByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.clear(field)
    await user.type(field, 'Grace')
    await user.click(screen.getByRole('button', { name: zhCN.profile.save }))

    // The write is not gated: the refusal's own text lands in the section's
    // feedback region, and no dialog opens.
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.sso_required)
    const saveButton = screen.getByRole('button', { name: zhCN.profile.save })
    expect(
      saveButton.compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('renders the error state with a retry that refetches when the load fails', async () => {
    const user = userEvent.setup()
    let failLoad = true
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return failLoad
          ? errorResponse(503, 'client.network')
          : jsonResponse(200, profileBody({ display_name: 'Grace' }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText(zhCN.profile.error.title)).toBeTruthy()

    failLoad = false
    await user.click(screen.getByRole('button', { name: zhCN.profile.retry }))
    expect(
      await screen.findByRole('textbox', { name: zhCN.profile.displayName.label }),
    ).toHaveValue('Grace')
  })

  it('keeps the loaded profile and marks it stale when a refresh fails', async () => {
    const user = userEvent.setup()
    let reads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        reads += 1
        // The first read settles the field; every later refresh fails.
        return reads === 1
          ? jsonResponse(200, profileBody({ display_name: 'Grace Hopper' }))
          : errorResponse(500, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { queryClient } = mount(respond)

    expect(
      await screen.findByRole('textbox', {
        name: zhCN.profile.displayName.label,
      }),
    ).toHaveValue('Grace Hopper')

    await act(async () => {
      await queryClient.invalidateQueries()
    })

    // The failed refresh keeps the settled field on screen under the stale
    // notice, rather than replacing it with the error placeholder.
    expect(
      screen.getByRole('textbox', { name: zhCN.profile.displayName.label }),
    ).toHaveValue('Grace Hopper')
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(
      alert.compareDocumentPosition(
        screen.getByRole('textbox', {
          name: zhCN.profile.displayName.label,
        }),
      ) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(screen.queryByText(zhCN.profile.error.title)).toBeNull()

    // The stale notice's retry is the section's own retry: it re-reads.
    const readsBeforeRetry = reads
    await user.click(screen.getByRole('button', { name: zhCN.profile.retry }))
    await waitFor(() => expect(reads).toBeGreaterThan(readsBeforeRetry))
  })
})
