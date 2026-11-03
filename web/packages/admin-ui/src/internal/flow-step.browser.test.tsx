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
import { page } from 'vitest/browser'
import { renderWithProviders } from '../../test-utils/render.js'
import { FlowStep } from './flow-step.js'

const OTPAUTH_URI = 'otpauth://totp/speed:ada?issuer=speed'

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
