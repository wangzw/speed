import { StrictMode } from 'react'
import { flushSync } from 'react-dom'
import { describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SocialCallbackHandler } from './SocialCallbackHandler.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  LOGIN_ANSWER,
  LOGIN_CURRENT,
  LOGIN_METHODS,
  PROFILE_GET,
  apiError,
  authenticatedCompletion,
  completeFlow,
  makeHarness,
  methodList,
  openFlow,
  principal,
} from '@speed/test-utils/session-harness'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

function waitSentence(seconds: number): string {
  const [before = '', after = ''] = zhCN.throttle.retryAfter.split('{{seconds}}')
  return `${before}${seconds}${after}`
}

function visibleSentence(alert: HTMLElement): HTMLElement | null {
  return alert.querySelector<HTMLElement>('span[aria-hidden="true"]')
}

function stableSentence(alert: HTMLElement): HTMLElement | null {
  return alert.querySelector<HTMLElement>('span:not([aria-hidden])')
}

function signupFlow() {
  return openFlow({
    seq: 1,
    state: 'signup',
    next: {
      kind: 'challenge',
      fields: [{ name: 'username', kind: 'text', required: true }],
    },
  })
}

describe('SocialCallbackHandler', () => {
  it('read the flow back once, take its result, then fire onSignedIn once', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />,
    )
    expect(await screen.findByText(zhCN.socialCallback.pending)).toBeInTheDocument()
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(harness.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
      'GET /api/v1/auth/flows/current',
      'POST /api/v1/auth/flows/answer',
      'GET /api/v1/auth/me',
      'GET /api/v1/features',
    ])
    expect(harness.calls[1]?.options?.body).toMatchObject({ step: 'complete' })
    expect(harness.store.get()).toBe('access-1')
  })

  it('start exactly one read under StrictMode double effect invocation', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <StrictMode>
        <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />
      </StrictMode>,
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows/current'),
    ).toHaveLength(1)
    expect(harness.store.get()).toBe('access-1')
  })

  it('render a no-flow read in one alert with a retry button', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    renderWithProviders(<SocialCallbackHandler session={harness.session} />)
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.flow_invalid,
      ),
    )
    expect(harness.store.get()).toBeNull()
    expect(
      screen.getByRole('button', { name: zhCN.socialCallback.retry }),
    ).toBeInTheDocument()
  })

  it('render the restart notice when a no-flow read finds a host exit', async () => {
    const onSignIn = vi.fn()
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignIn={onSignIn} />,
    )
    expect(
      await screen.findByText(zhCN.signInFlow.flowExpired),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.socialCallback.retry }),
    ).not.toBeInTheDocument()

    const before = harness.calls.length
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.signInFlow.signInAgain }))
    expect(onSignIn).toHaveBeenCalledTimes(1)
    expect(harness.calls.slice(before)).toHaveLength(0)
  })

  it('retry the read and succeed on the second attempt', async () => {
    let attempts = 0
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        attempts += 1
        if (attempts === 1) {
          throw apiError(503, 'authn.unavailable')
        }
        return completeFlow(1)
      },
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />,
    )
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.unavailable,
      ),
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: zhCN.socialCallback.retry }),
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows/current'),
    ).toHaveLength(2)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(harness.store.get()).toBe('access-1')
  })

  it('mount the pending live region empty on a retry re-entry, then fill it (mount-with-text regression)', async () => {
    let attempts = 0
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        attempts += 1
        if (attempts === 1) {
          throw apiError(503, 'authn.unavailable')
        }
        return completeFlow(1)
      },
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />,
    )
    await waitFor(() =>
      expect(screen.getByRole('alert')).toBeInTheDocument(),
    )
    for (const region of screen.queryAllByRole('status')) {
      expect(region.textContent).toBe('')
    }
    act(() => {
      flushSync(() => {
        screen
          .getByRole('button', { name: zhCN.socialCallback.retry })
          .click()
      })
      const region = screen.getByRole('status')
      expect(region.textContent).toBe('')
    })
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        zhCN.socialCallback.pending,
      ),
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
  })

  it('render a step the flow parked on and advance it to completion', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', secret: true }],
          },
        }),
      [LOGIN_ANSWER]: (call) => {
        const step = (call.options?.body as { step?: string }).step
        return step === 'complete'
          ? authenticatedCompletion()
          : completeFlow(2)
      },
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />,
    )
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await userEvent.setup().type(code, '123456')
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(harness.store.get()).toBe('access-1')
  })

  it('render a step this route cannot render with an action that reads the flow again', async () => {
    let reads = 0
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        reads += 1
        return openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'redirect',
            url: 'https://idp.example/authorize',
            method: 'oidc',
          },
        })
      },
    })
    renderWithProviders(<SocialCallbackHandler session={harness.session} />)

    expect(
      await screen.findByText(zhCN.signInFlow.unsupportedStep),
    ).toBeInTheDocument()
    await userEvent.setup().click(
      screen.getByRole('button', { name: zhCN.socialCallback.retry }),
    )

    await waitFor(() => expect(reads).toBe(2))
  })

  it('render the restart notice for a step this route cannot render when the host gives an exit', async () => {
    let reads = 0
    const onSignIn = vi.fn()
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        reads += 1
        return openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'redirect',
            url: 'https://idp.example/authorize',
            method: 'oidc',
          },
        })
      },
    })
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignIn={onSignIn} />,
    )

    expect(
      await screen.findByText(zhCN.signInFlow.flowExpired),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.signInFlow.unsupportedStep),
    ).not.toBeInTheDocument()
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.signInFlow.signInAgain }))
    expect(onSignIn).toHaveBeenCalledTimes(1)
    expect(reads).toBe(1)
  })

  it('not re-read when a remount finds the session already signed in', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    const first = renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />,
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows/current'),
    ).toHaveLength(1)
    expect(harness.store.get()).toBe('access-1')

    first.unmount()
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />,
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(2))
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows/current'),
    ).toHaveLength(1)
    expect(harness.store.get()).toBe('access-1')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(await screen.findByText(zhCN.socialCallback.pending)).toBeInTheDocument()
  })

  it('render the en-US pending notice on an English-starting instance', async () => {
    let resolveRead: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_CURRENT]: () =>
        new Promise((resolve) => {
          resolveRead = resolve
        }),
    })
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} />,
      { language: 'en-US' },
    )
    expect(await screen.findByText(enUS.socialCallback.pending)).toBeInTheDocument()
    resolveRead(completeFlow(1))
  })

  it('pass axe with no violations while pending', async () => {
    let resolveRead: (value: unknown) => void = () => {}
    const harness = makeHarness({
      [LOGIN_CURRENT]: () =>
        new Promise((resolve) => {
          resolveRead = resolve
        }),
    })
    renderWithProviders(
      <div>
        <h1>Completing sign in</h1>
        <SocialCallbackHandler session={harness.session} />
      </div>,
    )
    await expectNoAxeViolations()
    resolveRead(completeFlow(1))
  })

  it('contain a throwing onSignedIn after a successful read: the success outcome never flips to the failed state', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn(() => {
      throw new Error('host navigation failed')
    })
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />,
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(harness.store.get()).toBe('access-1')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.socialCallback.retry }),
    ).not.toBeInTheDocument()
    expect(await screen.findByText(zhCN.socialCallback.pending)).toBeInTheDocument()
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows/current'),
    ).toHaveLength(1)
  })

  it('render the host absent view when no flow is bound and the deployment offers no callback method', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
      [LOGIN_METHODS]: () => methodList(['password']),
    })
    renderWithProviders(
      <SocialCallbackHandler
        session={harness.session}
        absent={<p>No such page</p>}
      />,
    )

    expect(await screen.findByText('No such page')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(
      harness.calls.some((call) => call.path === '/api/v1/auth/methods'),
    ).toBe(true)
  })

  it('keep the flow-not-found banner when the deployment does offer a callback method', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
      [LOGIN_METHODS]: () =>
        methodList([{ name: 'google', kind: 'redirect' }]),
    })
    renderWithProviders(
      <SocialCallbackHandler
        session={harness.session}
        absent={<p>No such page</p>}
      />,
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.flow_invalid,
    )
    expect(
      screen.getByRole('button', { name: zhCN.socialCallback.retry }),
    ).toBeInTheDocument()
    expect(screen.queryByText('No such page')).not.toBeInTheDocument()
  })

  it('keep the flow-not-found banner when the discovery read fails', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
      [LOGIN_METHODS]: () => {
        throw apiError(503, 'authn.unavailable')
      },
    })
    renderWithProviders(
      <SocialCallbackHandler
        session={harness.session}
        absent={<p>No such page</p>}
      />,
    )

    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.queryByText('No such page')).not.toBeInTheDocument()
  })

  it('render the host absent view on an authenticated re-entry when no method returns here', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => authenticatedCompletion(),
      [PROFILE_GET]: () => principal(),
      [LOGIN_METHODS]: () => methodList(['password']),
    })
    const onSignedIn = vi.fn()
    const first = renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />,
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    first.unmount()

    renderWithProviders(
      <SocialCallbackHandler
        session={harness.session}
        onSignedIn={onSignedIn}
        absent={<p>No such page</p>}
      />,
    )
    expect(await screen.findByText('No such page')).toBeInTheDocument()
    expect(onSignedIn).toHaveBeenCalledTimes(1)
  })

  it('read no discovery document when the host passes no absent view', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    renderWithProviders(<SocialCallbackHandler session={harness.session} />)

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.flow_invalid,
      ),
    )
    expect(
      harness.calls.some((call) => call.path === '/api/v1/auth/methods'),
    ).toBe(false)
  })

  it('keep the parked step and render a refused answer beside it, retryable in place', async () => {
    let answers = 0
    const harness = makeHarness({
      [LOGIN_CURRENT]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', secret: true }],
          },
        }),
      [LOGIN_ANSWER]: (call) => {
        const step = (call.options?.body as { step?: string }).step
        if (step === 'complete') {
          return authenticatedCompletion()
        }
        answers += 1
        if (answers === 1) {
          throw apiError(429, 'authn.throttled')
        }
        return completeFlow(2)
      },
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />,
    )
    const user = userEvent.setup()
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(code, '123456')
    await user.click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.throttled,
    )
    expect(screen.getByLabelText(zhCN.smsSignIn.codeLabel)).toHaveValue(
      '123456',
    )
    expect(
      screen.queryByRole('button', { name: zhCN.socialCallback.retry }),
    ).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(harness.store.get()).toBe('access-1')
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows/current'),
    ).toHaveLength(1)
  })

  it('count a throttled read failure down in its banner without changing the announced text', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        throw apiError(429, 'authn.throttled', 2000)
      },
    })
    vi.useFakeTimers()
    try {
      await act(async () => {
        renderWithProviders(<SocialCallbackHandler session={harness.session} />)
        for (let turn = 0; turn < 20; turn += 1) {
          await Promise.resolve()
        }
      })
      const alert = screen.getByRole('alert')
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(2))
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(2))

      act(() => {
        vi.advanceTimersByTime(1000)
      })
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(1))
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(2))

      act(() => {
        vi.advanceTimersByTime(1000)
      })
      expect(visibleSentence(alert)).toHaveTextContent(
        zhCN.errors.authn.throttled,
      )
      expect(visibleSentence(alert)).not.toHaveTextContent('0')
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(2))
      expect(screen.getByRole('alert')).toBe(alert)
      expect(
        screen.getByRole('button', { name: zhCN.socialCallback.retry }),
      ).toBeInTheDocument()
    } finally {
      await restoreRealTimers()
    }
  })

  it('count a throttled answer down in the banner beside the parked step', async () => {
    let answers = 0
    const harness = makeHarness({
      [LOGIN_CURRENT]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', secret: true }],
          },
        }),
      [LOGIN_ANSWER]: (call) => {
        const step = (call.options?.body as { step?: string }).step
        if (step === 'complete') {
          return authenticatedCompletion()
        }
        answers += 1
        if (answers === 1) {
          throw apiError(429, 'authn.throttled', 3000)
        }
        return completeFlow(2)
      },
      [PROFILE_GET]: () => principal(),
    })
    renderWithProviders(<SocialCallbackHandler session={harness.session} />)
    const user = userEvent.setup()
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(code, '123456')

    vi.useFakeTimers()
    try {
      await act(async () => {
        fireEvent.click(
          screen.getByRole('button', { name: zhCN.signInFlow.submit }),
        )
        for (let turn = 0; turn < 10; turn += 1) {
          await Promise.resolve()
        }
      })
      const alert = screen.getByRole('alert')
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(3))
      expect(screen.getByLabelText(zhCN.smsSignIn.codeLabel)).toHaveValue(
        '123456',
      )

      act(() => {
        vi.advanceTimersByTime(1000)
      })
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(2))
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(3))

      act(() => {
        vi.advanceTimersByTime(2000)
      })
      expect(visibleSentence(alert)).toHaveTextContent(
        zhCN.errors.authn.throttled,
      )
      expect(screen.getByRole('alert')).toBe(alert)
    } finally {
      await restoreRealTimers()
    }
  })

  it.each([
    ['authn.username_taken', 409, zhCN.signInFlow.usernameTaken],
    ['authn.username_reserved', 400, zhCN.signInFlow.usernameReserved],
    ['authn.username_invalid', 400, zhCN.signInFlow.usernameInvalid],
  ])(
    'renders %s under the username field and keeps the step and its value',
    async (code, status, text) => {
      const harness = makeHarness({
        [LOGIN_CURRENT]: () => signupFlow(),
        [LOGIN_ANSWER]: () => {
          throw apiError(status, code)
        },
      })
      renderWithProviders(<SocialCallbackHandler session={harness.session} />)
      const user = userEvent.setup()
      const username = await screen.findByLabelText(zhCN.signInFlow.usernameLabel)
      await user.type(username, 'ada')
      await user.click(
        screen.getByRole('button', { name: zhCN.signInFlow.submit }),
      )

      await waitFor(() =>
        expect(username).toHaveAccessibleDescription(text),
      )
      expect(username).toHaveAttribute('aria-invalid', 'true')
      expect(username).toHaveFocus()
      expect(username).toHaveValue('ada')
      expect(screen.queryByRole('alert')).toBeNull()
      expect(
        screen.getByRole('heading', {
          level: 2,
          name: zhCN.signInFlow.signupTitle,
        }),
      ).toBeInTheDocument()
    },
  )

  it('keeps the whole-attempt banner for a failure that is not a name refusal', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => signupFlow(),
      [LOGIN_ANSWER]: () => {
        throw apiError(400, 'authn.bad_request')
      },
    })
    renderWithProviders(<SocialCallbackHandler session={harness.session} />)
    const user = userEvent.setup()
    const username = await screen.findByLabelText(zhCN.signInFlow.usernameLabel)
    await user.type(username, 'ada')
    await user.click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.bad_request,
    )
    expect(username).not.toHaveAttribute('aria-invalid', 'true')
    expect(username).toHaveAccessibleDescription(
      zhCN.signInFlow.usernameHelper,
    )
  })

  it('replaces a username step whose flow is gone with the restart notice', async () => {
    const onSignIn = vi.fn()
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => signupFlow(),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignIn={onSignIn} />,
    )
    const user = userEvent.setup()
    const username = await screen.findByLabelText(zhCN.signInFlow.usernameLabel)
    await user.type(username, 'ada')
    await user.click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))

    expect(
      await screen.findByText(zhCN.signInFlow.flowExpired),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(zhCN.signInFlow.usernameLabel),
    ).toBeNull()

    const before = harness.calls.length
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.signInAgain }),
    )
    expect(onSignIn).toHaveBeenCalledTimes(1)
    const after = harness.calls.slice(before)
    expect(
      after.filter((call) => call.path === '/api/v1/auth/flows/answer'),
    ).toHaveLength(0)
    expect(
      after.filter((call) => call.path === '/api/v1/auth/flows/current'),
    ).toHaveLength(0)
  })

  it('fall back to the re-reading retry when a step answer is refused as flow-invalid', async () => {
    let reads = 0
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        reads += 1
        return reads === 1
          ? openFlow({
              seq: 1,
              state: 'first',
              next: {
                kind: 'challenge',
                fields: [{ name: 'code', kind: 'code', secret: true }],
              },
            })
          : completeFlow(1)
      },
      [LOGIN_ANSWER]: (call) => {
        const step = (call.options?.body as { step?: string }).step
        if (step === 'complete') {
          return authenticatedCompletion()
        }
        throw apiError(401, 'authn.flow_invalid')
      },
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />,
    )
    const user = userEvent.setup()
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(code, '123456')
    await user.click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.flow_invalid,
    )
    expect(
      screen.queryByLabelText(zhCN.smsSignIn.codeLabel),
    ).not.toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.socialCallback.retry }),
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows/current'),
    ).toHaveLength(2)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(harness.store.get()).toBe('access-1')
  })

  it('render the restart notice when a refused answer finds the flow gone', async () => {
    const onSignIn = vi.fn()
    const harness = makeHarness({
      [LOGIN_CURRENT]: () =>
        openFlow({
          seq: 1,
          state: 'first',
          next: {
            kind: 'challenge',
            fields: [{ name: 'code', kind: 'code', secret: true }],
          },
        }),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignIn={onSignIn} />,
    )
    const user = userEvent.setup()
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(code, '123456')
    await user.click(screen.getByRole('button', { name: zhCN.signInFlow.submit }))

    expect(
      await screen.findByText(zhCN.signInFlow.flowExpired),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(zhCN.smsSignIn.codeLabel),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.socialCallback.retry }),
    ).not.toBeInTheDocument()

    const before = harness.calls.length
    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.signInAgain }),
    )
    expect(onSignIn).toHaveBeenCalledTimes(1)
    expect(harness.calls.slice(before)).toHaveLength(0)
  })

  it('fall back to the re-reading retry when a ready flow complete answer is refused', async () => {
    let answers = 0
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => {
        answers += 1
        if (answers === 1) {
          throw apiError(503, 'authn.unavailable')
        }
        return authenticatedCompletion()
      },
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    const onSignIn = vi.fn()
    renderWithProviders(
      <SocialCallbackHandler
        session={harness.session}
        onSignedIn={onSignedIn}
        onSignIn={onSignIn}
      />,
    )
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.unavailable,
      ),
    )
    expect(
      screen.getByRole('button', { name: zhCN.socialCallback.retry }),
    ).toBeInTheDocument()
    expect(onSignIn).not.toHaveBeenCalled()
    await userEvent.setup().click(
      screen.getByRole('button', { name: zhCN.socialCallback.retry }),
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows/current'),
    ).toHaveLength(3)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(harness.store.get()).toBe('access-1')
  })

  it('render the restart notice when a ready flow complete answer is refused as flow-invalid', async () => {
    const onSignIn = vi.fn()
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignIn={onSignIn} />,
    )
    expect(
      await screen.findByText(zhCN.signInFlow.flowExpired),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.socialCallback.retry }),
    ).not.toBeInTheDocument()

    const before = harness.calls.length
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.signInFlow.signInAgain }))
    expect(onSignIn).toHaveBeenCalledTimes(1)
    expect(harness.calls.slice(before)).toHaveLength(0)
  })

  it('treat a superseded completion as the lost race it is: no failed state, no onSignedIn', async () => {
    let releaseAnswer!: (value: unknown) => void
    const answerGate = new Promise((resolve) => {
      releaseAnswer = resolve
    })
    let answers = 0
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => completeFlow(1),
      [LOGIN_ANSWER]: () => {
        answers += 1
        return answers === 1
          ? answerGate
          : authenticatedCompletion({ access_token: 'access-race' })
      },
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />,
    )
    await waitFor(() =>
      expect(
        harness.calls.filter((call) => call.path === '/api/v1/auth/flows/answer'),
      ).toHaveLength(1),
    )
    await act(async () => {
      await harness.session.answerFlow({ seq: 1, step: 'complete', input: {} })
    })
    expect(harness.store.get()).toBe('access-race')
    await act(async () => {
      releaseAnswer(authenticatedCompletion({ access_token: 'access-social' }))
    })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.socialCallback.retry }),
    ).not.toBeInTheDocument()
    expect(onSignedIn).not.toHaveBeenCalled()
    expect(harness.store.get()).toBe('access-race')
  })
})

describe('SocialCallbackHandler social account-opening face', () => {
  function accountFaceFlow() {
    return openFlow({
      seq: 1,
      state: 'signup',
      next: {
        kind: 'challenge',
        fields: [
          { name: 'username', kind: 'text', required: true },
          { name: 'password', kind: 'password', secret: true, required: true },
        ],
        provided_method: 'google',
        provided_display_name: 'Ada Lovelace',
        provided_email: 'ada@example.com',
      },
    })
  }

  it('renders the face inside the host frame and sends both fields', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => accountFaceFlow(),
      [LOGIN_ANSWER]: (call) =>
        (call.options?.body as { step?: string }).step === 'complete'
          ? authenticatedCompletion()
          : completeFlow(2),
      [PROFILE_GET]: () => principal(),
    })
    const onSignedIn = vi.fn()
    renderWithProviders(
      <SocialCallbackHandler
        session={harness.session}
        onSignedIn={onSignedIn}
        frame={(content) => (
          <section aria-label="host card">
            <h1>Sign in</h1>
            {content}
          </section>
        )}
      />,
    )

    const face = await screen.findByRole('region', { name: 'host card' })
    expect(within(face).queryByRole('heading', { level: 2 })).toBeNull()
    expect(
      await within(face).findByText(zhCN.signInFlow.signupAccountInstruction),
    ).toBeInTheDocument()
    expect(within(face).getByText('Ada Lovelace')).toBeInTheDocument()
    expect(
      within(face).getByRole('textbox', { name: zhCN.register.emailLabel }),
    ).toHaveTextContent('ada@example.com')
    await expectNoAxeViolations()

    const user = userEvent.setup()
    await user.type(
      within(face).getByLabelText(zhCN.signInFlow.usernameLabel),
      'ada',
    )
    await user.type(
      within(face).getByLabelText(zhCN.passwordSignIn.passwordLabel),
      'correct-horse',
    )
    await user.type(
      within(face).getByLabelText(zhCN.register.confirmPasswordLabel),
      'correct-horse',
    )
    await user.click(
      within(face).getByRole('button', {
        name: zhCN.signInFlow.signupAccountSubmit,
      }),
    )

    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    const answer = harness.calls.find(
      (call) => call.path === '/api/v1/auth/flows/answer',
    )
    expect(answer?.options?.body).toMatchObject({
      step: 'answer',
      input: { username: 'ada', password: 'correct-horse' },
    })
  })

  it('renders a password-policy refusal under the password field, not the banner', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => accountFaceFlow(),
      [LOGIN_ANSWER]: () => {
        throw apiError(400, 'authn.bad_request')
      },
    })
    renderWithProviders(<SocialCallbackHandler session={harness.session} />)

    const user = userEvent.setup()
    await user.type(
      await screen.findByLabelText(zhCN.signInFlow.usernameLabel),
      'ada',
    )
    await user.type(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      'short',
    )
    await user.type(
      screen.getByLabelText(zhCN.register.confirmPasswordLabel),
      'short',
    )
    await user.click(
      screen.getByRole('button', {
        name: zhCN.signInFlow.signupAccountSubmit,
      }),
    )

    await waitFor(() =>
      expect(
        screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
      ).toHaveAccessibleDescription(zhCN.signInFlow.signupPasswordPolicy),
    )
    expect(
      screen.getByLabelText(zhCN.passwordSignIn.passwordLabel),
    ).toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('returns the absent view unwrapped by the frame', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
      [LOGIN_METHODS]: () =>
        methodList([], { signup: false }),
    })
    renderWithProviders(
      <SocialCallbackHandler
        session={harness.session}
        absent={<p>no such page</p>}
        frame={(content) => <section aria-label="host card">{content}</section>}
      />,
    )

    const absent = await screen.findByText('no such page')
    expect(absent.closest('section[aria-label="host card"]')).toBeNull()
  })
})

describe('SocialCallbackHandler error-page focus', () => {
  function codeChallenge() {
    return openFlow({
      seq: 1,
      state: 'first',
      next: {
        kind: 'challenge',
        fields: [{ name: 'code', kind: 'code', secret: true }],
      },
    })
  }

  async function answerRefusedStep(): Promise<HTMLElement> {
    const user = userEvent.setup()
    const code = await screen.findByLabelText(zhCN.smsSignIn.codeLabel)
    await user.type(code, '123456')
    const submit = screen.getByRole('button', { name: zhCN.signInFlow.submit })
    await user.click(submit)
    return submit
  }

  function heldRefusal() {
    let refuse!: (error: unknown) => void
    const gate = new Promise<never>((_resolve, reject) => {
      refuse = reject
    })
    return { gate, refuse }
  }

  it('land on the expired notice heading when a refused answer replaces the step', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => codeChallenge(),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    const onSignIn = vi.fn()
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignIn={onSignIn} />,
    )

    await answerRefusedStep()

    await screen.findByText(zhCN.signInFlow.flowExpired)
    await waitFor(() => expect(document.body).not.toHaveFocus())
    const heading = screen.getByRole('heading', {
      name: zhCN.socialCallback.incompleteTitle,
    })
    expect(heading).toHaveAttribute('tabindex', '-1')
    expect(heading).toHaveFocus()
  })

  it('leave the focus the viewer moved when the step gives way to the expired notice', async () => {
    const { gate, refuse } = heldRefusal()
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => codeChallenge(),
      [LOGIN_ANSWER]: () => gate,
    })
    const onSignIn = vi.fn()
    renderWithProviders(
      <>
        <SocialCallbackHandler session={harness.session} onSignIn={onSignIn} />
        <button type="button">Elsewhere</button>
      </>,
    )

    const submit = await answerRefusedStep()
    await waitFor(() => expect(submit).toHaveAttribute('aria-busy', 'true'))

    const user = userEvent.setup()
    const elsewhere = screen.getByRole('button', { name: 'Elsewhere' })
    await user.click(elsewhere)
    expect(elsewhere).toHaveFocus()

    await act(async () => {
      refuse(apiError(401, 'authn.flow_invalid'))
      await Promise.resolve()
    })

    const heading = await screen.findByRole('heading', {
      name: zhCN.socialCallback.incompleteTitle,
    })
    expect(elsewhere).toHaveFocus()
    expect(heading).not.toHaveFocus()
  })

  it('land on the failure page heading when a refused answer replaces the step', async () => {
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => codeChallenge(),
      [LOGIN_ANSWER]: () => {
        throw apiError(401, 'authn.flow_invalid')
      },
    })
    renderWithProviders(<SocialCallbackHandler session={harness.session} />)

    await answerRefusedStep()

    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.flow_invalid,
      ),
    )
    await waitFor(() => expect(document.body).not.toHaveFocus())
    const heading = screen.getByRole('heading', {
      name: zhCN.socialCallback.incompleteTitle,
    })
    expect(heading).toHaveAttribute('tabindex', '-1')
    expect(heading).toHaveFocus()
  })

  it('leave the focus the viewer moved when the step gives way to the failure page', async () => {
    const { gate, refuse } = heldRefusal()
    const harness = makeHarness({
      [LOGIN_CURRENT]: () => codeChallenge(),
      [LOGIN_ANSWER]: () => gate,
    })
    renderWithProviders(
      <>
        <SocialCallbackHandler session={harness.session} />
        <button type="button">Elsewhere</button>
      </>,
    )

    const submit = await answerRefusedStep()
    await waitFor(() => expect(submit).toHaveAttribute('aria-busy', 'true'))

    const user = userEvent.setup()
    const elsewhere = screen.getByRole('button', { name: 'Elsewhere' })
    await user.click(elsewhere)
    expect(elsewhere).toHaveFocus()

    await act(async () => {
      refuse(apiError(401, 'authn.flow_invalid'))
      await Promise.resolve()
    })

    const heading = await screen.findByRole('heading', {
      name: zhCN.socialCallback.incompleteTitle,
    })
    expect(elsewhere).toHaveFocus()
    expect(heading).not.toHaveFocus()
  })
})
