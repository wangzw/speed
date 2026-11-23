/**
 * ContactVerificationSection behaviour: the self-registration verification
 * state read from GET /api/v1/auth/me's `verification` field, and the writes
 * that move it (POST /api/v1/auth/identifiers and /identifiers/verify).
 *
 * What this suite pins: `none` renders nothing; `verified` renders the
 * completion state; `pending` with a target renders the address, the code
 * field and the send/resend control; `pending` with no target renders the
 * add-email form and a landed add moves to the code step; a send moves to the
 * code step, announces the destination once and focuses the code field, and
 * the resend control is disabled for the server's interval with the reason
 * stated and stops its timer when the interval expires; a landed verify clears
 * the entry state and refetches the profile alone; a wrong code and
 * a taken address render the section's own verify-step wording; a failed
 * profile read renders the error state and its retry re-reads; 429 and 503
 * render their code text; a 401 authn.insufficient_user_authentication of the
 * send renders as an ordinary failure rather than opening a step-up dialog,
 * while a refused verify opens the dialog and replays the same code; a send
 * waiting on its proof is dropped when the code already in hand verifies.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { profileBody } from '../test-utils/fixtures.js'
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
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import { ContactVerificationSection } from './ContactVerificationSection.js'
import { SessionsSection } from './SessionsSection.js'

const PROFILE_PATH = '/api/v1/auth/me'
const SESSIONS_PATH = '/api/v1/auth/sessions'
const ADD_PATH = '/api/v1/auth/identifiers'
const VERIFY_PATH = '/api/v1/auth/identifiers/verify'
const FLOW_START = '/api/v1/auth/flows/authenticated'
const FLOW_ANSWER = '/api/v1/auth/flows/answer'

/** A profile whose self-registration is one of the three states. */
function profileVerification(
  state: string,
  email: string,
): Record<string, unknown> {
  return profileBody({
    verification: { state, email, since: null },
  })
}

function mount(
  respond: RealResponder,
  props: {
    readonly autoSend?: boolean
    readonly initialChangeOpen?: boolean
    readonly humanGate?: {
      readonly phone: {
        readonly required: boolean
        readonly provider: string
        readonly site_key?: string
      }
      readonly other: {
        readonly required: boolean
        readonly provider: string
        readonly site_key?: string
      }
    }
  } = {},
) {
  const rig = makeRealClientRig(respond)
  rig.store.set('access-1')
  const rendered = renderWithProviders(
    <ContactVerificationSection session={rig.session} {...props} />,
  )
  return { rig, ...rendered }
}

/** Matches the section's resend note: the wait a successful send's
 * `resend_after` holds the control for. A throttled failure starts no note --
 * its shared banner counts the same wait down. */
function resendNote(before: string, after: string) {
  return (content: string): boolean =>
    content.startsWith(before) && content.endsWith(after)
}

/** One answer held open, so a case can assert the control's in-flight state
 * before the write settles. */
function deferredResponse(): {
  readonly response: Promise<Response>
  readonly release: (response: Response) => void
} {
  let release: (response: Response) => void = () => {}
  const response = new Promise<Response>((resolve) => {
    release = resolve
  })
  return { response, release }
}

describe('ContactVerificationSection', () => {
  it('renders the pending state with its target address and passes axe', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(
      await screen.findByRole('heading', { name: zhCN.verification.title }),
    ).toBeTruthy()
    expect(
      await screen.findByText(
        zhCN.verification.target.replace('{{email}}', 'ada@x.test'),
      ),
    ).toBeTruthy()
    expect(
      await screen.findByLabelText(zhCN.verification.codeLabel),
    ).toBeTruthy()
    expect(
      screen.getByRole('button', { name: zhCN.verification.send }),
    ).toBeTruthy()

    await expectNoAxeViolations()
  })

  it('renders the add-email form when a pending account has no target', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', ''))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(
      await screen.findByRole('heading', {
        name: zhCN.verification.addTitle,
      }),
    ).toBeTruthy()
    expect(
      screen.getByLabelText(zhCN.verification.addLabel),
    ).toBeTruthy()
    await expectNoAxeViolations()
  })

  it('moves a pending account with no target from the add form to the code step', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        // The add answers `pending`; the profile keeps reporting no target
        // (the address is only a pending contact server-side), so the step
        // the section shows comes from the send's own answer.
        return jsonResponse(200, profileVerification('pending', ''))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
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

    await user.type(
      await screen.findByLabelText(zhCN.verification.addLabel),
      'new@x.test',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.addSubmit }),
    )

    expect(
      await screen.findByText(
        zhCN.verification.sent.replace('{{email}}', 'new@x.test'),
      ),
    ).toBeTruthy()
    expect(
      screen.getByLabelText(zhCN.verification.codeLabel),
    ).toBeTruthy()
    expect(
      screen.queryByLabelText(zhCN.verification.addLabel),
    ).toBeNull()
    const posts = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === ADD_PATH,
    )
    expect(posts).toHaveLength(1)
    expect(posts[0]?.body).toEqual({ kind: 'email', value: 'new@x.test' })
  })

  it('guards the add form’s own submit event while its send is in flight', async () => {
    const held = deferredResponse()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', ''))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return held.response
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const field = await screen.findByLabelText(zhCN.verification.addLabel)
    await user.type(field, 'new@x.test')
    const form = field.closest('form') as HTMLFormElement
    // The form's own submit event stands in for the implicit submission an
    // Enter press makes; the guard, not a disabled control, is what keeps a
    // second request from leaving while the first is in flight.
    fireEvent.submit(form)
    await waitFor(() =>
      expect(
        rig.calls.filter(
          (call) => call.method === 'POST' && call.path === ADD_PATH,
        ),
      ).toHaveLength(1),
    )
    fireEvent.submit(form)
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === ADD_PATH,
      ),
    ).toHaveLength(1)

    await act(async () => {
      held.release(
        jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        }),
      )
    })
  })

  it('renders the completion state for a verified account', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('verified', 'ada@x.test'))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)

    expect(
      await screen.findByRole('heading', {
        name: zhCN.verification.verifiedTitle,
      }),
    ).toBeTruthy()
    expect(
      screen.getByText(
        zhCN.verification.verifiedDescription.replace(
          '{{email}}',
          'ada@x.test',
        ),
      ),
    ).toBeTruthy()
  })

  it('renders the completion state after a code verifies, clearing the entry state and refetching the profile alone', async () => {
    let verified = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileVerification(verified ? 'verified' : 'pending', 'ada@x.test'),
        )
      }
      if (call.method === 'GET' && call.path === SESSIONS_PATH) {
        return jsonResponse(200, { sessions: [] })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        verified = true
        return jsonResponse(200, { state: 'verified' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    // The session section observes the session list; proving the address
    // revokes no other session, so that query is not invalidated and the
    // observer's mount read stays the only one.
    renderWithProviders(
      <>
        <ContactVerificationSection session={rig.session} />
        <SessionsSection />
      </>,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )

    expect(
      await screen.findByRole('heading', {
        name: zhCN.verification.verifiedTitle,
      }),
    ).toBeTruthy()
    expect(
      screen.getByText(
        zhCN.verification.verifiedDescription.replace(
          '{{email}}',
          'ada@x.test',
        ),
      ),
    ).toBeTruthy()
    // The entry state is gone with the state it belonged to.
    expect(
      screen.queryByLabelText(zhCN.verification.codeLabel),
    ).toBeNull()
    expect(
      screen.queryByText(
        zhCN.verification.sent.replace('{{email}}', 'ada@x.test'),
      ),
    ).toBeNull()
    await waitFor(() =>
      expect(
        rig.calls.filter(
          (call) => call.method === 'GET' && call.path === PROFILE_PATH,
        ),
      ).toHaveLength(2),
    )
    expect(
      rig.calls.filter(
        (call) => call.method === 'GET' && call.path === SESSIONS_PATH,
      ),
    ).toHaveLength(1)
  })

  it('renders nothing for an account that never self-registered', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody())
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)

    await waitFor(() =>
      expect(rig.calls.some((call) => call.path === PROFILE_PATH)).toBe(true),
    )
    await waitFor(() =>
      expect(screen.queryByText(zhCN.verification.title)).toBeNull(),
    )
  })

  it('renders the read failure with a retry that re-reads the profile', async () => {
    let fail = true
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        // A 500 rather than a transient status: the client's own retry
        // budget covers 502/503/504, and a transient answer would make the
        // call count an observation about that budget rather than the retry.
        return fail
          ? errorResponse(500, 'authn.unavailable')
          : jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    expect(
      await screen.findByRole('heading', {
        name: zhCN.verification.error.title,
      }),
    ).toBeTruthy()
    expect(
      screen.getByText(zhCN.verification.error.description),
    ).toBeTruthy()

    fail = false
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.retry }),
    )

    expect(
      await screen.findByText(
        zhCN.verification.target.replace('{{email}}', 'ada@x.test'),
      ),
    ).toBeTruthy()
    expect(
      rig.calls.filter(
        (call) => call.method === 'GET' && call.path === PROFILE_PATH,
      ),
    ).toHaveLength(2)
  })

  it('sends a code, announces the destination and focuses the code field', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
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
      await screen.findByRole('button', { name: zhCN.verification.send }),
    )

    expect(
      await screen.findByText(
        zhCN.verification.sent.replace('{{email}}', 'ada@x.test'),
      ),
    ).toBeTruthy()
    // The confirmation names the address the code went to and lives in the
    // code step's own status region -- the destination notice, not the send
    // scope's outcome slot.
    expect(zhCN.verification.sent).toContain('{{email}}')
    expect(
      screen
        .getByText(zhCN.verification.sent.replace('{{email}}', 'ada@x.test'))
        .closest('[role="status"]'),
    ).not.toBeNull()
    const sent = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === ADD_PATH,
    )
    expect(sent).toHaveLength(1)
    expect(sent[0]?.body).toEqual({ kind: 'email', value: 'ada@x.test' })
    await waitFor(() =>
      expect(screen.getByLabelText(zhCN.verification.codeLabel)).toHaveFocus(),
    )
  })

  it('keeps the sent notice when the verify scope fails', async () => {
    captureRefreshWarning({ expected: true })
    // The destination notice is the code step's own state, not the send
    // scope's slot: the other control group's failure must not clear it.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 0,
        })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return errorResponse(401, 'authn.login_failed')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.verification.send }),
    )
    const sentText = zhCN.verification.sent.replace('{{email}}', 'ada@x.test')
    expect(await screen.findByText(sentText)).toBeTruthy()

    await user.type(
      screen.getByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.verification.codeInvalid)
    // The sent notice survives the verify group's failure.
    expect(screen.getByText(sentText)).toBeTruthy()
  })

  it('holds one result slot for the send and verify writes', async () => {
    // One code step, one scope: a refused resend and a refused verification
    // report through the same slot, so they never stand side by side -- the
    // attempt that settles last replaces the one before it.
    let firstSend = true
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        if (firstSend) {
          firstSend = false
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        return errorResponse(503, 'authn.unavailable')
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return errorResponse(400, 'authn.bad_request')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.verification.send }),
    )
    // The send waits on the deployment's gate read before it leaves; wait for
    // the code step it lands on (the resend control) before answering it.
    await screen.findByRole('button', { name: zhCN.verification.resend })
    await user.type(
      screen.getByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )
    expect(await screen.findByText(zhCN.errors.authn.bad_request)).toBeTruthy()

    // The resend settles the same scope to a failure.
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.resend }),
    )
    expect(await screen.findByText(zhCN.errors.authn.unavailable)).toBeTruthy()
    // The later result is the slot's one result: the earlier failure does
    // not stand beside it.
    expect(screen.queryByText(zhCN.errors.authn.bad_request)).toBeNull()
  })

  it('renders a refused change in the action row’s one result region', async () => {
    // The change form writes into the code step's shared scope; the group's
    // one result sits directly under the action row, ahead of the change
    // entry it fired from rather than at the card's bottom.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return errorResponse(400, 'authn.bad_request')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.verification.change }),
    )
    await user.type(
      await screen.findByLabelText(zhCN.verification.changeLabel),
      'new@x.test',
    )
    const changeForm = screen
      .getByLabelText(zhCN.verification.changeLabel)
      .closest('form') as HTMLFormElement
    const changeSubmit = within(changeForm).getByRole('button', {
      name: zhCN.verification.changeSubmit,
    })
    await user.click(changeSubmit)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.bad_request)
    // The form stays open on failure, and the result sits before it in the
    // document: under the action row, not at the card's bottom.
    expect(screen.getByLabelText(zhCN.verification.changeLabel)).toBeTruthy()
    expect(
      alert.compareDocumentPosition(changeForm) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('drops the code step’s result when the target address changes', async () => {
    captureRefreshWarning({ expected: true })
    // Replacing the target is a change of context: the wrong-code answer
    // describes the address being replaced and must not stand beside the
    // new one.
    let target = 'ada@x.test'
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', target))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        const body = call.body as { readonly value?: string } | undefined
        target = body?.value ?? target
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 0,
        })
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return errorResponse(401, 'authn.login_failed')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.verification.send }),
    )
    // The gate is read only once the send is asked for; the resend control
    // appearing and becoming usable is the send settling, so the
    // verification starts from the code step's own state rather than under
    // the send's pending scope.
    const resend = await screen.findByRole('button', {
      name: zhCN.verification.resend,
    })
    await waitFor(() => {
      expect(resend).toBeEnabled()
      expect(resend).not.toHaveAttribute('aria-disabled', 'true')
    })
    await user.type(
      screen.getByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )
    expect(
      await screen.findByText(zhCN.verification.codeInvalid),
    ).toBeTruthy()

    // Move the target to another address.
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.change }),
    )
    await user.type(
      await screen.findByLabelText(zhCN.verification.changeLabel),
      'new@x.test',
    )
    const changeForm = screen
      .getByLabelText(zhCN.verification.changeLabel)
      .closest('form') as HTMLFormElement
    await user.click(
      within(changeForm).getByRole('button', {
        name: zhCN.verification.changeSubmit,
      }),
    )

    // The replaced address's wrong-code answer is gone.
    await waitFor(() =>
      expect(screen.queryByText(zhCN.verification.codeInvalid)).toBeNull(),
    )
  })

  it('auto-sends one code on mount and never again on the refetch it triggers', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond, { autoSend: true })

    expect(
      await screen.findByText(
        zhCN.verification.sent.replace('{{email}}', 'ada@x.test'),
      ),
    ).toBeTruthy()
    // The landed write refetches the profile; the re-read must not send a
    // second code.
    await waitFor(() =>
      expect(
        rig.calls.filter(
          (call) => call.method === 'GET' && call.path === PROFILE_PATH,
        ),
      ).toHaveLength(2),
    )
    const posts = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === ADD_PATH,
    )
    expect(posts).toHaveLength(1)
    expect(posts[0]?.body).toEqual({ kind: 'email', value: 'ada@x.test' })
  })

  it('does not auto-send without the autoSend flag', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)

    await screen.findByText(
      zhCN.verification.target.replace('{{email}}', 'ada@x.test'),
    )
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === ADD_PATH,
      ),
    ).toHaveLength(0)
  })

  it('does not auto-send while the change-address form is open', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond, { autoSend: true, initialChangeOpen: true })

    await screen.findByLabelText(zhCN.verification.changeLabel)
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === ADD_PATH,
      ),
    ).toHaveLength(0)
  })

  it('never auto-sends for a mount landed with the change form open, even after closing it', async () => {
    // `?change=email` lands here: the visitor came to replace the address,
    // so closing the form must not fall back to sending a code to the old
    // one.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond, { autoSend: true, initialChangeOpen: true })
    const user = userEvent.setup()

    expect(
      await screen.findByLabelText(zhCN.verification.changeLabel),
    ).toBeTruthy()
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.changeCancel }),
    )
    await waitFor(() =>
      expect(
        screen.queryByLabelText(zhCN.verification.changeLabel),
      ).toBeNull(),
    )
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === ADD_PATH,
      ),
    ).toHaveLength(0)
  })

  it('does not auto-send for a verified account', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('verified', 'ada@x.test'))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond, { autoSend: true })

    await screen.findByText(
      zhCN.verification.verifiedDescription.replace(
        '{{email}}',
        'ada@x.test',
      ),
    )
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === ADD_PATH,
      ),
    ).toHaveLength(0)
  })

  it('does not auto-send for an account that never self-registered', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileBody())
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond, { autoSend: true })

    await waitFor(() =>
      expect(rig.calls.some((call) => call.path === PROFILE_PATH)).toBe(true),
    )
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === ADD_PATH,
      ),
    ).toHaveLength(0)
  })

  it('holds the resend control when the automatic send is throttled', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return new Response(
          JSON.stringify({ code: 'authn.throttled', traceId: 'trace-1' }),
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
    mount(respond, { autoSend: true })

    const alert = await screen.findByRole('alert')
    // The 429 stated how long to wait, so the sentence names it instead of
    // the bare "try again later".
    expect(alert).toHaveTextContent(
      zhCN.throttle.retryAfter.replace('{{seconds}}', '30'),
    )
    // The Retry-After a 429 carried starts the same countdown a send answer
    // does. The control stays focusable and reports itself unavailable.
    const send = screen.getByRole('button', { name: zhCN.verification.send })
    expect(send).toHaveAttribute('aria-disabled', 'true')
    // The banner is the one *visible* wait indicator: the section's own
    // note is not rendered beside it. The control names the same wait
    // through its hidden reason, which is not a second on-screen note.
    expect(
      document.getElementById('contact-verification-resend-note'),
    ).toBeNull()
    expect(send).toHaveAccessibleDescription(
      zhCN.verification.resendIn.replace('{{seconds}}', '30'),
    )
  })

  it('states the automatic step-up refusal instead of opening the dialog, then retries the send on the visitor click', async () => {
    captureRefreshWarning({ expected: true })
    // The automatic attempt cannot open a dialog the visitor did not ask
    // for, so it states why it could not send and leaves the manual action.
    // The send is not a gated write: the visitor's click retries it like any
    // other send, and no challenge replays it.
    let attempts = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        attempts += 1
        if (attempts === 1) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond, { autoSend: true })
    const user = userEvent.setup()

    // The automatic attempt's 401 leaves the manual action and its
    // explanation, and opens no dialog.
    expect(
      await screen.findByText(zhCN.verification.autoStepUp),
    ).toBeTruthy()
    expect(screen.queryByRole('dialog')).toBeNull()
    const manual = screen.getByRole('button', {
      name: zhCN.verification.send,
    })
    expect(manual).toBeEnabled()
    expect(manual).not.toHaveAttribute('aria-disabled', 'true')
    // The explanation is an info alert beside the manual action; it must
    // not introduce an accessibility violation of its own.
    await expectNoAxeViolations()

    await user.click(manual)

    expect(
      await screen.findByText(
        zhCN.verification.sent.replace('{{email}}', 'ada@x.test'),
      ),
    ).toBeTruthy()
    // The automatic attempt and the visitor's retry: two sends, no replay.
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === ADD_PATH,
      ),
    ).toHaveLength(2)
  })

  it('clears the automatic step-up explanation when a verification starts', async () => {
    captureRefreshWarning({ expected: true })
    // The explanation describes the automatic send's refusal, not a standing
    // state: a verification is a new attempt in the shared result position,
    // so it must not stand beside the attempt's answer.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return errorResponse(401, 'authn.insufficient_user_authentication')
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return errorResponse(401, 'authn.login_failed')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond, { autoSend: true })
    const user = userEvent.setup()

    expect(
      await screen.findByText(zhCN.verification.autoStepUp),
    ).toBeTruthy()

    await user.type(
      screen.getByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )

    expect(
      await screen.findByText(zhCN.verification.codeInvalid),
    ).toBeTruthy()
    // The failed verification's own wording is the one result: the automatic
    // send's explanation left with the attempt that replaced it.
    expect(screen.queryByText(zhCN.verification.autoStepUp)).toBeNull()
  })

  it('changes the pending target by sending a code to the new address', async () => {
    let current = 'ada@x.test'
    // A stale profile answer after the change: the display and the resend
    // must both follow the address this mount sent to, not the old target a
    // failed or lagging refetch still carries.
    let stale = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileVerification('pending', stale ? 'ada@x.test' : current),
        )
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        current = (call.body as { value: string }).value
        stale = true
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

    await user.click(
      await screen.findByRole('button', { name: zhCN.verification.change }),
    )
    await user.type(
      screen.getByLabelText(zhCN.verification.changeLabel),
      'new@x.test',
    )
    const changeForm = screen
      .getByLabelText(zhCN.verification.changeLabel)
      .closest('form') as HTMLFormElement
    await user.click(
      within(changeForm).getByRole('button', {
        name: zhCN.verification.changeSubmit,
      }),
    )

    expect(
      await screen.findByText(
        zhCN.verification.sent.replace('{{email}}', 'new@x.test'),
      ),
    ).toBeTruthy()
    const posts = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === ADD_PATH,
    )
    expect(posts).toHaveLength(1)
    expect(posts[0]?.body).toEqual({ kind: 'email', value: 'new@x.test' })
    // The target label follows the replacement, from the send answer first
    // and the profile refetch after.
    expect(
      screen.getByText(
        zhCN.verification.target.replace('{{email}}', 'new@x.test'),
      ),
    ).toBeTruthy()
    // The resend follows the address this mount displayed, not the stale
    // profile target: sending to the old address would silently undo the
    // change.
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.resend }),
    )
    await waitFor(() => {
      expect(
        rig.calls.filter(
          (call) => call.method === 'POST' && call.path === ADD_PATH,
        ),
      ).toHaveLength(2)
    })
    const afterResend = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === ADD_PATH,
    )
    expect(afterResend[1]?.body).toEqual({
      kind: 'email',
      value: 'new@x.test',
    })
  })

  it('disables the resend control for the server interval and states the wait in its note', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 60,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.verification.send }),
    )

    const resend = await screen.findByRole('button', {
      name: zhCN.verification.resend,
    })
    expect(resend).toHaveAttribute('aria-disabled', 'true')
    // A successful send leaves no banner, so the note is the wait's only
    // visible explanation, and the control points at it.
    const [before, after] = zhCN.verification.resendIn.split('{{seconds}}')
    const note = await screen.findByText(
      resendNote(before ?? '', after ?? ''),
    )
    expect(resend).toHaveAttribute('aria-describedby', note.id)
  })

  it('stops the resend countdown timer when its interval expires', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return jsonResponse(200, {
          state: 'pending',
          expires_in: 600,
          resend_after: 5,
        })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    // The gate is stated up front: this case is about the countdown, and a
    // gate that wants nothing sends as soon as the viewer asks.
    const { rig } = mount(respond, {
      humanGate: {
        phone: { required: false, provider: 'off' },
        other: { required: false, provider: 'off' },
      },
    })
    const send = await screen.findByRole('button', {
      name: zhCN.verification.send,
    })
    // The mount's reads settled on the real clock; the send and its
    // countdown run on the fake one so the five seconds pass instantly.
    vi.useFakeTimers()
    try {
      const setIntervalSpy = vi.spyOn(globalThis, 'setInterval')
      await act(async () => {
        fireEvent.click(send)
        for (let turn = 0; turn < 10; turn += 1) {
          await Promise.resolve()
        }
      })
      expect(
        screen.getByRole('button', { name: zhCN.verification.resend }),
      ).toHaveAttribute('aria-disabled', 'true')
      // The successful send states its hold in the section's own note.
      const [before, after] = zhCN.verification.resendIn.split('{{seconds}}')
      expect(
        screen.getByText(resendNote(before ?? '', after ?? '')),
      ).toBeTruthy()
      const timerId = setIntervalSpy.mock.results.at(-1)?.value
      expect(timerId).toBeDefined()

      const clearIntervalSpy = vi.spyOn(globalThis, 'clearInterval')
      act(() => {
        vi.advanceTimersByTime(5000)
      })

      // The timer is gone at its own expiry, not left ticking for the rest
      // of the mount, and the note leaves with the hold it stated.
      expect(clearIntervalSpy).toHaveBeenCalledWith(timerId)
      expect(
        screen.queryByText(resendNote(before ?? '', after ?? '')),
      ).toBeNull()
      expect(
        rig.calls.filter(
          (call) => call.method === 'POST' && call.path === ADD_PATH,
        ),
      ).toHaveLength(1)
    } finally {
      vi.useRealTimers()
      vi.restoreAllMocks()
    }
  })

  it('renders a wrong code as the unified invalid-code text and clears the field', async () => {
    captureRefreshWarning({ expected: true })
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return errorResponse(401, 'authn.login_failed')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    const code = await screen.findByLabelText(zhCN.verification.codeLabel)
    await user.type(code, '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.verification.codeInvalid)
    expect(code).toHaveValue('')
    expect(code).toHaveAttribute('aria-invalid', 'true')
    // The verify group's failure renders below the verify controls: the code
    // field and its verify action.
    expect(
      screen
        .getByRole('button', { name: zhCN.verification.verify })
        .compareDocumentPosition(alert) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('renders a taken address only after the code proves control', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return errorResponse(409, 'authn.conflict')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.verification.addressTaken)
    expect(alert).not.toHaveTextContent(enUS.errors.authn.conflict)
  })

  it('renders a throttled send as its code text and holds the resend control', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return new Response(
          JSON.stringify({ code: 'authn.throttled', traceId: 'trace-1' }),
          {
            status: 429,
            headers: {
              'content-type': 'application/json',
              'retry-after': '1',
            },
          },
        )
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.verification.send }),
    )

    const alert = await screen.findByRole('alert')
    // The 429 stated how long to wait, so the sentence names it instead of
    // the bare "try again later".
    expect(alert).toHaveTextContent(
      zhCN.throttle.retryAfter.replace('{{seconds}}', '1'),
    )
    // The Retry-After starts the same countdown the send answer does.
    expect(
      screen.getByRole('button', { name: zhCN.verification.send }),
    ).toHaveAttribute('aria-disabled', 'true')
  })

  it('renders an unavailable send as its code text', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return errorResponse(503, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.verification.send }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.unavailable)
  })

  it('renders a step-up refusal of the send as an ordinary failure, with no dialog', async () => {
    captureRefreshWarning({ expected: true })
    // Registering the address is not a change of what proves the account; the
    // route asks for no fresh authentication. A step-up refusal renders its
    // code text instead of opening the dialog, and the send is not replayed.
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
        return errorResponse(401, 'authn.insufficient_user_authentication')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.click(
      await screen.findByRole('button', { name: zhCN.verification.send }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(
      zhCN.errors.authn.insufficient_user_authentication,
    )
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === ADD_PATH,
      ),
    ).toHaveLength(1)
  })

  it('arms the step-up dialog on a refused verify and replays the same code', async () => {
    // A proof that makes the address the account's only address changes how
    // the account signs in, so the server may refuse it for a fresh
    // authentication. The verify arms the dialog with the same code, and the
    // won challenge replays it exactly once.
    captureRefreshWarning()
    let elevated = false
    let proven = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileVerification(proven ? 'verified' : 'pending', 'ada@x.test'),
        )
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        if (!elevated) {
          return errorResponse(401, 'authn.insufficient_user_authentication')
        }
        proven = true
        return jsonResponse(200, { state: 'verified' })
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

    await user.type(
      await screen.findByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )

    // The 401 opens the dialog; winning it replays the same code.
    await screen.findByRole('button', { name: zhCN.mfa.factor.kind.totp })
    await user.click(screen.getByRole('button', { name: zhCN.mfa.factor.kind.totp }))
    await user.type(await screen.findByLabelText(zhCN.flowStep.codeLabel), '123456')
    await user.click(
      screen.getByRole('button', { name: zhCN.mfa.stepUp.confirmLabel }),
    )

    await screen.findByText(zhCN.verification.verifiedTitle)
    const verifies = rig.calls.filter(
      (call) => call.method === 'POST' && call.path === VERIFY_PATH,
    )
    expect(verifies).toHaveLength(2)
    expect(verifies[0]?.body).toEqual({ code: '123456' })
    expect(verifies[1]?.body).toEqual({ code: '123456' })
  })

  it('proves the address without opening an identity-transition window', async () => {
    // Proving a contact address rotates no credential and revokes no other
    // session, so the surface opens no window; the profile refetch that
    // follows the verify stays authenticated and flips to the verified state.
    let proven = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          proven
            ? profileVerification('verified', 'ada@x.test')
            : profileVerification('pending', 'ada@x.test'),
        )
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        proven = true
        return jsonResponse(200, { state: 'verified' })
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(
      <ContactVerificationSection session={rig.session} />,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )

    expect(
      await screen.findByText(zhCN.verification.verifiedTitle),
    ).toBeTruthy()
    expect(rig.transitionBegins()).toBe(0)
    expect(rig.session.getSnapshot().state).toBe('authenticated')
    expect(rig.endedEdges()).toBe(0)
  })

  it('opens no identity-transition window for a refused verification', async () => {
    captureRefreshWarning({ expected: true })
    // The wrong-code refusal is an ordinary failure: no window is opened, so
    // a later genuine anonymous read still ends the session with one edge.
    let refused = false
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === SESSION_PATH) {
        return refused
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, AUTHENTICATED_SESSION)
      }
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return refused
          ? errorResponse(401, 'authn.login-failed')
          : jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'GET' && call.path === FEATURES_PATH) {
        return jsonResponse(200, EMPTY_FEATURES)
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        refused = true
        return errorResponse(401, 'authn.login_failed')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = await makeRotationRig(respond)
    renderWithProviders(
      <ContactVerificationSection session={rig.session} />,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )

    await screen.findByText(zhCN.verification.codeInvalid)
    expect(rig.transitionBegins()).toBe(0)

    // A fresh 401 read now settles the true state.
    await rig.session.restore()
    expect(rig.session.getSnapshot().state).toBe('anonymous')
    expect(rig.endedEdges()).toBe(1)
  })

  it('asks for the verifier only when the code is requested, then sends it with the proof', async () => {
    const fake = installFakeTurnstile()
    try {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
        }
        if (call.method === 'POST' && call.path === ADD_PATH) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond, {
        humanGate: {
          phone: { required: false, provider: 'off' },
          other: { required: true, provider: 'turnstile', site_key: 'site-1' },
        },
      })
      const user = userEvent.setup()

      const send = await screen.findByRole('button', {
        name: zhCN.verification.send,
      })
      // The step shows no challenge until its send is asked for, and the
      // control stays usable while no proof is outstanding.
      expect(fake.widgets).toHaveLength(0)
      expect(send).toBeEnabled()
      expect(send).not.toHaveAttribute('aria-disabled', 'true')
      await user.click(send)

      // The send waits on the proof: the block renders and the control that
      // asked reports that its own attempt is in flight.
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      expect(fake.widgets[0]?.siteKey).toBe('site-1')
      const waiting = screen.getByRole('button', {
        name: zhCN.verification.send,
      })
      expect(waiting).toHaveAttribute('aria-disabled', 'true')
      expect(waiting).toHaveAttribute('aria-busy', 'true')
      expect(
        rig.calls.filter(
          (call) => call.method === 'POST' && call.path === ADD_PATH,
        ),
      ).toHaveLength(0)

      // The proof arrives and the send leaves by itself.
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() =>
        expect(
          rig.calls.filter(
            (call) => call.method === 'POST' && call.path === ADD_PATH,
          ),
        ).toHaveLength(1),
      )
      const posts = rig.calls.filter(
        (call) => call.method === 'POST' && call.path === ADD_PATH,
      )
      expect(posts[0]?.body).toEqual({
        kind: 'email',
        value: 'ada@x.test',
        human_proof: 'token-1',
      })

      // The proof is spent with the send: a resend asks for a fresh one and
      // does not leave until the new widget yields it.
      await user.click(
        await screen.findByRole('button', { name: zhCN.verification.resend }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(2))
      expect(
        rig.calls.filter(
          (call) => call.method === 'POST' && call.path === ADD_PATH,
        ),
      ).toHaveLength(1)
      act(() => {
        fake.token('token-2')
      })
      await waitFor(() =>
        expect(
          rig.calls.filter(
            (call) => call.method === 'POST' && call.path === ADD_PATH,
          ),
        ).toHaveLength(2),
      )
      expect(
        rig.calls.filter(
          (call) => call.method === 'POST' && call.path === ADD_PATH,
        )[1]?.body,
      ).toEqual({
        kind: 'email',
        value: 'ada@x.test',
        human_proof: 'token-2',
      })
    } finally {
      fake.restore()
    }
  })

  it('drops a resend waiting on its proof when the code in hand verifies', async () => {
    const fake = installFakeTurnstile()
    try {
      let verified = false
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(
            200,
            profileVerification(
              verified ? 'verified' : 'pending',
              'ada@x.test',
            ),
          )
        }
        if (call.method === 'POST' && call.path === ADD_PATH) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        if (call.method === 'POST' && call.path === VERIFY_PATH) {
          verified = true
          return jsonResponse(200, { state: 'verified' })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond, {
        humanGate: {
          phone: { required: false, provider: 'off' },
          other: { required: true, provider: 'turnstile', site_key: 'site-1' },
        },
      })
      const user = userEvent.setup()
      const sends = () =>
        rig.calls.filter(
          (call) => call.method === 'POST' && call.path === ADD_PATH,
        )
      const verifies = () =>
        rig.calls.filter(
          (call) => call.method === 'POST' && call.path === VERIFY_PATH,
        )

      // The first send carries its proof and lands.
      await user.click(
        await screen.findByRole('button', { name: zhCN.verification.send }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(sends()).toHaveLength(1))

      // A resend waits on a fresh proof, but that wait is only the control
      // that asked for it: the code already in hand may still be verified.
      const codeField = screen.getByLabelText(zhCN.verification.codeLabel)
      await user.type(codeField, '123456')
      const resend = await screen.findByRole('button', {
        name: zhCN.verification.resend,
      })
      await waitFor(() => {
        expect(resend).toBeEnabled()
        expect(resend).not.toHaveAttribute('aria-disabled', 'true')
      })
      await user.click(resend)
      await waitFor(() => expect(fake.widgets).toHaveLength(2))
      const verifyControl = screen.getByRole('button', {
        name: zhCN.verification.verify,
      })
      expect(verifyControl).toBeEnabled()
      expect(verifyControl).not.toHaveAttribute('aria-disabled', 'true')
      expect(verifyControl).not.toHaveAttribute('aria-busy')

      // The verification can still begin while the wait is outstanding: a
      // step-up replay of a code the visitor already tried reaches verify
      // directly through the form's own submit event. It calls the wait off,
      // so the late proof never sends and the code just proved is not
      // invalidated by a second one.
      fireEvent.submit(codeField.closest('form') as HTMLFormElement)
      await waitFor(() => expect(verifies()).toHaveLength(1))
      act(() => {
        fake.token('token-2')
      })
      expect(
        await screen.findByRole('heading', {
          name: zhCN.verification.verifiedTitle,
        }),
      ).toBeTruthy()
      expect(sends()).toHaveLength(1)
    } finally {
      fake.restore()
    }
  })

  it('changes the address while a send waits on its proof', async () => {
    const fake = installFakeTurnstile()
    try {
      const respond: RealResponder = (call: RealCall) => {
        if (call.method === 'GET' && call.path === PROFILE_PATH) {
          return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
        }
        if (call.method === 'POST' && call.path === ADD_PATH) {
          return jsonResponse(200, {
            state: 'pending',
            expires_in: 600,
            resend_after: 0,
          })
        }
        throw new Error(`unexpected ${call.method} ${call.path}`)
      }
      const { rig } = mount(respond, {
        humanGate: {
          phone: { required: false, provider: 'off' },
          other: { required: true, provider: 'turnstile', site_key: 'site-1' },
        },
      })
      const user = userEvent.setup()
      const sends = () =>
        rig.calls.filter(
          (call) => call.method === 'POST' && call.path === ADD_PATH,
        )

      // The first send carries its proof and lands.
      await user.click(
        await screen.findByRole('button', { name: zhCN.verification.send }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(sends()).toHaveLength(1))

      // A resend waits on a fresh proof. Only the resend control waits: the
      // change-address action stays usable and calls the wait off.
      await user.click(
        await screen.findByRole('button', { name: zhCN.verification.resend }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(2))
      const change = screen.getByRole('button', {
        name: zhCN.verification.change,
      })
      expect(change).toBeEnabled()
      expect(change).not.toHaveAttribute('aria-disabled', 'true')
      await user.click(change)

      // Choosing another address calls the abandoned wait off: the block
      // that held the proof leaves the document.
      const changeField = await screen.findByLabelText(
        zhCN.verification.changeLabel,
      )
      expect(fake.widgets[1]?.container.isConnected).toBe(false)
      await user.type(changeField, 'new@x.test')
      // The change form's own submit event is not held by the abandoned
      // wait: the new address's send leaves the block once its proof
      // arrives.
      fireEvent.submit(changeField.closest('form') as HTMLFormElement)
      await waitFor(() => expect(fake.widgets).toHaveLength(3))
      expect(sends()).toHaveLength(1)
      act(() => {
        fake.token('token-3')
      })
      await waitFor(() => expect(sends()).toHaveLength(2))
      expect(sends()[1]?.body).toEqual({
        kind: 'email',
        value: 'new@x.test',
        human_proof: 'token-3',
      })
    } finally {
      fake.restore()
    }
  })

  it('keeps the pending target under a stale notice when a refresh fails', async () => {
    const user = userEvent.setup()
    let reads = 0
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        reads += 1
        // The first read settles the pending target; every later refresh
        // fails.
        return reads === 1
          ? jsonResponse(200, profileVerification('pending', 'ada@x.test'))
          : errorResponse(500, 'authn.unavailable')
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { queryClient } = mount(respond)
    const target = zhCN.verification.target.replace('{{email}}', 'ada@x.test')

    expect(await screen.findByText(target)).toBeTruthy()

    await act(async () => {
      await queryClient.invalidateQueries()
    })

    // The failed refresh keeps the settled target and the code step on
    // screen under the stale notice, not the error placeholder.
    expect(screen.getByText(target)).toBeTruthy()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(screen.queryByText(zhCN.verification.error.title)).toBeNull()

    // The stale notice's retry re-reads the profile.
    const readsBeforeRetry = reads
    await user.click(screen.getByRole('button', { name: zhCN.verification.retry }))
    await waitFor(() => expect(reads).toBeGreaterThan(readsBeforeRetry))
  })

  it('lands focus on the verified section when the verify step leaves the page', async () => {
    let verified = false
    const held = deferredResponse()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileVerification(verified ? 'verified' : 'pending', 'ada@x.test'),
        )
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return held.response
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )
    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) => call.method === 'POST' && call.path === VERIFY_PATH,
        ),
      ).toBe(true),
    )

    // The landed verification replaces the code step: the control that
    // started it leaves the document, so focus moves to the section that
    // stays, never to the document body.
    verified = true
    await act(async () => {
      held.release(jsonResponse(200, { state: 'verified' }))
    })
    const landing = await screen.findByRole('group', {
      name: zhCN.verification.verifiedTitle,
    })
    await waitFor(() => expect(landing).toHaveFocus())
  })

  it('lands on the verified section when focus was left on a detached node', async () => {
    let verified = false
    const held = deferredResponse()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileVerification(verified ? 'verified' : 'pending', 'ada@x.test'),
        )
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return held.response
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )
    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) => call.method === 'POST' && call.path === VERIFY_PATH,
        ),
      ).toBe(true),
    )

    // The browser can leave the active element on a node the landing
    // detached instead of dropping it to the body. The guard must treat
    // that dead node as focus the removal took, not as a place the
    // operator chose.
    const detached = document.createElement('button')
    Object.defineProperty(document, 'activeElement', {
      configurable: true,
      get: () => detached,
    })
    try {
      verified = true
      await act(async () => {
        held.release(jsonResponse(200, { state: 'verified' }))
      })
    } finally {
      delete (document as { activeElement?: Element }).activeElement
    }
    const landing = await screen.findByRole('group', {
      name: zhCN.verification.verifiedTitle,
    })
    await waitFor(() => expect(landing).toHaveFocus())
  })

  it('holds the sibling controls unavailable without a reason while the verify write is in flight', async () => {
    const held = deferredResponse()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return held.response
      }
      if (call.method === 'POST' && call.path === ADD_PATH) {
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

    await user.type(
      await screen.findByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )
    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) => call.method === 'POST' && call.path === VERIFY_PATH,
        ),
      ).toBe(true),
    )

    // The scope is busy with the verification, so every other initiating
    // control in the step is held without a reason: no aria-busy, no
    // description, and activation reaches no request.
    const send = screen.getByRole('button', { name: zhCN.verification.send })
    const change = screen.getByRole('button', {
      name: zhCN.verification.change,
    })
    for (const control of [send, change]) {
      expect(control).toHaveAttribute('aria-disabled', 'true')
      expect(control).not.toHaveAttribute('aria-busy')
      expect(control).not.toHaveAccessibleDescription()
    }

    await user.click(send)
    await user.click(change)
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === ADD_PATH,
      ),
    ).toHaveLength(0)
    // The change form did not open from a held control.
    expect(
      screen.queryByLabelText(zhCN.verification.changeLabel),
    ).toBeNull()

    // Keyboard activation is swallowed the same way.
    change.focus()
    await user.keyboard('{Enter}')
    expect(
      screen.queryByLabelText(zhCN.verification.changeLabel),
    ).toBeNull()

    await act(async () => {
      held.release(jsonResponse(200, { state: 'pending' }))
    })
  })

  it('keeps the verify action reachable with the empty-code reason on it', async () => {
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(200, profileVerification('pending', 'ada@x.test'))
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const { rig } = mount(respond)
    const user = userEvent.setup()

    const verify = await screen.findByRole('button', {
      name: zhCN.verification.verify,
    })
    // The precondition is unmet, but the control stays focusable and says
    // why it cannot run.
    expect(verify).toHaveAttribute('aria-disabled', 'true')
    expect(verify).toHaveAccessibleDescription(zhCN.verification.codeEmpty)
    verify.focus()
    expect(verify).toHaveFocus()

    await user.click(verify)
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === VERIFY_PATH,
      ),
    ).toHaveLength(0)
  })

  it('leaves focus where the operator moved it while the verify write lands', async () => {
    let verified = false
    const held = deferredResponse()
    const respond: RealResponder = (call: RealCall) => {
      if (call.method === 'GET' && call.path === PROFILE_PATH) {
        return jsonResponse(
          200,
          profileVerification(verified ? 'verified' : 'pending', 'ada@x.test'),
        )
      }
      if (call.method === 'POST' && call.path === VERIFY_PATH) {
        return held.response
      }
      throw new Error(`unexpected ${call.method} ${call.path}`)
    }
    const rig = makeRealClientRig(respond)
    rig.store.set('access-1')
    renderWithProviders(
      <>
        <ContactVerificationSection session={rig.session} />
        <button type="button">elsewhere</button>
      </>,
    )
    const user = userEvent.setup()

    await user.type(
      await screen.findByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )
    await waitFor(() =>
      expect(
        rig.calls.some(
          (call) => call.method === 'POST' && call.path === VERIFY_PATH,
        ),
      ).toBe(true),
    )

    const elsewhere = screen.getByRole('button', { name: 'elsewhere' })
    elsewhere.focus()

    verified = true
    await act(async () => {
      held.release(jsonResponse(200, { state: 'verified' }))
    })
    await screen.findByRole('group', {
      name: zhCN.verification.verifiedTitle,
    })
    // A resting place the operator chose during the write is left alone.
    expect(elsewhere).toHaveFocus()
  })
})
