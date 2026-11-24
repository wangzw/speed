
import { useState } from 'react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  expectNoAxeViolations,
} from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import {
  TotpEnrollmentPanel,
  type TotpEnrollmentPanelProps,
} from './TotpEnrollmentPanel.js'

const URI =
  'otpauth://totp/Example%20Co:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example%20Co'
const SECRET = 'JBSWY3DPEHPK3PXP'
const SECRET_DISPLAY = 'JBSW Y3DP EHPK 3PXP'
const QR_LABEL = 'Scan this code with an authenticator app'
const GUIDANCE = 'Scan the code, then enter the six-digit code.'
const TOGGLE_LABEL = "Can't scan? Enter the key manually"
const SECRET_LABEL = 'Setup key'
const COPY_LABEL = 'Copy setup key'
const COPIED_LABEL = 'Setup key copied'
const COPY_VISIBLE_LABEL = 'Copy'
const CODE_LABEL = 'Six-digit code'
const PARAMETERS = [
  { label: 'Algorithm', value: 'SHA1' },
  { label: 'Digits', value: '6' },
  { label: 'Period (seconds)', value: '30' },
] as const

type HarnessProps = Partial<TotpEnrollmentPanelProps> & {
  readonly primaryLabel?: string
  readonly secondaryLabel?: string
}

function Harness({
  primaryLabel = 'Continue',
  secondaryLabel,
  ...overrides
}: HarnessProps) {
  const [code, setCode] = useState('')
  const secondaryAction: ReactNode =
    secondaryLabel === undefined ? undefined : (
      <button type="button">{secondaryLabel}</button>
    )
  return (
    <TotpEnrollmentPanel
      otpauthUri={URI}
      qrLabel={QR_LABEL}
      guidance={GUIDANCE}
      manualToggleLabel={TOGGLE_LABEL}
      secret={{ value: SECRET, display: SECRET_DISPLAY }}
      secretLabel={SECRET_LABEL}
      copyLabel={COPY_LABEL}
      copiedLabel={COPIED_LABEL}
      copyVisibleLabel={COPY_VISIBLE_LABEL}
      parameters={PARAMETERS}
      codeLabel={CODE_LABEL}
      code={code}
      onCodeChange={setCode}
      secondaryAction={secondaryAction}
      primaryAction={<button type="button">{primaryLabel}</button>}
      {...overrides}
    />
  )
}

function stubClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText },
  })
  return writeText
}

describe('TotpEnrollmentPanel', () => {
  afterEach(() => {
    Reflect.deleteProperty(navigator, 'clipboard')
  })

  it('renders the caller symbol, guidance and code field', () => {
    renderWithProviders(<Harness />)

    expect(screen.getByRole('img', { name: QR_LABEL })).toBeTruthy()
    expect(screen.getByText(GUIDANCE)).toBeTruthy()
    expect(screen.getByLabelText(CODE_LABEL)).toBeTruthy()
    expect(screen.queryByText(/otpauth:\/\//)).toBeNull()
  })

  it('opens the manual entry with only the supplied secret and parameters', async () => {
    const user = userEvent.setup()
    renderWithProviders(<Harness />)

    const toggle = screen.getByRole('button', { name: TOGGLE_LABEL })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText(SECRET_DISPLAY)).toBeNull()

    await user.click(toggle)

    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText(SECRET_DISPLAY)).toBeTruthy()
    expect(screen.getByText(SECRET_LABEL)).toBeTruthy()
    for (const parameter of PARAMETERS) {
      expect(screen.getByText(parameter.label)).toBeTruthy()
      expect(screen.getByText(parameter.value)).toBeTruthy()
    }
    expect(screen.queryByText(/otpauth:\/\//)).toBeNull()
  })

  it('falls back to the raw secret when no display form is supplied', async () => {
    const user = userEvent.setup()
    renderWithProviders(<Harness secret={{ value: SECRET }} />)

    await user.click(screen.getByRole('button', { name: TOGGLE_LABEL }))
    expect(screen.getByText(SECRET)).toBeTruthy()
  })

  it('copies the raw secret and announces the caller wording', async () => {
    const user = userEvent.setup()
    const writeText = stubClipboard()
    renderWithProviders(<Harness />)

    await user.click(screen.getByRole('button', { name: TOGGLE_LABEL }))
    await user.click(screen.getByRole('button', { name: COPY_LABEL }))

    await waitFor(() => expect(writeText).toHaveBeenCalledWith(SECRET))
    expect(await screen.findByText(COPIED_LABEL)).toHaveAttribute(
      'role',
      'status',
    )
  })

  it('keeps the secret selectable and stays silent without a clipboard', async () => {
    const user = userEvent.setup()
    Reflect.deleteProperty(navigator, 'clipboard')
    renderWithProviders(<Harness />)

    await user.click(screen.getByRole('button', { name: TOGGLE_LABEL }))
    const secret = screen.getByText(SECRET_DISPLAY)
    expect(secret.tagName.toLowerCase()).toBe('code')

    await user.click(screen.getByRole('button', { name: COPY_LABEL }))

    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByText(COPIED_LABEL)).toBeNull()
    expect(secret).toBeVisible()
  })

  it('sizes the code field for six digits and asks for a numeric one-time code', () => {
    renderWithProviders(<Harness />)

    const input = screen.getByLabelText(CODE_LABEL)
    expect(input).toHaveAttribute('inputmode', 'numeric')
    expect(input).toHaveAttribute('autocomplete', 'one-time-code')
    expect(input).toHaveAttribute('maxlength', '6')
    expect(input).toHaveAttribute('size', '6')
  })

  it('keeps the controlled code to six digits', async () => {
    const user = userEvent.setup()
    renderWithProviders(<Harness />)

    const input = screen.getByLabelText(CODE_LABEL)
    await user.type(input, '1234567')
    expect(input).toHaveValue('123456')
  })

  it('locks the code field read-only while busy', () => {
    renderWithProviders(<Harness busy />)

    const input = screen.getByLabelText(CODE_LABEL)
    expect(input).toHaveAttribute('readonly')
    input.focus()
    expect(input).toBe(document.activeElement)
    expect(input).not.toBeDisabled()
  })

  it('renders the secondary action before the primary action', () => {
    renderWithProviders(
      <Harness primaryLabel="Continue" secondaryLabel="Cancel" />,
    )

    const secondary = screen.getByRole('button', { name: 'Cancel' })
    const primary = screen.getByRole('button', { name: 'Continue' })
    expect(
      secondary.compareDocumentPosition(primary) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('renders no manual entry without a parsed secret', () => {
    renderWithProviders(<Harness secret={undefined} parameters={undefined} />)

    expect(
      screen.queryByRole('button', { name: TOGGLE_LABEL }),
    ).toBeNull()
    expect(screen.getByLabelText(CODE_LABEL)).toBeTruthy()
  })

  it('renders no symbol without a URI', () => {
    renderWithProviders(<Harness otpauthUri={undefined} guidance={undefined} />)

    expect(screen.queryByRole('img')).toBeNull()
    expect(screen.queryByText(GUIDANCE)).toBeNull()
  })

  it('passes axe with the manual disclosure open', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <>
        <h1>Two-factor setup</h1>
        <Harness />
      </>,
    )

    await user.click(screen.getByRole('button', { name: TOGGLE_LABEL }))
    await expectNoAxeViolations({
      decidedInBrowser: 'totpManualDisclosure',
    })
  })
})
