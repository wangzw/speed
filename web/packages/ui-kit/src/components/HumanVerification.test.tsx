/**
 * HumanVerification: the presentational block of the human-verification
 * gate.
 *
 * Pins the mechanisms -- off renders nothing, Turnstile renders the
 * provider widget and reports its token (and resets once the host
 * consumes it), proof of work shows the work and offers a retry, and the
 * self-hosted captcha renders the image, slider and audio forms, reports
 * the answer it collected, offers the one audio accessibility entry, and
 * shows the wait a refusal states -- plus the per-surface selection, the
 * audio player's focus and language, and the accessibility of the states
 * a viewer waits on. It also pins the block as one named group, the
 * announcement it inserts a frame after it appears, and the two states
 * its own buttons take: `busy` for the action in flight, `unavailable`
 * with the page's explanatory text for a precondition that is unmet.
 * Text expectations read the bundle values, never inline language.
 */

import { useState } from 'react'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { installFakeTurnstile } from '@speed/test-utils/turnstile'
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import {
  HumanVerification,
  type HumanCaptchaChallenge,
  type HumanVerificationGate,
} from './HumanVerification.js'

afterEach(() => {
  delete (window as unknown as Record<string, unknown>).turnstile
})

const OFF: HumanVerificationGate = { required: false, provider: 'off' }

/** A whole gate: `phone` as given, `other` off unless supplied. */
function gateSet(
  phone: HumanVerificationGate,
  other: HumanVerificationGate = OFF,
): { phone: HumanVerificationGate; other: HumanVerificationGate } {
  return { phone, other }
}

/** One issued captcha challenge of the named form. */
function captcha(
  form: HumanCaptchaChallenge['form'],
): HumanCaptchaChallenge {
  return {
    id: 'challenge-1',
    form,
    instruction:
      form === 'slider'
        ? 'slide-the-piece'
        : form === 'audio'
          ? 'type-the-digits-you-hear'
          : 'type-the-digits',
    ...(form === 'image'
      ? { image_url: '/api/v1/auth/human/captcha/image/1' }
      : {}),
    ...(form === 'slider'
      ? {
          background_url: '/api/v1/auth/human/captcha/background/1',
          piece_url: '/api/v1/auth/human/captcha/piece/1',
        }
      : {}),
    ...(form === 'audio'
      ? { audio_url: '/api/v1/auth/human/captcha/audio/1' }
      : {}),
    expires_in: 300,
  }
}

const COPY = zhCN.humanVerification
const CAPTCHA_COPY = COPY.captcha

/** The block's one group, named for what it asks the viewer to do. */
function group(): HTMLElement {
  return screen.getByRole('group', { name: COPY.groupLabel })
}

/** The announcement region the block mounts: the group's first live status. */
function announcement(): HTMLElement {
  const region = group().querySelector<HTMLElement>('[role="status"]')
  expect(region).not.toBeNull()
  return region as HTMLElement
}

/** Give the slider's two images the decoded sizes jsdom does not report: a
 * 320x160 background and a 48x48 piece, so the range takes 272 steps. */
function loadSliderImages(container: HTMLElement): void {
  const images = container.querySelectorAll('img')
  expect(images).toHaveLength(2)
  for (const [index, image] of Array.from(images).entries()) {
    Object.defineProperty(image, 'naturalWidth', {
      configurable: true,
      value: index === 0 ? 320 : 48,
    })
    Object.defineProperty(image, 'naturalHeight', {
      configurable: true,
      value: index === 0 ? 160 : 48,
    })
    fireEvent.load(image)
  }
}

describe('HumanVerification', () => {
  it('renders nothing when the gate is absent, off, or requires nothing', () => {
    const { container, rerender } = renderWithProviders(
      <HumanVerification scope="phone" onToken={() => {}} />,
    )
    expect(container).toBeEmptyDOMElement()
    rerender(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: false, provider: 'off' })}
        onToken={() => {}}
      />,
    )
    expect(container).toBeEmptyDOMElement()
    rerender(
      <HumanVerification
        scope="phone"
        gate={gateSet({
          required: false,
          provider: 'turnstile',
          site_key: 'site-1',
        })}
        onToken={() => {}}
      />,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('serves the mechanism of the scope it is given', () => {
    const fake = installFakeTurnstile()
    renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet(
          { required: true, provider: 'pow' },
          { required: true, provider: 'turnstile', site_key: 'site-other' },
        )}
        onToken={() => {}}
      />,
    )
    // The phone surface runs the proof of work, not the other surface's
    // widget.
    expect(fake.widgets).toHaveLength(0)
    expect(screen.getByText(COPY.working)).toBeInTheDocument()
    fake.restore()
  })

  it('shows the proof-of-work work and its outcome', () => {
    const { rerender } = renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'pow' })}
        status="working"
        onToken={() => {}}
      />,
    )
    expect(screen.getByText(COPY.working)).toBeInTheDocument()
    rerender(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'pow' })}
        status="ready"
        proof="challenge.nonce"
        onToken={() => {}}
      />,
    )
    expect(screen.getByText(COPY.verified)).toBeInTheDocument()
  })

  it('offers a retry after a failed proof of work', async () => {
    const onRetry = vi.fn()
    renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'pow' })}
        status="failed"
        onToken={() => {}}
        onRetry={onRetry}
      />,
    )
    expect(screen.getByRole('alert')).toHaveTextContent(
      zhCN.humanVerification.failed,
    )
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.humanVerification.retry }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('renders the Turnstile widget, yields its token, and resets it once consumed', async () => {
    const fake = installFakeTurnstile()
    const seen: (string | null)[] = []

    function Host() {
      const [proof, setProof] = useState<string | null>(null)
      return (
        <div>
          <HumanVerification
            scope="other"
            gate={gateSet(OFF, {
              required: true,
              provider: 'turnstile',
              site_key: 'site-1',
              action: 'login',
            })}
            proof={proof}
            onToken={(token) => {
              seen.push(token)
              setProof(token)
            }}
          />
          <button type="button" onClick={() => setProof(null)}>
            consume
          </button>
        </div>
      )
    }

    const { unmount } = renderWithProviders(<Host />)
    await waitFor(() => expect(fake.widgets).toHaveLength(1))
    expect(fake.widgets[0]?.siteKey).toBe('site-1')
    expect(fake.widgets[0]?.action).toBe('login')

    act(() => {
      fake.token('token-1')
    })
    expect(seen).toEqual(['token-1'])

    await userEvent.setup().click(screen.getByRole('button', { name: 'consume' }))
    await waitFor(() => expect(fake.resets).toHaveLength(1))
    expect(fake.resets[0]).toBe(fake.widgets[0]!.id)

    unmount()
    expect(fake.removed).toHaveLength(1)
    fake.restore()
  })

  it('re-renders the Turnstile widget when the gate changes its action', async () => {
    const fake = installFakeTurnstile()
    const gate = (action: string) =>
      gateSet(OFF, {
        required: true,
        provider: 'turnstile',
        site_key: 'site-1',
        action,
      })
    const { rerender, unmount } = renderWithProviders(
      <HumanVerification
        scope="other"
        gate={gate('login')}
        onToken={() => {}}
      />,
    )
    await waitFor(() => expect(fake.widgets).toHaveLength(1))
    expect(fake.widgets[0]?.action).toBe('login')

    rerender(
      <HumanVerification
        scope="other"
        gate={gate('reset')}
        onToken={() => {}}
      />,
    )
    await waitFor(() => expect(fake.widgets).toHaveLength(2))
    expect(fake.widgets[1]?.action).toBe('reset')

    unmount()
    fake.restore()
  })

  it('shows the captcha issue in progress and its retry', async () => {
    const onRetry = vi.fn()
    const { rerender } = renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        status="working"
        onToken={() => {}}
        onRetry={onRetry}
      />,
    )
    expect(screen.getByText(COPY.working)).toBeInTheDocument()
    // A failed issue offers the retry.
    rerender(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        status="failed"
        onToken={() => {}}
        onRetry={onRetry}
      />,
    )
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: zhCN.humanVerification.retry }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('renders the image form, collects an answer and submits the proof', async () => {
    const onToken = vi.fn()
    renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('image')}
        onToken={onToken}
      />,
    )
    expect(
      screen.getByAltText(CAPTCHA_COPY.instruction.typeTheDigits),
    ).toHaveAttribute('src', '/api/v1/auth/human/captcha/image/1')
    expect(
      screen.getByRole('button', { name: CAPTCHA_COPY.useAudio }),
    ).toBeInTheDocument()
    const answer = screen.getByLabelText(CAPTCHA_COPY.answerLabel)
    await userEvent.setup().type(answer, '12345')
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: CAPTCHA_COPY.submit }))
    expect(onToken).toHaveBeenCalledWith('challenge-1.12345')
  })

  it('offers one accessibility entry for audio and a refresh that names no form', async () => {
    const onRequestAudio = vi.fn()
    const onRefresh = vi.fn()
    renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('image')}
        onToken={() => {}}
        onRefreshCaptcha={onRefresh}
        onRequestAudio={onRequestAudio}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: CAPTCHA_COPY.useAudio }),
    )
    expect(onRequestAudio).toHaveBeenCalledTimes(1)
    expect(onRequestAudio).toHaveBeenLastCalledWith()
    await user.click(screen.getByRole('button', { name: CAPTCHA_COPY.refresh }))
    expect(onRefresh).toHaveBeenCalledTimes(1)
    expect(onRefresh).toHaveBeenLastCalledWith()
  })

  it('holds the accessibility entry and shows the wait a refusal states', () => {
    renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('image')}
        onToken={() => {}}
        onRefreshCaptcha={() => {}}
        onRequestAudio={() => {}}
        audioRetryAfterSeconds={42}
      />,
    )
    const audio = screen.getByRole('button', { name: CAPTCHA_COPY.useAudio })
    expect(audio).toHaveAttribute('aria-disabled', 'true')
    expect(audio).toHaveAccessibleDescription(
      CAPTCHA_COPY.audioWait.replace('{{seconds}}', '42'),
    )
    // The refresh control is not the refused one and stays available.
    expect(
      screen.getByRole('button', { name: CAPTCHA_COPY.refresh }),
    ).not.toHaveAttribute('aria-disabled')
  })

  it('offers no control that asks for the image or slider form', () => {
    for (const form of ['image', 'slider'] as const) {
      const { unmount } = renderWithProviders(
        <HumanVerification
          scope="phone"
          gate={gateSet({ required: true, provider: 'captcha' })}
          captcha={captcha(form)}
          onToken={() => {}}
          onRefreshCaptcha={() => {}}
        />,
      )
      // The server chooses between the image and slider forms; the client
      // cannot force either one. The only form-naming control is the audio
      // accessibility entry, and the refresh takes no form. The image form
      // also carries its answer's submit; the slider submits on release,
      // so it has no separate confirm button.
      expect(
        screen.getByRole('button', { name: CAPTCHA_COPY.useAudio }),
      ).toBeInTheDocument()
      expect(
        screen.getByRole('button', { name: CAPTCHA_COPY.refresh }),
      ).toBeInTheDocument()
      expect(screen.getAllByRole('button')).toHaveLength(
        form === 'image' ? 3 : 2,
      )
      unmount()
    }
  })

  it('offers no form switch once the audio alternative is shown', () => {
    renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('audio')}
        onToken={() => {}}
        onRefreshCaptcha={() => {}}
      />,
    )
    // The audio alternative is where the switches end, so with audio on
    // screen none is shown; the refresh control remains.
    expect(
      screen.queryByRole('button', { name: CAPTCHA_COPY.useAudio }),
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: CAPTCHA_COPY.refresh }),
    ).toBeInTheDocument()
  })

  it('renders the audio challenge with a readable, language-marked player and submits its answer', async () => {
    const onToken = vi.fn()
    const { container } = renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('audio')}
        onToken={onToken}
        onRefreshCaptcha={() => {}}
      />,
    )
    const audio = container.querySelector('audio')
    expect(audio).not.toBeNull()
    expect(audio).toHaveAttribute('controls')
    expect(audio).toHaveAttribute('aria-label', CAPTCHA_COPY.audioLabel)
    expect(audio).toHaveAttribute(
      'src',
      '/api/v1/auth/human/captcha/audio/1',
    )
    // The digits are spoken in the active locale, so the player names it
    // and receives focus when the alternative appears.
    expect(audio).toHaveAttribute('lang', 'zh-CN')
    expect(document.activeElement).toBe(audio)
    await userEvent
      .setup()
      .type(screen.getByLabelText(CAPTCHA_COPY.answerLabel), '67890')
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: CAPTCHA_COPY.submit }))
    expect(onToken).toHaveBeenCalledWith('challenge-1.67890')
    // The audio alternative is where the switches end: no visual control
    // is offered, and the audio entry itself is gone.
    for (const name of [CAPTCHA_COPY.useAudio]) {
      expect(screen.queryByRole('button', { name })).not.toBeInTheDocument()
    }
  })

  it('renders the slider behind a native range input and submits its offset on release', async () => {
    const onToken = vi.fn()
    const { container } = renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('slider')}
        onToken={onToken}
        onRefreshCaptcha={() => {}}
      />,
    )
    loadSliderImages(container)
    const range = screen.getByLabelText(CAPTCHA_COPY.sliderLabel)
    expect(range).toHaveAttribute('type', 'range')
    await waitFor(() => expect(range).toHaveAttribute('max', '272'))
    fireEvent.change(range, { target: { value: '140' } })
    // Moving the control is not submitting it: the offset travels on
    // release, not on every change the browser reports while dragging.
    expect(onToken).not.toHaveBeenCalled()
    fireEvent.pointerUp(range)
    expect(onToken).toHaveBeenCalledTimes(1)
    expect(onToken).toHaveBeenCalledWith('challenge-1.140')
    // The slider was the server's rotation: the client offers no control
    // to ask for it, only the audio accessibility entry and the refresh.
    expect(
      screen.getByRole('button', { name: CAPTCHA_COPY.useAudio }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: CAPTCHA_COPY.refresh }),
    ).toBeInTheDocument()
    expect(screen.getAllByRole('button')).toHaveLength(2)
  })

  it('submits the slider once on Enter after the keyboard moved it', async () => {
    const onToken = vi.fn()
    const { container } = renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('slider')}
        onToken={onToken}
        onRefreshCaptcha={() => {}}
      />,
    )
    loadSliderImages(container)
    const range = screen.getByLabelText(CAPTCHA_COPY.sliderLabel)
    await waitFor(() => expect(range).toHaveAttribute('max', '272'))
    // A native range moves on the arrow keys and the browser reports each
    // move as a change; the component itself submits on Enter, and only
    // the offset in force when it is pressed travels.
    fireEvent.change(range, { target: { value: '80' } })
    fireEvent.change(range, { target: { value: '210' } })
    expect(onToken).not.toHaveBeenCalled()
    fireEvent.keyDown(range, { key: 'Enter' })
    expect(onToken).toHaveBeenCalledTimes(1)
    expect(onToken).toHaveBeenCalledWith('challenge-1.210')
  })

  it('shows the verified state once the host holds the captcha proof', () => {
    renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('image')}
        proof="challenge-1.12345"
        status="ready"
        onToken={() => {}}
      />,
    )
    expect(screen.getByText(COPY.verified)).toBeInTheDocument()
    expect(
      screen.queryByLabelText(CAPTCHA_COPY.answerLabel),
    ).not.toBeInTheDocument()
  })

  it('renders the block as one named group and drops the captcha form own group', () => {
    renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('image')}
        onToken={() => {}}
      />,
    )
    expect(screen.getAllByRole('group')).toHaveLength(1)
    expect(group()).toHaveAttribute('tabindex', '-1')
  })

  it('announces that verification is required once the block appears, for every mechanism', async () => {
    const fake = installFakeTurnstile()
    try {
      const cases = [
        { provider: 'captcha', element: () => captcha('image') },
        { provider: 'turnstile', element: () => null },
        { provider: 'pow', element: () => null },
      ] as const
      for (const surface of cases) {
        const { unmount } = renderWithProviders(
          <HumanVerification
            scope="phone"
            gate={gateSet({
              required: true,
              provider: surface.provider,
              ...(surface.provider === 'turnstile'
                ? { site_key: 'site-1' }
                : {}),
            })}
            captcha={surface.element()}
            status="working"
            onToken={() => {}}
          />,
        )
        // The commit that inserted the region left it empty.
        expect(announcement().textContent).toBe('')
        await waitFor(() =>
          expect(announcement()).toHaveTextContent(COPY.required),
        )
        unmount()
      }
    } finally {
      fake.restore()
    }
  })

  it('does not announce the requirement again when a challenge is replaced', async () => {
    const { rerender } = renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('image')}
        onToken={() => {}}
        onRefreshCaptcha={() => {}}
      />,
    )
    await waitFor(() => expect(announcement()).toHaveTextContent(COPY.required))
    rerender(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={null}
        status="working"
        onToken={() => {}}
        onRefreshCaptcha={() => {}}
      />,
    )
    expect(announcement()).toHaveTextContent(COPY.required)
    expect(screen.getAllByText(COPY.required)).toHaveLength(1)
  })

  it('keeps the block buttons focusable and inert while the host send is in flight', async () => {
    const onToken = vi.fn()
    const onRefresh = vi.fn()
    const onRequestAudio = vi.fn()
    renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('image')}
        onToken={onToken}
        onRefreshCaptcha={onRefresh}
        onRequestAudio={onRequestAudio}
        disabled={true}
      />,
    )
    const user = userEvent.setup()
    const buttons = [
      screen.getByRole('button', { name: CAPTCHA_COPY.submit }),
      screen.getByRole('button', { name: CAPTCHA_COPY.useAudio }),
      screen.getByRole('button', { name: CAPTCHA_COPY.refresh }),
    ]
    for (const button of buttons) {
      expect(button).toHaveAttribute('aria-disabled', 'true')
      expect(button).not.toHaveAttribute('aria-busy')
      expect(button).not.toHaveAttribute('disabled')
      await user.click(button)
    }
    expect(onToken).not.toHaveBeenCalled()
    expect(onRefresh).not.toHaveBeenCalled()
    expect(onRequestAudio).not.toHaveBeenCalled()
    // A write in flight takes the form's input to read-only, not to a
    // native disable: the field stays focusable, and the block's ADR gives
    // inputs the read-only treatment rather than the disabled one.
    const answer = screen.getByLabelText(CAPTCHA_COPY.answerLabel)
    expect(answer).toHaveAttribute('readonly')
    expect(answer).not.toBeDisabled()
    answer.focus()
    expect(document.activeElement).toBe(answer)
  })

  it('keeps the slider focusable and inert while the host send is in flight', async () => {
    // A native range has no read-only semantics -- `readonly` is ignored on
    // it -- so the slider reports the held state with `aria-disabled` and
    // swallows the interactions itself instead of taking the native
    // disable that would drop focus.
    const onToken = vi.fn()
    const { container } = renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('slider')}
        onToken={onToken}
        onRefreshCaptcha={() => {}}
        disabled={true}
      />,
    )
    loadSliderImages(container)
    const range = screen.getByLabelText(
      CAPTCHA_COPY.sliderLabel,
    ) as HTMLInputElement
    await waitFor(() => expect(range).toHaveAttribute('max', '272'))
    expect(range).not.toHaveAttribute('disabled')
    expect(range).toHaveAttribute('aria-disabled', 'true')
    range.focus()
    expect(document.activeElement).toBe(range)
    fireEvent.change(range, { target: { value: '140' } })
    expect(range).toHaveValue('0')
    fireEvent.pointerUp(range)
    fireEvent.keyDown(range, { key: 'Enter' })
    expect(onToken).not.toHaveBeenCalled()
  })

  it('keeps the retry controls focusable and inert while the host send is in flight', async () => {
    const onRetry = vi.fn()
    const user = userEvent.setup()
    for (const provider of ['pow', 'captcha'] as const) {
      const { unmount } = renderWithProviders(
        <HumanVerification
          scope="phone"
          gate={gateSet({ required: true, provider })}
          status="failed"
          onToken={() => {}}
          onRetry={onRetry}
          disabled={true}
        />,
      )
      const retry = screen.getByRole('button', { name: COPY.retry })
      expect(retry).toHaveAttribute('aria-disabled', 'true')
      expect(retry).not.toHaveAttribute('disabled')
      await user.click(retry)
      expect(onRetry).not.toHaveBeenCalled()
      unmount()
    }
  })

  it('explains an empty answer submit by the instruction it cannot follow yet', async () => {
    const onToken = vi.fn()
    renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('image')}
        onToken={onToken}
      />,
    )
    const submit = screen.getByRole('button', { name: CAPTCHA_COPY.submit })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      CAPTCHA_COPY.instruction.typeTheDigits,
    )
    const user = userEvent.setup()
    await user.click(submit)
    expect(onToken).not.toHaveBeenCalled()
    await user.type(
      screen.getByLabelText(CAPTCHA_COPY.answerLabel),
      '12345',
    )
    expect(submit).not.toHaveAttribute('aria-disabled')
  })

  it('keeps the refresh control mounted and busy while the next challenge is requested', async () => {
    const onRefresh = vi.fn()
    const { rerender } = renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('image')}
        onToken={() => {}}
        onRefreshCaptcha={onRefresh}
      />,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: CAPTCHA_COPY.refresh }))
    rerender(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={null}
        status="working"
        onToken={() => {}}
        onRefreshCaptcha={onRefresh}
      />,
    )
    const refresh = screen.getByRole('button', { name: CAPTCHA_COPY.refresh })
    expect(refresh).toHaveAttribute('aria-busy', 'true')
    expect(refresh).toHaveAttribute('aria-disabled', 'true')
    const audio = screen.getByRole('button', { name: CAPTCHA_COPY.useAudio })
    expect(audio).toHaveAttribute('aria-disabled', 'true')
    expect(audio).not.toHaveAttribute('aria-busy')
    expect(screen.getByText(COPY.working)).toBeInTheDocument()
    // The refresh button and its sibling swallow activation while the swap
    // is in flight.
    await user.click(refresh)
    await user.click(audio)
    expect(onRefresh).toHaveBeenCalledTimes(1)
    rerender(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={{ ...captcha('image'), id: 'challenge-2' }}
        onToken={() => {}}
        onRefreshCaptcha={onRefresh}
      />,
    )
    expect(refresh).not.toHaveAttribute('aria-busy')
  })

  it('puts the busy state on the audio entry when that entry asked for the swap', async () => {
    const { rerender } = renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('image')}
        onToken={() => {}}
        onRefreshCaptcha={() => {}}
        onRequestAudio={() => {}}
      />,
    )
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', { name: CAPTCHA_COPY.useAudio }),
    )
    rerender(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={null}
        status="working"
        onToken={() => {}}
        onRefreshCaptcha={() => {}}
        onRequestAudio={() => {}}
      />,
    )
    const audio = screen.getByRole('button', { name: CAPTCHA_COPY.useAudio })
    const refresh = screen.getByRole('button', { name: CAPTCHA_COPY.refresh })
    expect(audio).toHaveAttribute('aria-busy', 'true')
    expect(refresh).toHaveAttribute('aria-disabled', 'true')
    expect(refresh).not.toHaveAttribute('aria-busy')
  })

  it('does not mark the refresh control busy when the retry asked for the swap', async () => {
    // A refresh whose request fails leaves no swap in flight. The retry
    // that asks for the next attempt is the action in flight, so the
    // refresh control is not busy.
    const onRefresh = vi.fn()
    const onRetry = vi.fn()
    const props = {
      scope: 'phone' as const,
      gate: gateSet({ required: true, provider: 'captcha' }),
      onToken: () => {},
      onRefreshCaptcha: onRefresh,
      onRetry,
    }
    const { rerender } = renderWithProviders(
      <HumanVerification {...props} captcha={captcha('image')} />,
    )
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: CAPTCHA_COPY.refresh }))
    rerender(<HumanVerification {...props} captcha={null} status="working" />)
    expect(
      screen.getByRole('button', { name: CAPTCHA_COPY.refresh }),
    ).toHaveAttribute('aria-busy', 'true')
    // The refresh failed; the retry now asks for the next attempt.
    rerender(<HumanVerification {...props} captcha={null} status="failed" />)
    await user.click(screen.getByRole('button', { name: COPY.retry }))
    rerender(<HumanVerification {...props} captcha={null} status="working" />)
    expect(onRefresh).toHaveBeenCalledTimes(1)
    expect(onRetry).toHaveBeenCalledTimes(1)
    expect(
      screen.getByRole('button', { name: CAPTCHA_COPY.refresh }),
    ).not.toHaveAttribute('aria-busy')
    // The failed request cleared the refresh's pending action, so the retry
    // started this swap and the refresh did not: it is unavailable, not
    // busy, and activating it must not add a second request to the one in
    // flight.
    const refresh = screen.getByRole('button', { name: CAPTCHA_COPY.refresh })
    expect(refresh).toHaveAttribute('aria-disabled', 'true')
    await user.click(refresh)
    expect(onRefresh).toHaveBeenCalledTimes(1)
  })

  it('does not mark the refresh control busy when the host refetches after a proof', async () => {
    // The host consumed the proof and asks for the next challenge on its
    // own: no entry started the swap, so neither entry is busy.
    const props = {
      scope: 'phone' as const,
      gate: gateSet({ required: true, provider: 'captcha' }),
      onToken: () => {},
      onRefreshCaptcha: () => {},
    }
    const { rerender } = renderWithProviders(
      <HumanVerification {...props} captcha={captcha('image')} />,
    )
    rerender(
      <HumanVerification
        {...props}
        captcha={null}
        proof="proof-1"
        status="ready"
      />,
    )
    expect(screen.getByText(COPY.verified)).toBeInTheDocument()
    rerender(<HumanVerification {...props} captcha={null} status="working" />)
    expect(screen.getByText(COPY.working)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: CAPTCHA_COPY.refresh }),
    ).not.toHaveAttribute('aria-busy')
  })

  it('holds the refresh control during a swap neither entry started', async () => {
    // The host refetches on its own, so neither entry is the author of the
    // swap in flight. The refresh has nothing to ask for until the swap
    // lands, and the audio entry already reports the hold; the refresh must
    // report it too, or a second request can be layered onto the first.
    const onRefresh = vi.fn()
    const props = {
      scope: 'phone' as const,
      gate: gateSet({ required: true, provider: 'captcha' }),
      onToken: () => {},
      onRefreshCaptcha: onRefresh,
      onRequestAudio: () => {},
    }
    const { rerender } = renderWithProviders(
      <HumanVerification {...props} captcha={captcha('image')} />,
    )
    rerender(<HumanVerification {...props} captcha={null} status="working" />)
    const refresh = screen.getByRole('button', { name: CAPTCHA_COPY.refresh })
    expect(refresh).toHaveAttribute('aria-disabled', 'true')
    expect(refresh).not.toHaveAttribute('aria-busy')
    const user = userEvent.setup()
    await user.click(refresh)
    expect(onRefresh).not.toHaveBeenCalled()
  })

  it('does not claim focus after the viewer left the block and their new target disappeared', async () => {
    // The escaped-focus record has no other reachable host path: the viewer
    // moves from the block to a field beside it, the host removes that
    // field (focus falls to the body), and only then the block's own state
    // change removes the control that last held focus inside. Without the
    // record this is exactly the case that would pull focus back into the
    // group; with it, focus stays on the body.
    function Host({
      showOther,
      proof,
    }: {
      readonly showOther: boolean
      readonly proof: string | null
    }) {
      return (
        <div>
          {showOther ? <input aria-label="Other field" /> : null}
          <HumanVerification
            scope="phone"
            gate={gateSet({ required: true, provider: 'captcha' })}
            captcha={captcha('image')}
            proof={proof}
            status={proof === null ? 'idle' : 'ready'}
            onToken={() => {}}
          />
        </div>
      )
    }
    const { rerender } = renderWithProviders(
      <Host showOther={true} proof={null} />,
    )
    const answer = screen.getByLabelText(CAPTCHA_COPY.answerLabel)
    answer.focus()
    const other = screen.getByLabelText('Other field')
    other.focus()
    expect(document.activeElement).toBe(other)
    // The host removes the field the viewer moved to.
    rerender(<Host showOther={false} proof={null} />)
    expect(document.activeElement).toBe(document.body)
    // The proof arrives and replaces the answer field with the verified
    // state. The block's own control left the document, but the viewer was
    // not in the block any more: focus must not move.
    rerender(<Host showOther={false} proof="proof-1" />)
    expect(screen.getByText(COPY.verified)).toBeInTheDocument()
    expect(document.activeElement).toBe(document.body)
  })

  it('passes axe for the proof-of-work states', async () => {
    renderWithProviders(
      <div>
        <h1>Account</h1>
        <HumanVerification
          scope="phone"
          gate={gateSet({ required: true, provider: 'pow' })}
          status="ready"
          onToken={() => {}}
        />
      </div>,
    )
    await expectNoAxeViolations()
  })

  it('passes axe for the captcha image and slider forms', async () => {
    const { rerender } = renderWithProviders(
      <div>
        <h1>Account</h1>
        <HumanVerification
          scope="phone"
          gate={gateSet({ required: true, provider: 'captcha' })}
          captcha={captcha('image')}
          onToken={() => {}}
        />
      </div>,
    )
    await expectNoAxeViolations()
    rerender(
      <div>
        <h1>Account</h1>
        <HumanVerification
          scope="phone"
          gate={gateSet({ required: true, provider: 'captcha' })}
          captcha={captcha('slider')}
          onToken={() => {}}
          onRefreshCaptcha={() => {}}
        />
      </div>,
    )
    await expectNoAxeViolations()
  })
})
