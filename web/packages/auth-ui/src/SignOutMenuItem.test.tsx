/**
 * SignOutMenuItem behaviour: the account-menu item drives one logout
 * round-trip through the bindRequestFn harness, calling the host's
 * onActivate first, and renders the shared signOut.label text. A refused
 * logout is swallowed -- auth-core clears the local session and flips the
 * snapshot to anonymous either way, and the item is gone with the menu --
 * so nothing renders an alert. Text expectations read the bundle values,
 * never inline language.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import MenuList from '@mui/material/MenuList'
import { SignOutMenuItem } from './SignOutMenuItem.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  LOGIN_ANSWER,
  LOGOUT,
  PROFILE_GET,
  apiError,
  authenticatedCompletion,
  makeHarness,
  principal,
  type Harness,
} from '@speed/test-utils/session-harness'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const LABEL_ZH = zhCN.signOut.label

function makeScriptedHarness(
  extra: Parameters<typeof makeHarness>[0] = {},
): Harness {
  return makeHarness({
    [LOGIN_ANSWER]: () => authenticatedCompletion(),
    [PROFILE_GET]: () => principal(),
    ...extra,
  })
}

/** Signs the harness session in through the real session operation. */
async function signIn(harness: Harness): Promise<void> {
  await harness.session.answerFlow({ seq: 1, step: 'complete', input: {} })
}

describe('SignOutMenuItem', () => {
  it('activate the host callback first, then sign out', async () => {
    const events: string[] = []
    const harness = makeScriptedHarness({
      [LOGOUT]: () => {
        events.push('logout')
      },
    })
    await signIn(harness)
    renderWithProviders(
      <MenuList>
        <SignOutMenuItem
          session={harness.session}
          onActivate={() => events.push('activate')}
        />
      </MenuList>,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('menuitem', { name: LABEL_ZH }))
    await waitFor(() => expect(harness.store.get()).toBeNull())
    // The menu closes (onActivate) before the request is made; the item
    // then drives the one logout round-trip.
    expect(events).toEqual(['activate', 'logout'])
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/logout'),
    ).toHaveLength(1)
  })

  it('swallow a refused logout: the local session still ends', async () => {
    const harness = makeScriptedHarness({
      [LOGOUT]: () => {
        throw apiError(503, 'authn.unavailable')
      },
    })
    await signIn(harness)
    renderWithProviders(
      <MenuList>
        <SignOutMenuItem session={harness.session} />
      </MenuList>,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('menuitem', { name: LABEL_ZH }))
    // The refusal is not rendered and does not stop the local end.
    await waitFor(() => expect(harness.store.get()).toBeNull())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/logout'),
    ).toHaveLength(1)
  })

  it('still sign out when the host callback throws', async () => {
    const harness = makeScriptedHarness({ [LOGOUT]: () => undefined })
    await signIn(harness)
    renderWithProviders(
      <MenuList>
        <SignOutMenuItem
          session={harness.session}
          onActivate={() => {
            throw new Error('menu close failed')
          }}
        />
      </MenuList>,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('menuitem', { name: LABEL_ZH }))
    await waitFor(() => expect(harness.store.get()).toBeNull())
  })
})
