/**
 * The self-hosted captcha's slider form in a real browser: the challenge's
 * background and piece are decoded images, so the control can read their
 * natural size, and the native range input must be operable from the
 * keyboard -- an arrow key moves the piece, End takes it to the far edge.
 * A press of the submit control yields the offset the server compares.
 *
 * The cases run in Chromium because jsdom decodes no image and lays no
 * control out; the unit suite covers the same form's markup with supplied
 * sizes.
 */
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
// The provider-backed keyboard performs the browser's own default action
// (the DOM-simulating user-event does not move a native range).
import { userEvent as browserUserEvent } from 'vitest/browser'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import {
  HumanVerification,
  type HumanCaptchaChallenge,
  type HumanVerificationGate,
} from './HumanVerification.js'

const CAPTCHA_COPY = zhCN.humanVerification.captcha

/** An inline SVG image of the given natural size. */
function svg(width: number, height: number): string {
  return `data:image/svg+xml,${encodeURIComponent(
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}"><rect width="100%" height="100%" fill="#246"/></svg>`,
  )}`
}

const CHALLENGE: HumanCaptchaChallenge = {
  id: 'challenge-1',
  form: 'slider',
  instruction: 'slide-the-piece',
  background_url: svg(320, 160),
  piece_url: svg(48, 48),
  expires_in: 300,
}

const CAPTCHA_GATE: HumanVerificationGate = {
  required: true,
  provider: 'captcha',
}

describe('HumanVerification slider in Chromium', () => {
  it('moves the piece from the keyboard and submits its offset', async () => {
    const onToken = vi.fn()
    renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={{ phone: CAPTCHA_GATE, other: CAPTCHA_GATE }}
        captcha={CHALLENGE}
        onToken={onToken}
        onRefreshCaptcha={() => {}}
      />,
    )
    const range = screen.getByLabelText(
      CAPTCHA_COPY.sliderLabel,
    ) as HTMLInputElement
    // The images load in a real browser, so the range takes the piece's
    // travel (320 - 48).
    await waitFor(() => expect(range.max).toBe('272'))
    expect(range.type).toBe('range')
    range.focus()
    expect(document.activeElement).toBe(range)
    await browserUserEvent.keyboard('{ArrowRight}')
    expect(range.value).toBe('1')
    await browserUserEvent.keyboard('{End}')
    expect(range.value).toBe('272')
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: CAPTCHA_COPY.submit }))
    expect(onToken).toHaveBeenCalledWith('challenge-1.272')
  })
})
