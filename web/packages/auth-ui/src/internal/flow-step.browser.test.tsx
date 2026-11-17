/**
 * FlowStepFields in a real browser: axe's label-content-name-mismatch (WCAG
 * 2.5.3 Label in Name) over the manual-entry disclosure.
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
import { FlowStepFields } from './flow-step.js'
import type { OpenFlowState } from './flow.js'

const TOTP_URI =
  'otpauth://totp/Example%20Co:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example%20Co'

function step(next: AuthnStep): OpenFlowState {
  return { seq: 1, state: 'enroll', next }
}

describe('FlowStepFields Label in Name in Chromium', () => {
  it('settles Label in Name with the FlowStepFields manual-entry disclosure open', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <>
        <h1>Two-factor setup</h1>
        <FlowStepFields
          step={step({ kind: 'webauthn', options: { otpauth_uri: TOTP_URI } })}
          busy={false}
          onAnswer={vi.fn()}
          onUnsupported={vi.fn()}
        />
      </>,
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.signInFlow.totpManualToggle }),
    )
    await expectNoAxeViolations({
      decidedInBrowser: 'flowStepFieldsManualDisclosure',
    })
  })
})
