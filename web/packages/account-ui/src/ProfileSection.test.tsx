/**
 * ProfileSection checks the display-name surface over the profile read and PATCH.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
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
    expect(zhCN.profile.saved).toContain(zhCN.profile.displayName.label)
    const saveButton = screen.getByRole('button', { name: zhCN.profile.save })
    expect(
      saveButton.compareDocumentPosition(screen.getByRole('status')) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(screen.queryByRole('alert')).toBeNull()
    const patch = rig.calls.find((call) => call.method === 'PATCH')
    expect(patch?.body).toEqual({ display_name: 'Grace' })
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

  it('keeps the field read-only until the refetch that follows the save settles', async () => {
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

    await waitFor(() => expect(releaseRefetch).not.toBeNull())
    expect(field).toHaveAttribute('readonly')
    expect(field).not.toHaveAttribute('disabled')

    releaseRefetch!()
    await waitFor(() => expect(field).not.toHaveAttribute('readonly'))
    expect(field).not.toHaveAttribute('disabled')
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
    const saveButton = screen.getByRole('button', { name: zhCN.profile.save })
    expect(
      saveButton.compareDocumentPosition(alert) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('holds the save button unavailable with its reason while the field still holds the account name', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: 'Ada' }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const save = await screen.findByRole('button', { name: zhCN.profile.save })
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).not.toHaveAttribute('disabled')
    expect(save).not.toHaveAttribute('aria-busy')
    expect(save).toHaveAccessibleDescription(zhCN.profile.saveUnavailable)
    save.focus()
    expect(save).toHaveFocus()

    await user.click(save)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    fireEvent.submit(save.closest('form') as HTMLFormElement)
    expect(rig.calls.filter((call) => call.method === 'PATCH')).toHaveLength(0)

    const field = screen.getByRole('textbox', {
      name: zhCN.profile.displayName.label,
    })
    await user.type(field, '!')
    expect(save).not.toHaveAttribute('aria-disabled', 'true')
    expect(save).not.toHaveAccessibleDescription()

    await user.clear(field)
    await user.type(field, 'Ada')
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).toHaveAccessibleDescription(zhCN.profile.saveUnavailable)
  })

  it('keeps focus on the save button while the save is in flight and after it lands', async () => {
    let stored = 'Ada'
    let releasePatch: (() => void) | null = null
    const respond: RealResponder = async (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: stored }))
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        const nextName = (call.body as { display_name: string }).display_name
        await new Promise<void>((resolve) => {
          releasePatch = resolve
        })
        stored = nextName
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
    const save = screen.getByRole('button', { name: zhCN.profile.save })
    await user.click(save)

    await waitFor(() => expect(save).toHaveAttribute('aria-busy', 'true'))
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).not.toHaveAttribute('disabled')
    expect(save).toHaveFocus()
    expect(field).toHaveAttribute('readonly')
    expect(field).not.toHaveAttribute('disabled')

    releasePatch!()
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(zhCN.profile.saved),
    )

    await waitFor(() => expect(save).not.toHaveAttribute('aria-busy'))
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).toHaveAccessibleDescription(zhCN.profile.saveUnavailable)
    expect(save).toHaveFocus()
  })

  it('keeps focus in the inline field while an Enter-started write is in flight', async () => {
    let stored = 'Ada'
    let releasePatch: (() => void) | null = null
    const respond: RealResponder = async (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ display_name: stored }))
      }
      if (call.method === 'PATCH' && call.path === PROFILE_PATH) {
        const nextName = (call.body as { display_name: string }).display_name
        await new Promise<void>((resolve) => {
          releasePatch = resolve
        })
        stored = nextName
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
    await user.clear(field)
    await user.type(field, 'Grace')
    field.focus()
    await user.keyboard('{Enter}')

    await waitFor(() => expect(field).toHaveAttribute('readonly'))
    expect(field).not.toHaveAttribute('disabled')
    expect(field).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(rig.calls.filter((call) => call.method === 'PATCH')).toHaveLength(1)

    releasePatch!()
    await waitFor(() => expect(field).not.toHaveAttribute('readonly'))
    expect(field).toHaveFocus()
    expect(field).toHaveValue('Grace')
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
    expect(screen.queryByRole('button', { name: zhCN.profile.save })).toBeNull()

    await user.clear(field)
    await user.type(field, 'Grace')
    await user.tab()
    await waitFor(() => expect(stored).toBe('Grace'))
    const afterBlur = rig.calls.filter((call) => call.method === 'PATCH')
    expect(afterBlur).toHaveLength(1)
    expect(afterBlur[0]?.body).toEqual({ display_name: 'Grace' })

    await waitFor(() => expect(field).not.toHaveAttribute('readonly'))
    expect(field).toHaveValue('Grace')

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

    const readsBeforeRetry = reads
    await user.click(screen.getByRole('button', { name: zhCN.profile.retry }))
    await waitFor(() => expect(reads).toBeGreaterThan(readsBeforeRetry))
  })
})
