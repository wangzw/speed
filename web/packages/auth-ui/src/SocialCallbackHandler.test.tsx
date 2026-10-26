/**
 * SocialCallbackHandler behaviour: the handler reads the flow back once
 * (asserted on method and path), shows the pending notice while the read
 * is in flight, takes a ready flow's result and fires onSignedIn once on
 * success; StrictMode's double effect invocation starts exactly one read.
 * A read that finds no flow renders its code text in one alert under a
 * retry button that reads once more, and a read that fails transiently
 * renders its code text the same way. A mount that finds the session
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
import { act, screen, waitFor } from '@testing-library/react'
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
    // The failed state carries no live region: nothing to announce.
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
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

  it('fall back to the re-reading retry when a ready flow complete answer is refused', async () => {
    // A ready flow is taken with step: complete and never parked; a
    // refusal of that answer leaves no step to keep, so the banner renders
    // with the retry that reads the flow back.
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
    renderWithProviders(
      <SocialCallbackHandler session={harness.session} onSignedIn={onSignedIn} />,
    )
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        zhCN.errors.authn.unavailable,
      ),
    )
    await userEvent.setup().click(
      screen.getByRole('button', { name: zhCN.socialCallback.retry }),
    )
    await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1))
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/flows/current'),
    ).toHaveLength(2)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(harness.store.get()).toBe('access-1')
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
