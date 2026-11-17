/**
 * FlowStep in a real browser: axe's label-content-name-mismatch (WCAG 2.5.3
 * Label in Name) over the manual-entry disclosure.
 *
 * The rule reads a control's visible text through the canvas text
 * measurement API, which jsdom answers with null; the unit suite's scan
 * therefore cannot decide it, and this case is that unit case's deciding
 * verdict (see @speed/test-utils/axe's BROWSER_DETERMINATIONS).
 */
import { describe, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AuthnStep } from '@speed/auth-core'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStep } from './flow-step.js'

const OTPAUTH_URI =
  'otpauth://totp/Example%20Co:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example%20Co'

const TOTP_STEP: AuthnStep = {
  kind: 'webauthn',
  options: { otpauth_uri: OTPAUTH_URI },
}

describe('FlowStep Label in Name in Chromium', () => {
  it('settles Label in Name with the account FlowStep manual disclosure open', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <FlowStep
        step={TOTP_STEP}
        sequence={1}
        busy={false}
        answerStep="answer"
        submitLabel="Continue"
        onSubmit={vi.fn()}
      />,
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.flowStep.totpManualToggle }),
    )
    await expectNoAxeViolations({
      decidedInBrowser: 'flowStepAccountManualDisclosure',
    })
  })
})
