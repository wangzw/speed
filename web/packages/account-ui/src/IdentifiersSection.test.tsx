/**
 * IdentifiersSection checks identifiers, their grouping, the row actions and the removal write.
 */

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import type { ReactElement } from 'react'
import { useState } from 'react'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { chipClasses } from '@mui/material/Chip'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { identifier, profileBody } from '../test-utils/fixtures.js'
import {
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  type RealCall,
  type RealResponder,
} from '../test-utils/real-client.js'
import { renderWithProviders } from '../test-utils/render.js'
import { captureRefreshWarning } from '../test-utils/refresh-warning.js'
import {
  AUTHENTICATED_SESSION,
  EMPTY_FEATURES,
  FEATURES_PATH,
  SESSION_PATH,
  makeRotationRig,
} from '../test-utils/rotation.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import {
  pendingStepUpResume,
  rememberStepUpResume,
} from './internal/step-up-resume.js'
import { IdentifiersSection } from './IdentifiersSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

function mount(
  respond: RealResponder,
  gate?: {
    readonly required: boolean
    readonly provider: string
    readonly site_key?: string
  },
) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const humanGate =
    gate === undefined
      ? undefined
      : { phone: { required: false, provider: 'off' }, other: gate }
  const rendered = renderWithProviders(
    <IdentifiersSection session={rig.session} humanGate={humanGate} />,
  )
  return { rig, ...rendered }
}

function rowScope(value: string): HTMLElement {
  for (const text of screen.getAllByText(value)) {
    const row = text.closest('li, tr')
    if (row !== null) {
      return row as HTMLElement
    }
  }
  throw new Error(`no row shows ${value}`)
}

function rowActions(value: string): HTMLElement[] {
  return within(rowScope(value)).queryAllByRole('button')
}

function rowAction(value: string, name: string): HTMLElement | null {
  return within(rowScope(value)).queryByRole('button', { name })
}

async function clickRowAction(
  user: ReturnType<typeof userEvent.setup>,
  value: string,
  name: string,
): Promise<void> {
  await waitFor(() => expect(rowScope(value)).toBeTruthy())
  await user.click(
    within(rowScope(value)).getByRole('button', { name }),
  )
}

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

function verificationForm(): HTMLElement {
  const field = screen.getByLabelText(zhCN.identifiers.add.code.label)
  const form = field.closest('form')
  if (form === null) {
    throw new Error('the verification code field has no form')
  }
  return form as HTMLElement
}

describe('IdentifiersSection', () => {
  beforeEach(() => {
    window.sessionStorage.clear()
  })

  it('renders each identifier with its kind, value and verification, and passes axe', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({ id: 'id-email', kind: 'email', value: 'ada@x.test' }),
              identifier({
                id: 'id-phone',
                kind: 'phone',
                value: '+14155550123',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'admin',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(
      await screen.findByRole('heading', { name: zhCN.identifiers.title }),
    ).toBeTruthy()
    await waitFor(() =>
      expect(screen.getAllByText('ada@x.test')).toHaveLength(2),
    )
    expect(screen.getAllByText(zhCN.identifiers.kind.email)).toHaveLength(1)
    expect(screen.getByText('+14155550123')).toBeTruthy()
    expect(screen.getAllByText(zhCN.identifiers.verified)).toHaveLength(1)
    expect(screen.getByText(zhCN.identifiers.unverified)).toBeTruthy()

    await expectNoAxeViolations()
  })

  describe('verification chip colors', () => {
    function chipFor(text: string): HTMLElement {
      const label = screen.getByText(text, {
        selector: `.${chipClasses.label}`,
      })
      const chip = label.closest(`.${chipClasses.root}`)
      if (chip === null) {
        throw new Error(`the "${text}" label is not inside a chip`)
      }
      return chip as HTMLElement
    }

    const views = [
      { name: 'the merged view', axis: undefined },
      { name: 'the contact table', axis: 'contact' as const },
    ]

    for (const view of views) {
      it(`colors a proved identifier success and an unproved one default in ${view.name}`, async () => {
        const respond: RealResponder = (call: RealCall) => {
          if (call.method === 'GET' && call.path === PROFILE_PATH) {
            return jsonResponse(
              200,
              profileBody({
                identifiers: [
                  identifier({
                    id: 'id-verified',
                    kind: 'email',
                    value: 'ada@x.test',
                    proven: true,
                    sign_in: true,
                  }),
                  identifier({
                    id: 'id-unverified',
                    kind: 'email',
                    value: 'bea@x.test',
                    proven: false,
                    verified_at: '2026-01-01T00:00:00Z',
                    verified_via: 'admin',
                    sign_in: false,
                  }),
                ],
              }),
            )
          }
          throw new Error(`unexpected ${call.method} ${call.path}`)
        }
        const rig = makeRealClientRig(respond)
        rig.store.set('access-1')
        renderWithProviders(
          <IdentifiersSection session={rig.session} axis={view.axis} />,
        )

        expect(await screen.findAllByText('ada@x.test')).not.toHaveLength(0)
        const verified = chipFor(zhCN.identifiers.verified)
        expect(verified).toHaveClass(chipClasses.colorSuccess)
        expect(verified).not.toHaveClass(chipClasses.colorPrimary)

        const unverified = chipFor(zhCN.identifiers.unverified)
        expect(unverified).toHaveClass(chipClasses.colorDefault)
        expect(unverified).not.toHaveClass(chipClasses.colorPrimary)
      })
    }
  })

  it('re-issues the recorded removal when the page mounts after the new sign-in', async () => {
    rememberStepUpResume('user-1', 'identifiers', '/account/profile', {
      step: 'remove',
      id: 'id-phone',
    })
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-phone',
                kind: 'phone',
                value: '+14155550123',
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/identifiers/id-phone'
      ) {
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(
      <IdentifiersSection
        session={rig.session}
        returnPath="/account/profile"
      />,
    )

    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) =>
            call.method === 'DELETE' &&
            call.path === '/api/v1/auth/identifiers/id-phone',
        ),
      ).toBe(true),
    )
    expect(pendingStepUpResume('user-1')).toBeNull()
  })

  it('renders every unproved address as Not verified, whatever established it', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-admin',
                kind: 'email',
                value: 'operator@demo.test',
                proven: false,
                verified_via: 'admin',
              }),
              identifier({
                id: 'id-import',
                kind: 'email',
                value: 'imported@demo.test',
                proven: false,
                verified_via: 'import-unverified',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await waitFor(() =>
      expect(screen.getAllByText('operator@demo.test')).toHaveLength(2),
    )
    expect(screen.getAllByText(zhCN.identifiers.unverified)).toHaveLength(2)
    expect(screen.queryByText(zhCN.identifiers.verified)).toBeNull()
    expect(zhCN.identifiers).not.toHaveProperty('addedByAdmin')
    expect(zhCN.identifiers).not.toHaveProperty('importedUnverified')
  })

  it('offers a verify action on an unverified email row of a verified account', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'verified', email: 'ada@x.test', since: null },
            identifiers: [
              identifier({ id: 'id-primary', kind: 'email', value: 'ada@x.test' }),
              identifier({
                id: 'id-secondary',
                kind: 'email',
                value: 'second@x.test',
                proven: false,
                verified_via: 'admin',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await waitFor(() =>
      expect(screen.getAllByText('second@x.test')).toHaveLength(1),
    )
    expect(rowAction('second@x.test', zhCN.identifiers.verify)).toBeTruthy()
    expect(rowAction('ada@x.test', zhCN.identifiers.verify)).toBeNull()
  })

  it('keeps the verify action when the row is the only login name', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: true,
            identifiers: [
              identifier({
                id: 'id-only',
                kind: 'email',
                value: 'only@x.test',
                proven: false,
                verified_via: 'self',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await waitFor(() =>
      expect(screen.getAllByText('only@x.test')).toHaveLength(2),
    )
    const actions = rowActions('only@x.test')
    expect(actions.map((button) => button.textContent?.trim())).toEqual([
      zhCN.identifiers.verify,
    ])
    const onlyVerify = rowAction('only@x.test', zhCN.identifiers.verify)
    expect(onlyVerify).toBeEnabled()
    expect(onlyVerify).not.toHaveAttribute('aria-disabled', 'true')
    expect(rowAction('only@x.test', zhCN.identifiers.remove)).toBeNull()
    expect(
      actions.some((button) => button.hasAttribute('aria-haspopup')),
    ).toBe(false)
    expect(screen.queryByRole('menuitem')).toBeNull()
  })

  it('proves an unverified row through its own address and flips the chip', async () => {
    let proven = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'verified', email: 'ada@x.test', since: null },
            identifiers: [
              identifier({ id: 'id-primary', kind: 'email', value: 'ada@x.test' }),
              identifier({
                id: 'id-secondary',
                kind: 'email',
                value: 'second@x.test',
                proven,
                verified_via: proven ? 'email-code' : 'admin',
              }),
            ],
          }),
        )
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify'
      ) {
        proven = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await clickRowAction(user, 'second@x.test', zhCN.identifiers.verify)

    const valueField = screen.getByLabelText(
      zhCN.identifiers.add.value.labelEmail,
    )
    expect(valueField).toHaveValue('second@x.test')
    expect(valueField).toHaveAttribute('readonly')

    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )
    await screen.findByLabelText(zhCN.identifiers.add.code.label)
    await expectNoAxeViolations()
    const sends = rig.calls.filter(
      (call) =>
        call.method === 'POST' && call.path === '/api/v1/auth/identifiers',
    )
    expect(sends).toHaveLength(1)
    expect(sends[0]?.body).toEqual({ kind: 'email', value: 'second@x.test' })

    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.code.label),
      '123456',
    )
    await user.click(
      within(verificationForm()).getByRole('button', {
        name: zhCN.identifiers.add.verify,
      }),
    )

    const verifies = rig.calls.filter(
      (call) =>
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify',
    )
    expect(verifies).toHaveLength(1)
    expect(verifies[0]?.body).toEqual({ code: '123456' })
    await waitFor(() =>
      expect(screen.getAllByText(zhCN.identifiers.verified)).toHaveLength(2),
    )
    expect(screen.queryByText(zhCN.identifiers.unverified)).toBeNull()
    expect(
      await screen.findByText(
        zhCN.verification.verifiedDescription.replace(
          '{{email}}',
          'second@x.test',
        ),
      ),
    ).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('opens the host add signal through the section entry, closing the row flow and its result', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'verified', email: 'ada@x.test', since: null },
            identifiers: [
              identifier({
                id: 'id-secondary',
                kind: 'email',
                value: 'second@x.test',
                proven: false,
                verified_via: 'admin',
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers'
      ) {
        return errorResponse(503, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    function Host(): ReactElement {
      const [signal, setSignal] = useState(0)
      return (
        <>
          <button type="button" onClick={() => setSignal((count) => count + 1)}>
            host-add
          </button>
          <IdentifiersSection
            session={rig.session}
            showAddButton={false}
            openAddSignal={signal}
          />
        </>
      )
    }
    renderWithProviders(<Host />)
    const user = userEvent.setup()

    await clickRowAction(user, 'second@x.test', zhCN.identifiers.verify)
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )
    expect(
      await screen.findByText(zhCN.errors.authn.unavailable),
    ).toBeTruthy()

    await user.click(screen.getByRole('button', { name: 'host-add' }))

    expect(
      await screen.findByRole('heading', { name: zhCN.identifiers.add.title }),
    ).toBeTruthy()
    expect(
      screen.getAllByRole('button', { name: zhCN.identifiers.add.cancel }),
    ).toHaveLength(1)
    expect(
      screen.getAllByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toHaveLength(1)
    expect(screen.queryByText(zhCN.errors.authn.unavailable)).toBeNull()

    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.cancel }),
    )
    expect(
      screen.queryByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toBeNull()
  })

  it.each(['contact', 'both'] as const)(
    'gives the %s view the resend path and holds it for the server interval',
    async (axis) => {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              verification: {
                state: 'verified',
                email: 'ada@x.test',
                since: null,
              },
              identifiers: [
                identifier({
                  id: 'id-unproved',
                  kind: 'email',
                  value: 'ada@x.test',
                  proven: false,
                  verified_at: '2026-01-01T00:00:00Z',
                  verified_via: 'admin',
                }),
              ],
            }),
          )
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 5,
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(
        <IdentifiersSection
          session={rig.session}
          axis={axis}
          humanGate={{
            phone: { required: false, provider: 'off' },
            other: { required: false, provider: 'off' },
          }}
        />,
      )
      const user = userEvent.setup()

      await screen.findAllByText('ada@x.test')
      await clickRowAction(user, 'ada@x.test', zhCN.identifiers.verify)

      const send = await screen.findByRole('button', {
        name: zhCN.identifiers.add.submitEmail,
      })

      vi.useFakeTimers()
      try {
        await act(async () => {
          fireEvent.click(send)
          for (let turn = 0; turn < 10; turn += 1) {
            await Promise.resolve()
          }
        })

        const address = screen.getByLabelText(
          zhCN.identifiers.add.value.labelEmail,
        )
        const form = address.closest('form')
        expect(form).not.toBeNull()
        const scoped = within(form as HTMLElement)

        expect(
          scoped
            .getAllByRole('button')
            .map((button) => button.textContent?.trim()),
        ).toEqual([
          zhCN.identifiers.add.cancel,
          zhCN.verification.resend,
          zhCN.identifiers.add.verify,
        ])

        const resend = scoped.getByRole('button', {
          name: zhCN.verification.resend,
        })
        expect(resend).toHaveAttribute('aria-disabled', 'true')
        const note = scoped.getByText(
          zhCN.verification.resendIn.replace('{{seconds}}', '5'),
        )
        expect(resend).toHaveAttribute('aria-describedby', note.id)

        act(() => {
          vi.advanceTimersByTime(5000)
        })
        expect(resend).toBeEnabled()
        expect(resend).not.toHaveAttribute('aria-disabled', 'true')
        expect(
          scoped.queryByText(
            zhCN.verification.resendIn.replace('{{seconds}}', '5'),
          ),
        ).toBeNull()
        expect(resend).not.toHaveAttribute('aria-describedby')

        await act(async () => {
          fireEvent.click(resend)
          for (let turn = 0; turn < 10; turn += 1) {
            await Promise.resolve()
          }
        })

        const sends = rig.calls.filter(
          (call) =>
            call.method === 'POST' &&
            call.path === '/api/v1/auth/identifiers',
        )
        expect(sends).toHaveLength(2)
        expect(sends[1]?.body).toEqual({
          kind: 'email',
          value: 'ada@x.test',
        })
      } finally {
        try {
          vi.advanceTimersByTime(100)
        } finally {
          await restoreRealTimers()
        }
      }
    },
  )

  it('names a throttled row resend in the banner and renders no row note', async () => {
    let sends = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: {
              state: 'verified',
              email: 'ada@x.test',
              since: null,
            },
            identifiers: [
              identifier({
                id: 'id-unproved',
                kind: 'email',
                value: 'ada@x.test',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'admin',
              }),
            ],
          }),
        )
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        sends += 1
        if (sends === 1) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        return new Response(
          JSON.stringify({
            type: 'urn:speed:authn:throttled',
            title: 'Too Many Requests',
            status: 429,
          }),
          {
            status: 429,
            headers: {
              'content-type': 'application/json',
              'retry-after': '30',
            },
          },
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection
        session={rig.session}
        humanGate={{
          phone: { required: false, provider: 'off' },
          other: { required: false, provider: 'off' },
        }}
      />,
    )
    const user = userEvent.setup()

    await screen.findAllByText('ada@x.test')
    await clickRowAction(user, 'ada@x.test', zhCN.identifiers.verify)
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.identifiers.add.submitEmail,
      }),
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.verification.resend,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(
      zhCN.throttle.retryAfter.replace('{{seconds}}', '30'),
    )
    const address = screen.getByLabelText(
      zhCN.identifiers.add.value.labelEmail,
    )
    const scoped = within(address.closest('form') as HTMLElement)
    const resend = scoped.getByRole('button', {
      name: zhCN.verification.resend,
    })
    expect(resend).toHaveAttribute('aria-disabled', 'true')
    const [before, after] = zhCN.verification.resendIn.split('{{seconds}}')
    expect(
      scoped.queryByText((content) =>
        content.startsWith(before ?? '') && content.endsWith(after ?? ''),
      ),
    ).toBeNull()
    expect(resend).toHaveAccessibleDescription(zhCN.identifiers.resendWaiting)
  })

  it('does not offer the row verify action while the account is pending', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            verification: {
              state: 'pending',
              email: 'pending@x.test',
              since: null,
            },
            identifiers: [
              identifier({
                id: 'id-pending',
                kind: 'email',
                value: 'pending@x.test',
                proven: false,
                verified_via: 'admin',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await waitFor(() =>
      expect(screen.getAllByText('pending@x.test')).toHaveLength(2),
    )
    expect(screen.getAllByText(zhCN.identifiers.unverified)).toHaveLength(1)
    expect(rowAction('pending@x.test', zhCN.identifiers.verify)).toBeNull()
    expect(rowAction('pending@x.test', zhCN.identifiers.remove)).toBeTruthy()
  })

  it('renders no verification chip for a username', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-self',
                kind: 'username',
                value: 'ada',
                proven: false,
                verified_via: 'self',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await waitFor(() => expect(screen.getAllByText('ada')).toHaveLength(1))
    expect(screen.queryByText(zhCN.identifiers.unverified)).toBeNull()
    expect(screen.queryByText(zhCN.identifiers.verified)).toBeNull()
  })

  it('renders no removal action for the username and states it is permanent', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: true,
            identifiers: [
              identifier({
                id: 'id-user',
                kind: 'username',
                value: 'ada',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'self',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await waitFor(() => expect(screen.getAllByText('ada')).toHaveLength(1))
    expect(
      within(
        screen.getByRole('group', { name: zhCN.identifiers.kind.username }),
      ).queryAllByRole('button'),
    ).toHaveLength(0)
    expect(screen.getByText(zhCN.identifiers.usernamePermanent)).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('withholds removal of the only login name when it is an email and explains why', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: true,
            identifiers: [
              identifier({ id: 'id-email', kind: 'email', value: 'ada@x.test' }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await waitFor(() =>
      expect(screen.getAllByText('ada@x.test')).toHaveLength(2),
    )
    expect(rowActions('ada@x.test')).toHaveLength(0)
    expect(screen.getByText(zhCN.identifiers.lastLoginName)).toBeTruthy()
  })

  it('keeps a phone removable when the username is the only login name', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: true,
            identifiers: [
              identifier({
                id: 'id-user',
                kind: 'username',
                value: 'ada',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'self',
              }),
              identifier({
                id: 'id-phone',
                kind: 'phone',
                value: '+14155550123',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'admin',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await waitFor(() => expect(screen.getAllByText('ada')).toHaveLength(1))
    expect(screen.getByText('+14155550123')).toBeTruthy()
    expect(
      within(
        screen.getByRole('group', { name: zhCN.identifiers.kind.username }),
      ).queryAllByRole('button'),
    ).toHaveLength(0)
    const phoneRemove = rowAction('+14155550123', zhCN.identifiers.remove)
    expect(phoneRemove).toBeEnabled()
    expect(phoneRemove).not.toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByText(zhCN.identifiers.usernamePermanent)).toBeTruthy()
  })

  it('never offers to remove the username, even when an email also signs in', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: true,
            identifiers: [
              identifier({
                id: 'id-user',
                kind: 'username',
                value: 'ada',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'self',
              }),
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'ada@x.test',
                proven: true,
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await waitFor(() => expect(screen.getAllByText('ada')).toHaveLength(1))
    expect(
      within(
        screen.getByRole('group', { name: zhCN.identifiers.kind.username }),
      ).queryAllByRole('button'),
    ).toHaveLength(0)
    expect(screen.getByText(zhCN.identifiers.usernamePermanent)).toBeTruthy()
  })

  it('keeps the only login name removable on an account with no password', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'ada@x.test',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await waitFor(() =>
      expect(screen.getAllByText('ada@x.test')).toHaveLength(2),
    )
    expect(rowAction('ada@x.test', zhCN.identifiers.remove)).toBeTruthy()
    expect(screen.queryByText(zhCN.identifiers.lastLoginName)).toBeNull()
  })

  it('does not offer the email kind while verification is pending', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [],
            verification: {
              state: 'pending',
              email: 'ada@x.test',
              since: null,
            },
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    const combo = await screen.findByRole('combobox', {
      name: zhCN.identifiers.add.kind.label,
    })
    expect(combo).toHaveTextContent(zhCN.identifiers.add.kind.username)
    expect(combo).not.toHaveTextContent(zhCN.identifiers.add.kind.email)
  })

  it('removes an identifier behind the confirm and refetches', async () => {
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: removed ? [] : [identifier({ id: 'id-email' })],
          }),
        )
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/identifiers/id-email') {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await clickRowAction(user, 'ada@example.test', zhCN.identifiers.remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )

    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.empty.title)).toBeTruthy(),
    )
    expect(
      await screen.findByText(
        zhCN.identifiers.removed.replace('{{value}}', 'ada@example.test'),
      ),
    ).toBeTruthy()
    expect(zhCN.identifiers.removed).toContain('{{value}}')
    expect(
      rig.calls.filter(
        (call) =>
          call.method === 'DELETE' && call.path === '/api/v1/auth/identifiers/id-email',
      ),
    ).toHaveLength(1)
  })

  it('renders a last-way-in refusal as its code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [identifier({ id: 'id-email' })],
          }),
        )
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/identifiers/id-email') {
        return errorResponse(409, 'authn.last_login_method')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await clickRowAction(user, 'ada@example.test', zhCN.identifiers.remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.last_login_method)
  })

  it('renders a username-immutable refusal as its code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [identifier({ id: 'id-email' })],
          }),
        )
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/identifiers/id-email') {
        return errorResponse(409, 'authn.username_immutable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await clickRowAction(user, 'ada@example.test', zhCN.identifiers.remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.username_immutable)
    expect(alert).not.toHaveTextContent(zhCN.errors.authn.conflict)
  })

  it('renders the last-login-name refusal when the guard read a stale profile', async () => {
    let profileReads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        profileReads += 1
        return jsonResponse(
          200,
          profileBody({
            password_set: true,
            identifiers:
              profileReads === 1
                ? [
                    identifier({
                      id: 'id-user',
                      kind: 'username',
                      value: 'ada',
                      proven: false,
                      verified_at: '2026-01-01T00:00:00Z',
                      verified_via: 'self',
                    }),
                    identifier({
                      id: 'id-email',
                      kind: 'email',
                      value: 'ada@x.test',
                    }),
                  ]
                : [
                    identifier({
                      id: 'id-email',
                      kind: 'email',
                      value: 'ada@x.test',
                    }),
                  ],
          }),
        )
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/identifiers/id-email'
      ) {
        return errorResponse(409, 'authn.login_identifier_required')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await clickRowAction(user, 'ada@x.test', zhCN.identifiers.remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.login_identifier_required)
    expect(alert).not.toHaveTextContent(zhCN.errors.authn.conflict)
    await waitFor(() => expect(profileReads).toBe(2))
    expect(screen.getByText(zhCN.identifiers.lastLoginName)).toBeTruthy()
    expect(rowActions('ada@x.test')).toHaveLength(0)
  })

  it('gates a step_up_required removal behind the dialog and re-runs it once', async () => {
    const warn = captureRefreshWarning()
    let elevated = false
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: removed ? [] : [identifier({ id: 'id-email' })],
          }),
        )
      }
      if (call.method === 'DELETE' && call.path === '/api/v1/auth/identifiers/id-email') {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        removed = true
        return new Response(null, { status: 204 })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, {
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'App',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number }
        if (body.seq === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'second',
            next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
          })
        }
        elevated = true
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await clickRowAction(user, 'ada@example.test', zhCN.identifiers.remove)
    const confirm = await screen.findByRole('dialog')
    await user.click(
      within(confirm).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )

    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    await user.click(screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.empty.title)).toBeTruthy(),
    )
    expect(
      rig.calls.filter(
        (call) =>
          call.method === 'DELETE' && call.path === '/api/v1/auth/identifiers/id-email',
      ),
    ).toHaveLength(2)
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({
        status: 401,
        code: 'authn.insufficient_user_authentication',
        trace_id: 'trace-1',
      }),
    )
    expect(warn).toHaveBeenCalledTimes(1)
  })

  it('renders the empty state for an account with no identifiers', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText(zhCN.identifiers.empty.title)).toBeTruthy()
    expect(
      screen.queryByRole('heading', { name: zhCN.identifiers.title }),
    ).toBeNull()
  })

  it('renders the error state when the answer omits the identifiers key', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        const body = profileBody()
        delete body.identifiers
        return jsonResponse(200, body)
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findByText(zhCN.identifiers.error.title)).toBeTruthy()
    expect(screen.queryByText(zhCN.identifiers.empty.title)).toBeNull()
  })

  it('adds a username in one step and refetches the profile', async () => {
    let added = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: added
              ? [identifier({ id: 'id-user', kind: 'username', value: 'ada2' })]
              : [],
          }),
        )
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        added = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    fireEvent.mouseDown(
      screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
      'ada2',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
    )

    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.add.added)).toBeTruthy(),
    )
    expect(zhCN.identifiers.add.added).toContain(zhCN.identifiers.title)
    const posts = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === '/api/v1/auth/identifiers',
    )
    expect(posts).toHaveLength(1)
    expect(posts[0]?.body).toEqual({ kind: 'username', value: 'ada2' })
    await waitFor(() =>
      expect(screen.getAllByText('ada2')).toHaveLength(1),
    )
  })

  it('names a taken username instead of the generic conflict text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(409, 'authn.username_taken')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    fireEvent.mouseDown(
      screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
      'alice',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
    )

    const field = screen.getByLabelText(zhCN.identifiers.add.value.labelUsername)
    await waitFor(() => expect(field).toHaveAttribute('aria-invalid', 'true'))
    expect(screen.getByText(zhCN.identifiers.add.usernameTaken)).toBeInTheDocument()
    expect(field).toHaveFocus()
    expect(field).toHaveValue('alice')
    expect(screen.queryByRole('alert')).toBeNull()
    expect(enUS.identifiers.add.usernameTaken).toMatch(/already taken/i)
    expect(field).toBeTruthy()
  })

  it('names a taken username the identifier route answers as a conflict', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(409, 'authn.conflict')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    fireEvent.mouseDown(
      screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
      'alice',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
    )

    const field = screen.getByLabelText(zhCN.identifiers.add.value.labelUsername)
    await waitFor(() => expect(field).toHaveAttribute('aria-invalid', 'true'))
    expect(screen.getByText(zhCN.identifiers.add.usernameTaken)).toBeInTheDocument()
    expect(field).toHaveFocus()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('states a refused username in its field rather than the banner', async () => {
    for (const [code, text] of [
      ['authn.username_reserved', zhCN.identifiers.add.usernameReserved],
      ['authn.username_invalid', zhCN.identifiers.add.usernameInvalid],
    ] as const) {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(200, profileBody({ identifiers: [] }))
        }
        if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
          return errorResponse(400, code)
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { unmount } = mount(respond)
      const user = userEvent.setup()

      await user.click(
        await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
      )
      fireEvent.mouseDown(
        screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
      )
      await user.click(
        screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
      )
      const field = screen.getByLabelText(zhCN.identifiers.add.value.labelUsername)
      await user.type(field, 'ada')
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
      )

      expect(await screen.findByText(text)).toBeInTheDocument()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      await user.type(field, '2')
      expect(screen.queryByText(text)).not.toBeInTheDocument()
      unmount()
    }
  })

  it('leaves a bad-request username add to the generic banner', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(400, 'authn.bad_request')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    fireEvent.mouseDown(
      screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
    )
    const field = screen.getByLabelText(
      zhCN.identifiers.add.value.labelUsername,
    )
    await user.type(field, 'ada')
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    expect(alert).not.toHaveTextContent(zhCN.identifiers.add.usernameInvalid)
    expect(field).not.toHaveAttribute('aria-invalid', 'true')
  })

  it('states the username first-character rule in the add helper', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    fireEvent.mouseDown(
      screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
    )

    expect(
      await screen.findByText(zhCN.identifiers.add.value.helperUsername),
    ).toBeTruthy()
    expect(enUS.identifiers.add.value.helperUsername).toMatch(
      /first character must be a letter or a digit/i,
    )
  })

  it('adds an email through the code step', async () => {
    let verified = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: verified
              ? [identifier({ id: 'id-email', kind: 'email', value: 'ada@x.test' })]
              : [],
          }),
        )
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers/verify') {
        verified = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    await screen.findByLabelText(zhCN.identifiers.add.code.label)
    const sentNotice = zhCN.identifiers.add.sent.replace(
      '{{value}}',
      'ada@x.test',
    )
    const sentText = await screen.findByText(sentNotice)
    expect(sentText.closest('[role="status"]')).not.toBeNull()
    expect(screen.getByLabelText(zhCN.identifiers.add.code.label)).toHaveFocus()
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.code.label),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.verify }),
    )

    const added = await screen.findByText(zhCN.identifiers.add.added)
    expect(added).toBeTruthy()
    expect(
      screen
        .getByRole('button', { name: zhCN.identifiers.add.open })
        .compareDocumentPosition(added) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    const verifies = rig.calls.filter(
      (call) =>
        call.method === 'POST' && call.path === '/api/v1/auth/identifiers/verify',
    )
    expect(verifies).toHaveLength(1)
    expect(verifies[0]?.body).toEqual({ code: '123456' })
  })

  it('refuses a malformed email in the field without sending a request', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@example@com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    const message = await screen.findByText(zhCN.identifiers.add.invalidEmail)
    expect(message).toBeTruthy()
    expect(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryByRole('alert')).toBeNull()
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === '/api/v1/auth/identifiers',
      ),
    ).toHaveLength(0)

    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.cancel }),
    )
    expect(screen.queryByText(zhCN.identifiers.add.invalidEmail)).toBeNull()
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.open }),
    )
    expect(
      await screen.findByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toHaveValue('')
    expect(screen.queryByText(zhCN.identifiers.add.invalidEmail)).toBeNull()
  })

  it('sends the trimmed address for a well-formed email', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    const field = screen.getByLabelText(zhCN.identifiers.add.value.labelEmail)
    fireEvent.change(field, { target: { value: '  ada@x.test  ' } })
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    await screen.findByLabelText(zhCN.identifiers.add.code.label)
    const posts = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === '/api/v1/auth/identifiers',
    )
    expect(posts).toHaveLength(1)
    expect(posts[0]?.body).toEqual({ kind: 'email', value: 'ada@x.test' })
  })

  it('renders a server bad-request for an email as the field error', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(400, 'authn.bad_request')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    expect(
      await screen.findByText(zhCN.identifiers.add.invalidEmail),
    ).toBeTruthy()
    expect(
      screen.queryByText(zhCN.errors.authn.bad_request),
    ).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('renders a taken email on the value field, not the failure banner', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(409, 'authn.email_taken')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    expect(
      await screen.findByText(zhCN.identifiers.add.emailTaken),
    ).toBeTruthy()
    expect(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryByText(zhCN.errors.authn.email_taken)).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('returns a taken email from the verification step to the value field', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers/verify') {
        return errorResponse(409, 'authn.email_taken')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )
    await user.type(
      await screen.findByLabelText(zhCN.identifiers.add.code.label),
      '123456',
    )
    await user.click(
      within(verificationForm()).getByRole('button', {
        name: zhCN.identifiers.add.verify,
      }),
    )

    const email = await screen.findByLabelText(
      zhCN.identifiers.add.value.labelEmail,
    )
    expect(email).toHaveValue('ada@x.test')
    expect(email).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByText(zhCN.identifiers.add.emailTaken)).toBeTruthy()
    expect(
      screen.queryByLabelText(zhCN.identifiers.add.code.label),
    ).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('clears the failure banner when the add face is cancelled', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(503, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.unavailable,
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.cancel }),
    )

    expect(screen.queryByRole('alert')).toBeNull()
    expect(
      screen.queryByText(zhCN.errors.authn.unavailable),
    ).toBeNull()
    expect(
      screen.queryByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toBeNull()
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.open }),
    )
    expect(
      await screen.findByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toHaveValue('')
  })

  it('renders a no-channel refusal of an email add as its code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(503, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.unavailable)
  })

  it('renders a step-up refusal of an email add as an ordinary failure, with no dialog', async () => {
    captureRefreshWarning({ expected: true })
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(401, 'authn.insufficient_user_authentication')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(
      zhCN.errors.authn.insufficient_user_authentication,
    )
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === '/api/v1/auth/identifiers',
      ),
    ).toHaveLength(1)
  })

  it('re-proves an SMS first factor past the window and re-runs the username add', async () => {
    captureRefreshWarning({ expected: true })
    let elevated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        return jsonResponse(200, { state: 'added' })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, { factors: [] })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/session') {
        return jsonResponse(200, {
          authenticated: true,
          session_id: 'session-1',
          account_id: 'account-1',
          methods: ['sms'],
          assurance: 'single',
          auth_time: new Date(Date.now() - 6 * 60 * 1000).toISOString(),
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'first',
          next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        elevated = true
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    fireEvent.mouseDown(
      screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
      'ada2',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
    )

    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.add.added)).toBeTruthy(),
    )
    const posts = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === '/api/v1/auth/identifiers',
    )
    expect(posts).toHaveLength(2)
    expect(posts[0]?.body).toEqual({ kind: 'username', value: 'ada2' })
    expect(posts[1]?.body).toEqual({ kind: 'username', value: 'ada2' })
    const start = rig.calls.find((call) => call.path === FLOW_START)
    expect(start?.body).toEqual({ purpose: 'step-up', method: 'sms' })
  })

  it('offers the email kind by default when the host does not gate identifiers', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    const combo = await screen.findByRole('combobox')
    expect(combo).toHaveTextContent(zhCN.identifiers.add.kind.email)
    expect(combo).not.toHaveTextContent(zhCN.identifiers.add.kind.username)
  })

  it('offers usernames only when the deployment serves no email identifier', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} emailAvailable={false} />,
    )
    fireEvent.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    const combo = await screen.findByRole('combobox')
    expect(combo).toHaveTextContent(zhCN.identifiers.add.kind.username)
    expect(combo).not.toHaveTextContent(zhCN.identifiers.add.kind.email)
  })

  it('does not offer the username kind when the host opts out', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} offerUsername={false} />,
    )
    fireEvent.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    const combo = await screen.findByRole('combobox')
    expect(combo).toHaveTextContent(zhCN.identifiers.add.kind.email)
    fireEvent.mouseDown(combo)
    expect(
      screen.queryByRole('option', {
        name: zhCN.identifiers.add.kind.username,
      }),
    ).toBeNull()
  })

  it('does not offer the username kind when the profile already lists one', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({ id: 'id-user', kind: 'username', value: 'ada' }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await waitFor(() => expect(screen.getAllByText('ada')).toHaveLength(1))
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.open }),
    )
    const combo = await screen.findByRole('combobox', {
      name: zhCN.identifiers.add.kind.label,
    })
    expect(combo).toHaveTextContent(zhCN.identifiers.add.kind.email)
    fireEvent.mouseDown(combo)
    expect(
      await screen.findByRole('option', {
        name: zhCN.identifiers.add.kind.email,
      }),
    ).toBeTruthy()
    expect(
      screen.queryByRole('option', {
        name: zhCN.identifiers.add.kind.username,
      }),
    ).toBeNull()
  })

  it('renders no add face when a username exists and email is unavailable', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({ id: 'id-user', kind: 'username', value: 'ada' }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} emailAvailable={false} />,
    )

    await waitFor(() => expect(screen.getAllByText('ada')).toHaveLength(1))
    expect(
      screen.queryByRole('button', { name: zhCN.identifiers.add.open }),
    ).toBeNull()
  })

  it('renders one failure banner while the add face is open', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({ identifiers: [identifier({ id: 'id-email' })] }),
        )
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(503, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await screen.findAllByText('ada@example.test')
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'new@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    await waitFor(() =>
      expect(screen.getAllByRole('alert')).toHaveLength(1),
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.unavailable,
    )
  })

  it('raises the identifier actions to the 44px floor under a coarse pointer', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [identifier({ id: 'id-email' })],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const actions = await screen.findByRole('button', {
      name: zhCN.identifiers.remove,
    })
    const add = screen.getByRole('button', { name: zhCN.identifiers.add.open })
    expect(hasCoarsePointerTouchTarget(actions)).toBe(true)
    expect(hasCoarsePointerTouchTarget(add)).toBe(true)
  })

  it('holds the session through the credential rotation a username add performs', async () => {
    const warn = captureRefreshWarning()
    let rotated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return rotated
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return rotated
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        rotated = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<IdentifiersSection session={rig.session} />)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    fireEvent.mouseDown(
      screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
      'ada2',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
    )

    await waitFor(() =>
      expect(warn).toHaveBeenCalledWith(
        'access token refresh failed',
        expect.objectContaining({ status: 401 }),
      ),
    )
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('proves an added email without opening an identity-transition window', async () => {
    let proven = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: proven
              ? [
                  identifier({
                    id: 'id-email',
                    kind: 'email',
                    value: 'ada@x.test',
                  }),
                ]
              : [],
          }),
        )
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify'
      ) {
        proven = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<IdentifiersSection session={rig.session} />)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'ada@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )
    await user.type(
      await screen.findByLabelText(zhCN.identifiers.add.code.label),
      '123456',
    )
    await user.click(
      within(verificationForm()).getByRole('button', {
        name: zhCN.identifiers.add.verify,
      }),
    )

    await waitFor(() => expect(screen.getAllByText('ada@x.test').length).toBeGreaterThan(0))
    expect(rig.transitionBegins()).toBe(0)
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('proves a row address without opening an identity-transition window', async () => {
    let proven = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: {
              state: 'verified',
              email: 'ada@x.test',
              since: null,
            },
            identifiers: [
              identifier({
                id: 'id-primary',
                kind: 'email',
                value: 'ada@x.test',
              }),
              identifier({
                id: 'id-secondary',
                kind: 'email',
                value: 'second@x.test',
                proven,
                verified_via: proven ? 'email-code' : 'admin',
              }),
            ],
          }),
        )
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify'
      ) {
        proven = true
        return jsonResponse(200, { state: 'added' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<IdentifiersSection session={rig.session} />)
    const user = userEvent.setup()

    await clickRowAction(user, 'second@x.test', zhCN.identifiers.verify)
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )
    await user.type(
      await screen.findByLabelText(zhCN.identifiers.add.code.label),
      '123456',
    )
    await user.click(
      within(verificationForm()).getByRole('button', {
        name: zhCN.identifiers.add.verify,
      }),
    )

    await waitFor(() =>
      expect(screen.getAllByText(zhCN.identifiers.verified)).toHaveLength(2),
    )
    expect(rig.transitionBegins()).toBe(0)
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('releases the window a refused username add opened', async () => {
    captureRefreshWarning({ expected: true })
    let gone = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return gone
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return gone
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return errorResponse(409, 'authn.username_taken')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(<IdentifiersSection session={rig.session} />)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    fireEvent.mouseDown(
      screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
      'ada2',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
    )

    await screen.findByText(zhCN.identifiers.add.usernameTaken)
    expect(rig.transitionBegins()).toBe(1)

    gone = true
    await rig.session.restore()
    expect(rig.session.getSnapshot().state).toBe('anonymous')
    expect(rig.endedEdges()).toBe(1)
  })

  it('asks for the verifier only when the add is requested, then sends it with the proof', async () => {
    const fake = installFakeTurnstile()
    try {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(200, profileBody({ identifiers: [] }))
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 60,
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond, {
        required: true,
        provider: 'turnstile',
        site_key: 'site-1',
      })
      const user = userEvent.setup()

      await user.click(
        await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
      )
      await user.type(
        screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
        'new@x.test',
      )
      expect(fake.widgets).toHaveLength(0)
      const submit = screen.getByRole('button', {
        name: zhCN.identifiers.add.submitEmail,
      })
      expect(submit).toBeEnabled()
      expect(submit).not.toHaveAttribute('aria-disabled', 'true')
      await user.click(submit)

      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(fake.widgets[0]?.siteKey).toBe('site-1')
      const waitingSend = screen.getByRole('button', {
        name: zhCN.identifiers.add.submitEmail,
      })
      expect(waitingSend).toHaveAttribute('aria-disabled', 'true')
      expect(waitingSend).toHaveAttribute('aria-busy', 'true')
      expect(
        rig.calls.filter(
          (call) =>
            call.method === 'POST' &&
            call.path === '/api/v1/auth/identifiers',
        ),
      ).toHaveLength(0)

      act(() => {
        fake.token('token-1')
      })
      await waitFor(() =>
        expect(
          rig.calls.filter(
            (call) =>
              call.method === 'POST' &&
              call.path === '/api/v1/auth/identifiers',
          ),
        ).toHaveLength(1),
      )
      const posts = rig.calls.filter(
        (call) =>
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers',
      )
      expect(posts[0]?.body).toEqual({
        kind: 'email',
        value: 'new@x.test',
        human_proof: 'token-1',
      })
    } finally {
      fake.restore()
    }
  })

  it('drops a row resend waiting on its proof when the code in hand verifies', async () => {
    const fake = installFakeTurnstile()
    try {
      let proven = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              verification: {
                state: 'verified',
                email: 'ada@x.test',
                since: null,
              },
              identifiers: [
                identifier({
                  id: 'id-primary',
                  kind: 'email',
                  value: 'ada@x.test',
                }),
                identifier({
                  id: 'id-secondary',
                  kind: 'email',
                  value: 'second@x.test',
                  proven,
                  verified_via: proven ? 'email-code' : 'admin',
                }),
              ],
            }),
          )
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers/verify'
        ) {
          proven = true
          return jsonResponse(200, { state: 'added' })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond, {
        required: true,
        provider: 'turnstile',
        site_key: 'site-1',
      })
      const user = userEvent.setup()
      const sends = () =>
        rig.calls.filter(
          (call) =>
            call.method === 'POST' &&
            call.path === '/api/v1/auth/identifiers',
        )

      await clickRowAction(user, 'second@x.test', zhCN.identifiers.verify)
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(sends()).toHaveLength(1))

      const codeField = await screen.findByLabelText(
        zhCN.identifiers.add.code.label,
      )
      const form = codeField.closest('form') as HTMLFormElement
      await user.type(codeField, '123456')
      const resend = within(form).getByRole('button', {
        name: zhCN.verification.resend,
      })
      await waitFor(() => {
        expect(resend).toBeEnabled()
        expect(resend).not.toHaveAttribute('aria-disabled', 'true')
      })
      await user.click(resend)
      await waitFor(() => expect(fake.widgets).toHaveLength(2))
      const blockedVerify = within(form).getByRole('button', {
        name: zhCN.identifiers.add.verify,
      })
      blockedVerify.focus()
      expect(blockedVerify).toHaveFocus()
      expect(blockedVerify).toBeEnabled()
      expect(blockedVerify).not.toHaveAttribute('aria-disabled', 'true')
      expect(blockedVerify).not.toHaveAttribute('aria-busy')
      const cancel = within(form).getByRole('button', {
        name: zhCN.identifiers.add.cancel,
      })
      expect(cancel).toBeEnabled()
      expect(cancel).not.toHaveAttribute('aria-disabled', 'true')

      blockedVerify.focus()
      await user.keyboard('{Enter}')
      await waitFor(() =>
        expect(
          rig.calls.filter(
            (call) =>
              call.method === 'POST' &&
              call.path === '/api/v1/auth/identifiers/verify',
          ),
        ).toHaveLength(1),
      )
      act(() => {
        fake.token('token-2')
      })
      await waitFor(() =>
        expect(screen.getAllByText(zhCN.identifiers.verified)).toHaveLength(2),
      )
      expect(sends()).toHaveLength(1)
    } finally {
      fake.restore()
    }
  })

  it('holds a row resend whose proof arrives during another row’s sign-in write', async () => {
    const fake = installFakeTurnstile()
    try {
      let primarySignIn = true
      let sends = 0
      const held = deferred<Response>()
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              verification: {
                state: 'verified',
                email: 'ada@x.test',
                since: null,
              },
              identifiers: [
                identifier({
                  id: 'id-primary',
                  kind: 'email',
                  value: 'ada@x.test',
                  proven: true,
                  sign_in: primarySignIn,
                }),
                identifier({
                  id: 'id-secondary',
                  kind: 'email',
                  value: 'second@x.test',
                  proven: false,
                  sign_in: false,
                  verified_via: 'admin',
                }),
              ],
            }),
          )
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          sends += 1
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: sends === 1 ? 0 : 60,
          })
        }
        if (
          call.method === 'PUT' &&
          call.path === '/api/v1/auth/identifiers/id-primary'
        ) {
          primarySignIn = (call.body as { sign_in?: boolean }).sign_in === true
          return held.promise
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond, {
        required: true,
        provider: 'turnstile',
        site_key: 'site-1',
      })
      const user = userEvent.setup()
      const posted = () =>
        rig.calls.filter(
          (call) =>
            call.method === 'POST' &&
            call.path === '/api/v1/auth/identifiers',
        )

      await clickRowAction(user, 'second@x.test', zhCN.identifiers.verify)
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(posted()).toHaveLength(1))

      const codeField = await screen.findByLabelText(
        zhCN.identifiers.add.code.label,
      )
      const form = codeField.closest('form') as HTMLFormElement
      const resend = within(form).getByRole('button', {
        name: zhCN.verification.resend,
      })
      await waitFor(() => expect(resend).toBeEnabled())
      await user.click(resend)
      await waitFor(() => expect(fake.widgets).toHaveLength(2))

      const signInRegion = screen.getByRole('region', {
        name: zhCN.identifiers.groups.signIn,
      })
      await user.click(
        within(signInRegion).getByRole('switch', {
          name: zhCN.identifiers.action.disable,
        }),
      )
      await waitFor(() =>
        expect(
          rig.calls.filter(
            (call) =>
              call.method === 'PUT' &&
              call.path === '/api/v1/auth/identifiers/id-primary',
          ),
        ).toHaveLength(1),
      )

      act(() => {
        fake.token('token-2')
      })
      await act(async () => {
        await Promise.resolve()
      })
      expect(posted()).toHaveLength(1)

      act(() => {
        held.resolve(new Response(null, { status: 204 }))
      })
      await waitFor(() => expect(posted()).toHaveLength(2))
      expect(posted()[1]?.body).toEqual({
        kind: 'email',
        value: 'second@x.test',
        human_proof: 'token-2',
      })

      await waitFor(() =>
        expect(
          within(signInRegion).getByRole('switch', {
            name: zhCN.identifiers.action.enable,
          }),
        ).toBeTruthy(),
      )
      expect(codeField).toBeInTheDocument()
      expect(resend).toHaveAttribute('aria-disabled', 'true')
      const note = within(form).getByText(
        new RegExp(
          zhCN.verification.resendIn.replace('{{seconds}}', '\\d+'),
        ),
      )
      expect(note).toBeTruthy()
    } finally {
      fake.restore()
    }
  })

  it('keeps the identifier rows and marks them stale when a refresh fails', async () => {
    const user = userEvent.setup()
    let reads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        reads += 1
        return reads === 1
          ? jsonResponse(
              200,
              profileBody({
                identifiers: [
                  identifier({
                    id: 'id-email',
                    kind: 'email',
                    value: 'ada@x.test',
                  }),
                ],
              }),
            )
          : errorResponse(500, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { queryClient } = mount(respond)

    await waitFor(() =>
      expect(screen.getAllByText('ada@x.test')).toHaveLength(2),
    )

    await act(async () => {
      await queryClient.invalidateQueries()
    })

    expect(screen.getAllByText('ada@x.test')).toHaveLength(2)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(screen.queryByText(zhCN.identifiers.error.title)).toBeNull()

    const readsBeforeRetry = reads
    await user.click(screen.getByRole('button', { name: zhCN.identifiers.retry }))
    await waitFor(() => expect(reads).toBeGreaterThan(readsBeforeRetry))
  })

  it('groups the identifiers into sign-in ways and contacts, and passes axe', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-user',
                kind: 'username',
                value: 'ada',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'self',
              }),
              identifier({
                id: 'id-signin',
                kind: 'email',
                value: 'signin@x.test',
                sign_in: true,
                contact: false,
              }),
              identifier({
                id: 'id-contact',
                kind: 'email',
                value: 'contact@x.test',
                sign_in: false,
                contact: true,
              }),
              identifier({
                id: 'id-phone',
                kind: 'phone',
                value: '+14155550123',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'admin',
                sign_in: false,
                contact: false,
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    const signIn = await screen.findByRole('region', {
      name: zhCN.identifiers.groups.signIn,
    })
    const contacts = screen.getByRole('region', {
      name: zhCN.identifiers.groups.contact,
    })
    expect(within(signIn).getAllByText('ada')).toHaveLength(1)
    expect(within(signIn).getAllByText('signin@x.test')).toHaveLength(1)
    expect(within(contacts).getByText('signin@x.test')).toBeTruthy()
    expect(within(contacts).getByText('contact@x.test')).toBeTruthy()
    expect(within(contacts).getByText('+14155550123')).toBeTruthy()
    expect(within(contacts).queryByText('ada')).toBeNull()
    expect(zhCN.identifiers.groups).not.toHaveProperty('neither')
    expect(zhCN.identifiers.groups).not.toHaveProperty('neitherEmpty')

    await expectNoAxeViolations()
  })

  it('renders an empty note for a list that has no row', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-user',
                kind: 'username',
                value: 'ada',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'self',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    await screen.findAllByText('ada')
    expect(screen.getByText(zhCN.identifiers.groups.contactEmpty)).toBeTruthy()
    expect(zhCN.identifiers.groups).not.toHaveProperty('neitherEmpty')
  })

  it('lists every registered address as a contact, with no contact control', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'contact@x.test',
                proven: true,
                sign_in: false,
                contact: false,
              }),
              identifier({
                id: 'id-phone',
                kind: 'phone',
                value: '+14155550123',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'admin',
                sign_in: false,
                contact: false,
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    const contacts = await screen.findByRole('region', {
      name: zhCN.identifiers.groups.contact,
    })
    expect(within(contacts).getByText('contact@x.test')).toBeTruthy()
    expect(within(contacts).getByText('+14155550123')).toBeTruthy()
    expect(within(contacts).queryAllByRole('switch')).toHaveLength(0)
    expect(zhCN.identifiers.axes).not.toHaveProperty('contact')
    expect(zhCN.identifiers.axes).not.toHaveProperty('contactAria')
    expect(zhCN.identifiers.axes).not.toHaveProperty('contactChanged')
  })

  it('puts a proved address into sign-in through the email group enable action', async () => {
    let signIn = false
    const writes: unknown[] = []
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'ada@x.test',
                sign_in: signIn,
                contact: false,
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-email'
      ) {
        writes.push(call.body)
        signIn = (call.body as { sign_in?: boolean }).sign_in === true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const signInRegion = await screen.findByRole('region', {
      name: zhCN.identifiers.groups.signIn,
    })
    expect(
      within(signInRegion).getByText(zhCN.identifiers.usage.emailNone),
    ).toBeTruthy()
    await user.click(
      within(signInRegion).getByRole('switch', {
        name: zhCN.identifiers.action.enable,
      }),
    )

    await waitFor(() => expect(writes).toEqual([{ sign_in: true }]))
    await waitFor(() =>
      expect(
        within(signInRegion).getByRole('combobox', {
          name: zhCN.identifiers.kind.email,
        }),
      ).toHaveTextContent('ada@x.test'),
    )
    expect(
      within(signInRegion).getAllByText('ada@x.test'),
    ).toHaveLength(1)
  })

  it('makes a proved email the default contact through the choice on its row', async () => {
    let defaultContact = false
    const writes: unknown[] = []
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'ada@x.test',
                sign_in: true,
                contact: false,
                default_contact: defaultContact,
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-email'
      ) {
        writes.push(call.body)
        defaultContact =
          (call.body as { default_contact?: boolean }).default_contact === true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('radio', {
        name: zhCN.identifiers.axes.defaultAria.replace(
          '{{value}}',
          'ada@x.test',
        ),
      }),
    )

    await waitFor(() => expect(writes).toEqual([{ default_contact: true }]))
    const contacts = await screen.findByRole('region', {
      name: zhCN.identifiers.groups.contact,
    })
    await waitFor(() =>
      expect(within(contacts).getByText('ada@x.test')).toBeTruthy(),
    )
  })

  it('renders the last-login-way code when taking the only way out of sign-in is refused', async () => {
    let writes = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: [
              identifier({
                id: 'id-phone',
                kind: 'phone',
                value: '+14155550123',
                sign_in: true,
                contact: false,
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-phone'
      ) {
        writes += 1
        return errorResponse(409, 'authn.last_login_method')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const signInRegion = await screen.findByRole('region', {
      name: zhCN.identifiers.groups.signIn,
    })
    await user.click(
      within(signInRegion).getByRole('switch', {
        name: zhCN.identifiers.action.disable,
      }),
    )

    await waitFor(() => expect(writes).toBe(1))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.last_login_method)
  })

  it('arms the step-up dialog on a refused add-face verify and replays the same code', async () => {
    captureRefreshWarning()
    let elevated = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody({ identifiers: [] }))
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify'
      ) {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        return jsonResponse(200, { state: 'added' })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, {
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'App',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number }
        if (body.seq === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'second',
            next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
          })
        }
        elevated = true
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'new@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )
    await user.type(
      await screen.findByLabelText(zhCN.identifiers.add.code.label),
      '123456',
    )
    await user.click(
      within(verificationForm()).getByRole('button', {
        name: zhCN.identifiers.add.verify,
      }),
    )

    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    await user.click(screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await screen.findByText(zhCN.identifiers.add.added)
    const verifies = rig.calls.filter(
      (call) =>
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify',
    )
    expect(verifies).toHaveLength(2)
    expect(verifies[0]?.body).toEqual({ code: '123456' })
    expect(verifies[1]?.body).toEqual({ code: '123456' })
  })

  it('arms the step-up dialog on a refused row verify and replays the same code', async () => {
    captureRefreshWarning()
    let elevated = false
    let proven = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'verified', email: 'ada@x.test', since: null },
            identifiers: [
              identifier({ id: 'id-primary', kind: 'email', value: 'ada@x.test' }),
              identifier({
                id: 'id-secondary',
                kind: 'email',
                value: 'second@x.test',
                proven,
                verified_via: proven ? 'email-code' : 'admin',
              }),
            ],
          }),
        )
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify'
      ) {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        proven = true
        return jsonResponse(200, { state: 'added' })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, {
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'App',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number }
        if (body.seq === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'second',
            next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
          })
        }
        elevated = true
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await clickRowAction(user, 'second@x.test', zhCN.identifiers.verify)
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )
    await user.type(
      await screen.findByLabelText(zhCN.identifiers.add.code.label),
      '123456',
    )
    await user.click(
      within(verificationForm()).getByRole('button', {
        name: zhCN.identifiers.add.verify,
      }),
    )

    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    await user.click(screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() =>
      expect(screen.getAllByText(zhCN.identifiers.verified)).toHaveLength(2),
    )
    const verifies = rig.calls.filter(
      (call) =>
        call.method === 'POST' &&
        call.path === '/api/v1/auth/identifiers/verify',
    )
    expect(verifies).toHaveLength(2)
    expect(verifies[0]?.body).toEqual({ code: '123456' })
    expect(verifies[1]?.body).toEqual({ code: '123456' })
  })

  it('shows the contact axis table without a contact switch', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'ada@x.test',
                proven: true,
                sign_in: true,
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="contact" />,
    )

    expect(await screen.findByText('ada@x.test')).toBeTruthy()
    expect(screen.queryAllByRole('switch')).toHaveLength(0)
    expect(rowAction('ada@x.test', zhCN.identifiers.remove)).toBeTruthy()
    expect(
      rowAction('ada@x.test', zhCN.identifiers.axes.defaultContact),
    ).toBeTruthy()
  })

  it('names the wait a throttled email send stated', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({ id: 'id-user', kind: 'username', value: 'ada' }),
            ],
          }),
        )
      }
      if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
        return new Response(
          JSON.stringify({
            type: 'urn:speed:authn:throttled',
            title: 'Too Many Requests',
            status: 429,
          }),
          {
            status: 429,
            headers: {
              'content-type': 'application/json',
              'retry-after': '90',
            },
          },
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
    )
    await user.type(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
      'new@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(
      zhCN.throttle.retryAfter.replace('{{seconds}}', '90'),
    )
  })

  it('states that email sign-in is off when no address is chosen, with no select', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'ada@x.test',
                proven: true,
                sign_in: false,
                contact: false,
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="signIn" />,
    )

    expect(
      await screen.findByText(zhCN.identifiers.usage.emailNone),
    ).toBeTruthy()
    expect(
      screen.queryByRole('combobox', {
        name: zhCN.identifiers.kind.email,
      }),
    ).toBeNull()
    expect(
      screen.getByRole('switch', { name: zhCN.identifiers.action.enable }),
    ).toBeTruthy()
  })

  it('uses the username copy for an empty username group on the sign-in axis', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'ada@x.test',
                proven: true,
                sign_in: true,
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="signIn" />,
    )

    expect(
      await screen.findByText(zhCN.identifiers.usage.usernameNone),
    ).toBeTruthy()
    expect(screen.getAllByText(zhCN.identifiers.usage.phoneNone)).toHaveLength(1)
  })

  it('states that a proved phone is not a sign-in way when its group offers to enable it', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-phone',
                kind: 'phone',
                value: '+14155550123',
                proven: true,
                sign_in: false,
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="signIn" />,
    )

    const phoneGroup = await screen.findByRole('group', {
      name: zhCN.identifiers.kind.phone,
    })
    expect(
      within(phoneGroup).getByText(zhCN.identifiers.usage.phoneNotSignIn),
    ).toBeTruthy()
    expect(
      within(phoneGroup).getByRole('switch', {
        name: zhCN.identifiers.action.enable,
      }),
    ).toBeTruthy()
  })

  it('replays a whole sign-in email selection as one gate action', async () => {
    captureRefreshWarning()
    let elevated = false
    let aSignIn = true
    let bSignIn = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-a',
                kind: 'email',
                value: 'a@x.test',
                proven: true,
                sign_in: aSignIn,
              }),
              identifier({
                id: 'id-b',
                kind: 'email',
                value: 'b@x.test',
                proven: true,
                sign_in: bSignIn,
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-b'
      ) {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        bSignIn = (call.body as { sign_in?: boolean }).sign_in === true
        return new Response(null, { status: 204 })
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-a'
      ) {
        aSignIn = (call.body as { sign_in?: boolean }).sign_in === true
        return new Response(null, { status: 204 })
      }
      if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
        return jsonResponse(200, {
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'App',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        })
      }
      if (call.method === 'POST' && call.path === FLOW_START) {
        return jsonResponse(200, {
          seq: 1,
          state: 'second',
          next: {
            kind: 'factor',
            factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
          },
        })
      }
      if (call.method === 'POST' && call.path === FLOW_ANSWER) {
        const body = call.body as { seq: number }
        if (body.seq === 1) {
          return jsonResponse(200, {
            seq: 2,
            state: 'second',
            next: { kind: 'challenge', fields: [{ name: 'code', kind: 'text' }] },
          })
        }
        elevated = true
        return jsonResponse(200, {
          state: 'elevated',
          token_type: 'Bearer',
          access_token: 'access-2',
          expires_in: 3600,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="signIn" />,
    )
    const user = userEvent.setup()

    const combo = await screen.findByRole('combobox', {
      name: zhCN.identifiers.kind.email,
    })
    expect(combo).toHaveTextContent('a@x.test')
    fireEvent.mouseDown(combo)
    await user.click(screen.getByRole('option', { name: 'b@x.test' }))

    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    await user.click(screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await waitFor(() => expect(bSignIn).toBe(true))
    await waitFor(() => expect(aSignIn).toBe(false))
    const bPuts = rig.calls.filter(
      (call) =>
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-b',
    )
    const aPuts = rig.calls.filter(
      (call) =>
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-a',
    )
    expect(bPuts).toHaveLength(2)
    expect(
      bPuts.every(
        (call) => (call.body as { sign_in?: boolean }).sign_in === true,
      ),
    ).toBe(true)
    expect(aPuts).toHaveLength(1)
    expect(aPuts[0]?.body).toEqual({ sign_in: false })
    await waitFor(() =>
      expect(
        screen.getByRole('combobox', { name: zhCN.identifiers.kind.email }),
      ).toHaveTextContent('b@x.test'),
    )
  })

  it('offers no enable/disable action on the username group', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-user',
                kind: 'username',
                value: 'ada',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'self',
              }),
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'ada@x.test',
                proven: true,
                sign_in: true,
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="signIn" />,
    )

    const usernameHeading = await screen.findByRole('heading', {
      name: zhCN.identifiers.kind.username,
    })
    const usernameRow = usernameHeading.parentElement
    expect(usernameRow).not.toBeNull()
    expect(within(usernameRow as HTMLElement).queryByRole('button')).toBeNull()
    expect(within(usernameRow as HTMLElement).queryByRole('switch')).toBeNull()

    expect(
      screen.getByRole('switch', { name: zhCN.identifiers.action.disable }),
    ).toBeTruthy()
  })

  it('turns email sign-in off from the group switch and states that it is off', async () => {
    const writes: unknown[] = []
    let signIn = true
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-email',
                kind: 'email',
                value: 'ada@x.test',
                proven: true,
                sign_in: signIn,
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-email'
      ) {
        writes.push(call.body)
        signIn = (call.body as { sign_in?: boolean }).sign_in === true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="signIn" />,
    )
    const user = userEvent.setup()

    const combo = await screen.findByRole('combobox', {
      name: zhCN.identifiers.kind.email,
    })
    expect(combo).toHaveTextContent('ada@x.test')
    await user.click(
      screen.getByRole('switch', { name: zhCN.identifiers.action.disable }),
    )

    await waitFor(() => expect(writes).toEqual([{ sign_in: false }]))
    await waitFor(() =>
      expect(
        screen.queryByRole('combobox', {
          name: zhCN.identifiers.kind.email,
        }),
      ).toBeNull(),
    )
    expect(screen.getByText(zhCN.identifiers.usage.emailNone)).toBeTruthy()
    expect(
      screen.getByRole('switch', { name: zhCN.identifiers.action.enable }),
    ).toBeTruthy()
  })

  it('turns email sign-in back on with the address the same visit switched off', async () => {
    let aSignIn = true
    let bSignIn = false
    const writes: { readonly id: string; readonly body: unknown }[] = []
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-b',
                kind: 'email',
                value: 'b@x.test',
                proven: true,
                sign_in: bSignIn,
                default_contact: true,
              }),
              identifier({
                id: 'id-a',
                kind: 'email',
                value: 'a@x.test',
                proven: true,
                sign_in: aSignIn,
              }),
            ],
          }),
        )
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-a'
      ) {
        writes.push({ id: 'id-a', body: call.body })
        aSignIn = (call.body as { sign_in?: boolean }).sign_in === true
        return new Response(null, { status: 204 })
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-b'
      ) {
        writes.push({ id: 'id-b', body: call.body })
        bSignIn = (call.body as { sign_in?: boolean }).sign_in === true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="signIn" />,
    )
    const user = userEvent.setup()

    expect(
      await screen.findByRole('combobox', {
        name: zhCN.identifiers.kind.email,
      }),
    ).toHaveTextContent('a@x.test')
    await user.click(
      screen.getByRole('switch', { name: zhCN.identifiers.action.disable }),
    )
    await waitFor(() =>
      expect(screen.queryByRole('combobox')).toBeNull(),
    )

    await user.click(
      screen.getByRole('switch', { name: zhCN.identifiers.action.enable }),
    )

    await waitFor(() => expect(aSignIn).toBe(true))
    expect(bSignIn).toBe(false)
    expect(writes).toEqual([
      { id: 'id-a', body: { sign_in: false } },
      { id: 'id-a', body: { sign_in: true } },
    ])
    await waitFor(() =>
      expect(
        screen.getByRole('combobox', { name: zhCN.identifiers.kind.email }),
      ).toHaveTextContent('a@x.test'),
    )
  })

  it('falls back to the default contact when the switched-off address is gone', async () => {
    let includeA = true
    let aSignIn = true
    let bSignIn = false
    const cSignIn = false
    const writes: { readonly id: string; readonly body: unknown }[] = []
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-c',
                kind: 'email',
                value: 'c@x.test',
                proven: true,
                sign_in: cSignIn,
              }),
              identifier({
                id: 'id-b',
                kind: 'email',
                value: 'b@x.test',
                proven: true,
                sign_in: bSignIn,
                default_contact: true,
              }),
              ...(includeA
                ? [
                    identifier({
                      id: 'id-a',
                      kind: 'email',
                      value: 'a@x.test',
                      proven: true,
                      sign_in: aSignIn,
                    }),
                  ]
                : []),
            ],
          }),
        )
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-a'
      ) {
        writes.push({ id: 'id-a', body: call.body })
        aSignIn = (call.body as { sign_in?: boolean }).sign_in === true
        includeA = false
        return new Response(null, { status: 204 })
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/auth/identifiers/id-b'
      ) {
        writes.push({ id: 'id-b', body: call.body })
        bSignIn = (call.body as { sign_in?: boolean }).sign_in === true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="signIn" />,
    )
    const user = userEvent.setup()

    expect(
      await screen.findByRole('combobox', {
        name: zhCN.identifiers.kind.email,
      }),
    ).toHaveTextContent('a@x.test')
    await user.click(
      screen.getByRole('switch', { name: zhCN.identifiers.action.disable }),
    )
    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.usage.emailNone)).toBeTruthy(),
    )

    await user.click(
      screen.getByRole('switch', { name: zhCN.identifiers.action.enable }),
    )

    await waitFor(() => expect(bSignIn).toBe(true))
    expect(cSignIn).toBe(false)
    expect(writes).toEqual([
      { id: 'id-a', body: { sign_in: false } },
      { id: 'id-b', body: { sign_in: true } },
    ])
    await waitFor(() =>
      expect(
        screen.getByRole('combobox', { name: zhCN.identifiers.kind.email }),
      ).toHaveTextContent('b@x.test'),
    )
  })

  it('renders no set-default action on the address that already is the default', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-default',
                kind: 'email',
                value: 'ada@x.test',
                proven: true,
                default_contact: true,
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="contact" />,
    )
    await screen.findAllByText('ada@x.test')
    expect(
      rowAction('ada@x.test', zhCN.identifiers.axes.defaultContact),
    ).toBeNull()
    expect(
      rowActions('ada@x.test').map((button) => button.textContent?.trim()),
    ).toEqual([zhCN.identifiers.remove])
    expect(screen.queryByRole('menuitem')).toBeNull()
  })

  it('paints the row remove action with the danger tone the other removals use', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [
              identifier({
                id: 'id-unproved',
                kind: 'email',
                value: 'ada@x.test',
                proven: false,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'admin',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="contact" />,
    )
    await screen.findAllByText('ada@x.test')
    const verify = rowAction('ada@x.test', zhCN.identifiers.verify)
    const remove = rowAction('ada@x.test', zhCN.identifiers.remove)
    expect(verify).not.toBeNull()
    expect(remove).not.toBeNull()
    expect(getComputedStyle(remove as HTMLElement).color).not.toBe(
      getComputedStyle(verify as HTMLElement).color,
    )
    expect(remove).toHaveClass('MuiButton-colorError')
  })

  it('renders a contact row\'s actions inline and opens the remove confirm', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [identifier({ id: 'id-email', value: 'ada@x.test' })],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <IdentifiersSection session={rig.session} axis="contact" />,
    )
    const user = userEvent.setup()
    await screen.findAllByText('ada@x.test')
    const actions = rowActions('ada@x.test')
    expect(actions.map((button) => button.textContent?.trim())).toEqual([
      zhCN.identifiers.axes.defaultContact,
      zhCN.identifiers.remove,
    ])
    expect(
      actions.every((button) => button.closest('[data-action-row]') !== null),
    ).toBe(true)
    expect(screen.queryByRole('menuitem')).toBeNull()
    await user.click(
      rowAction('ada@x.test', zhCN.identifiers.remove) as HTMLElement,
    )
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent(zhCN.identifiers.confirmTitle)
  })

  it('renders a merged contact row\'s verify and remove inline, in order', async () => {
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: removed
              ? []
              : [
                  identifier({
                    id: 'id-email',
                    kind: 'email',
                    value: 'ada@example.test',
                    proven: false,
                    verified_at: '2026-01-01T00:00:00Z',
                    verified_via: 'admin',
                  }),
                ],
          }),
        )
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/identifiers/id-email'
      ) {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await screen.findAllByText('ada@example.test')
    const actions = rowActions('ada@example.test')
    expect(actions.map((button) => button.textContent?.trim())).toEqual([
      zhCN.identifiers.verify,
      zhCN.identifiers.remove,
    ])
    expect(
      actions.every((button) => button.closest('[data-action-row]') !== null),
    ).toBe(true)
    expect(screen.queryByRole('menuitem')).toBeNull()
    const mergedVerify = rowAction('ada@example.test', zhCN.identifiers.verify)
    const mergedRemove = rowAction('ada@example.test', zhCN.identifiers.remove)
    expect(mergedVerify).toBeEnabled()
    expect(mergedVerify).not.toHaveAttribute('aria-disabled', 'true')
    expect(mergedRemove).toBeEnabled()
    expect(mergedRemove).not.toHaveAttribute('aria-disabled', 'true')

    await clickRowAction(user, 'ada@example.test', zhCN.identifiers.verify)
    expect(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toHaveValue('ada@example.test')
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.cancel }),
    )

    await clickRowAction(user, 'ada@example.test', zhCN.identifiers.remove)
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent(zhCN.identifiers.confirmTitle)
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )
    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.empty.title)).toBeTruthy(),
    )
    expect(
      rig.calls.filter((call) => call.method === 'DELETE'),
    ).toHaveLength(1)
  })

  it('renders a row with one action as a plain danger button that opens its confirm', async () => {
    let removed = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            identifiers: removed
              ? []
              : [
                  identifier({
                    id: 'id-phone',
                    kind: 'phone',
                    value: '+14155550123',
                    proven: false,
                    verified_at: '2026-01-01T00:00:00Z',
                    verified_via: 'admin',
                  }),
                ],
          }),
        )
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/auth/identifiers/id-phone'
      ) {
        removed = true
        return new Response(null, { status: 204 })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await screen.findByText('+14155550123')
    const actions = rowActions('+14155550123')
    expect(actions.map((button) => button.textContent?.trim())).toEqual([
      zhCN.identifiers.remove,
    ])
    const remove = rowAction('+14155550123', zhCN.identifiers.remove)
    expect(remove).not.toBeNull()
    expect(remove).toHaveClass('MuiButton-colorError')
    expect((remove as HTMLElement).closest('[data-action-row]')).toBeNull()
    expect(
      actions.some((button) => button.hasAttribute('aria-haspopup')),
    ).toBe(false)
    expect(screen.queryByRole('menuitem')).toBeNull()

    await user.click(remove as HTMLElement)
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent(zhCN.identifiers.confirmTitle)
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.identifiers.confirmLabel,
      }),
    )
    await waitFor(() =>
      expect(
        rig.calls.filter(
          (call) =>
            call.method === 'DELETE' &&
            call.path === '/api/v1/auth/identifiers/id-phone',
        ),
      ).toHaveLength(1),
    )
    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.empty.title)).toBeTruthy(),
    )
  })

  describe('focusable initiating controls', () => {
    function twoEmailProfile(
      overrides: Record<string, unknown> = {},
    ): Record<string, unknown> {
      return profileBody({
        password_set: false,
        identifiers: [
          identifier({
            id: 'id-a',
            kind: 'email',
            value: 'a@x.test',
            proven: true,
            sign_in: true,
          }),
          identifier({
            id: 'id-b',
            kind: 'email',
            value: 'b@x.test',
            proven: true,
            sign_in: true,
          }),
        ],
        ...overrides,
      })
    }

    it('keeps the switch that started the write busy and holds its siblings without a reason', async () => {
      const put = deferred<Response>()
      const writes: unknown[] = []
      let signInA = true
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              password_set: false,
              identifiers: [
                identifier({
                  id: 'id-a',
                  kind: 'email',
                  value: 'a@x.test',
                  proven: true,
                  sign_in: signInA,
                }),
                identifier({
                  id: 'id-b',
                  kind: 'email',
                  value: 'b@x.test',
                  proven: true,
                  sign_in: false,
                }),
              ],
            }),
          )
        }
        if (
          call.method === 'PUT' &&
          call.path === '/api/v1/auth/identifiers/id-a'
        ) {
          writes.push(call.body)
          return put.promise.then((response) => {
            signInA =
              (call.body as { sign_in?: boolean }).sign_in === true
            return response
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond)
      const user = userEvent.setup()

      await screen.findAllByText('a@x.test')
      const toggle = screen.getByRole('switch', {
        name: zhCN.identifiers.action.disable,
      })
      await user.click(toggle)
      await waitFor(() => expect(writes).toHaveLength(1))

      expect(toggle).toHaveFocus()
      expect(toggle).toHaveAttribute('aria-disabled', 'true')
      expect(toggle).not.toHaveAttribute('aria-busy')
      expect(toggle).not.toHaveAccessibleDescription()
      expect(toggle).toBeChecked()
      await user.click(toggle)
      await user.keyboard(' ')
      expect(toggle).toHaveFocus()
      expect(writes).toHaveLength(1)
      expect(toggle).toBeChecked()
      const siblingRemove = rowAction('b@x.test', zhCN.identifiers.remove)
      expect(siblingRemove).not.toBeNull()
      ;(siblingRemove as HTMLElement).focus()
      expect(siblingRemove).toHaveFocus()
      expect(siblingRemove).toHaveAttribute('aria-disabled', 'true')
      expect(siblingRemove).not.toHaveAttribute('aria-busy')
      expect(siblingRemove).not.toHaveAccessibleDescription()
      const picker = screen.getByRole('combobox', {
        name: zhCN.identifiers.kind.email,
      })
      picker.focus()
      expect(picker).toHaveFocus()
      expect(picker).toHaveAttribute('aria-readonly', 'true')
      const siblingDefault = screen.getByRole('radio', {
        name: zhCN.identifiers.axes.defaultAria.replace(
          '{{value}}',
          'b@x.test',
        ),
      })
      siblingDefault.focus()
      expect(siblingDefault).toHaveFocus()
      expect(siblingDefault).toHaveAttribute('aria-disabled', 'true')
      expect(siblingDefault).not.toHaveAttribute('aria-busy')
      expect(siblingDefault).not.toHaveAccessibleDescription()
      await user.click(siblingDefault)
      await user.keyboard(' ')
      expect(siblingDefault).toHaveFocus()
      expect(writes).toHaveLength(1)
      expect(siblingDefault).not.toBeChecked()

      const deleteCalls = () =>
        rig.calls.filter((call) => call.method === 'DELETE')
      await user.click(siblingRemove as HTMLElement)
      ;(siblingRemove as HTMLElement).focus()
      await user.keyboard('{Enter}')
      await user.keyboard(' ')
      expect(deleteCalls()).toHaveLength(0)
      expect(screen.queryByRole('dialog')).toBeNull()

      put.resolve(new Response(null, { status: 204 }))
      await waitFor(() => {
        const ready = screen.getByRole('switch', {
          name: zhCN.identifiers.action.enable,
        })
        expect(ready).toBeEnabled()
        expect(ready).not.toHaveAttribute('aria-disabled', 'true')
      })
    })

    it('keeps an empty add-form send focusable and guards its programmatic submit', async () => {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(200, profileBody({ identifiers: [] }))
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond)
      const user = userEvent.setup()
      const sends = () =>
        rig.calls.filter(
          (call) =>
            call.method === 'POST' &&
            call.path === '/api/v1/auth/identifiers',
        )

      await user.click(
        await screen.findByRole('button', {
          name: zhCN.identifiers.add.open,
        }),
      )
      const submit = screen.getByRole('button', {
        name: zhCN.identifiers.add.submitEmail,
      })
      const form = screen
        .getByLabelText(zhCN.identifiers.add.value.labelEmail)
        .closest('form') as HTMLFormElement

      submit.focus()
      expect(submit).toHaveFocus()
      expect(submit).toHaveAttribute('aria-disabled', 'true')
      expect(submit).not.toHaveAttribute('disabled')
      expect(submit).toHaveAccessibleDescription(
        zhCN.identifiers.add.emailEmpty,
      )

      await user.keyboard('{Enter}')
      expect(sends()).toHaveLength(0)
      expect(screen.queryByText(zhCN.identifiers.add.invalidEmail)).toBeNull()

      fireEvent.submit(form)
      expect(sends()).toHaveLength(0)

      await user.type(
        screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
        'new@x.test',
      )
      expect(submit).toBeEnabled()
      expect(submit).not.toHaveAttribute('aria-disabled', 'true')
      fireEvent.submit(form)
      await waitFor(() => expect(sends()).toHaveLength(1))
    })

    it('keeps an empty verification send focusable and explains the missing code', async () => {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(200, twoEmailProfile())
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond)
      const user = userEvent.setup()

      await screen.findAllByText('a@x.test')
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.open }),
      )
      await user.type(
        screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
        'new@x.test',
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
      )
      await screen.findByLabelText(zhCN.identifiers.add.code.label)
      const verify = screen.getByRole('button', {
        name: zhCN.identifiers.add.verify,
      })
      verify.focus()
      expect(verify).toHaveFocus()
      expect(verify).toHaveAttribute('aria-disabled', 'true')
      expect(verify).toHaveAccessibleDescription(zhCN.identifiers.add.codeEmpty)
      const verifies = () =>
        rig.calls.filter(
          (call) =>
            call.method === 'POST' &&
            call.path === '/api/v1/auth/identifiers/verify',
        )
      await user.keyboard('{Enter}')
      expect(verifies()).toHaveLength(0)
    })

    it('keeps a default choice without a manageable record focusable and names why', async () => {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              identifiers: [
                identifier({
                  id: undefined,
                  kind: 'email',
                  value: 'ada@x.test',
                  proven: true,
                  sign_in: true,
                }),
              ],
            }),
          )
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond)
      const user = userEvent.setup()

      await screen.findAllByText('ada@x.test')
      const choice = screen.getByRole('radio', {
        name: zhCN.identifiers.axes.defaultAria.replace(
          '{{value}}',
          'ada@x.test',
        ),
      })
      choice.focus()
      expect(choice).toHaveFocus()
      expect(choice).toHaveAttribute('aria-disabled', 'true')
      expect(choice).not.toHaveAttribute('aria-busy')
      expect(choice).toHaveAccessibleDescription(
        zhCN.identifiers.axes.defaultUnavailable,
      )

      const writes = () =>
        rig.calls.filter((call) => call.method === 'PUT')
      await user.click(choice)
      await user.keyboard(' ')
      expect(writes()).toHaveLength(0)
      expect(choice).not.toBeChecked()
    })

    it('guards the row verification form\'s programmatic submit while its code is missing', async () => {
      let proven = false
      let verifies = 0
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              identifiers: [
                identifier({
                  id: 'id-primary',
                  kind: 'email',
                  value: 'ada@x.test',
                  proven: true,
                }),
                identifier({
                  id: 'id-secondary',
                  kind: 'email',
                  value: 'second@x.test',
                  proven,
                  verified_via: proven ? 'email-code' : 'admin',
                }),
              ],
            }),
          )
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers/verify'
        ) {
          verifies += 1
          proven = true
          return jsonResponse(200, { state: 'added' })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      mount(respond)
      const user = userEvent.setup()

      await clickRowAction(user, 'second@x.test', zhCN.identifiers.verify)
      const form = screen
        .getByLabelText(zhCN.identifiers.add.value.labelEmail)
        .closest('form') as HTMLFormElement
      await user.click(
        within(form).getByRole('button', {
          name: zhCN.identifiers.add.submitEmail,
        }),
      )
      await screen.findByLabelText(zhCN.identifiers.add.code.label)

      fireEvent.submit(form)
      expect(verifies).toBe(0)

      await user.type(
        screen.getByLabelText(zhCN.identifiers.add.code.label),
        '123456',
      )
      fireEvent.submit(form)
      await waitFor(() => expect(verifies).toBe(1))
    })

    it("hands focus to the next row's remove control when the removed row leaves", async () => {
      let removed = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            removed
              ? twoEmailProfile({
                  identifiers: [
                    identifier({
                      id: 'id-b',
                      kind: 'email',
                      value: 'b@x.test',
                      proven: true,
                      sign_in: true,
                    }),
                  ],
                })
              : twoEmailProfile(),
          )
        }
        if (
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/identifiers/id-a'
        ) {
          removed = true
          return new Response(null, { status: 204 })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond)
      const user = userEvent.setup()

      await screen.findAllByText('a@x.test')
      const nextRemove = rowAction('b@x.test', zhCN.identifiers.remove)
      expect(nextRemove).not.toBeNull()
      await clickRowAction(user, 'a@x.test', zhCN.identifiers.remove)
      const dialog = await screen.findByRole('dialog')
      await user.click(
        within(dialog).getByRole('button', {
          name: zhCN.identifiers.confirmLabel,
        }),
      )

      await waitFor(() => expect(screen.queryByText('a@x.test')).toBeNull())
      await waitFor(() => expect(nextRemove).toHaveFocus())
      expect(rig.calls.filter((call) => call.method === 'DELETE')).toHaveLength(1)
    })

    it('hands focus to the section container when the only row leaves', async () => {
      let removed = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              password_set: false,
              identifiers: removed
                ? []
                : [
                    identifier({
                      id: 'id-only',
                      kind: 'email',
                      value: 'only@x.test',
                      proven: true,
                      sign_in: true,
                    }),
                  ],
            }),
          )
        }
        if (
          call.method === 'DELETE' &&
          call.path === '/api/v1/auth/identifiers/id-only'
        ) {
          removed = true
          return new Response(null, { status: 204 })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      mount(respond)
      const user = userEvent.setup()

      await screen.findAllByText('only@x.test')
      await clickRowAction(user, 'only@x.test', zhCN.identifiers.remove)
      const dialog = await screen.findByRole('dialog')
      await user.click(
        within(dialog).getByRole('button', {
          name: zhCN.identifiers.confirmLabel,
        }),
      )

      await waitFor(() =>
        expect(screen.getByText(zhCN.identifiers.empty.title)).toBeTruthy(),
      )
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      const container = screen.getByRole('group', {
        name: zhCN.identifiers.title,
      })
      await waitFor(() => expect(container).toHaveFocus())
    })

    it('does not take focus back when the operator moved it during the removal', async () => {
      const remove = deferred<Response>()
      let removed = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            removed
              ? twoEmailProfile({
                  identifiers: [
                    identifier({
                      id: 'id-b',
                      kind: 'email',
                      value: 'b@x.test',
                      proven: true,
                      sign_in: true,
                    }),
                  ],
                })
              : twoEmailProfile(),
          )
        }
        if (call.method === 'DELETE') {
          return remove.promise.then(() => {
            removed = true
            return new Response(null, { status: 204 })
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      mount(respond)
      const user = userEvent.setup()

      await screen.findAllByText('a@x.test')
      await clickRowAction(user, 'a@x.test', zhCN.identifiers.remove)
      const dialog = await screen.findByRole('dialog')
      await user.click(
        within(dialog).getByRole('button', {
          name: zhCN.identifiers.confirmLabel,
        }),
      )
      await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
      const elsewhere = screen.getByRole('button', {
        name: zhCN.identifiers.add.open,
      })
      elsewhere.focus()
      expect(elsewhere).toHaveFocus()

      remove.resolve(new Response(null, { status: 204 }))
      await waitFor(() => expect(screen.queryByText('a@x.test')).toBeNull())
      expect(elsewhere).toHaveFocus()
    })

    it('hands focus to the add entry when a landed add closes its form', async () => {
      let added = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              identifiers: added
                ? [
                    identifier({
                      id: 'id-new',
                      kind: 'email',
                      value: 'new@x.test',
                    }),
                  ]
                : [],
            }),
          )
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers/verify'
        ) {
          added = true
          return jsonResponse(200, { state: 'added' })
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      mount(respond)
      const user = userEvent.setup()

      await user.click(
        await screen.findByRole('button', {
          name: zhCN.identifiers.add.open,
        }),
      )
      await user.type(
        screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
        'new@x.test',
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
      )
      const code = await screen.findByLabelText(
        zhCN.identifiers.add.code.label,
      )
      await user.type(code, '123456')
      const verify = screen.getByRole('button', {
        name: zhCN.identifiers.add.verify,
      })
      await user.click(verify)

      const entry = await screen.findByRole('button', {
        name: zhCN.identifiers.add.open,
      })
      await waitFor(() => expect(entry).toHaveFocus())
    })

    it('closes a landed add at once and lands on the entry when the re-read fails', async () => {
      const reRead = deferred<Response>()
      let added = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return added
            ? reRead.promise
            : jsonResponse(200, profileBody({ identifiers: [] }))
        }
        if (call.method === 'POST' && call.path === '/api/v1/auth/identifiers') {
          added = true
          return jsonResponse(200, { state: 'added' })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      mount(respond)
      const user = userEvent.setup()

      await user.click(
        await screen.findByRole('button', { name: zhCN.identifiers.add.open }),
      )
      fireEvent.mouseDown(
        screen.getByRole('combobox', { name: zhCN.identifiers.add.kind.label }),
      )
      await user.click(
        screen.getByRole('option', { name: zhCN.identifiers.add.kind.username }),
      )
      await user.type(
        screen.getByLabelText(zhCN.identifiers.add.value.labelUsername),
        'ada2',
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.submitUsername }),
      )

      await waitFor(() =>
        expect(
          screen.queryByLabelText(zhCN.identifiers.add.value.labelUsername),
        ).toBeNull(),
      )

      reRead.resolve(errorResponse(500, 'authn.unavailable'))
      const entry = await screen.findByRole('button', {
        name: zhCN.identifiers.add.open,
      })
      await waitFor(() => expect(entry).toHaveFocus())
      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent(zhCN.stale.description)
    })

    it("hands focus to the next row's default action when the landed choice removes it", async () => {
      let defaultA = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              password_set: false,
              identifiers: [
                identifier({
                  id: 'id-a',
                  kind: 'email',
                  value: 'a@x.test',
                  proven: true,
                  sign_in: true,
                  default_contact: defaultA,
                }),
                identifier({
                  id: 'id-b',
                  kind: 'email',
                  value: 'b@x.test',
                  proven: true,
                  sign_in: true,
                }),
              ],
            }),
          )
        }
        if (
          call.method === 'PUT' &&
          call.path === '/api/v1/auth/identifiers/id-a'
        ) {
          defaultA = true
          return new Response(null, { status: 204 })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(
        <IdentifiersSection session={rig.session} axis="contact" />,
      )
      const user = userEvent.setup()

      await screen.findAllByText('a@x.test')
      const nextDefault = rowAction(
        'b@x.test',
        zhCN.identifiers.axes.defaultContact,
      )
      expect(nextDefault).not.toBeNull()
      await user.click(
        rowAction('a@x.test', zhCN.identifiers.axes.defaultContact) as HTMLElement,
      )

      await waitFor(() => expect(nextDefault).toHaveFocus())
    })

    it("hands focus to the next row's verify action when a landed verification removes one", async () => {
      let provenA = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              password_set: false,
              identifiers: [
                identifier({
                  id: 'id-a',
                  kind: 'email',
                  value: 'a@x.test',
                  proven: provenA,
                  verified_via: provenA ? 'email-code' : 'admin',
                }),
                identifier({
                  id: 'id-b',
                  kind: 'email',
                  value: 'b@x.test',
                  proven: false,
                  verified_via: 'admin',
                }),
              ],
            }),
          )
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers/verify'
        ) {
          provenA = true
          return jsonResponse(200, { state: 'added' })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(<IdentifiersSection session={rig.session} />)
      const user = userEvent.setup()

      await screen.findAllByText('a@x.test')
      const nextVerify = rowAction('b@x.test', zhCN.identifiers.verify)
      expect(nextVerify).not.toBeNull()
      await clickRowAction(user, 'a@x.test', zhCN.identifiers.verify)
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
      )
      const code = await screen.findByLabelText(
        zhCN.identifiers.add.code.label,
      )
      await user.type(code, '123456')
      await user.click(
        within(verificationForm()).getByRole('button', {
          name: zhCN.identifiers.add.verify,
        }),
      )

      await waitFor(() => expect(nextVerify).toHaveFocus())
    })

    it('closes the add form and drops the waiting send when it is cancelled', async () => {
      const fake = installFakeTurnstile()
      try {
        const respond: RealResponder = (call: RealCall) => {
          if (call.method === 'GET' && call.path === PROFILE_PATH) {
            return jsonResponse(200, profileBody({ identifiers: [] }))
          }
          if (
            call.method === 'POST' &&
            call.path === '/api/v1/auth/identifiers'
          ) {
            return jsonResponse(200, {
              state: 'pending',
              expires_in: 600,
              resend_after: 60,
            })
          }
          throw new Error(`unexpected ${call.method} ${call.path}`)
        }
        const { rig } = mount(respond, {
          required: true,
          provider: 'turnstile',
          site_key: 'site-1',
        })
        const user = userEvent.setup()

        await user.click(
          await screen.findByRole('button', {
            name: zhCN.identifiers.add.open,
          }),
        )
        await user.type(
          screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
          'new@x.test',
        )
        await user.click(
          screen.getByRole('button', {
            name: zhCN.identifiers.add.submitEmail,
          }),
        )
        await waitFor(() => expect(fake.widgets).toHaveLength(1))

        const cancel = screen.getByRole('button', {
          name: zhCN.identifiers.add.cancel,
        })
        cancel.focus()
        expect(cancel).toHaveFocus()
        await user.keyboard('{Enter}')

        await waitFor(() =>
          expect(
            screen.queryByLabelText(zhCN.identifiers.add.value.labelEmail),
          ).toBeNull(),
        )
        act(() => {
          fake.token('token-1')
        })
        expect(
          rig.calls.filter(
            (call) =>
              call.method === 'POST' &&
              call.path === '/api/v1/auth/identifiers',
          ),
        ).toHaveLength(0)
      } finally {
        fake.restore()
      }
    })

    it('closes the row flow and drops the waiting resend when it is cancelled', async () => {
      const fake = installFakeTurnstile()
      try {
        const respond: RealResponder = (call: RealCall) => {
          if (call.method === 'GET' && call.path === PROFILE_PATH) {
            return jsonResponse(
              200,
              profileBody({
                identifiers: [
                  identifier({
                    id: 'id-primary',
                    kind: 'email',
                    value: 'ada@x.test',
                  }),
                  identifier({
                    id: 'id-secondary',
                    kind: 'email',
                    value: 'second@x.test',
                    proven: false,
                    verified_via: 'admin',
                  }),
                ],
              }),
            )
          }
          if (
            call.method === 'POST' &&
            call.path === '/api/v1/auth/identifiers'
          ) {
            return jsonResponse(200, {
              state: 'pending',
              expires_in: 600,
              resend_after: 0,
            })
          }
          throw new Error(`unexpected ${call.method} ${call.path}`)
        }
        const { rig } = mount(respond, {
          required: true,
          provider: 'turnstile',
          site_key: 'site-1',
        })
        const user = userEvent.setup()
        const sends = () =>
          rig.calls.filter(
            (call) =>
              call.method === 'POST' &&
              call.path === '/api/v1/auth/identifiers',
          )

        await clickRowAction(user, 'second@x.test', zhCN.identifiers.verify)
        await user.click(
          screen.getByRole('button', {
            name: zhCN.identifiers.add.submitEmail,
          }),
        )
        await waitFor(() => expect(fake.widgets).toHaveLength(1))
        act(() => {
          fake.token('token-1')
        })
        await waitFor(() => expect(sends()).toHaveLength(1))

        const codeField = await screen.findByLabelText(
          zhCN.identifiers.add.code.label,
        )
        const form = codeField.closest('form') as HTMLFormElement
        await user.click(
          within(form).getByRole('button', { name: zhCN.verification.resend }),
        )
        await waitFor(() => expect(fake.widgets).toHaveLength(2))

        const cancel = within(form).getByRole('button', {
          name: zhCN.identifiers.add.cancel,
        })
        cancel.focus()
        expect(cancel).toHaveFocus()
        await user.keyboard('{Enter}')

        await waitFor(() =>
          expect(
            screen.queryByLabelText(zhCN.identifiers.add.code.label),
          ).toBeNull(),
        )
        act(() => {
          fake.token('token-2')
        })
        expect(sends()).toHaveLength(1)
      } finally {
        fake.restore()
      }
    })

    it('lands on the form when the add entry opens it', async () => {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(200, profileBody({ identifiers: [] }))
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      mount(respond)
      const user = userEvent.setup()

      await user.click(
        await screen.findByRole('button', {
          name: zhCN.identifiers.add.open,
        }),
      )
      const firstField = screen.getByRole('combobox', {
        name: zhCN.identifiers.add.kind.label,
      })
      await waitFor(() => expect(firstField).toHaveFocus())
    })

    it("lands on the next row's default action when a step-up challenge replays the choice", async () => {
      captureRefreshWarning()
      let elevated = false
      let defaultA = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              password_set: false,
              identifiers: [
                identifier({
                  id: 'id-a',
                  kind: 'email',
                  value: 'a@x.test',
                  proven: true,
                  sign_in: true,
                  default_contact: defaultA,
                }),
                identifier({
                  id: 'id-b',
                  kind: 'email',
                  value: 'b@x.test',
                  proven: true,
                  sign_in: true,
                }),
              ],
            }),
          )
        }
        if (
          call.method === 'PUT' &&
          call.path === '/api/v1/auth/identifiers/id-a'
        ) {
          if (!elevated) {
            return errorResponse(
              401,
              'authn.insufficient_user_authentication',
            )
          }
          defaultA = true
          return new Response(null, { status: 204 })
        }
        if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
          return jsonResponse(200, {
            factors: [
              {
                id: 'factor-1',
                kind: 'totp',
                label: 'App',
                status: 'active',
                created_at: '2026-01-01T00:00:00Z',
              },
            ],
          })
        }
        if (call.method === 'POST' && call.path === FLOW_START) {
          return jsonResponse(200, {
            seq: 1,
            state: 'second',
            next: {
              kind: 'factor',
              factors: [{ id: 'factor-1', kind: 'totp', label: 'App' }],
            },
          })
        }
        if (call.method === 'POST' && call.path === FLOW_ANSWER) {
          const body = call.body as { seq: number }
          if (body.seq === 1) {
            return jsonResponse(200, {
              seq: 2,
              state: 'second',
              next: {
                kind: 'challenge',
                fields: [{ name: 'code', kind: 'text' }],
              },
            })
          }
          elevated = true
          return jsonResponse(200, {
            state: 'elevated',
            token_type: 'Bearer',
            access_token: 'access-2',
            expires_in: 3600,
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(
        <IdentifiersSection session={rig.session} axis="contact" />,
      )
      const user = userEvent.setup()

      await screen.findAllByText('a@x.test')
      const nextDefault = rowAction(
        'b@x.test',
        zhCN.identifiers.axes.defaultContact,
      )
      expect(nextDefault).not.toBeNull()
      await user.click(
        rowAction(
          'a@x.test',
          zhCN.identifiers.axes.defaultContact,
        ) as HTMLElement,
      )

      await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
      await user.click(
        screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }),
      )
      await user.type(
        await screen.findByLabelText(zhCN.flowStep.codeLabel),
        '123456',
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
      )

      await waitFor(() => expect(nextDefault).toHaveFocus())
      expect(
        rig.calls.filter((call) => call.method === 'PUT'),
      ).toHaveLength(2)
    })

    it("lands on the previous row's default action when the last row's choice removes it", async () => {
      let defaultC = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              password_set: false,
              identifiers: [
                identifier({
                  id: 'id-a',
                  kind: 'email',
                  value: 'a@x.test',
                  proven: true,
                  sign_in: true,
                }),
                identifier({
                  id: 'id-b',
                  kind: 'email',
                  value: 'b@x.test',
                  proven: true,
                  sign_in: true,
                }),
                identifier({
                  id: 'id-c',
                  kind: 'email',
                  value: 'c@x.test',
                  proven: true,
                  sign_in: true,
                  default_contact: defaultC,
                }),
              ],
            }),
          )
        }
        if (
          call.method === 'PUT' &&
          call.path === '/api/v1/auth/identifiers/id-c'
        ) {
          defaultC = true
          return new Response(null, { status: 204 })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(
        <IdentifiersSection session={rig.session} axis="contact" />,
      )
      const user = userEvent.setup()

      await screen.findAllByText('c@x.test')
      const previousDefault = rowAction(
        'b@x.test',
        zhCN.identifiers.axes.defaultContact,
      )
      expect(previousDefault).not.toBeNull()
      await user.click(
        rowAction(
          'c@x.test',
          zhCN.identifiers.axes.defaultContact,
        ) as HTMLElement,
      )

      await waitFor(() => expect(previousDefault).toHaveFocus())
    })

    it('does not take focus back when the operator moved it during the default choice', async () => {
      const put = deferred<Response>()
      let defaultA = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              password_set: false,
              identifiers: [
                identifier({
                  id: 'id-a',
                  kind: 'email',
                  value: 'a@x.test',
                  proven: true,
                  sign_in: true,
                  default_contact: defaultA,
                }),
                identifier({
                  id: 'id-b',
                  kind: 'email',
                  value: 'b@x.test',
                  proven: true,
                  sign_in: true,
                }),
              ],
            }),
          )
        }
        if (
          call.method === 'PUT' &&
          call.path === '/api/v1/auth/identifiers/id-a'
        ) {
          return put.promise.then(() => {
            defaultA = true
            return new Response(null, { status: 204 })
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(
        <IdentifiersSection session={rig.session} axis="contact" />,
      )
      const user = userEvent.setup()

      await screen.findAllByText('a@x.test')
      await user.click(
        rowAction(
          'a@x.test',
          zhCN.identifiers.axes.defaultContact,
        ) as HTMLElement,
      )
      await waitFor(() =>
        expect(
          rig.calls.filter((call) => call.method === 'PUT'),
        ).toHaveLength(1),
      )

      const elsewhere = rowAction('b@x.test', zhCN.identifiers.remove)
      expect(elsewhere).not.toBeNull()
      ;(elsewhere as HTMLElement).focus()
      expect(elsewhere).toHaveFocus()

      put.resolve(new Response(null, { status: 204 }))
      await waitFor(() =>
        expect(
          rowAction('a@x.test', zhCN.identifiers.axes.defaultContact),
        ).toBeNull(),
      )
      expect(elsewhere).toHaveFocus()
    })

    it("lands on the previous row's verify action when the last row's verification removes one", async () => {
      let provenB = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              identifiers: [
                identifier({
                  id: 'id-a',
                  kind: 'email',
                  value: 'a@x.test',
                  proven: false,
                  verified_via: 'admin',
                }),
                identifier({
                  id: 'id-b',
                  kind: 'email',
                  value: 'b@x.test',
                  proven: provenB,
                  verified_via: provenB ? 'email-code' : 'admin',
                }),
              ],
            }),
          )
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers/verify'
        ) {
          provenB = true
          return jsonResponse(200, { state: 'added' })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(<IdentifiersSection session={rig.session} />)
      const user = userEvent.setup()

      await screen.findAllByText('a@x.test')
      const previousVerify = rowAction('a@x.test', zhCN.identifiers.verify)
      expect(previousVerify).not.toBeNull()
      await clickRowAction(user, 'b@x.test', zhCN.identifiers.verify)
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
      )
      const code = await screen.findByLabelText(
        zhCN.identifiers.add.code.label,
      )
      await user.type(code, '123456')
      await user.click(
        within(verificationForm()).getByRole('button', {
          name: zhCN.identifiers.add.verify,
        }),
      )

      await waitFor(() => expect(previousVerify).toHaveFocus())
    })

    it('does not take focus back when the operator moved it during a row verification', async () => {
      const verify = deferred<Response>()
      let provenB = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              identifiers: [
                identifier({
                  id: 'id-a',
                  kind: 'email',
                  value: 'a@x.test',
                  proven: false,
                  verified_via: 'admin',
                }),
                identifier({
                  id: 'id-b',
                  kind: 'email',
                  value: 'b@x.test',
                  proven: provenB,
                  verified_via: provenB ? 'email-code' : 'admin',
                }),
              ],
            }),
          )
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers/verify'
        ) {
          return verify.promise.then(() => {
            provenB = true
            return jsonResponse(200, { state: 'added' })
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(<IdentifiersSection session={rig.session} />)
      const user = userEvent.setup()

      await screen.findAllByText('a@x.test')
      await clickRowAction(user, 'b@x.test', zhCN.identifiers.verify)
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
      )
      const code = await screen.findByLabelText(
        zhCN.identifiers.add.code.label,
      )
      await user.type(code, '123456')
      await user.click(
        within(verificationForm()).getByRole('button', {
          name: zhCN.identifiers.add.verify,
        }),
      )
      await waitFor(() =>
        expect(
          rig.calls.filter(
            (call) =>
              call.method === 'POST' &&
              call.path === '/api/v1/auth/identifiers/verify',
          ),
        ).toHaveLength(1),
      )

      const elsewhere = screen.getByRole('button', {
        name: zhCN.identifiers.add.open,
      })
      elsewhere.focus()
      expect(elsewhere).toHaveFocus()

      verify.resolve(jsonResponse(200, { state: 'added' }))
      await waitFor(() =>
        expect(rowAction('b@x.test', zhCN.identifiers.verify)).toBeNull(),
      )
      expect(elsewhere).toHaveFocus()
    })

    it('does not take focus back when the operator moved it during a landed add', async () => {
      const verify = deferred<Response>()
      let added = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              password_set: false,
              identifiers: [
                identifier({
                  id: 'id-a',
                  kind: 'email',
                  value: 'a@x.test',
                  proven: true,
                  sign_in: true,
                }),
                ...(added
                  ? [
                      identifier({
                        id: 'id-new',
                        kind: 'email',
                        value: 'new@x.test',
                      }),
                    ]
                  : []),
              ],
            }),
          )
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers'
        ) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        if (
          call.method === 'POST' &&
          call.path === '/api/v1/auth/identifiers/verify'
        ) {
          return verify.promise.then(() => {
            added = true
            return jsonResponse(200, { state: 'added' })
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const rig = makeRealClientRig(respond)
      rig.store.set('access-1')
      renderWithProviders(<IdentifiersSection session={rig.session} />)
      const user = userEvent.setup()

      await screen.findAllByText('a@x.test')
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.open }),
      )
      await user.type(
        screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
        'new@x.test',
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
      )
      const code = await screen.findByLabelText(
        zhCN.identifiers.add.code.label,
      )
      await user.type(code, '123456')
      await user.click(
        screen.getByRole('button', { name: zhCN.identifiers.add.verify }),
      )
      await waitFor(() =>
        expect(
          rig.calls.filter(
            (call) =>
              call.method === 'POST' &&
              call.path === '/api/v1/auth/identifiers/verify',
          ),
        ).toHaveLength(1),
      )

      const elsewhere = rowAction('a@x.test', zhCN.identifiers.remove)
      expect(elsewhere).not.toBeNull()
      ;(elsewhere as HTMLElement).focus()
      expect(elsewhere).toHaveFocus()

      verify.resolve(jsonResponse(200, { state: 'added' }))
      await waitFor(() =>
        expect(
          screen.queryByLabelText(zhCN.identifiers.add.code.label),
        ).toBeNull(),
      )
      expect(elsewhere).toHaveFocus()
    })
  })
})

