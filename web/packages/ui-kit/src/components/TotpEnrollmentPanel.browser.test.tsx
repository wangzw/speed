import { useState } from 'react'
import type { ReactNode } from 'react'
import { describe, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { TotpEnrollmentPanel } from './TotpEnrollmentPanel.js'

const PANEL_PROPS = {
  otpauthUri:
    'otpauth://totp/Example%20Co:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example%20Co',
  qrLabel: 'Scan this code with an authenticator app',
  guidance: 'Scan the code, then enter the six-digit code.',
  manualToggleLabel: "Can't scan? Enter the key manually",
  secret: { value: 'JBSWY3DPEHPK3PXP', display: 'JBSW Y3DP EHPK 3PXP' },
  secretLabel: 'Setup key',
  copyLabel: 'Copy setup key',
  copiedLabel: 'Setup key copied',
  copyVisibleLabel: 'Copy',
  parameters: [
    { label: 'Algorithm', value: 'SHA1' },
    { label: 'Digits', value: '6' },
    { label: 'Period (seconds)', value: '30' },
  ] as const,
  codeLabel: 'Six-digit code',
}

function Harness(): ReactNode {
  const [code, setCode] = useState('')
  return (
    <TotpEnrollmentPanel
      {...PANEL_PROPS}
      code={code}
      onCodeChange={setCode}
      primaryAction={<button type="button">Continue</button>}
    />
  )
}

describe('TotpEnrollmentPanel Label in Name in Chromium', () => {
  it('settles Label in Name with the manual disclosure open', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <>
        <h1>Two-factor setup</h1>
        <Harness />
      </>,
    )

    await user.click(
      screen.getByRole('button', {
        name: PANEL_PROPS.manualToggleLabel,
      }),
    )
    await screen.findByText(PANEL_PROPS.secretLabel)
    await expectNoAxeViolations({
      decidedInBrowser: 'totpManualDisclosure',
    })
  })
})
