/**
 * ContactVerificationSection checks the profile's `verification` states and the identifier writes that move them.
 */

import { describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
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

function resendNote(before: string, after: string) {
  return (content: string): boolean =>
    content.startsWith(before) && content.endsWith(after)
}

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
    expect(screen.getByText(sentText)).toBeTruthy()
  })

  it('holds one result slot for the send and verify writes', async () => {
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
    await screen.findByRole('button', { name: zhCN.verification.resend })
    await user.type(
      screen.getByLabelText(zhCN.verification.codeLabel),
      '123456',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.verification.verify }),
    )
    expect(await screen.findByText(zhCN.errors.authn.bad_request)).toBeTruthy()

    await user.click(
      screen.getByRole('button', { name: zhCN.verification.resend }),
    )
    expect(await screen.findByText(zhCN.errors.authn.unavailable)).toBeTruthy()
    expect(screen.queryByText(zhCN.errors.authn.bad_request)).toBeNull()
  })

  it('renders a refused change in the action row’s one result region', async () => {
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
    expect(screen.getByLabelText(zhCN.verification.changeLabel)).toBeTruthy()
    expect(
      alert.compareDocumentPosition(changeForm) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('drops the code step’s result when the target address changes', async () => {
    captureRefreshWarning({ expected: true })
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
    expect(alert).toHaveTextContent(
      zhCN.throttle.retryAfter.replace('{{seconds}}', '30'),
    )
    const send = screen.getByRole('button', { name: zhCN.verification.send })
    expect(send).toHaveAttribute('aria-disabled', 'true')
    expect(
      document.getElementById('contact-verification-resend-note'),
    ).toBeNull()
    expect(send).toHaveAccessibleDescription(
      zhCN.verification.resendIn.replace('{{seconds}}', '30'),
    )
  })

  it('states the automatic step-up refusal instead of opening the dialog, then retries the send on the visitor click', async () => {
    captureRefreshWarning({ expected: true })
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

    expect(
      await screen.findByText(zhCN.verification.autoStepUp),
    ).toBeTruthy()
    expect(screen.queryByRole('dialog')).toBeNull()
    const manual = screen.getByRole('button', {
      name: zhCN.verification.send,
    })
    expect(manual).toBeEnabled()
    expect(manual).not.toHaveAttribute('aria-disabled', 'true')
    await expectNoAxeViolations()

    await user.click(manual)

    expect(
      await screen.findByText(
        zhCN.verification.sent.replace('{{email}}', 'ada@x.test'),
      ),
    ).toBeTruthy()
    expect(
      rig.calls.filter(
        (call) => call.method === 'POST' && call.path === ADD_PATH,
      ),
    ).toHaveLength(2)
  })

  it('clears the automatic step-up explanation when a verification starts', async () => {
    captureRefreshWarning({ expected: true })
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
    expect(screen.queryByText(zhCN.verification.autoStepUp)).toBeNull()
  })

  it('changes the pending target by sending a code to the new address', async () => {
    let current = 'ada@x.test'
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
    expect(
      screen.getByText(
        zhCN.verification.target.replace('{{email}}', 'new@x.test'),
      ),
    ).toBeTruthy()
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
    const { rig } = mount(respond, {
      humanGate: {
        phone: { required: false, provider: 'off' },
        other: { required: false, provider: 'off' },
      },
    })
    const send = await screen.findByRole('button', {
      name: zhCN.verification.send,
    })
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
      await restoreRealTimers()
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
    expect(alert).toHaveTextContent(
      zhCN.throttle.retryAfter.replace('{{seconds}}', '1'),
    )
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
      expect(fake.widgets).toHaveLength(0)
      expect(send).toBeEnabled()
      expect(send).not.toHaveAttribute('aria-disabled', 'true')
      await user.click(send)

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

      await user.click(
        await screen.findByRole('button', { name: zhCN.verification.send }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(sends()).toHaveLength(1))

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

      await user.click(
        await screen.findByRole('button', { name: zhCN.verification.send }),
      )
      await waitFor(() => expect(fake.widgets).toHaveLength(1))
      act(() => {
        fake.token('token-1')
      })
      await waitFor(() => expect(sends()).toHaveLength(1))

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

      const changeField = await screen.findByLabelText(
        zhCN.verification.changeLabel,
      )
      expect(fake.widgets[1]?.container.isConnected).toBe(false)
      await user.type(changeField, 'new@x.test')
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

    expect(screen.getByText(target)).toBeTruthy()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    expect(screen.queryByText(zhCN.verification.error.title)).toBeNull()

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
    expect(
      screen.queryByLabelText(zhCN.verification.changeLabel),
    ).toBeNull()

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
    expect(elsewhere).toHaveFocus()
  })
})
