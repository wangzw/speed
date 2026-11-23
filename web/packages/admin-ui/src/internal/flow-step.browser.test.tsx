/**
 * FlowStep's action rows in a real browser: a step's single primary action
 * closes the row at the right edge, whether it belongs to the complete
 * branch, the challenge form or the TOTP enrolment panel. Before the rows
 * moved to the shared primitive a lone primary kept the step's left
 * alignment; jsdom lays nothing out, so only this tier can see where the
 * button lands.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import enUS from '../locales/en-US.json' with { type: 'json' }
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStep } from './flow-step.js'

const OTPAUTH_URI = 'otpauth://totp/speed:ada?issuer=speed'

/** A URI carrying a secret, so the panel offers the manual disclosure. */
const TOTP_URI =
  'otpauth://totp/Example%20Co:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example%20Co'

/** The row an action belongs to, found by the row's own marker. */
function actionRowOf(action: Element): HTMLElement {
  const row = action.closest<HTMLElement>('[data-action-row]')
  if (row === null) {
    throw new Error('the action is not inside an action row')
  }
  return row
}

/** The row's right content edge (the row may carry its own padding). */
function contentRightOf(row: HTMLElement): number {
  const rowBox = row.getBoundingClientRect()
  return rowBox.right - Number.parseFloat(getComputedStyle(row).paddingRight)
}

async function expectClosedOnTheRight(button: HTMLElement): Promise<void> {
  await waitFor(() => {
    expect(button.getBoundingClientRect().width).toBeGreaterThan(0)
  })
  const row = actionRowOf(button)
  expect(
    Math.abs(button.getBoundingClientRect().right - contentRightOf(row)),
  ).toBeLessThanOrEqual(1)
}

describe('FlowStep action rows in Chromium', () => {
  it('closes the complete step primary on the right', async () => {
    await page.viewport(900, 700)
    renderWithProviders(
      <FlowStep
        step={{ kind: 'complete' }}
        sequence={1}
        answerStep="answer"
        submitLabel="Continue"
        onSubmit={vi.fn()}
      />,
      { language: 'en-US' },
    )

    await expectClosedOnTheRight(
      await screen.findByRole('button', { name: 'Continue' }),
    )
  })

  it('closes the challenge step primary on the right', async () => {
    await page.viewport(900, 700)
    renderWithProviders(
      <FlowStep
        step={{ kind: 'challenge', fields: [{ name: 'code', kind: 'code' }] }}
        sequence={1}
        answerStep="answer"
        submitLabel="Continue"
        onSubmit={vi.fn()}
      />,
      { language: 'en-US' },
    )

    await expectClosedOnTheRight(
      await screen.findByRole('button', { name: 'Continue' }),
    )
  })

  it('closes the TOTP panel primary on the right', async () => {
    await page.viewport(900, 700)
    renderWithProviders(
      <FlowStep
        step={{
          kind: 'webauthn',
          options: { otpauth_uri: OTPAUTH_URI },
        }}
        sequence={1}
        answerStep="answer"
        submitLabel="Confirm"
        onSubmit={vi.fn()}
      />,
      { language: 'en-US' },
    )

    const confirm = await screen.findByRole('button', { name: 'Confirm' })
    await expectClosedOnTheRight(confirm)
  })
})

describe('FlowStep Label in Name in Chromium', () => {
  it('settles Label in Name with the FlowStep manual disclosure open', async () => {
    // axe's Label in Name rule measures visible text through the canvas API,
    // which jsdom answers with null; this case is the verdict the unit
    // suite's scan cannot reach (see @speed/test-utils/axe).
    const user = userEvent.setup()
    renderWithProviders(
      <>
        <h1>Verify it's you</h1>
        <FlowStep
          step={{ kind: 'webauthn', options: { otpauth_uri: TOTP_URI } }}
          sequence={1}
          answerStep="answer"
          submitLabel="Continue"
          onSubmit={vi.fn()}
        />
      </>,
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.stepUp.totpManualToggle }),
    )
    await expectNoAxeViolations({
      decidedInBrowser: 'flowStepManualDisclosure',
    })
  })
})

describe('FlowStep waiting announcement in Chromium', () => {
  it('inserts the waiting region empty and fills it a frame later', async () => {
    // The waiting phase's region and its cancellation row mount in one
    // commit. A MutationObserver records the region's text in every batch
    // after it appears, so the empty birth frame and the later fill are
    // observed facts rather than one snapshot taken when the case chose to
    // look.
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
        <FlowStep
          step={{ kind: 'webauthn', options: { challenge: 'Y2hhbGxlbmdl' } }}
          sequence={1}
          answerStep="answer"
          submitLabel="Continue"
          onSubmit={vi.fn()}
        />,
        { language: 'en-US' },
      )

      await waitFor(() => expect(snapshots.length).toBeGreaterThan(0))
      // The batch that reported the region's insertion read it empty.
      expect(snapshots[0]).toBe('')
      await waitFor(() =>
        expect(screen.getByRole('status')).toHaveTextContent(
          enUS.stepUp.passkeyWaiting,
        ),
      )
      // Some later batch carried the text, so the change reached an
      // already-inserted region rather than being born with it.
      expect(snapshots).toContain(enUS.stepUp.passkeyWaiting)
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
