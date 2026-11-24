/**
 * SessionsSection checks the session list, per-row and bulk revocation, and the display time zone.
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

const PROFILE = jsonResponse(200, { time_zone: '' })

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

function mountSection(respond: RealResponder, ui = <SessionsSection />) {
  const rig = makeRealClientRigWithoutSession(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(ui)
  return { rig, ...rendered }
}

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

function revokeAria(device: string): string {
  return zhCN.sessions.revokeAriaWithDevice.replace('{{device}}', device)
}

function deferredResponse(): {
  readonly promise: Promise<Response>
  readonly resolve: (response: Response) => void
} {
  let resolve!: (response: Response) => void
  const promise = new Promise<Response>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function revokeButtons(): HTMLElement[] {
  return screen.getAllByRole('button', {
    name: revokeAria(zhCN.sessions.deviceUnknown),
  })
}

const SESSION_INSTANT = '2026-08-01T02:05:00.000Z'

const SESSION_LAST_SEEN = '2026-08-03T09:12:00.000Z'

function bodyRows(): HTMLElement[] {
  return within(screen.getByRole('list')).getAllByRole('listitem')
}

function rowDetail(address: string, time: string): string {
  return zhCN.sessions.rowDetail
    .replace('{{address}}', address)
    .replace('{{time}}', time)
}

function renderedInstant(instant: string, zone: string = TEST_TIME_ZONE): string {
  return formatInstant(instant, 'zh-CN', { timeZone: zone })
}

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
    expect(screen.getByText(zhCN.sessions.deviceUnknown)).toBeTruthy()
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
    expect(screen.queryByRole('table')).toBeNull()
    expect(screen.queryByRole('columnheader')).toBeNull()
    const [current, other] = bodyRows() as [HTMLElement, HTMLElement]
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
    expect(
      screen.queryByText(rowDetail('198.51.100.4', renderedIn(storedZone))),
    ).toBeNull()
  })

  it('renders the harness’s fixed zone when the host supplies none', async () => {
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
    ).toHaveAttribute('aria-disabled', 'true')

    await user.click(screen.getByRole('button', { name: zhCN.sessions.nextPage }))
    await waitFor(() => expect(bodyRows()).toHaveLength(4))
    expect(
      screen.getByRole('button', { name: zhCN.sessions.nextPage }),
    ).toHaveAttribute('aria-disabled', 'true')
  })

  it('keeps the last-page control focused and unavailable without a reason', async () => {
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

    await waitFor(() => expect(bodyRows()).toHaveLength(8))
    await user.click(
      screen.getByRole('button', { name: zhCN.sessions.nextPage }),
    )
    await waitFor(() => expect(bodyRows()).toHaveLength(4))
    const next = screen.getByRole('button', {
      name: zhCN.sessions.nextPage,
    })
    expect(document.activeElement).toBe(next)
    expect(next).toHaveAttribute('aria-disabled', 'true')
    expect(next).not.toHaveAttribute('disabled')
    expect(next).toHaveAccessibleDescription('')
    expect(
      screen.getByRole('button', { name: zhCN.sessions.prevPage }),
    ).not.toHaveAttribute('aria-disabled')
  })

  it('shows a labelled row sign-out action that keeps focus while its revoke is in flight', async () => {
    const gate = deferredResponse()
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
              ? [session({ id: 'session-3' })]
              : [session({ id: 'session-2' }), session({ id: 'session-3' })]),
          ],
        })
      }
      if (
        call.method === 'DELETE' &&
        call.path === `${SESSIONS_PATH}/session-2`
      ) {
        return gate.promise.then(() => {
          revoked = true
          return new Response(null, { status: 204 })
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mountSection(respond)
    const user = userEvent.setup()

    await waitFor(() => expect(revokeButtons()).toHaveLength(2))
    const [initiating, next] = revokeButtons() as [HTMLElement, HTMLElement]
    expect(initiating).toHaveTextContent(zhCN.sessions.revokeRowLabel)
    expect(initiating.getAttribute('aria-label')).toContain(
      zhCN.sessions.revokeRowLabel,
    )

    await user.click(initiating)
    await waitFor(() =>
      expect(initiating).toHaveAttribute('aria-busy', 'true'),
    )
    expect(document.activeElement).toBe(initiating)
    expect(initiating).toHaveAttribute('aria-disabled', 'true')
    expect(initiating).not.toHaveAttribute('disabled')

    await user.click(initiating)
    expect(
      rig.calls.filter((call) => call.method === 'DELETE'),
    ).toHaveLength(1)

    await act(async () => {
      gate.resolve(new Response(null, { status: 204 }))
    })
    await waitFor(() => expect(revokeButtons()).toHaveLength(1))
    await waitFor(() => expect(document.activeElement).toBe(next))
    expect(document.activeElement).not.toBe(document.body)
  })

  it('holds the other rows and the pager unavailable without a reason while a row revoke runs', async () => {
    const gate = deferredResponse()
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
      if (
        call.method === 'DELETE' &&
        call.path === `${SESSIONS_PATH}/session-0`
      ) {
        return gate.promise
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mountSection(respond)
    const user = userEvent.setup()

    await waitFor(() => expect(revokeButtons()).toHaveLength(8))
    const [initiating, sibling] = revokeButtons() as [
      HTMLElement,
      HTMLElement,
    ]
    const deleteCount = () =>
      rig.calls.filter((call) => call.method === 'DELETE').length
    await user.click(initiating)
    await waitFor(() =>
      expect(initiating).toHaveAttribute('aria-busy', 'true'),
    )

    expect(sibling).toHaveAttribute('aria-disabled', 'true')
    expect(sibling).not.toHaveAttribute('aria-busy')
    expect(sibling).toHaveAccessibleDescription('')
    const before = deleteCount()
    await user.click(sibling)
    sibling.focus()
    await user.keyboard('{Enter}')
    expect(deleteCount()).toBe(before)

    const previous = screen.getByRole('button', {
      name: zhCN.sessions.prevPage,
    })
    const next = screen.getByRole('button', {
      name: zhCN.sessions.nextPage,
    })
    expect(previous).toHaveAttribute('aria-disabled', 'true')
    expect(next).toHaveAttribute('aria-disabled', 'true')
    expect(previous).toHaveAccessibleDescription('')
    expect(next).toHaveAccessibleDescription('')

    await act(async () => {
      gate.resolve(new Response(null, { status: 204 }))
    })
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: zhCN.sessions.nextPage }),
      ).not.toHaveAttribute('aria-disabled'),
    )
  })

  it('hands focus to the previous row when the last sign-out control leaves', async () => {
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
              ? [session({ id: 'session-2' })]
              : [session({ id: 'session-2' }), session({ id: 'session-3' })]),
          ],
        })
      }
      if (
        call.method === 'DELETE' &&
        call.path === `${SESSIONS_PATH}/session-3`
      ) {
        revoked = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)
    const user = userEvent.setup()

    await waitFor(() => expect(revokeButtons()).toHaveLength(2))
    const [previous] = revokeButtons() as [HTMLElement, HTMLElement]
    await user.click(revokeButtons()[1] as HTMLElement)
    await waitFor(() => expect(revokeButtons()).toHaveLength(1))
    await waitFor(() => expect(document.activeElement).toBe(previous))
  })

  it('lands on the row that slides up from the next page when the page’s last row is revoked', async () => {
    let revoked = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: Array.from({ length: 12 }, (_, index) => index)
            .filter((index) => !(revoked && index === 7))
            .map((index) => session({ id: `session-${index}`, user_agent: '' })),
        })
      }
      if (call.method === 'DELETE' && call.path === `${SESSIONS_PATH}/session-7`) {
        revoked = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)
    const user = userEvent.setup()

    await waitFor(() => expect(revokeButtons()).toHaveLength(8))
    await user.click(revokeButtons()[7] as HTMLElement)
    expect(
      await screen.findByText(
        zhCN.sessions.revokedOne.replace(
          '{{device}}',
          zhCN.sessions.deviceUnknown,
        ),
      ),
    ).toBeTruthy()
    const buttons = revokeButtons()
    expect(buttons).toHaveLength(8)
    await waitFor(() => expect(document.activeElement).toBe(buttons[7]))
  })

  it('lands on the previous page’s last row when the last page empties', async () => {
    let revoked = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: Array.from({ length: 9 }, (_, index) => index)
            .filter((index) => !(revoked && index === 8))
            .map((index) => session({ id: `session-${index}`, user_agent: '' })),
        })
      }
      if (call.method === 'DELETE' && call.path === `${SESSIONS_PATH}/session-8`) {
        revoked = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)
    const user = userEvent.setup()

    await waitFor(() => expect(bodyRows()).toHaveLength(8))
    await user.click(
      screen.getByRole('button', { name: zhCN.sessions.nextPage }),
    )
    await waitFor(() => expect(bodyRows()).toHaveLength(1))
    await user.click(revokeButtons()[0] as HTMLElement)
    expect(
      await screen.findByText(
        zhCN.sessions.revokedOne.replace(
          '{{device}}',
          zhCN.sessions.deviceUnknown,
        ),
      ),
    ).toBeTruthy()
    await waitFor(() => expect(bodyRows()).toHaveLength(8))
    const buttons = revokeButtons()
    expect(document.activeElement).toBe(buttons[7])
  })

  it('lands focus on the list when no row action remains', async () => {
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
      if (
        call.method === 'DELETE' &&
        call.path === `${SESSIONS_PATH}/session-2`
      ) {
        revoked = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)
    const user = userEvent.setup()

    await waitFor(() => expect(revokeButtons()).toHaveLength(1))
    await user.click(revokeButtons()[0] as HTMLElement)
    await waitFor(() => expect(bodyRows()).toHaveLength(1))
    const list = screen.getByRole('list', { name: zhCN.sessions.title })
    await waitFor(() => expect(document.activeElement).toBe(list))
    expect(document.activeElement).not.toBe(document.body)
  })

  it('lands focus on the section when the revoked row leaves an empty list', async () => {
    let revoked = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: revoked ? [] : [session({ id: 'session-2' })],
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
    mountSection(respond)
    const user = userEvent.setup()

    await waitFor(() => expect(revokeButtons()).toHaveLength(1))
    await user.click(revokeButtons()[0] as HTMLElement)
    expect(await screen.findByText(zhCN.sessions.empty.title)).toBeTruthy()
    expect(screen.queryByRole('list')).toBeNull()
    const section = screen.getByRole('group', { name: zhCN.sessions.title })
    await waitFor(() => expect(document.activeElement).toBe(section))
  })

  it('does not take focus back when the operator moved it during the revoke', async () => {
    const gate = deferredResponse()
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
              ? [session({ id: 'session-3' })]
              : [session({ id: 'session-2' }), session({ id: 'session-3' })]),
          ],
        })
      }
      if (
        call.method === 'DELETE' &&
        call.path === `${SESSIONS_PATH}/session-2`
      ) {
        return gate.promise.then(() => {
          revoked = true
          return new Response(null, { status: 204 })
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)
    const user = userEvent.setup()

    await waitFor(() => expect(revokeButtons()).toHaveLength(2))
    const [initiating] = revokeButtons() as [HTMLElement, HTMLElement]
    const bulk = screen.getByRole('button', {
      name: zhCN.sessions.revokeOthers.label,
    })
    await user.click(initiating)
    await waitFor(() =>
      expect(initiating).toHaveAttribute('aria-busy', 'true'),
    )
    bulk.focus()
    expect(document.activeElement).toBe(bulk)

    await act(async () => {
      gate.resolve(new Response(null, { status: 204 }))
    })
    await waitFor(() => expect(revokeButtons()).toHaveLength(1))
    expect(document.activeElement).toBe(bulk)
  })

  it('does not hand focus over for a row a later read removes', async () => {
    let reads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        reads += 1
        return jsonResponse(200, {
          sessions: [
            session({ id: 'session-current', current: true }),
            ...(reads > 2 ? [] : [session({ id: 'session-2', user_agent: '' })]),
          ],
        })
      }
      if (
        call.method === 'DELETE' &&
        call.path === `${SESSIONS_PATH}/session-2`
      ) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { queryClient } = mountSection(respond)
    const user = userEvent.setup()

    await waitFor(() => expect(revokeButtons()).toHaveLength(1))
    const triggering = revokeButtons()[0] as HTMLElement
    await user.click(triggering)
    expect(
      await screen.findByText(
        zhCN.sessions.revokedOne.replace(
          '{{device}}',
          zhCN.sessions.deviceUnknown,
        ),
      ),
    ).toBeTruthy()
    expect(revokeButtons()).toHaveLength(1)
    expect(document.activeElement).toBe(triggering)

    await act(async () => {
      await queryClient.invalidateQueries({
        queryKey: ['/api/v1/auth/sessions'],
      })
    })
    await waitFor(() => expect(bodyRows()).toHaveLength(1))
    expect(document.activeElement).toBe(document.body)
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
    expect(
      rig.calls.filter(
        (call) => call.method === 'GET' && call.path === SESSIONS_PATH,
      ).length,
    ).toBeGreaterThanOrEqual(2)
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
    expect(screen.queryByText(confirmation)).toBeNull()
  })

  it('holds the pager while a revoke attempt is pending', async () => {
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
    await user.click(
      screen.getByRole('button', { name: zhCN.sessions.nextPage }),
    )
    await waitFor(() => expect(bodyRows()).toHaveLength(4))
    expect(
      screen.getByRole('button', { name: zhCN.sessions.prevPage }),
    ).not.toHaveAttribute('aria-disabled')

    await user.click(revokeButtons()[0] as HTMLElement)
    await waitFor(() => expect(releaseDelete).toBeDefined())
    expect(
      screen.getByRole('button', { name: zhCN.sessions.prevPage }),
    ).toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.getByRole('button', { name: zhCN.sessions.nextPage }),
    ).toHaveAttribute('aria-disabled', 'true')

    await act(async () => {
      releaseDelete?.(new Response(null, { status: 204 }))
    })
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: zhCN.sessions.prevPage }),
      ).not.toHaveAttribute('aria-disabled'),
    )
  })

  it('renders its bulk action into the host header when the host supplies the slot', async () => {
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
    expect(screen.getByTestId('host-block-header')).toContainElement(bulk)
    expect(
      within(
        screen.getByRole('region', { name: zhCN.sessions.title }),
      ).queryByRole('button', { name: zhCN.sessions.revokeOthers.label }),
    ).toBeNull()
  })

  it('renders its bulk action inside the section when no slot is supplied', async () => {
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
    expect(bulk).toHaveClass('MuiButton-sizeSmall')
    const [rowRevoke] = revokeButtons()
    expect(rowRevoke).toBeDefined()
    let bulkColor = getComputedStyle(bulk).color
    for (let depth = 0; depth < 5 && bulkColor.startsWith('var('); depth += 1) {
      const name = bulkColor.slice(4, -1).trim()
      bulkColor = getComputedStyle(bulk).getPropertyValue(name).trim()
    }
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

  it('keeps the bulk control busy and the row actions unavailable while the confirmed revoke runs', async () => {
    const gate = deferredResponse()
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, {
          sessions: [
            session({ id: 'session-current', current: true }),
            session({ id: 'session-2' }),
            session({ id: 'session-3' }),
          ],
        })
      }
      if (call.method === 'DELETE' && call.path === SESSIONS_PATH) {
        return gate.promise
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mountSection(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.sessions.revokeOthers.label,
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.sessions.revokeOthers.confirmLabel,
      }),
    )
    await waitFor(() =>
      expect(
        within(screen.getByRole('dialog')).getByRole('button', {
          name: zhCN.sessions.revokeOthers.confirmLabel,
        }),
      ).toHaveAttribute('aria-busy', 'true'),
    )
    const rowActions = screen.getAllByRole('button', {
      name: revokeAria(zhCN.sessions.deviceUnknown),
      hidden: true,
    })
    for (const row of rowActions) {
      expect(row).toHaveAttribute('aria-disabled', 'true')
      expect(row).not.toHaveAttribute('aria-busy')
      expect(row).toHaveAccessibleDescription('')
    }
    const deletes = () =>
      rig.calls.filter((call) => call.method === 'DELETE').length
    const before = deletes()
    const [firstRow] = rowActions as [HTMLElement]
    await user.click(firstRow)
    firstRow.focus()
    await user.keyboard('{Enter}')
    expect(deletes()).toBe(before)

    await act(async () => {
      gate.resolve(jsonResponse(200, { revoked: 2 }))
    })
    expect(
      await screen.findByText(
        zhCN.sessions.revokeOthers.done_other.replace('{{count}}', '2'),
      ),
    ).toBeTruthy()
  })

  it('returns focus to the bulk control when its confirmation closes while the refetch is in flight', async () => {
    let reads = 0
    let releaseRefetch: ((response: Response) => void) | undefined
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        reads += 1
        if (reads > 1) {
          return new Promise<Response>((resolve) => {
            releaseRefetch = resolve
          })
        }
        return jsonResponse(200, {
          sessions: [
            session({ id: 'session-current', current: true }),
            session({ id: 'session-2' }),
            session({ id: 'session-3' }),
          ],
        })
      }
      if (call.method === 'DELETE' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, { revoked: 2 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mountSection(respond)
    const user = userEvent.setup()

    const bulk = await screen.findByRole('button', {
      name: zhCN.sessions.revokeOthers.label,
    })
    await user.click(bulk)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.sessions.revokeOthers.confirmLabel,
      }),
    )

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(releaseRefetch).toBeDefined())
    await waitFor(() => expect(bulk).toHaveFocus())
    expect(bulk).toHaveAttribute('aria-busy', 'true')
    expect(bulk).toHaveAttribute('aria-disabled', 'true')

    await act(async () => {
      releaseRefetch?.(
        jsonResponse(200, {
          sessions: [session({ id: 'session-current', current: true })],
        }),
      )
    })
    await waitFor(() =>
      expect(
        screen.queryByRole('button', {
          name: zhCN.sessions.revokeOthers.label,
        }),
      ).toBeNull(),
    )
  })

  it('does not take focus back when the operator moved it while the bulk revoke lands', async () => {
    let reads = 0
    let releaseRefetch: ((response: Response) => void) | undefined
    const respond: RealResponder = (call: RealCall) => {
      if (call.path === PROFILE_PATH) {
        return PROFILE
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        reads += 1
        if (reads > 1) {
          return new Promise<Response>((resolve) => {
            releaseRefetch = resolve
          })
        }
        return jsonResponse(200, {
          sessions: [
            session({ id: 'session-current', current: true }),
            session({ id: 'session-2' }),
          ],
        })
      }
      if (call.method === 'DELETE' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, { revoked: 1 })
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
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.sessions.revokeOthers.confirmLabel,
      }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(releaseRefetch).toBeDefined())

    const section = screen.getByRole('group', { name: zhCN.sessions.title })
    section.focus()
    expect(section).toHaveFocus()

    await act(async () => {
      releaseRefetch?.(
        jsonResponse(200, {
          sessions: [session({ id: 'session-current', current: true })],
        }),
      )
    })
    await waitFor(() =>
      expect(
        screen.queryByRole('button', {
          name: zhCN.sessions.revokeOthers.label,
        }),
      ).toBeNull(),
    )
    expect(section).toHaveFocus()
  })

  it('lands focus on the list when the bulk action leaves after revoking the other sessions', async () => {
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
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.sessions.revokeOthers.confirmLabel,
      }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(bodyRows()).toHaveLength(1))
    expect(
      screen.queryByRole('button', { name: zhCN.sessions.revokeOthers.label }),
    ).toBeNull()
    const list = screen.getByRole('list', { name: zhCN.sessions.title })
    await waitFor(() => expect(document.activeElement).toBe(list))
    expect(document.activeElement).not.toBe(document.body)
  })

  it('lands focus on the list although another role=dialog node stays in the document', async () => {
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
    mountSection(
      respond,
      <>
        <div role="dialog" aria-label="shell drawer" />
        <SessionsSection />
      </>,
    )
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.sessions.revokeOthers.label,
      }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.sessions.revokeOthers.confirmTitle,
    })
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.sessions.revokeOthers.confirmLabel,
      }),
    )
    await waitFor(() => expect(bodyRows()).toHaveLength(1))
    expect(
      screen.queryByRole('button', { name: zhCN.sessions.revokeOthers.label }),
    ).toBeNull()
    const list = screen.getByRole('list', { name: zhCN.sessions.title })
    await waitFor(() => expect(document.activeElement).toBe(list))
    expect(document.activeElement).not.toBe(document.body)
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

    expect(await screen.findByText(zhCN.sessions.empty.title)).toBeTruthy()
    expect(
      screen.queryByRole('heading', { name: zhCN.sessions.title }),
    ).toBeNull()

    mode = 'omitted'
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ['/api/v1/auth/sessions'] })
    })
    expect(await screen.findByText(zhCN.sessions.error.title)).toBeTruthy()

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

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(target()).not.toBeNull()
    expect(screen.queryByText(zhCN.sessions.error.title)).toBeNull()

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
