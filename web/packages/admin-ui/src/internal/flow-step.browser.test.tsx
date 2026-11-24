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

const TOTP_URI =
  'otpauth://totp/Example%20Co:alice%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Example%20Co'

function actionRowOf(action: Element): HTMLElement {
  const row = action.closest<HTMLElement>('[data-action-row]')
  if (row === null) {
    throw new Error('the action is not inside an action row')
  }
  return row
}

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
  // axe's Label in Name measures visible text through canvas, which jsdom answers with null.
  it('settles Label in Name with the FlowStep manual disclosure open', async () => {
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
      expect(snapshots[0]).toBe('')
      await waitFor(() =>
        expect(screen.getByRole('status')).toHaveTextContent(
          enUS.stepUp.passkeyWaiting,
        ),
      )
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
