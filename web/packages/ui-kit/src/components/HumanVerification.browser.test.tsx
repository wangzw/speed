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
import { useEffect, useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import { useTheme } from '@mui/material/styles'
// The provider-backed keyboard performs the browser's own default action
// (the DOM-simulating user-event does not move a native range).
import { page, userEvent as browserUserEvent } from 'vitest/browser'
import { themePresets } from '@speed/theme-presets'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import {
  HumanVerification,
  type HumanCaptchaChallenge,
  type HumanVerificationGate,
  type HumanVerificationGateSet,
  type HumanVerificationStatus,
} from './HumanVerification.js'

const COPY = zhCN.humanVerification
const CAPTCHA_COPY = COPY.captcha

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

  it('spans exactly the image frame in a wide container', async () => {
    // The card can be wider than the 320px challenge image. The track must
    // cover the frame, not the card, or the drag distance stops matching
    // the piece's travel: a full-width track on a 1000px card would move
    // the piece a third as far as the thumb moves.
    await page.viewport(1280, 900)
    const { container } = renderWithProviders(
      <HumanVerification
        scope="phone"
        gate={{ phone: CAPTCHA_GATE, other: CAPTCHA_GATE }}
        captcha={CHALLENGE}
        onToken={() => {}}
        onRefreshCaptcha={() => {}}
      />,
    )
    const range = screen.getByLabelText(
      CAPTCHA_COPY.sliderLabel,
    ) as HTMLInputElement
    await waitFor(() => expect(range.max).toBe('272'))
    const background = container.querySelectorAll('img')[0] as HTMLImageElement
    const frame = background.parentElement as HTMLElement
    // The form's own column really is wider than the image: without that,
    // the equality below would hold even for a full-width track.
    const group = screen.getByRole('group')
    expect(group.getBoundingClientRect().width).toBeGreaterThan(320)
    await waitFor(() =>
      expect(background.getBoundingClientRect().width).toBe(320),
    )
    const frameRect = frame.getBoundingClientRect()
    const rangeRect = range.getBoundingClientRect()
    // The track is the image frame's width, not the card's.
    expect(rangeRect.width).toBeCloseTo(frameRect.width, 1)
    expect(rangeRect.width).toBeLessThan(group.getBoundingClientRect().width)
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
    // The track shrinks with the frame here too, so the drag distance and
    // the piece's travel stay in the same rendered pixels.
    const frame = background.parentElement as HTMLElement
    await waitFor(() =>
      expect(range.getBoundingClientRect().width).toBeCloseTo(
        frame.getBoundingClientRect().width,
        1,
      ),
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

/** An RGB triple, 0-255 per channel. */
type Rgb = readonly [number, number, number]

/** Reads a computed CSS colour (`rgb(...)` / `rgba(...)`) into its channels. */
function parseRgb(value: string): Rgb {
  const match = /rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/.exec(value)
  if (match === null) {
    throw new Error(`unexpected computed colour: ${value}`)
  }
  return [Number(match[1]), Number(match[2]), Number(match[3])]
}

/** One channel's linear-light value, per WCAG 2.x. */
function linear(channel: number): number {
  const value = channel / 255
  return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
}

/** The WCAG contrast ratio between two opaque colours, from 1:1 to 21:1. */
function contrastRatio(first: Rgb, second: Rgb): number {
  const luminance = (colour: Rgb): number =>
    0.2126 * linear(colour[0]) +
    0.7152 * linear(colour[1]) +
    0.0722 * linear(colour[2])
  const a = luminance(first)
  const b = luminance(second)
  const [lighter, darker] = a >= b ? [a, b] : [b, a]
  return (lighter + 0.05) / (darker + 0.05)
}

/**
 * The colour the browser actually paints the range's unfilled track with,
 * sampled from a screenshot. The track belongs to the native control's
 * user-agent shadow DOM, so `getComputedStyle` exposes no colour for it
 * (Chromium returns the input's own background instead). The right end of
 * the track is sampled while the thumb sits at the range's minimum, so the
 * pixel is track, never thumb or the accent-filled portion.
 */
async function trackColor(range: HTMLElement): Promise<Rgb> {
  const base64 = await page.screenshot({ element: range, save: false })
  const image = new Image()
  image.src = base64.startsWith('data:')
    ? base64
    : `data:image/png;base64,${base64}`
  await image.decode()
  const canvas = document.createElement('canvas')
  canvas.width = image.naturalWidth
  canvas.height = image.naturalHeight
  const context = canvas.getContext('2d')
  if (context === null) {
    throw new Error('the browser exposed no 2D canvas context')
  }
  context.drawImage(image, 0, 0)
  const pixel = context.getImageData(
    Math.floor(canvas.width * 0.9),
    Math.floor(canvas.height / 2),
    1,
    1,
  ).data
  const [red, green, blue] = pixel
  if (red === undefined || green === undefined || blue === undefined) {
    throw new Error('the sampled pixel carries no colour channels')
  }
  return [red, green, blue]
}

/** The active theme's primary, rendered as a computed colour. */
function PrimaryProbe() {
  const theme = useTheme()
  return (
    <span
      data-testid="primary-probe"
      style={{ color: theme.palette.primary.main }}
    />
  )
}

/** The floor the accent must keep against the track it is painted on. */
const MINIMUM_ACCENT_CONTRAST = 3

describe('HumanVerification slider accent in Chromium', () => {
  for (const preset of themePresets) {
    for (const mode of ['light', 'dark'] as const) {
      it(`paints the ${preset.id} preset's primary on the slider in ${mode} mode`, async () => {
        await page.viewport(1280, 900)
        renderWithProviders(
          <>
            <PrimaryProbe />
            <HumanVerification
              scope="phone"
              gate={{ phone: CAPTCHA_GATE, other: CAPTCHA_GATE }}
              captcha={CHALLENGE}
              onToken={() => {}}
              onRefreshCaptcha={() => {}}
            />
          </>,
          {
            presets: themePresets,
            defaultPreset: preset.id,
            defaultMode: mode,
          },
        )
        const range = screen.getByLabelText(
          CAPTCHA_COPY.sliderLabel,
        ) as HTMLInputElement
        await waitFor(() => expect(range.max).toBe('272'))

        // The accent follows the theme's primary, not the browser's default
        // blue: the control's accent-color resolves to the same computed
        // value as an element painted with `palette.primary.main`.
        const primary = screen.getByTestId('primary-probe')
        const accent = parseRgb(getComputedStyle(range).accentColor)
        expect(accent).toEqual(parseRgb(getComputedStyle(primary).color))

        // The thumb and filled track are painted in the accent, so the
        // accent has to stay distinguishable from the unfilled track it
        // sits on: WCAG 1.4.11's 3:1 for a control's boundary.
        const track = await trackColor(range)
        expect(contrastRatio(accent, track)).toBeGreaterThanOrEqual(
          MINIMUM_ACCENT_CONTRAST,
        )
      })
    }
  }
})

const GATE_SET: HumanVerificationGateSet = {
  phone: CAPTCHA_GATE,
  other: CAPTCHA_GATE,
}

const IMAGE_CHALLENGE: HumanCaptchaChallenge = {
  id: 'challenge-image',
  form: 'image',
  instruction: 'type-the-digits',
  image_url: svg(120, 40),
  expires_in: 300,
}

/** The host that asks for a send and unmounts the block once it holds a proof. */
function CompletingHost() {
  const [pending, setPending] = useState(false)
  const [proof, setProof] = useState<string | null>(null)
  useEffect(() => {
    if (proof !== null) {
      setPending(false)
    }
  }, [proof])
  return (
    <div>
      <button type="button" onClick={() => setPending(true)}>
        Send code
      </button>
      {pending ? (
        <HumanVerification
          scope="phone"
          gate={GATE_SET}
          captcha={IMAGE_CHALLENGE}
          proof={proof}
          onToken={(token) => setProof(token)}
        />
      ) : null}
    </div>
  )
}

/** The swap the refresh control starts, finished by the case. */
let finishRefresh: (() => void) | null = null

/** The host that holds a challenge swap in flight until the case lets it land. */
function RefreshHost() {
  const [challenge, setChallenge] = useState<HumanCaptchaChallenge | null>(
    IMAGE_CHALLENGE,
  )
  const [status, setStatus] = useState<HumanVerificationStatus>('ready')
  const refresh = (): void => {
    setChallenge(null)
    setStatus('working')
    finishRefresh = () => {
      setChallenge({ ...IMAGE_CHALLENGE, id: 'challenge-image-2' })
      setStatus('ready')
    }
  }
  return (
    <HumanVerification
      scope="phone"
      gate={GATE_SET}
      captcha={challenge}
      status={status}
      onToken={() => {}}
      onRefreshCaptcha={refresh}
    />
  )
}

/** The host whose failed solve turns to working when the retry is activated. */
function PowRetryHost() {
  const [status, setStatus] = useState<HumanVerificationStatus>('failed')
  return (
    <HumanVerification
      scope="phone"
      gate={{
        phone: { required: true, provider: 'pow' },
        other: { required: true, provider: 'pow' },
      }}
      status={status}
      onToken={() => {}}
      onRetry={() => setStatus('working')}
    />
  )
}

/** The host that keeps the verified state once it holds the answer's proof. */
function CaptchaProofHost() {
  const [proof, setProof] = useState<string | null>(null)
  return (
    <HumanVerification
      scope="phone"
      gate={GATE_SET}
      captcha={IMAGE_CHALLENGE}
      proof={proof}
      status={proof === null ? 'idle' : 'ready'}
      onToken={(token) => setProof(token)}
    />
  )
}

/** The unmount the escape case asks for, from outside any event on the
 * field that holds focus. */
let escapeBlock: (() => void) | null = null

/** The host that asks for the block from a control, keeps a field beside
 * it, and unmounts the block when the case asks. */
function EscapeHost() {
  const [pending, setPending] = useState(false)
  const [shown, setShown] = useState(true)
  useEffect(() => {
    escapeBlock = () => setShown(false)
    return () => {
      escapeBlock = null
    }
  }, [])
  return (
    <div>
      <button type="button" onClick={() => setPending(true)}>
        Send code
      </button>
      <input aria-label="Other field" />
      {pending && shown ? (
        <HumanVerification
          scope="phone"
          gate={GATE_SET}
          captcha={IMAGE_CHALLENGE}
          onToken={() => {}}
        />
      ) : null}
    </div>
  )
}

describe('HumanVerification focus in Chromium', () => {
  it('returns focus to the control that asked once the block completes', async () => {
    renderWithProviders(<CompletingHost />)
    const send = screen.getByRole('button', { name: 'Send code' })
    send.focus()
    await browserUserEvent.keyboard('{Enter}')
    const answer = await screen.findByLabelText(CAPTCHA_COPY.answerLabel)
    // The block does not move focus into itself while it waits.
    expect(document.activeElement).toBe(send)
    answer.focus()
    await browserUserEvent.keyboard('12345')
    await browserUserEvent.keyboard('{Enter}')
    await waitFor(() =>
      expect(screen.queryByRole('group')).not.toBeInTheDocument(),
    )
    expect(document.activeElement).toBe(send)
  })

  it('keeps focus on the refresh control through the swap it starts', async () => {
    finishRefresh = null
    renderWithProviders(<RefreshHost />)
    const refresh = screen.getByRole('button', { name: CAPTCHA_COPY.refresh })
    refresh.focus()
    await browserUserEvent.keyboard('{Enter}')
    await waitFor(() => expect(refresh).toHaveAttribute('aria-busy', 'true'))
    expect(document.activeElement).toBe(refresh)
    act(() => {
      finishRefresh?.()
    })
    await waitFor(() => expect(refresh).not.toHaveAttribute('aria-busy'))
    expect(screen.getByLabelText(CAPTCHA_COPY.answerLabel)).toBeInTheDocument()
    expect(document.activeElement).toBe(refresh)
  })

  it('continues at the group container when a failed solve removes the retry that held focus', async () => {
    renderWithProviders(<PowRetryHost />)
    const retry = screen.getByRole('button', { name: COPY.retry })
    retry.focus()
    await browserUserEvent.keyboard('{Enter}')
    await waitFor(() =>
      expect(document.activeElement).toBe(
        screen.getByRole('group', { name: COPY.groupLabel }),
      ),
    )
  })

  it('continues at the group container when a submitted answer leaves the form', async () => {
    renderWithProviders(<CaptchaProofHost />)
    const answer = screen.getByLabelText(CAPTCHA_COPY.answerLabel)
    answer.focus()
    await browserUserEvent.keyboard('1234')
    await browserUserEvent.keyboard('{Enter}')
    await waitFor(() =>
      expect(screen.getByText(COPY.verified)).toBeInTheDocument(),
    )
    expect(document.activeElement).toBe(
      screen.getByRole('group', { name: COPY.groupLabel }),
    )
  })

  it('leaves focus where the viewer moved it when the block goes away', async () => {
    // The viewer asks for the block, steps into it, then moves to a field
    // beside it. When the block unmounts, that field keeps focus: the block
    // hands focus back only while the viewer was still inside it.
    renderWithProviders(<EscapeHost />)
    const send = screen.getByRole('button', { name: 'Send code' })
    send.focus()
    await browserUserEvent.keyboard('{Enter}')
    const answer = await screen.findByLabelText(CAPTCHA_COPY.answerLabel)
    answer.focus()
    await browserUserEvent.keyboard('12345')
    const other = screen.getByLabelText('Other field')
    other.focus()
    expect(document.activeElement).toBe(other)
    // Record every focus the unmount takes. The block must not touch the
    // control that asked once the viewer has moved away, not even for the
    // instant before React restores the pre-commit focus.
    const focused: Element[] = []
    const record = (event: FocusEvent): void => {
      if (event.target instanceof Element) {
        focused.push(event.target)
      }
    }
    document.addEventListener('focusin', record)
    act(() => {
      escapeBlock?.()
    })
    document.removeEventListener('focusin', record)
    await waitFor(() =>
      expect(screen.queryByRole('group')).not.toBeInTheDocument(),
    )
    // This assertion is the criterion: no focus ever landed on the control
    // that asked while the block unmounted. The final position alone does
    // not discriminate, because React restores the pre-commit focus once
    // the unmount is done, so a mutant that grabs the inviter for an
    // instant also ends up back at `other`.
    expect(focused).not.toContain(send)
    expect(document.activeElement).toBe(other)
  })
})
