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
