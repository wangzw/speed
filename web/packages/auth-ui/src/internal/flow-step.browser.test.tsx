/**
 * FlowStepFields in a real browser: axe's label-content-name-mismatch (WCAG
 * 2.5.3 Label in Name) over the manual-entry disclosure.
 *
 * The rule reads a control's visible text through the canvas text
 * measurement API, which jsdom answers with null; the unit suite's scan
 * therefore cannot decide it, and this case is that unit case's deciding
 * verdict (see @speed/test-utils/axe's BROWSER_DETERMINATIONS).
 */
import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import type { AuthnStep } from '@speed/auth-core'
import enUS from '../locales/en-US.json' with { type: 'json' }
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

describe('FlowStepFields passkey waiting in Chromium', () => {
  it('inserts the waiting region empty and fills it a frame later', async () => {
    // The waiting phase's region and its cancellation row mount in one
    // commit. A MutationObserver records the region's text in every batch
    // after it appears, so the empty birth frame and the later fill are
    // observed facts.
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    const original = Object.getOwnPropertyDescriptor(
      window.navigator,
      'credentials',
    )
    Object.defineProperty(window.navigator, 'credentials', {
      value: { get, create: vi.fn() },
      configurable: true,
    })
    const snapshots: string[] = []
    const observer = new MutationObserver(() => {
      const region = document.querySelector('[role="status"]')
      if (region !== null) {
        snapshots.push(region.textContent ?? '')
      }
    })
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      characterData: true,
    })
    try {
      await page.viewport(900, 700)
      renderWithProviders(
        <FlowStepFields
          step={step({
            kind: 'webauthn',
            options: { challenge: 'Y2hhbGxlbmdl' },
          })}
          busy={false}
          onAnswer={vi.fn()}
          onUnsupported={vi.fn()}
        />,
        { language: 'en-US' },
      )

      await waitFor(() => expect(snapshots.length).toBeGreaterThan(0))
      // The batch that reported the region's insertion read it empty.
      expect(snapshots[0]).toBe('')
      await waitFor(() =>
        expect(screen.getByRole('status')).toHaveTextContent(
          enUS.signInFlow.passkeyWaiting,
        ),
      )
      expect(snapshots).toContain(enUS.signInFlow.passkeyWaiting)
    } finally {
      observer.disconnect()
      if (original === undefined) {
        delete (window.navigator as { credentials?: unknown }).credentials
      } else {
        Object.defineProperty(window.navigator, 'credentials', original)
      }
    }
  })
})
