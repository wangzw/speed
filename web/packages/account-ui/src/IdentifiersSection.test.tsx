/**
 * IdentifiersSection behaviour: the identifiers and the removal write
 * (DELETE /api/v1/auth/identifiers/{id}) of the profile read
 * (GET /api/v1/auth/me).
 *
 * What this suite pins: each identifier renders with its kind and value and
 * its one-of-two verified/unverified state; the sign-in ways and contacts
 * lists follow the profile, with every registered address a contact and no
 * control that would change that; an unverified email row proves its
 * own address through the code flow and flips its chip; a row's remove sends
 * DELETE /api/v1/auth/identifiers/{id} behind the danger confirm and refetches
 * the profile so the row drops off; a last-way-in refusal (409) renders
 * its code text, and the server's last-login-name code renders its own text
 * and re-reads the profile when the client guard read a stale list; a
 * removal refused with 401
 * authn.insufficient_user_authentication opens the step-up dialog, a won
 * step-up re-running it once; an account with no identifiers renders the
 * empty state; and an answer that omitted the identifiers key renders the
 * error state (never a "no identifiers" claim).
 */

import { beforeEach, describe, expect, it, vi } from 'vitest'
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
  humanGate?: {
    readonly required: boolean
    readonly provider: string
    readonly site_key?: string
  },
) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(
    <IdentifiersSection session={rig.session} humanGate={humanGate} />,
  )
  return { rig, ...rendered }
}

/** The list row or table row that shows `value`: the contact record's own
 * container, whichever view renders it. */
function rowScope(value: string): HTMLElement {
  for (const text of screen.getAllByText(value)) {
    const row = text.closest('li, tr')
    if (row !== null) {
      return row as HTMLElement
    }
  }
  throw new Error(`no row shows ${value}`)
}

/** The action buttons the row that shows `value` renders, in display order. */
function rowActions(value: string): HTMLElement[] {
  return within(rowScope(value)).queryAllByRole('button')
}

/** The row's action button named `name`, or null. */
function rowAction(value: string, name: string): HTMLElement | null {
  return within(rowScope(value)).queryByRole('button', { name })
}

/** Click one of the row's inline action buttons. */
async function clickRowAction(
  user: ReturnType<typeof userEvent.setup>,
  value: string,
  name: string,
): Promise<void> {
  // The row arrives with the profile read; wait for it before choosing one
  // of its actions.
  await waitFor(() => expect(rowScope(value)).toBeTruthy())
  await user.click(
    within(rowScope(value)).getByRole('button', { name }),
  )
}

/** The open verification form, scoped by its code field so the row's own
 * verify action (which shares the action word) is not matched. */
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
                verified_at: '',
                verified_via: '',
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
    // The email is a sign-in way, so it reads as the email group's chosen
    // value and as a row in the contacts list; the phone is a contact only.
    expect(await screen.findAllByText('ada@x.test')).toHaveLength(2)
    expect(screen.getAllByText(zhCN.identifiers.kind.email)).toHaveLength(1)
    expect(screen.getByText('+14155550123')).toBeTruthy()
    expect(screen.getAllByText(zhCN.identifiers.verified)).toHaveLength(1)
    expect(screen.getByText(zhCN.identifiers.unverified)).toBeTruthy()

    await expectNoAxeViolations()
  })

  describe('verification chip colors', () => {
    /** The chip whose visible label is `text`. */
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

    // Both views that show a verification state: the merged view's contact
    // list and the contact axis's address table.
    const views = [
      { name: 'the merged view', axis: undefined },
      { name: 'the contact table', axis: 'contact' as const },
    ]

    for (const view of views) {
      it(`colors a proved identifier success and an unproved one default in ${view.name}`, async () => {
        // The chip's color states the same one-of-two answer as its label:
        // proved ownership is a completed positive state (success), and an
        // unproved address is neutral (default). Primary is reserved for the
        // session the viewer is using, never for a status.
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
                    verified_at: '',
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
    // A no-password account's phone removal was refused for a fresh
    // authentication the session could not supply, so the re-login exit
    // recorded it. The surface mounts again on the recorded page and replays
    // the removal once, then consumes the record.
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
    // The chip answers one question -- has ownership been proved? An address
    // an administrator asserted and an unverified import are both unproved,
    // so they read Not verified like any other; there is no third state.
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

    // The first address is the email group's chosen value; both addresses
    // are contact rows.
    expect(await screen.findAllByText('operator@demo.test')).toHaveLength(2)
    expect(screen.getAllByText(zhCN.identifiers.unverified)).toHaveLength(2)
    expect(screen.queryByText(zhCN.identifiers.verified)).toBeNull()
    // The binary vocabulary has no channel labels left.
    expect(zhCN.identifiers).not.toHaveProperty('addedByAdmin')
    expect(zhCN.identifiers).not.toHaveProperty('importedUnverified')
  })

  it('offers a verify action on an unverified email row of a verified account', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'verified', email: 'ada@x.test', since: '' },
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

    // The unverified address is a contact; the picker displays the account's
    // chosen address, so this one reads once.
    expect(await screen.findAllByText('second@x.test')).toHaveLength(1)
    // The account as a whole is verified, so the banner offers no entry for
    // this row; the row itself must.
    expect(rowAction('second@x.test', zhCN.identifiers.verify)).toBeTruthy()
    // A proved address has nothing to verify, so its row omits the action.
    expect(rowAction('ada@x.test', zhCN.identifiers.verify)).toBeNull()
  })

  it('keeps the verify action when the row is the only login name', async () => {
    // The remove action is withheld on a password account's only login name;
    // the verify action must still appear, or the unverified row would have
    // no entry at all.
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

    expect(await screen.findAllByText('only@x.test')).toHaveLength(2)
    // Removal is withheld, leaving verification as the row's one action: it
    // renders as a plain button, not behind an overflow trigger.
    const actions = rowActions('only@x.test')
    expect(actions.map((button) => button.textContent?.trim())).toEqual([
      zhCN.identifiers.verify,
    ])
    expect(rowAction('only@x.test', zhCN.identifiers.verify)).toBeEnabled()
    // The only login name withholds removal, not verification.
    expect(rowAction('only@x.test', zhCN.identifiers.remove)).toBeNull()
    // The row carries no overflow trigger and no menu item.
    expect(
      screen.queryByRole('button', {
        name: zhCN.identifiers.actionsAria.replace(
          '{{value}}',
          'only@x.test',
        ),
      }),
    ).toBeNull()
    expect(screen.queryByRole('menuitem')).toBeNull()
  })

  it('proves an unverified row through its own address and flips the chip', async () => {
    let proven = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'verified', email: 'ada@x.test', since: '' },
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

    // The flow shows the row's address and does not let it change.
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
    // The refetched profile flips the row's chip; both contact rows are
    // proved now.
    await waitFor(() =>
      expect(screen.getAllByText(zhCN.identifiers.verified)).toHaveLength(2),
    )
    expect(screen.queryByText(zhCN.identifiers.unverified)).toBeNull()
    // The row verification confirms itself, naming the address it proved.
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
    // The host opens the add face with `openAddSignal`; the component's own
    // button opens the same face. Both entries must settle the section the
    // same way: one code flow at a time, and no result from the closed flow
    // standing under the new form.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'verified', email: 'ada@x.test', since: '' },
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
    // The host's stand-in for the card header's add button: it bumps the
    // counter, which is the only entry the section sees.
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
    // The row flow's send fails and settles the section's result slot.
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.submitEmail }),
    )
    expect(
      await screen.findByText(zhCN.errors.authn.unavailable),
    ).toBeTruthy()

    await user.click(screen.getByRole('button', { name: 'host-add' }))

    // The add form owns the section now: one cancel action, one value field,
    // and the row flow's failure is gone from the shared slot.
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

    // Cancelling the add form leaves no row flow behind to reappear.
    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.add.cancel }),
    )
    expect(
      screen.queryByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toBeNull()
  })

  it.each(['contact', 'both'] as const)(
    'gives the %s view the resend path and countdown after a send',
    async (axis) => {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileBody({
              verification: {
                state: 'verified',
                email: 'ada@x.test',
                since: '',
              },
              identifiers: [
                identifier({
                  id: 'id-unproved',
                  kind: 'email',
                  value: 'ada@x.test',
                  proven: false,
                  verified_at: '',
                  verified_via: '',
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
        <IdentifiersSection session={rig.session} axis={axis} />,
      )
      const user = userEvent.setup()

      // Both views open the flow from the row's inline verify action.
      await screen.findAllByText('ada@x.test')
      await clickRowAction(user, 'ada@x.test', zhCN.identifiers.verify)

      const send = await screen.findByRole('button', {
        name: zhCN.identifiers.add.submitEmail,
      })

      // The mount's reads settled on the real clock; the send and its
      // countdown run on the fake one so the five seconds pass instantly.
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

        // Both views show the same controls in the same order: cancel on
        // the left, the resend in the middle, the primary verify closing
        // the row on the right.
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
        expect(resend).toBeDisabled()
        const [before, after] = zhCN.verification.resendIn.split('{{seconds}}')
        const note = `${before}5${after}`
        expect(scoped.getByText(note)).toBeTruthy()

        // Once the wait expires the resend becomes usable and its note
        // clears.
        act(() => {
          vi.advanceTimersByTime(5000)
        })
        expect(resend).toBeEnabled()
        expect(scoped.queryByText(note)).toBeNull()

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
        vi.useRealTimers()
      }
    },
  )

  it('does not offer the row verify action while the account is pending', async () => {
    // The pending state's one code record belongs to the verification
    // section and the host's banner; a second concurrent request from a row
    // would overwrite it.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            password_set: false,
            verification: {
              state: 'pending',
              email: 'pending@x.test',
              since: '',
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

    expect(await screen.findAllByText('pending@x.test')).toHaveLength(2)
    expect(screen.getAllByText(zhCN.identifiers.unverified)).toHaveLength(1)
    // The row still offers removal, but the verify action is absent while
    // the account-wide flow owns the pending code.
    expect(rowAction('pending@x.test', zhCN.identifiers.verify)).toBeNull()
    expect(rowAction('pending@x.test', zhCN.identifiers.remove)).toBeTruthy()
  })

  it('renders no verification chip for a username', async () => {
    // A username is a login name with no ownership to prove, so its row has
    // no verified state at all -- not even one read off `proven`.
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

    expect(await screen.findAllByText('ada')).toHaveLength(1)
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
                verified_at: '',
                verified_via: 'self',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findAllByText('ada')).toHaveLength(1)
    // A username is permanent: the group states the rule and carries no row
    // action, whether or not the name is the only login name.
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

    expect(await screen.findAllByText('ada@x.test')).toHaveLength(2)
    // An email address is a login name for a password account too. Removal is
    // withheld and the row has nothing to verify, so it carries no action at
    // all.
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
                verified_at: '',
                verified_via: 'self',
              }),
              identifier({
                id: 'id-phone',
                kind: 'phone',
                value: '+14155550123',
                proven: false,
                verified_at: '',
                verified_via: '',
              }),
            ],
          }),
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(await screen.findAllByText('ada')).toHaveLength(1)
    expect(screen.getByText('+14155550123')).toBeTruthy()
    // The username is permanent, so its group carries no row action; the
    // phone signs in through its own code route and stays removable from its
    // own row.
    expect(
      within(
        screen.getByRole('group', { name: zhCN.identifiers.kind.username }),
      ).queryAllByRole('button'),
    ).toHaveLength(0)
    expect(
      rowAction('+14155550123', zhCN.identifiers.remove),
    ).toBeEnabled()
    // The permanence note belongs to the username group, not to the phone row.
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
                verified_at: '',
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

    expect(await screen.findAllByText('ada')).toHaveLength(1)
    // Another login name does not make the username removable: the name is
    // permanent, so its group keeps stating the rule and carries no row
    // action.
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

    expect(await screen.findAllByText('ada@x.test')).toHaveLength(2)
    // With no password the address is not the withheld "last login name", so
    // its row keeps the removal action.
    expect(rowAction('ada@x.test', zhCN.identifiers.remove)).toBeTruthy()
    expect(screen.queryByText(zhCN.identifiers.lastLoginName)).toBeNull()
  })

  it('does not offer the email kind while verification is pending', async () => {
    // A pending registration carries one contact awaiting proof; the add face
    // must not put a second pending address on the account.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            identifiers: [],
            verification: {
              state: 'pending',
              email: 'ada@x.test',
              since: '',
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
    // The silent removal now confirms itself, naming the identifier removed.
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
    // This surface renders no removal action for a username, so the normal
    // path never asks to delete one. A request issued outside this surface is
    // refused with authn.username_immutable, and any refused removal lands in
    // this section's one failure slot: that slot must render the code's own
    // sentence rather than the generic conflict text.
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
    // The client guard withholds the action only when the profile it read
    // shows this row as the sole username/email. Another session can delete
    // the other login name in between, so the server refuses with its own
    // 409 and the section must render that code's text rather than the
    // generic conflict sentence. The re-read then converges on the server's
    // state: the remaining row is the only login name and loses its action.
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
                      verified_at: '',
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
    // The decisive re-read: the profile is fetched again and the remaining
    // email becomes the only login name, so its removal gives way to the
    // note.
    await waitFor(() => expect(profileReads).toBe(2))
    expect(screen.getByText(zhCN.identifiers.lastLoginName)).toBeTruthy()
    // The row loses its actions: removal gives way to the note and the
    // proved address has nothing to verify.
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

    // The 401 opens the step-up dialog; winning it re-runs the removal.
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
    // The step-up shortfall's 401 is the expected refresh-failure diagnostic.
    expect(warn).toHaveBeenCalledWith(
      'access token refresh failed',
      expect.objectContaining({
        status: 401,
        code: 'authn.insufficient_user_authentication',
        trace_id: 'trace-1',
      }),
    )
    // One shortfall, one refresh failure: a duplicated removal would warn
    // again.
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
    // The confirmation names the changed kind (the sign-in identifier).
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
        // An existing username is the expected 409 of the one write the
        // setup page's add face exists for.
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
    // The name is the value to change: the field carries it, focused, and
    // the generic account-state sentence (refresh) never appears.
    expect(field).toHaveFocus()
    expect(field).toHaveValue('alice')
    expect(screen.queryByRole('alert')).toBeNull()
    expect(enUS.identifiers.add.usernameTaken).toMatch(/already taken/i)
    // The form stays open so another name can be typed.
    expect(field).toBeTruthy()
  })

  it('names a taken username the identifier route answers as a conflict', async () => {
    // The identifier route answers the same collision with its own conflict
    // code; for this face both refusals mean the name is not available.
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
    // A reserved name and a name the server cannot normalize are both the
    // value this face can change, so the field carries the message.
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
      // Editing the name clears the message for the next attempt.
      await user.type(field, '2')
      expect(screen.queryByText(text)).not.toBeInTheDocument()
      unmount()
    }
  })

  it('leaves a bad-request username add to the generic banner', async () => {
    // authn.bad_request is not the username's shape answer (that is
    // authn.username_invalid), so the field stays clean and the banner says
    // what the route said.
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

    // The server's username shape begins with a letter or a digit
    // (^[a-z0-9][a-z0-9._-]{2,63}$); a leading dot, dash or underscore is
    // refused with the generic bad-request text. The helper is the setup
    // page's only guidance for the username it exists to add, so it has to
    // state that rule, in both languages.
    expect(
      await screen.findByText(zhCN.identifiers.add.value.helperUsername),
    ).toBeTruthy()
    // The wording claim is pinned on the en-US leaf; the zh-CN leaf is
    // pinned by the render assertion above.
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

    // The pending answer moves the form to the code step.
    await screen.findByLabelText(zhCN.identifiers.add.code.label)
    // The send announces the destination through its own status region and
    // hands focus to the code field.
    const sentNotice = zhCN.identifiers.add.sent.replace(
      '{{value}}',
      'ada@x.test',
    )
    expect(screen.getByText(sentNotice)).toBeTruthy()
    expect(screen.getByText(sentNotice).closest('[role="status"]')).not.toBeNull()
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
    // The result sits below the whole control group (the add face), not
    // between the list and the add face.
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
    // Two @ signs: the shape the server would answer with a bare 400.
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

    // Cancelling clears the field error with the rest of the attempt.
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

    // The address's own failure: the field describes it, so the value the
    // viewer can change carries the message.
    expect(
      await screen.findByText(zhCN.identifiers.add.emailTaken),
    ).toBeTruthy()
    expect(
      screen.getByLabelText(zhCN.identifiers.add.value.labelEmail),
    ).toHaveAttribute('aria-invalid', 'true')
    // The code's generic account-state text and the shared banner stay out
    // of it: the section's one failure slot renders no role="alert".
    expect(screen.queryByText(zhCN.errors.authn.email_taken)).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('returns a taken email from the verification step to the value field', async () => {
    // The write is the authoritative refusal: the address can be taken
    // between the code request and the code's submission, so the face
    // returns to the value -- the thing to change -- with the field's
    // message rather than asking for a code nothing can accept.
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

    // The banner belonged to the attempt, not to the section.
    expect(screen.queryByRole('alert')).toBeNull()
    expect(
      screen.queryByText(zhCN.errors.authn.unavailable),
    ).toBeNull()
    // The form is closed and an empty one opens on the next attempt.
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
    // Registering a contact address is not a change of what proves the
    // account; the route asks for no fresh authentication. A step-up refusal
    // that reached the add face renders its code text instead of opening the
    // dialog, and the refused attempt is not replayed.
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
    // The registration journey: an account opened by SMS sign-up has no
    // factor and no username. Past the five-minute window its identifier
    // write is refused for a fresh first factor -- which enrolling a
    // factor cannot satisfy either, so the dialog re-proves the SMS first
    // factor itself and the refused add runs again.
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

    // The refusal opens the dialog on the SMS method's first step.
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
    // The refused add ran once more, with the same input.
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
    // The add face's kind selector starts on the email option.
    const combo = await screen.findByRole('combobox')
    expect(combo).toHaveTextContent(zhCN.identifiers.add.kind.email)
    expect(combo).not.toHaveTextContent(zhCN.identifiers.add.kind.username)
  })

  it('offers usernames only when the deployment serves no email identifier', async () => {
    // The host reads the feature document and tells the section whether
    // authn.identifiers.email is served; a deployment without it must not
    // offer a write that would be refused.
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
    // An email-only product passes offerUsername={false}: the add face
    // keeps the email kind and never offers a username.
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
    // An account holds at most one username, and the server answers a
    // second one with the same 409 as a name someone else took. The add
    // face never sends that write: once a username is listed, the kind
    // leaves the selector.
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

    // The listed username still reads in its group's sentence.
    expect(await screen.findAllByText('ada')).toHaveLength(1)
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
    // With the username kind gone and no email channel there is nothing
    // left to add, so the section does not offer the opening at all.
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

    expect(await screen.findAllByText('ada')).toHaveLength(1)
    expect(
      screen.queryByRole('button', { name: zhCN.identifiers.add.open }),
    ).toBeNull()
  })

  it('renders one failure banner while the add face is open', async () => {
    // Removal, add and verification share one slot: a failure with a
    // non-empty list and the open form is still exactly one alert.
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

    const alerts = await screen.findAllByRole('alert')
    expect(alerts).toHaveLength(1)
    expect(alerts[0]).toHaveTextContent(zhCN.errors.authn.unavailable)
  })

  it('raises the identifier actions to the 44px floor under a coarse pointer', async () => {
    // The row's trailing actions button and the add-open control are compact
    // actions; on a finger-driven device each one gates up to the 44x44 floor.
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
    // The one-step username add rotates the requesting session's
    // credential. The profile refetch that follows the write is answered
    // 401, standing for a read that left under the outgoing credential. The
    // surface opens an identity-transition window around that write, so the
    // anonymous answer is discarded: the snapshot stays authenticated and no
    // sign-out edge is observed. Without the window the same script ends the
    // session.
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
    // Registering and proving a contact address rotates no credential and
    // revokes no other session, so the surface opens no window and the
    // refetch that follows the verify stays authenticated.
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
    // A row's verification proves the address exactly as the add-face
    // verification does: no fresh authentication, no rotated credential and
    // no revoked sibling session, so no window opens.
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
              since: '',
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
    // A refused one-step username add must not leave its window open: the
    // next genuine anonymous read has to end the session. The add is refused
    // 409 (the name is taken), so no silent leg runs; once the failure is on
    // screen the window is closed, and a fresh 401 read turns the snapshot
    // anonymous with one sign-out edge.
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

    // A fresh 401 read now settles the true state.
    gone = true
    await rig.session.restore()
    expect(rig.session.getSnapshot().state).toBe('anonymous')
    expect(rig.endedEdges()).toBe(1)
  })

  it('refuses an email add until the required verifier holds a token, then sends it', async () => {
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
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      const submit = screen.getByRole('button', {
        name: zhCN.identifiers.add.submitEmail,
      })
      expect(submit).toBeDisabled()
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(submit).toBeEnabled())
      await user.click(submit)

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
      expect(posts).toHaveLength(1)
      expect(posts[0]?.body).toEqual({
        kind: 'email',
        value: 'new@x.test',
        human_proof: 'token-1',
      })
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

    expect(await screen.findAllByText('ada@x.test')).toHaveLength(2)

    await act(async () => {
      await queryClient.invalidateQueries()
    })

    // The failed refresh keeps the settled rows on screen under the stale
    // notice rather than replacing them with the error placeholder.
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
                verified_at: '',
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
                verified_at: '',
                verified_via: '',
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
    // A username and the address the profile marks sign_in are sign-in ways.
    // The username reads in its group's sentence; the address reads as the
    // email group's chosen value.
    expect(within(signIn).getAllByText('ada')).toHaveLength(1)
    expect(within(signIn).getAllByText('signin@x.test')).toHaveLength(1)
    // Every registered address is a contact, whether or not it signs in and
    // whatever the profile's own contact flag says.
    expect(within(contacts).getByText('signin@x.test')).toBeTruthy()
    expect(within(contacts).getByText('contact@x.test')).toBeTruthy()
    expect(within(contacts).getByText('+14155550123')).toBeTruthy()
    // A username is not an address and never joins the contacts list.
    expect(within(contacts).queryByText('ada')).toBeNull()
    // There is no third list to hold an address that is not a sign-in way.
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
                verified_at: '',
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
    // Registering an email address or phone number is what makes it a
    // contact: the profile's own contact flag cannot keep it out of the list,
    // and the section offers no control that would change the position.
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
                verified_at: '',
                verified_via: '',
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
    // The contacts list carries no sign-in control: whether an address signs
    // in is the email group's decision, made in the sign-in group. There is
    // also no control that would take an address out of the contact position.
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
    // The email group's one action states that no address signs in yet.
    expect(
      within(signInRegion).getByText(zhCN.identifiers.usage.emailNone),
    ).toBeTruthy()
    await user.click(
      within(signInRegion).getByRole('button', {
        name: zhCN.identifiers.action.enable,
      }),
    )

    await waitFor(() => expect(writes).toEqual([{ sign_in: true }]))
    // The address joins the sign-in group as the picker's chosen value; the
    // group renders no address row of its own.
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
    // The default contact is always a contact, so the row joins that list
    // while its sign-in axis stays as it was.
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

    // The phone is a sign-in way and a contact, so it reads in the phone
    // group's sentence and as a contact row; its sign-in choice is the phone
    // group's one action, so the contact row offers no control for it.
    const signInRegion = await screen.findByRole('region', {
      name: zhCN.identifiers.groups.signIn,
    })
    await user.click(
      within(signInRegion).getByRole('button', {
        name: zhCN.identifiers.action.disable,
      }),
    )

    await waitFor(() => expect(writes).toBe(1))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.last_login_method)
  })

  it('arms the step-up dialog on a refused add-face verify and replays the same code', async () => {
    // Proving an address carries no fresh authentication of its own, but a
    // proof that makes the address the default contact or the account's only
    // address changes how the account signs in and may answer a step-up
    // refusal. The verify arms the dialog with the same code and replays it.
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
    // The row's verify proves the address the row shows; the consequence of
    // the proof can answer a step-up refusal, which arms the dialog with the
    // same code and replays it once a challenge is won.
    captureRefreshWarning()
    let elevated = false
    let proven = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileBody({
            verification: { state: 'verified', email: 'ada@x.test', since: '' },
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

    // The refetched rows flip to proved; both contact rows are proved now.
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
    // A host that shows the contact axis alone gets the address table with
    // its row actions; the contact position itself is not a control, and the
    // sign-in switch belongs to the sign-in axis.
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
    // The table row carries its actions inline.
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

    // The refusal names how long to wait instead of the bare "try again
    // later": the 429 carried Retry-After and the surface says so.
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(
      zhCN.throttle.retryAfter.replace('{{seconds}}', '90'),
    )
  })

  it('states that email sign-in is off when no address is chosen, with no select', async () => {
    // No address is a sign-in way, so the group shows its off note instead of
    // a picker that would display the first proved address as if the account
    // had chosen it.
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
      screen.getByRole('button', { name: zhCN.identifiers.action.enable }),
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

    // The empty username group reads its own copy; the phone group keeps its
    // own, so exactly one phone-none sentence remains.
    expect(
      await screen.findByText(zhCN.identifiers.usage.usernameNone),
    ).toBeTruthy()
    expect(screen.getAllByText(zhCN.identifiers.usage.phoneNone)).toHaveLength(1)
  })

  it('states that a proved phone is not a sign-in way when its group offers to enable it', async () => {
    // The group's button offers to enable this number, so the sentence beside
    // it must say the number does not sign in rather than claim it does.
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
      within(phoneGroup).getByRole('button', {
        name: zhCN.identifiers.action.enable,
      }),
    ).toBeTruthy()
  })

  it('replays a whole sign-in email selection as one gate action', async () => {
    // Choosing a different sign-in email turns the chosen address on and the
    // previous one off. The first write meets the step-up refusal; the gate
    // remembers the whole selection, so one challenge lands both rows rather
    // than only the row that was refused.
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

    // The first write meets the refusal and arms the dialog with the whole
    // selection; one challenge lands both rows.
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
    // B was attempted before and after the challenge; A was turned off only
    // once, as part of the same selection.
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
    // A username is always a login name and the server refuses a sign-in flag
    // change on it, so its group states the name and offers no switch; the
    // email group keeps the section's one group action.
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
                verified_at: '',
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
    // The heading's own row is the whole username group: no action shares it.
    const usernameRow = usernameHeading.parentElement
    expect(usernameRow).not.toBeNull()
    expect(within(usernameRow as HTMLElement).queryByRole('button')).toBeNull()

    expect(
      screen.getByRole('button', { name: zhCN.identifiers.action.disable }),
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
      screen.getByRole('button', { name: zhCN.identifiers.action.disable }),
    )

    await waitFor(() => expect(writes).toEqual([{ sign_in: false }]))
    // The picker gives way to the off note; there is nothing left to choose.
    await waitFor(() =>
      expect(
        screen.queryByRole('combobox', {
          name: zhCN.identifiers.kind.email,
        }),
      ).toBeNull(),
    )
    expect(screen.getByText(zhCN.identifiers.usage.emailNone)).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.identifiers.action.enable }),
    ).toBeTruthy()
  })

  it('turns email sign-in back on with the address the same visit switched off', async () => {
    // The default contact is b, but the viewer switched a off last, so the
    // group restores the viewer's own last choice instead of the fallback.
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
      screen.getByRole('button', { name: zhCN.identifiers.action.disable }),
    )
    await waitFor(() =>
      expect(screen.queryByRole('combobox')).toBeNull(),
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.action.enable }),
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
    // The visit switched a off, and a is no longer on the account when the
    // group is turned back on: the default contact b is restored, not the
    // first proved address c.
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
        // The account lost the address between the write and the re-read.
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
      screen.getByRole('button', { name: zhCN.identifiers.action.disable }),
    )
    await waitFor(() =>
      expect(screen.getByText(zhCN.identifiers.usage.emailNone)).toBeTruthy(),
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.identifiers.action.enable }),
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
    // The row's "Default contact" chip already states the position, so the
    // row offers no set-default action at all; the removal stays, and it is
    // the row's only action.
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
                verified_at: '',
                verified_via: '',
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
    // The contact table's actions render inline in their intended order:
    // the default-contact choice first, the danger-toned removal last.
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
                    verified_at: '',
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
    // The merged view's contact row renders both actions inline, verification
    // before removal, inside one action row.
    const actions = rowActions('ada@example.test')
    expect(actions.map((button) => button.textContent?.trim())).toEqual([
      zhCN.identifiers.verify,
      zhCN.identifiers.remove,
    ])
    expect(
      actions.every((button) => button.closest('[data-action-row]') !== null),
    ).toBe(true)
    expect(screen.queryByRole('menuitem')).toBeNull()
    expect(rowAction('ada@example.test', zhCN.identifiers.verify)).toBeEnabled()
    expect(rowAction('ada@example.test', zhCN.identifiers.remove)).toBeEnabled()

    // The verify action opens the row's fixed-address flow, and the remove
    // action still asks for confirmation before its write.
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
                    verified_at: '',
                    verified_via: '',
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
    // A phone offers removal alone: one action, rendered as its own small
    // button rather than behind an overflow trigger or in an action row.
    const actions = rowActions('+14155550123')
    expect(actions.map((button) => button.textContent?.trim())).toEqual([
      zhCN.identifiers.remove,
    ])
    const remove = rowAction('+14155550123', zhCN.identifiers.remove)
    expect(remove).not.toBeNull()
    expect(remove).toHaveClass('MuiButton-colorError')
    expect((remove as HTMLElement).closest('[data-action-row]')).toBeNull()
    // Neither an overflow trigger nor a menu item remains.
    expect(
      screen.queryByRole('button', {
        name: zhCN.identifiers.actionsAria.replace(
          '{{value}}',
          '+14155550123',
        ),
      }),
    ).toBeNull()
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
})

