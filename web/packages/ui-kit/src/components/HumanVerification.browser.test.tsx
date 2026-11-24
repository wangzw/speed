import { useEffect, useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor } from '@testing-library/react'
import { useTheme } from '@mui/material/styles'
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
    await waitFor(() => expect(range.max).toBe('272'))
    expect(range.type).toBe('range')
    range.focus()
    expect(document.activeElement).toBe(range)
    await browserUserEvent.keyboard('{ArrowRight}')
    expect(range.value).toBe('1')
    await browserUserEvent.keyboard('{End}')
    expect(range.value).toBe('272')
    await browserUserEvent.keyboard('{Enter}')
    expect(onToken).toHaveBeenCalledWith('challenge-1.272')
  })

  it('spans exactly the image frame in a wide container', async () => {
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
    const group = screen.getByRole('group')
    expect(group.getBoundingClientRect().width).toBeGreaterThan(320)
    await waitFor(() =>
      expect(background.getBoundingClientRect().width).toBe(320),
    )
    const frameRect = frame.getBoundingClientRect()
    const rangeRect = range.getBoundingClientRect()
    expect(rangeRect.width).toBeCloseTo(frameRect.width, 1)
    expect(rangeRect.width).toBeLessThan(group.getBoundingClientRect().width)
  })

  it('keeps the piece aligned at a narrow rendered width', async () => {
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
    expect(background.getAttribute('src')).toBe(CHALLENGE.background_url)
    expect(piece.getAttribute('src')).toBe(CHALLENGE.piece_url)
    const range = screen.getByLabelText(
      CAPTCHA_COPY.sliderLabel,
    ) as HTMLInputElement
    await waitFor(() => expect(range.max).toBe('272'))
    await waitFor(() =>
      expect(background.getBoundingClientRect().width).toBeLessThan(320),
    )
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
    expect(pieceRect.width).toBeCloseTo(48 * scale, 0)
    const naturalOffset = (pieceRect.left - backgroundRect.left) / scale
    expect(Math.abs(naturalOffset - 272)).toBeLessThanOrEqual(2)

    await browserUserEvent.keyboard('{Enter}')
    expect(onToken).toHaveBeenCalledWith('challenge-1.272')
  })
})

type Rgb = readonly [number, number, number]

function parseRgb(value: string): Rgb {
  const match = /rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/.exec(value)
  if (match === null) {
    throw new Error(`unexpected computed colour: ${value}`)
  }
  return [Number(match[1]), Number(match[2]), Number(match[3])]
}

function linear(channel: number): number {
  const value = channel / 255
  return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
}

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

function PrimaryProbe() {
  const theme = useTheme()
  return (
    <span
      data-testid="primary-probe"
      style={{ color: theme.palette.primary.main }}
    />
  )
}

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

        const primary = screen.getByTestId('primary-probe')
        const accent = parseRgb(getComputedStyle(range).accentColor)
        expect(accent).toEqual(parseRgb(getComputedStyle(primary).color))

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

let finishRefresh: (() => void) | null = null

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

let escapeBlock: (() => void) | null = null

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
    expect(focused).not.toContain(send)
    expect(document.activeElement).toBe(other)
  })
})
