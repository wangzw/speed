/**
 * TotpQrCode: the SVG symbol an enrolment panel shows for its URI.
 *
 * The cases pin the contract the panel relies on: the image is exposed
 * as one accessible image under the caller's own label, the same URI
 * always renders the same symbol (so a re-render does not flicker), the
 * symbol carries a quiet zone and scales to the requested size, and an
 * empty or unrepresentable URI renders nothing rather than throwing into
 * the tree.
 */

import { describe, expect, it } from 'vitest'
import { renderWithProviders } from '../../test-utils/render.js'
import { TotpQrCode } from './TotpQrCode.js'

const URI =
  'otpauth://totp/Example%20Co:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example%20Co'
const LABEL = 'Scan this code with an authenticator app'

describe('TotpQrCode', () => {
  it('renders one image under the caller-supplied accessible name', () => {
    const { getByRole, container } = renderWithProviders(
      <TotpQrCode uri={URI} label={LABEL} />,
    )

    const image = getByRole('img', { name: LABEL })
    expect(image.tagName.toLowerCase()).toBe('svg')
    expect(container.querySelectorAll('svg')).toHaveLength(1)
  })

  it('renders the same symbol for the same URI', () => {
    const first = renderWithProviders(<TotpQrCode uri={URI} label={LABEL} />)
    const firstSvg = first.getByRole('img', { name: LABEL }).outerHTML
    first.unmount()

    const second = renderWithProviders(<TotpQrCode uri={URI} label={LABEL} />)
    const secondSvg = second.getByRole('img', { name: LABEL }).outerHTML

    expect(secondSvg).toBe(firstSvg)
  })

  it('draws dark modules inside a four-module quiet zone', () => {
    const { getByRole } = renderWithProviders(
      <TotpQrCode uri={URI} label={LABEL} />,
    )

    const image = getByRole('img', { name: LABEL })
    const [, , viewBoxWidth, viewBoxHeight] = (
      image.getAttribute('viewBox') ?? ''
    ).split(' ')
    expect(viewBoxWidth).toBe(viewBoxHeight)
    const edge = Number(viewBoxWidth)

    const path = image.querySelector('path')
    expect(path).not.toBeNull()
    const starts = [...(path?.getAttribute('d') ?? '').matchAll(/M(\d+) (\d+)/g)]
    expect(starts.length).toBeGreaterThan(0)
    for (const start of starts) {
      const x = Number(start[1])
      const y = Number(start[2])
      expect(x).toBeGreaterThanOrEqual(4)
      expect(y).toBeGreaterThanOrEqual(4)
      expect(x).toBeLessThan(edge - 4)
      expect(y).toBeLessThan(edge - 4)
    }
  })

  it('defaults to a 192-pixel symbol and follows an explicit size', () => {
    const first = renderWithProviders(<TotpQrCode uri={URI} label={LABEL} />)
    expect(first.getByRole('img', { name: LABEL }).getAttribute('width')).toBe(
      '192',
    )
    first.unmount()

    const second = renderWithProviders(
      <TotpQrCode uri={URI} label={LABEL} size={320} />,
    )
    const image = second.getByRole('img', { name: LABEL })
    expect(image.getAttribute('width')).toBe('320')
    expect(image.getAttribute('height')).toBe('320')
  })

  it('renders nothing for an empty URI', () => {
    const { container } = renderWithProviders(
      <TotpQrCode uri="   " label={LABEL} />,
    )

    expect(container.querySelector('svg')).toBeNull()
  })

  it('renders nothing when the encoder cannot represent the URI', () => {
    const tooLong = `otpauth://totp/alice?secret=${'A'.repeat(6000)}`
    const { container } = renderWithProviders(
      <TotpQrCode uri={tooLong} label={LABEL} />,
    )

    expect(container.querySelector('svg')).toBeNull()
  })
})
