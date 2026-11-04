/**
 * SocialCallbackHandler behaviour: the handler reads the flow back once
 * (asserted on method and path), shows the pending notice while the read
 * is in flight, takes a ready flow's result and fires onSignedIn once on
 * success; StrictMode's double effect invocation starts exactly one read.
 * A read that finds no flow renders its code text in one alert under a
 * retry button that reads once more when the host gives no exit, and the
 * terminal restart notice when it does give onSignIn; the same terminal
 * notice covers an answer refused as authn.flow_invalid (a parked step's or
 * a ready flow's complete), a no-flow read, and a step this route cannot
 * render, each leaving the route without another read. A read that fails
 * transiently keeps its code text and retry. A mount that finds the session
 * already authenticated starts no read and fires onSignedIn again -- the
 * re-entry to the callback route after a completed login. The optional
 * absent slot reads the discovery document only when the flow read finds
 * nothing (or on a re-entry): without a redirect or enterprise method the
 * host's 404 renders, and every other outcome (a method, a failed discovery
 * read, no absent slot) keeps the banner and reads nothing. The en-US bundle
 * renders on an English-starting instance and the pending tree passes axe.
 * Text expectations read the bundle values, never inline language.
 */

import { StrictMode } from 'react'
import { flushSync } from 'react-dom'
import { describe, expect, it, vi } from 'vitest'
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

/** The failure banner's sentence naming a whole-second wait. */
function waitSentence(seconds: number): string {
  const [before = '', after = ''] = zhCN.throttle.retryAfter.split('{{seconds}}')
  return `${before}${seconds}${after}`
}

/** The alert's painted (aria-hidden) sentence, which follows the countdown. */
function visibleSentence(alert: HTMLElement): HTMLElement | null {
  return alert.querySelector<HTMLElement>('span[aria-hidden="true"]')
}

/** The alert's accessible (clipped) sentence, which never changes. */
function stableSentence(alert: HTMLElement): HTMLElement | null {
  return alert.querySelector<HTMLElement>('span:not([aria-hidden])')
}

/** The username step a first login parks at while it waits for a name. */
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
    expect(screen.getByText(zhCN.socialCallback.pending)).toBeInTheDocument()
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
    // The flow is definitively gone, so a re-read deterministically finds
    // nothing again. With the host's exit the terminal notice replaces the
    // banner whose retry would spin on the same dead record, and its one
    // action leaves the callback route without reading again.
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
    // The pending notice's role="status" region mounts empty on every
    // entry into the pending state -- the initial mount and a retry's
    // re-entry alike -- because a live region announces content changes
    // that follow its own existence, never text that mounts with it.
    // The text is gated behind a one-commit lag armed by an effect
    // after the pending phase's first commit, so the region's first
    // committed frame is empty whatever transition entered the phase,
    // and the text fills an existing region a commit later. The
    // assertion between the two commits below is what distinguishes
    // the shapes: the region's text content must still be empty the
    // moment the node re-appears on the retry. flushSync lands the
    // retry's one commit synchronously, and the act scope holds back
    // the post-commit effect that fills the region until this assertion
    // has seen the birth frame.
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
    // The failed state announces nothing through a live region: the only
    // status node it carries is the feedback slot's always-mounted,
    // still-empty success region.
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
    // The flow is still parked on a second factor when the handler reads
    // it back: the shared step renderer renders the step and the answer
    // completes the same flow.
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
    // A redirect step belongs to the host's navigation. The callback route
    // cannot complete it, so the notice carries the same re-read the retry
    // performs: the handler never goes back to waiting without reading.
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
    // A redirect step belongs to the host's navigation and this route can
    // never render it. With the host's exit the step becomes the terminal
    // notice instead of a re-read that deterministically parks the same
    // unrenderable step again.
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

    // The viewer returns to the callback URL after the sign-in landed
    // (a back/forward re-entry in the same SPA instance): the mount
    // finds the session authenticated, starts no second read and signals
    // the host onward instead.
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
    expect(screen.getByText(zhCN.socialCallback.pending)).toBeInTheDocument()
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
    expect(screen.getByText(enUS.socialCallback.pending)).toBeInTheDocument()
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
    // Rendered under the page h1 a real host page supplies: the handler
    // renders no heading of its own, and page-has-heading-one is
    // determinate in jsdom now (see the axe helper header) -- a scan
    // without that context fails instead of passing by indeterminacy.
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
    // The flow verdict settles first; the host callback runs after. A
    // host callback that throws is not a failure of the flow: it must not
    // flip the screen to the failed state -- whose retry would read a
    // flow that is already gone -- and must not suppress the success
    // outcome (the session holds the issued token, and the containment
    // keeps the throw from becoming an unhandled rejection).
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
    // Still the success outcome: no failed-state banner, no retry, and
    // the pending notice stays up.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.socialCallback.retry }),
    ).not.toBeInTheDocument()
    expect(screen.getByText(zhCN.socialCallback.pending)).toBeInTheDocument()
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

    // Nothing can return to this route: the discovery document offers no
    // redirect or enterprise method, so the host's 404 answers instead of a
    // flow-not-found banner.
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

    // A method exists that could have returned here, so the empty read is a
    // real flow-not-found: the retry banner stays, and the absent view does
    // not take over.
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

    // An unreadable discovery document cannot settle "this deployment has no
    // callback": the failure banner (with its retry) is the honest answer.
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

    // The re-entry reads the discovery document (the session is already
    // authenticated): with no callback method, the host's 404 answers and the
    // one-shot onSignedIn does not fire for a route nothing can land on.
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
    // Behaviour is unchanged for a host without an absent slot: the handler
    // never reads the discovery document.
    expect(
      harness.calls.some((call) => call.path === '/api/v1/auth/methods'),
    ).toBe(false)
  })

  it('keep the parked step and render a refused answer beside it, retryable in place', async () => {
    // A parked step's answer refused with any code but authn.flow_invalid
    // leaves the flow parked on that step: the failure banner renders
    // beside the step -- whose own controls are the retry -- instead of
    // replacing it with the flow-read retry that belongs to a lost flow.
    // Answering again begins a new attempt, clearing the banner first.
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

    // The step is still there, with the refusal beside it and no
    // flow-read retry button.
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.throttled,
    )
    expect(screen.getByLabelText(zhCN.smsSignIn.codeLabel)).toHaveValue(
      '123456',
    )
    expect(
      screen.queryByRole('button', { name: zhCN.socialCallback.retry }),
    ).not.toBeInTheDocument()

    // The step's own controls are the retry: answering again clears the
    // banner and advances the same flow to completion.
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
      // The banner names the wait the answer stated; the sentence assistive
      // technology reads is fixed at that starting point.
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(2))
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(2))

      // The painted sentence follows the clock; the announced one does not.
      act(() => {
        vi.advanceTimersByTime(1000)
      })
      expect(visibleSentence(alert)).toHaveTextContent(waitSentence(1))
      expect(stableSentence(alert)).toHaveTextContent(waitSentence(2))

      // At expiry the number is gone -- never a stale "0 seconds" -- and
      // the banner itself stays (no timer clears a result).
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
      vi.useRealTimers()
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
      // The parked step keeps its own controls, with the banner beneath it.
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
      vi.useRealTimers()
    }
  })

  it.each([
    ['authn.username_taken', 409, zhCN.signInFlow.usernameTaken],
    ['authn.username_reserved', 400, zhCN.signInFlow.usernameReserved],
    ['authn.username_invalid', 400, zhCN.signInFlow.usernameInvalid],
  ])(
    'renders %s under the username field and keeps the step and its value',
    async (code, status, text) => {
      // A first login through a method that opens an account parks at
      // signup until the name arrives. Each name refusal is the field's:
      // the step stays, the typed name stays, and no whole-attempt banner
      // renders.
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
      // The focus moved off the submit key and onto the value to change.
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
    // authn.bad_request is another field's refusal (or a malformed body),
    // so it never lands on the username field.
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
    // The flow is gone, so the answer cannot be retried on it. The step
    // becomes a terminal notice whose one action hands the viewer back to
    // the host's sign-in surface; it never resends the refused answer.
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
    // authn.flow_invalid means the flow is gone: no step is left to keep,
    // so the banner renders with the retry that reads the flow back -- the
    // same shape as a read that finds no flow.
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

    // No step survives the lost flow: the banner carries the retry.
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
    // authn.flow_invalid means the flow no longer exists: no step survives
    // and re-reading is a deterministic empty turn. With the host's exit the
    // terminal notice replaces the banner whose retry re-reads the dead flow.
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
    // A ready flow is taken with step: complete and never parked as a
    // surface; a refusal of that answer leaves no rendered step to keep, so
    // the banner renders with the retry that reads the flow back. The
    // driver's own re-read classifies the refusal -- the flow is still
    // ready -- before the banner offers the same read again.
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
    // A refusal that is not authn.flow_invalid says the flow may still be
    // ready, so the banner with the re-read stays even though the host did
    // supply an exit.
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
    // The ready flow's completion was refused because the flow is gone:
    // nothing is left to read back, so the terminal notice is the only
    // honest outcome when the host offers an exit.
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
    // A sibling login committing through the same session while this
    // read's completion is in flight makes auth-core reject it with
    // OperationSupersededError when its answer arrives: the session is
    // authenticated under the WINNER's identity, which is not
    // necessarily this flow's. The handler must not flip to the failed
    // state and must not fire onSignedIn, a one-shot side effect reserved
    // for the identity the session actually runs under.
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
    // The completion is in flight (the gate holds its answer).
    await waitFor(() =>
      expect(
        harness.calls.filter((call) => call.path === '/api/v1/auth/flows/answer'),
      ).toHaveLength(1),
    )
    // The winning login commits while the completion is in flight.
    await act(async () => {
      await harness.session.answerFlow({ seq: 1, step: 'complete', input: {} })
    })
    expect(harness.store.get()).toBe('access-race')
    // The completion's answer arrives after the winner committed:
    // auth-core rejects it as superseded. No failure banner, no retry
    // button, no onSignedIn, and the loser's tokens never applied.
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
  /** The face a first social login parks at: username, password, and the
   * provider identity it shared, shown read-only. */
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
    // The face adds no second heading: the card's own h1 says this is a
    // sign-in.
    expect(within(face).queryByRole('heading', { level: 2 })).toBeNull()
    // The card frame renders before the face it wraps: the flow's first
    // read fills the content, so the assertion waits for it instead of
    // reading the loading state the synchronous query would catch.
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

    // The pending read is the handler's own surface and may sit in the host
    // card, but the absent view is the host's page: the card is gone once it
    // renders.
    const absent = await screen.findByText('no such page')
    expect(absent.closest('section[aria-label="host card"]')).toBeNull()
  })
})
