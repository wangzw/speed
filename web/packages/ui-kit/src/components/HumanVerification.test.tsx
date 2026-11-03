/**
 * HumanVerification: the presentational block of the human-verification
 * gate.
 *
 * Pins the mechanisms -- off renders nothing, Turnstile renders the
 * provider widget and reports its token (and resets once the host
 * consumes it), proof of work shows the work and offers a retry, and the
 * self-hosted captcha renders the image, slider and audio forms, reports
 * the answer it collected and offers only the audio alternative as a form
 * switch -- plus the per-surface selection and the accessibility of the
 * states a viewer waits on. Text expectations read the bundle values,
 * never inline language.
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

const CAPTCHA_COPY = zhCN.humanVerification.captcha

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
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.humanVerification.working,
    )
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
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.humanVerification.working,
    )
    rerender(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'pow' })}
        status="ready"
        proof="challenge.nonce"
        onToken={() => {}}
      />,
    )
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.humanVerification.verified,
    )
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
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.humanVerification.working,
    )
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

  it('switches to the audio challenge and refreshes in the selected mode', async () => {
    const onRefresh = vi.fn()
    renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={gateSet({ required: true, provider: 'captcha' })}
        captcha={captcha('image')}
        onToken={() => {}}
        onRefreshCaptcha={onRefresh}
      />,
    )
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: CAPTCHA_COPY.useAudio }))
    expect(onRefresh).toHaveBeenLastCalledWith('audio')
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: CAPTCHA_COPY.refresh }))
    // A refresh re-requests the selected mode: the host names no form
    // unless the audio alternative is the one on screen.
    expect(onRefresh).toHaveBeenLastCalledWith()
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
      // cannot force either one.
      expect(
        screen.queryByRole('button', { name: CAPTCHA_COPY.useImage }),
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('button', { name: CAPTCHA_COPY.useSlider }),
      ).not.toBeInTheDocument()
      expect(
        screen.getByRole('button', { name: CAPTCHA_COPY.useAudio }),
      ).toBeInTheDocument()
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
    // The audio alternative is the only switch the block offers, so with
    // audio on screen none is shown; the refresh control remains.
    expect(
      screen.queryByRole('button', { name: CAPTCHA_COPY.useAudio }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: CAPTCHA_COPY.useImage }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: CAPTCHA_COPY.useSlider }),
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: CAPTCHA_COPY.refresh }),
    ).toBeInTheDocument()
  })

  it('renders the audio challenge with a readable label and submits its answer', async () => {
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
    await userEvent
      .setup()
      .type(screen.getByLabelText(CAPTCHA_COPY.answerLabel), '67890')
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: CAPTCHA_COPY.submit }))
    expect(onToken).toHaveBeenCalledWith('challenge-1.67890')
    // The audio alternative is where the switches end: no image or slider
    // control is offered, and the audio switch itself is gone.
    for (const name of [
      CAPTCHA_COPY.useImage,
      CAPTCHA_COPY.useSlider,
      CAPTCHA_COPY.useAudio,
    ]) {
      expect(screen.queryByRole('button', { name })).not.toBeInTheDocument()
    }
  })

  it('renders the slider behind a native range input and submits its offset', async () => {
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
    const images = container.querySelectorAll('img')
    expect(images).toHaveLength(2)
    // The browser reports the decoded size; jsdom does not decode, so the
    // case supplies it the way a loaded image would.
    for (const image of images) {
      Object.defineProperty(image, 'naturalWidth', {
        configurable: true,
        value: image === images[0] ? 320 : 48,
      })
      Object.defineProperty(image, 'naturalHeight', {
        configurable: true,
        value: image === images[0] ? 160 : 48,
      })
      fireEvent.load(image)
    }
    const range = screen.getByLabelText(CAPTCHA_COPY.sliderLabel)
    expect(range).toHaveAttribute('type', 'range')
    await waitFor(() => expect(range).toHaveAttribute('max', '272'))
    fireEvent.change(range, { target: { value: '140' } })
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: CAPTCHA_COPY.submit }))
    expect(onToken).toHaveBeenCalledWith('challenge-1.140')
    // The slider was the server's choice: the client offers no control to
    // ask for it, only the audio accessibility alternative.
    expect(
      screen.queryByRole('button', { name: CAPTCHA_COPY.useSlider }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: CAPTCHA_COPY.useImage }),
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: CAPTCHA_COPY.useAudio }),
    ).toBeInTheDocument()
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
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.humanVerification.verified,
    )
    expect(
      screen.queryByLabelText(CAPTCHA_COPY.answerLabel),
    ).not.toBeInTheDocument()
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
