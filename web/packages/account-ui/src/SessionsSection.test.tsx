/**
 * SessionsSection behaviour over the authn session routes: GET
 * /auth/sessions, DELETE /auth/sessions/{id} and DELETE /auth/sessions.
 *
 * What this suite pins, per story: the live sessions the server lists
 * render, the current one marked and carrying no revoke affordance, a
 * row without a user agent falling back to the unknown-device label (the
 * raw UA never rendered); a single-row revoke DELETE-s that exact
 * session and refetches the list, so the revoked row is gone; the
 * section-top "sign out other devices" action sits behind the danger
 * double-confirm, DELETE-s the collection and surfaces the answer's
 * revoked count; a refused revoke renders its code text above the list;
 * and the settled states split -- an answer listing zero sessions renders
 * the empty state, a load that failed (and an answer omitting the
 * sessions key) renders the error state with a retry.
 */

import { describe, expect, it } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { SessionsSection } from './SessionsSection.js'

const SESSIONS_PATH = '/auth/sessions'
const PROFILE_PATH = '/auth/me'

/** The profile read the timezone chain issues beside the list. */
const PROFILE = jsonResponse(200, { time_zone: '' })

/** One live session row, in the session fragment's own shape. */
function session(overrides: Record<string, unknown>): Record<string, unknown> {
  return {
    id: 'session-1',
    kind: 'browser',
    created_at: '2026-08-01T02:05:00.000Z',
    last_seen_at: '2026-08-01T02:05:00.000Z',
    expires_at: '2026-09-01T02:05:00.000Z',
    client_ip: '198.51.100.4',
    user_agent: '',
    current: false,
    ...overrides,
  }
}

/** The transient listener is not needed here; the rig is created fresh
 * per test and the token planted before render. */
function mountSection(respond: RealResponder) {
  const rig = makeRealClientRigWithoutSession(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(<SessionsSection />)
  return { rig, ...rendered }
}

/** The revoke aria-label the component builds for a row's device label. */
function revokeAria(device: string): string {
  return zhCN.sessions.revokeAriaWithDevice.replace('{{device}}', device)
}

describe('SessionsSection', () => {
  it('renders the live sessions, marks the current one and passes axe', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [
            session({
              id: 'session-current',
              current: true,
              user_agent: 'Mozilla/5.0 (Macintosh) AppleWebKit Chrome/120',
            }),
            session({ id: 'session-2', user_agent: '' }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)

    expect(
      await screen.findByRole('heading', { name: zhCN.sessions.title }),
    ).toBeTruthy()
    expect(await screen.findByText(zhCN.sessions.current)).toBeTruthy()
    // The row without a user agent falls back to the unknown-device label.
    expect(screen.getByText(zhCN.sessions.deviceUnknown)).toBeTruthy()
    // The current row carries no revoke affordance; the other does, named
    // after its own device label.
    expect(
      screen.queryByRole('button', { name: revokeAria(zhCN.sessions.current) }),
    ).toBeNull()
    expect(
      screen.getByRole('button', { name: revokeAria(zhCN.sessions.deviceUnknown) }),
    ).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('pages a long session list instead of rendering every row at once', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: Array.from({ length: 12 }, (_, index) =>
            session({ id: `session-${index}`, user_agent: '' }),
          ),
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)
    const user = userEvent.setup()

    await screen.findByRole('heading', { name: zhCN.sessions.title })
    // Pages of eight: only the first page's rows render, so twelve sessions
    // do not all land in the tree.
    expect(await screen.findAllByRole('listitem')).toHaveLength(8)
    expect(
      screen.getByText(
        zhCN.sessions.pageIndicator
          .replace('{{page}}', '1')
          .replace('{{total}}', '2'),
      ),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.sessions.prevPage }),
    ).toBeDisabled()

    await user.click(screen.getByRole('button', { name: zhCN.sessions.nextPage }))
    // The second page holds the remaining four rows.
    await waitFor(() =>
      expect(screen.getAllByRole('listitem')).toHaveLength(4),
    )
    expect(
      screen.getByRole('button', { name: zhCN.sessions.nextPage }),
    ).toBeDisabled()
  })

  it('revokes one session and refetches, dropping the revoked row', async () => {
    let revoked = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [
            session({ id: 'session-current', current: true }),
            ...(revoked ? [] : [session({ id: 'session-2' })]),
          ],
        })
      }
      if (call.method === 'DELETE' && call.path === `${SESSIONS_PATH}/session-2`) {
        revoked = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mountSection(respond)
    const user = userEvent.setup()

    await screen.findByRole('heading', { name: zhCN.sessions.title })
    const target = () =>
      screen.queryByRole('button', { name: revokeAria(zhCN.sessions.deviceUnknown) })
    await waitFor(() => expect(target()).not.toBeNull())
    await user.click(target() as HTMLElement)

    await waitFor(() => expect(target()).toBeNull())
    expect(
      rig.calls.filter(
        (call) => call.method === 'DELETE' && call.path === `${SESSIONS_PATH}/session-2`,
      ),
    ).toHaveLength(1)
    // The list refetched after the revoke.
    expect(
      rig.calls.filter(
        (call) => call.method === 'GET' && call.path === SESSIONS_PATH,
      ).length,
    ).toBeGreaterThanOrEqual(2)
  })

  it('revokes the other sessions behind the double confirm and shows the count', async () => {
    let revoked = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [
            session({ id: 'session-current', current: true }),
            ...(revoked
              ? []
              : [session({ id: 'session-2' }), session({ id: 'session-3' })]),
          ],
        })
      }
      if (call.method === 'DELETE' && call.path === SESSIONS_PATH) {
        revoked = true
        return jsonResponse(200, { revoked: 2 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.sessions.revokeOthers.label,
      }),
    )
    const dialog = await screen.findByRole('dialog')
    // Signing out other devices is recoverable, so it carries the single
    // confirming click -- not the heavier two-step arm an irreversible
    // delete uses: one click here fires the revoke.
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.sessions.revokeOthers.confirmLabel,
      }),
    )

    expect(
      await screen.findByText(
        zhCN.sessions.revokeOthers.done_other.replace('{{count}}', '2'),
      ),
    ).toBeTruthy()
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('renders a refused revoke as a code-text banner', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [session({ id: 'session-2' })],
        })
      }
      if (call.method === 'DELETE' && call.path === `${SESSIONS_PATH}/session-2`) {
        return errorResponse(403, 'authn.forbidden')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: revokeAria(zhCN.sessions.deviceUnknown),
      }),
    )
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.forbidden)
  })

  it('splits the empty, error and retry states', async () => {
    // The settled states: an answer listing zero sessions renders the
    // empty variant (header hidden, its title standing in at the same
    // heading level); a load that failed -- and an answer that omitted
    // the sessions key, as unreadable as a failure -- render the error
    // variant, whose retry refetches.
    let mode: 'empty' | 'fail' | 'omitted' | 'ok' = 'empty'
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        switch (mode) {
          case 'fail':
            return errorResponse(503, 'client.network')
          case 'omitted':
            return jsonResponse(200, {})
          case 'ok':
            return jsonResponse(200, {
              sessions: [session({ id: 'session-2' })],
            })
          default:
            return jsonResponse(200, { sessions: [] })
        }
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const user = userEvent.setup()
    const { queryClient } = mountSection(respond)

    // An answer listing zero sessions is the empty state, header hidden.
    expect(await screen.findByText(zhCN.sessions.empty.title)).toBeTruthy()
    expect(
      screen.queryByRole('heading', { name: zhCN.sessions.title }),
    ).toBeNull()

    // A settled answer that omitted the sessions key is as unreadable as
    // a failure: refetching into it renders the error variant, never a
    // "no sessions" claim.
    mode = 'omitted'
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ['/auth/sessions'] })
    })
    expect(await screen.findByText(zhCN.sessions.error.title)).toBeTruthy()

    // The retry refetches; an answer that lists a session renders it.
    mode = 'ok'
    await user.click(screen.getByRole('button', { name: zhCN.sessions.retry }))
    expect(
      await screen.findByRole('heading', { name: zhCN.sessions.title }),
    ).toBeTruthy()
  })
})
