/**
 * The self-hosted captcha's slider form in a real browser: the challenge's
 * background and piece are decoded images, so the control can read their
 * natural size, and the native range input must be operable from the
 * keyboard -- an arrow key moves the piece, End takes it to the far edge,
 * and Enter on the focused range yields the offset the server compares.
 *
 * The cases run in Chromium because jsdom decodes no image and lays no
 * control out; the unit suite covers the same form's markup with supplied
 * sizes.
 */
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
// The provider-backed keyboard performs the browser's own default action
// (the DOM-simulating user-event does not move a native range).
import { page, userEvent as browserUserEvent } from 'vitest/browser'
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
    // The slider is the answer: Enter on the focused range submits its
    // offset, the same way releasing the pointer does.
    await browserUserEvent.keyboard('{Enter}')
    expect(onToken).toHaveBeenCalledWith('challenge-1.272')
  })

  it('keeps the piece aligned at a narrow rendered width', async () => {
    // The background scales down with the container while the piece and
    // its offset were drawn for the 320px natural width. A narrow viewport
    // must scale both, so the position the viewer aligns is the offset the
    // server compares.
    await page.viewport(240, 800)
    const onToken = vi.fn()
    const { container } = renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={{ phone: CAPTCHA_GATE, other: CAPTCHA_GATE }}
        captcha={CHALLENGE}
        onToken={onToken}
        onRefreshCaptcha={() => {}}
      />,
    )
    const images = container.querySelectorAll('img')
    expect(images).toHaveLength(2)
    const background = images[0] as HTMLImageElement
    const piece = images[1] as HTMLImageElement
    // Both media are the challenge's own: the piece is drawn from the same
    // challenge document as the background, so the pair the viewer aligns
    // (and the server compares) is never a stand-in fitted by the test.
    expect(background.getAttribute('src')).toBe(CHALLENGE.background_url)
    expect(piece.getAttribute('src')).toBe(CHALLENGE.piece_url)
    const range = screen.getByLabelText(
      CAPTCHA_COPY.sliderLabel,
    ) as HTMLInputElement
    await waitFor(() => expect(range.max).toBe('272'))
    await waitFor(() =>
      expect(background.getBoundingClientRect().width).toBeLessThan(320),
    )
    range.focus()
    await browserUserEvent.keyboard('{End}')
    expect(range.value).toBe('272')

    const backgroundRect = background.getBoundingClientRect()
    const pieceRect = piece.getBoundingClientRect()
    const scale = backgroundRect.width / 320
    // The piece is drawn at its natural size scaled by the rendered width:
    // the piece's visual width and its left edge map back to the same
    // natural coordinates the server compares.
    expect(pieceRect.width).toBeCloseTo(48 * scale, 0)
    const naturalOffset = (pieceRect.left - backgroundRect.left) / scale
    // The server accepts an offset within 2px of the cut
    // (pkg/authn/humanverify/captcha/slider.go's sliderTolerance). The
    // position the viewer aligned maps back to a natural offset there, so
    // the submitted value lands inside the band the server accepts.
    expect(Math.abs(naturalOffset - 272)).toBeLessThanOrEqual(2)

    await browserUserEvent.keyboard('{Enter}')
    expect(onToken).toHaveBeenCalledWith('challenge-1.272')
    // A real cutout cannot be asserted from this side: the cut position is
    // the server's secret, absent from the challenge document, and these
    // images are inline SVGs the test supplies rather than pixels drawn by
    // the server. The server's own suite (captcha/slider.go's tolerance
    // cases) proves the piece is cut from the background; this case proves
    // the client maps the rendered position back to the natural offset the
    // server compares.
  })
})
