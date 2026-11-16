/**
 * SessionsSection behaviour over the authn session routes: GET
 * /api/v1/auth/sessions, DELETE /api/v1/auth/sessions/{id} and DELETE /api/v1/auth/sessions.
 *
 * What this suite pins, per story: the live sessions the server lists
 * render, the current one marked and carrying no revoke affordance, a
 * row without a user agent falling back to the unknown-device label (the
 * raw UA never rendered); a single-row revoke DELETE-s that exact
 * session and refetches the list, so the revoked row is gone; the
 * section-top "sign out other devices" action sits behind the danger
 * double-confirm, DELETE-s the collection and surfaces the answer's
 * revoked count; a refused revoke renders its code text below the list;
 * paging clears the settled result and is held while an attempt is in
 * flight; and the settled states split -- an answer listing zero sessions renders
 * the empty state, a load that failed (and an answer omitting the
 * sessions key) renders the error state with a retry. The display-timezone
 * cases pin the single resolved tier: the host supplies the account's
 * stored choice through DisplayTimeZoneProvider, the section renders that
 * value, and with no provider the harness's fixed TEST_TIME_ZONE applies.
 */

import { useState } from 'react'
import type { ReactElement } from 'react'
import Portal from '@mui/material/Portal'
import { describe, expect, it } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { formatInstant } from '@speed/i18n'
import { DisplayTimeZoneProvider } from '@speed/ui-kit'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import {
  errorResponse,
  jsonResponse,
  makeRealClientRigWithoutSession,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { TEST_TIME_ZONE } from '@speed/test-utils/render'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { SessionsSection } from './SessionsSection.js'

const SESSIONS_PATH = '/api/v1/auth/sessions'
const PROFILE_PATH = '/api/v1/auth/me'

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
 * per test and the token planted before render. The unit renders inside
 * the caller's own tree when one is given (the display-timezone cases
 * mount their host context above the section). */
function mountSection(respond: RealResponder, ui = <SessionsSection />) {
  const rig = makeRealClientRigWithoutSession(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(ui)
  return { rig, ...rendered }
}

/**
 * A host that owns the surrounding block: it takes the section's bulk
 * action through the render slot and portals it into a header slot of its
 * own, the way a host lifts the action beside its block title.
 */
function HostedSessionsSection(): ReactElement {
  const [target, setTarget] = useState<HTMLDivElement | null>(null)
  return (
    <div>
      <div data-testid="host-block-header">
        <div ref={setTarget} />
      </div>
      <SessionsSection
        renderHeaderAction={(action) =>
          target === null ? null : (
            <Portal container={target}>{action}</Portal>
          )
        }
      />
    </div>
  )
}

/** The revoke aria-label the component builds for a row's device label. */
function revokeAria(device: string): string {
  return zhCN.sessions.revokeAriaWithDevice.replace('{{device}}', device)
}

/** The instant the default row helper carries. */
const SESSION_INSTANT = '2026-08-01T02:05:00.000Z'

/** A last-active instant later than the default row's sign-in instant. */
const SESSION_LAST_SEEN = '2026-08-03T09:12:00.000Z'

/** The session list's rows, one item per session. */
function bodyRows(): HTMLElement[] {
  return within(screen.getByRole('list')).getAllByRole('listitem')
}

/** The second line of a row: the address and the last activity, as the
 * bundle composes them. */
function rowDetail(address: string, time: string): string {
  return zhCN.sessions.rowDetail
    .replace('{{address}}', address)
    .replace('{{time}}', time)
}

/** The instant rendered in `zone`; the harness's fixed zone when absent. */
function renderedInstant(instant: string, zone: string = TEST_TIME_ZONE): string {
  return formatInstant(instant, 'zh-CN', { timeZone: zone })
}

/** The default row's sign-in instant rendered in `zone`. */
function renderedIn(zone?: string): string {
  return renderedInstant(SESSION_INSTANT, zone)
}

/**
 * A zone whose rendering of the row's instant reads differently from
 * `reference`. Picking it from the rendering, not from the zone name,
 * keeps the assertion able to tell the two apart on any machine (two
 * zone names can share an offset at one instant, and the browser zone is
 * whatever the test host happens to set).
 */
function zoneRenderingDifferentlyFrom(reference: string): string {
  const zone = [
    'Asia/Tokyo',
    'America/New_York',
    'Pacific/Kiritimati',
    'UTC',
  ].find((candidate) => renderedIn(candidate) !== reference)
  if (zone === undefined) {
    throw new Error('no candidate zone renders the instant differently')
  }
  return zone
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

  it("renders the session's activity instant in the activity column", async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [session({ id: 'session-2', user_agent: '' })],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)

    // The listed session's activity column names its last activity -- here
    // its sign-in instant, since the default row has not been touched since
    // -- in the display zone.
    await waitFor(() => expect(bodyRows()).toHaveLength(1))
    expect(
      within(bodyRows()[0] as HTMLElement).getByText(
        rowDetail('198.51.100.4', renderedInstant(SESSION_INSTANT)),
      ),
    ).toBeTruthy()
  })

  it('renders the last-active time when it differs from the sign-in time', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [
            session({
              id: 'session-2',
              user_agent: '',
              last_seen_at: SESSION_LAST_SEEN,
            }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)

    // A coalesced touch moves the row's last activity past its creation, so
    // the activity column names the later instant rather than the sign-in one.
    await waitFor(() => expect(bodyRows()).toHaveLength(1))
    const row = bodyRows()[0] as HTMLElement
    expect(
      within(row).getByText(
        rowDetail('198.51.100.4', renderedInstant(SESSION_LAST_SEEN)),
      ),
    ).toBeTruthy()
    expect(
      within(row).queryByText(
        rowDetail('198.51.100.4', renderedInstant(SESSION_INSTANT)),
      ),
    ).toBeNull()
  })

  it('renders the sessions as list items, never as a table', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [
            session({ id: 'session-2', user_agent: '', current: true }),
            session({ id: 'session-3', user_agent: '' }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)

    await waitFor(() => expect(bodyRows()).toHaveLength(2))
    // No table, no header rail: the section is one list of sessions.
    expect(screen.queryByRole('table')).toBeNull()
    expect(screen.queryByRole('columnheader')).toBeNull()
    const [current, other] = bodyRows() as [HTMLElement, HTMLElement]
    // The current session carries its chip and offers no sign-out; every
    // other row's control names the device it signs out.
    expect(within(current).getByText(zhCN.sessions.current)).toBeTruthy()
    expect(within(current).queryByRole('button')).toBeNull()
    expect(
      within(other).getByRole('button', {
        name: revokeAria(zhCN.sessions.deviceUnknown),
      }),
    ).toBeTruthy()
    await expectNoAxeViolations()
  })

  it('renders in the host display timezone, not the profile’s stored zone', async () => {
    // The host owns the resolution: it reads the account's stored choice
    // and supplies it through DisplayTimeZoneProvider. The section renders
    // that value and keeps no second chain over the profile, so a stored
    // zone the host did not provide has no effect here. The two zones are
    // chosen by their rendering of this instant, so the expected text
    // cannot coincide with the other's.
    const storedZone = zoneRenderingDifferentlyFrom(renderedIn())
    const hostZone = zoneRenderingDifferentlyFrom(renderedIn(storedZone))
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return jsonResponse(200, { time_zone: storedZone })
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [session({ id: 'session-2', user_agent: '' })],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(
      respond,
      <DisplayTimeZoneProvider timeZone={hostZone}>
        <SessionsSection />
      </DisplayTimeZoneProvider>,
    )

    expect(
      await screen.findByText(rowDetail('198.51.100.4', renderedIn(hostZone))),
    ).toBeTruthy()
    // The profile's stored zone is not a second source of truth.
    expect(
      screen.queryByText(rowDetail('198.51.100.4', renderedIn(storedZone))),
    ).toBeNull()
  })

  it('renders the harness’s fixed zone when the host supplies none', async () => {
    // With no provider the tree renders in TestTimeZoneProvider's fixed
    // zone, and the profile's stored zone still does not reach the row:
    // the account surfaces read the host's resolved value only.
    const storedZone = zoneRenderingDifferentlyFrom(renderedIn())
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return jsonResponse(200, { time_zone: storedZone })
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [session({ id: 'session-2', user_agent: '' })],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)

    expect(
      await screen.findByText(rowDetail('198.51.100.4', renderedIn())),
    ).toBeTruthy()
    expect(
      screen.queryByText(rowDetail('198.51.100.4', renderedIn(storedZone))),
    ).toBeNull()
  })

  it('renders in the browser zone when the account’s time zone is empty', async () => {
    // The host forwards an unset account preference to the provider as an
    // empty value, overriding the harness's fixed zone. The section must
    // land on the browser's zone, not the terminal UTC tier. The suite's
    // vitest config pins a non-UTC browser zone so the two differ.
    const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone
    expect(browserZone).not.toBe('UTC')
    expect(renderedIn(browserZone)).not.toBe(renderedIn('UTC'))
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [session({ id: 'session-2', user_agent: '' })],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(
      respond,
      <DisplayTimeZoneProvider timeZone="">
        <SessionsSection />
      </DisplayTimeZoneProvider>,
    )

    expect(
      await screen.findByText(
        rowDetail('198.51.100.4', renderedIn(browserZone)),
      ),
    ).toBeTruthy()
    expect(
      screen.queryByText(rowDetail('198.51.100.4', renderedIn('UTC'))),
    ).toBeNull()
  })

  it('raises the row revoke control to the 44px touch-target floor under a coarse pointer', async () => {
    // The per-row revoke control is a compact icon button; on a
    // finger-driven device it gates up to the 44x44 floor (the layout
    // design's pointer rule). jsdom evaluates no media condition, so the
    // emitted CSS is matched against the control itself: a coarse rule
    // elsewhere on the page cannot satisfy this assertion for it.
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [session({ id: 'session-2', user_agent: '' })],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)
    const revoke = await screen.findByRole('button', {
      name: revokeAria(zhCN.sessions.deviceUnknown),
    })
    expect(hasCoarsePointerTouchTarget(revoke)).toBe(true)
  })

  it('raises the session pager to the 44px floor under a coarse pointer', async () => {
    // The pager buttons are compact action controls on the same surface
    // as the row revoke: on a finger-driven device each one gates up to
    // the 44x44 floor. Asserted per element, so the row control's own
    // gate does not vouch for them.
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
    const next = await screen.findByRole('button', {
      name: zhCN.sessions.nextPage,
    })
    const previous = screen.getByRole('button', {
      name: zhCN.sessions.prevPage,
    })
    expect(hasCoarsePointerTouchTarget(next)).toBe(true)
    expect(hasCoarsePointerTouchTarget(previous)).toBe(true)
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
    await waitFor(() => expect(bodyRows()).toHaveLength(8))
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
    await waitFor(() => expect(bodyRows()).toHaveLength(4))
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
    // The single revoke no longer succeeds silently: its confirmation names
    // the session it signed out.
    expect(
      await screen.findByText(
        zhCN.sessions.revokedOne.replace(
          '{{device}}',
          zhCN.sessions.deviceUnknown,
        ),
      ),
    ).toBeTruthy()
    expect(zhCN.sessions.revokedOne).toContain('{{device}}')
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
    // The confirmation sits below the table whose row action wrote it, not
    // above it.
    const confirmation = screen.getByText(
      zhCN.sessions.revokedOne.replace(
        '{{device}}',
        zhCN.sessions.deviceUnknown,
      ),
    )
    expect(
      screen
        .getByRole('list')
        .compareDocumentPosition(confirmation) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('clears a row result when the list pages', async () => {
    // Paging changes what the list describes: a settled result naming a
    // device must not stand over the next page's rows.
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
      if (call.method === 'DELETE' && call.path === `${SESSIONS_PATH}/session-2`) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)
    const user = userEvent.setup()

    await screen.findByRole('heading', { name: zhCN.sessions.title })
    await waitFor(() => expect(bodyRows()).toHaveLength(8))
    const revokeButtons = screen.getAllByRole('button', {
      name: revokeAria(zhCN.sessions.deviceUnknown),
    })
    await user.click(revokeButtons[2] as HTMLElement)

    const confirmation = zhCN.sessions.revokedOne.replace(
      '{{device}}',
      zhCN.sessions.deviceUnknown,
    )
    expect(await screen.findByText(confirmation)).toBeTruthy()

    await user.click(
      screen.getByRole('button', { name: zhCN.sessions.nextPage }),
    )
    await waitFor(() =>
      expect(
        screen.getByText(
          zhCN.sessions.pageIndicator
            .replace('{{page}}', '2')
            .replace('{{total}}', '2'),
        ),
      ).toBeTruthy(),
    )
    // The previous page's result is gone with the context that produced it.
    expect(screen.queryByText(confirmation)).toBeNull()
  })

  it('holds the pager while a revoke attempt is pending', async () => {
    // Paging is a context change for the section's feedback scope; while an
    // attempt is in flight (and through the refetch that follows it) the
    // pager is disabled, so no reset can drop a result still to land.
    let releaseDelete: ((response: Response) => void) | undefined
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
      if (call.method === 'DELETE' && call.path === `${SESSIONS_PATH}/session-8`) {
        return new Promise<Response>((resolve) => {
          releaseDelete = resolve
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)
    const user = userEvent.setup()

    await screen.findByRole('heading', { name: zhCN.sessions.title })
    await waitFor(() => expect(bodyRows()).toHaveLength(8))
    // Move to the second page, where the previous-page control is enabled.
    await user.click(
      screen.getByRole('button', { name: zhCN.sessions.nextPage }),
    )
    await waitFor(() => expect(bodyRows()).toHaveLength(4))
    expect(
      screen.getByRole('button', { name: zhCN.sessions.prevPage }),
    ).not.toBeDisabled()

    await user.click(
      screen.getAllByRole('button', {
        name: revokeAria(zhCN.sessions.deviceUnknown),
      })[0] as HTMLElement,
    )
    await waitFor(() => expect(releaseDelete).toBeDefined())
    expect(
      screen.getByRole('button', { name: zhCN.sessions.prevPage }),
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: zhCN.sessions.nextPage }),
    ).toBeDisabled()

    await act(async () => {
      releaseDelete?.(new Response(null, { status: 204 }))
    })
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: zhCN.sessions.prevPage }),
      ).not.toBeDisabled(),
    )
  })

  it('renders its bulk action into the host header when the host supplies the slot', async () => {
    // The slot is the additive contract: supplied, the section leaves the
    // action's placement to the host, which portals it into a block header.
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [
            session({ id: 'session-current', current: true }),
            session({ id: 'session-2', user_agent: '' }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond, <HostedSessionsSection />)

    const bulk = await screen.findByRole('button', {
      name: zhCN.sessions.revokeOthers.label,
    })
    // The host's header holds the action ...
    expect(screen.getByTestId('host-block-header')).toContainElement(bulk)
    // ... and the section's own content does not: no action row was
    // rendered beside the list.
    expect(
      within(
        screen.getByRole('region', { name: zhCN.sessions.title }),
      ).queryByRole('button', { name: zhCN.sessions.revokeOthers.label }),
    ).toBeNull()
  })

  it('renders its bulk action inside the section when no slot is supplied', async () => {
    // Without the slot the default placement is unchanged: the action is
    // the section's own, rendered with its block.
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [
            session({ id: 'session-current', current: true }),
            session({ id: 'session-2', user_agent: '' }),
          ],
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)

    const bulk = await screen.findByRole('button', {
      name: zhCN.sessions.revokeOthers.label,
    })
    expect(
      within(
        screen.getByRole('region', { name: zhCN.sessions.title }),
      ).getByRole('button', { name: zhCN.sessions.revokeOthers.label }),
    ).toBe(bulk)
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

    const bulk = await screen.findByRole('button', {
      name: zhCN.sessions.revokeOthers.label,
    })
    // The bulk control is a block-level action like the other sections'
    // header actions: small, and painted with the same danger color the
    // row-level sign-out glyph carries.
    expect(bulk).toHaveClass('MuiButton-sizeSmall')
    const rowRevoke = screen
      .getAllByRole('button')
      .find((button) => button.className.includes('MuiIconButton-root'))
    expect(rowRevoke).toBeDefined()
    // MUI paints the outlined bulk control through a CSS variable chain;
    // resolve it to compare the literal color the glyph carries.
    let bulkColor = getComputedStyle(bulk).color
    for (let depth = 0; depth < 5 && bulkColor.startsWith('var('); depth += 1) {
      const name = bulkColor.slice(4, -1).trim()
      bulkColor = getComputedStyle(bulk).getPropertyValue(name).trim()
    }
    // The variable chain ends in a hex token while the glyph's computed
    // color is rgb(...); compare the same color in one notation.
    const toRgb = (value: string): string => {
      const hex = /^#([0-9a-f]{6})$/i.exec(value.trim())
      if (hex === null) {
        return value
      }
      const n = Number.parseInt(hex[1] ?? '', 16)
      return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`
    }
    expect(getComputedStyle(rowRevoke as Element).color).toBe(toRgb(bulkColor))

    await user.click(bulk)
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
      await queryClient.invalidateQueries({ queryKey: ['/api/v1/auth/sessions'] })
    })
    expect(await screen.findByText(zhCN.sessions.error.title)).toBeTruthy()

    // The retry refetches; an answer that lists a session renders it.
    mode = 'ok'
    await user.click(screen.getByRole('button', { name: zhCN.sessions.retry }))
    expect(
      await screen.findByRole('heading', { name: zhCN.sessions.title }),
    ).toBeTruthy()
  })

  it('keeps the old rows under a stale notice when the refetch after a revoke fails', async () => {
    const user = userEvent.setup()
    let revoked = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        // The first read settles the list; the refetch after the revoke
        // fails, so the section keeps the rows it already had.
        return revoked
          ? errorResponse(500, 'authn.unavailable')
          : jsonResponse(200, {
              sessions: [session({ id: 'session-2', user_agent: '' })],
            })
      }
      if (
        call.method === 'DELETE' &&
        call.path === `${SESSIONS_PATH}/session-2`
      ) {
        revoked = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mountSection(respond)

    await screen.findByRole('heading', { name: zhCN.sessions.title })
    const target = () =>
      screen.queryByRole('button', {
        name: revokeAria(zhCN.sessions.deviceUnknown),
      })
    await waitFor(() => expect(target()).not.toBeNull())
    await user.click(target() as HTMLElement)

    // The refresh failed, but the old row stays on screen under the stale
    // notice rather than being replaced by the error placeholder.
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(target()).not.toBeNull()
    expect(screen.queryByText(zhCN.sessions.error.title)).toBeNull()

    // The retry is the section's own retry: it re-reads the list.
    const readsBeforeRetry = rig.calls.filter(
      (call) => call.method === 'GET' && call.path === SESSIONS_PATH,
    ).length
    await user.click(screen.getByRole('button', { name: zhCN.sessions.retry }))
    await waitFor(() =>
      expect(
        rig.calls.filter(
          (call) => call.method === 'GET' && call.path === SESSIONS_PATH,
        ).length,
      ).toBeGreaterThan(readsBeforeRetry),
    )
  })
})
