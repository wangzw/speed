import { useState } from 'react'
import type { ReactElement } from 'react'
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { fireEvent } from '@testing-library/react'
import axe from 'axe-core'
import { page, userEvent } from 'vitest/browser'
import { ImpersonationBanner } from './ImpersonationBanner.js'
import type { ImpersonationBannerState } from './ImpersonationBanner.js'
import { renderWithProviders } from '../test-utils/render.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const END_LABEL = enUS.impersonation.end
const ENDING_LABEL = enUS.impersonation.ending
const ENDED_TEXT = enUS.impersonation.ended

function renderBanner(ui: ReactElement) {
  return renderWithProviders(ui, { language: 'en-US' })
}

const SUBJECT = 'Ada Lovelace'
const TENANT = 'tenant-1'
const EXPIRES_AT = new Date('2026-10-18T12:30:00Z')

async function scrollableRegionViolations(): Promise<readonly axe.Result[]> {
  const result = await axe.run(document, {
    runOnly: { type: 'rule', values: ['scrollable-region-focusable'] },
  })
  expect(result.incomplete).toEqual([])
  return result.violations
}

describe('ImpersonationBanner end control in Chromium', () => {
  it('keeps focus on the end control through the attempt and a refusal', async () => {
    await page.viewport(900, 900)

    function Host() {
      const [pending, setPending] = useState(false)
      const [failure, setFailure] = useState<string | null>(null)
      const [attempts, setAttempts] = useState(0)
      return (
        <>
          <ImpersonationBanner
            impersonation={{ subject: SUBJECT }}
            pending={pending}
            failureCode={failure}
            onEnd={() => {
              setAttempts((count) => count + 1)
              setPending(true)
            }}
          />
          {/* The host's own settlement control, after the banner in DOM
              order so the end control is the first Tab stop. */}
          <button
            type="button"
            onClick={() => {
              setPending(false)
              setFailure('authn.unavailable')
            }}
          >
            fail
          </button>
          <output data-testid="attempts">{attempts}</output>
        </>
      )
    }

    renderBanner(<Host />)
    const end = screen.getByRole('button', { name: END_LABEL })
    end.focus()
    await userEvent.click(end)

    expect(end.textContent).toBe(ENDING_LABEL)
    expect(document.activeElement).toBe(end)
    expect(end).toHaveAttribute('aria-disabled', 'true')
    expect(end).toHaveAttribute('aria-busy', 'true')

    const fail = screen.getByRole('button', { name: 'fail' })
    fail.focus()
    await userEvent.keyboard('{Shift>}{Tab}{/Shift}')
    expect(document.activeElement).toBe(end)

    fireEvent.click(fail)
    await waitFor(() => {
      expect(end).not.toHaveAttribute('aria-busy', 'true')
    })
    expect(end.textContent).toBe(END_LABEL)
    expect(end).not.toHaveAttribute('aria-disabled', 'true')
    expect(document.activeElement).toBe(end)
    expect(screen.getByTestId('attempts').textContent).toBe('1')
    await userEvent.click(end)
    expect(screen.getByTestId('attempts').textContent).toBe('2')
  })
})

describe('ImpersonationBanner live region in Chromium', () => {
  it('mounts an empty live region that is not a keyboard stop while idle', async () => {
    await page.viewport(900, 900)
    renderBanner(
      <ImpersonationBanner impersonation={null} onEnd={() => undefined} />,
    )

    const region = screen.getByRole('status')
    expect(region).toBeInTheDocument()
    expect(region.textContent ?? '').toBe('')
    expect(await scrollableRegionViolations()).toEqual([])
  })

  it('announces the acting-as sentence without becoming a scroll stop, and Tab reaches the end control', async () => {
    await page.viewport(900, 900)
    renderBanner(
      <ImpersonationBanner
        impersonation={{
          subject: SUBJECT,
          tenant: TENANT,
          expiresAt: EXPIRES_AT,
        }}
        onEnd={() => undefined}
        onDismiss={() => undefined}
      />,
    )

    await waitFor(() => {
      expect(screen.getByRole('status').textContent).toContain(SUBJECT)
    })
    expect(await scrollableRegionViolations()).toEqual([])

    ;(document.activeElement as HTMLElement | null)?.blur()
    document.body.focus()
    await userEvent.keyboard('{Tab}')

    expect(document.activeElement?.textContent).toBe(END_LABEL)
    expect(document.activeElement).not.toBe(screen.getByRole('status'))
  })

  it('keeps the live region mounted and quiet-looking after the impersonation disappears', async () => {
    await page.viewport(900, 900)

    function Host() {
      const [impersonation, setImpersonation] =
        useState<ImpersonationBannerState | null>({ subject: SUBJECT })
      return (
        <ImpersonationBanner
          impersonation={impersonation}
          onEnd={() => setImpersonation(null)}
        />
      )
    }

    renderBanner(<Host />)
    await waitFor(() => {
      expect(screen.getByRole('status').textContent).toContain(SUBJECT)
    })

    await userEvent.click(screen.getByRole('button', { name: END_LABEL }))

    await waitFor(() => {
      expect(screen.getByRole('status').textContent).toBe(ENDED_TEXT)
    })
    expect(await scrollableRegionViolations()).toEqual([])

    ;(document.activeElement as HTMLElement | null)?.blur()
    document.body.focus()
    await userEvent.keyboard('{Tab}')
    expect(document.activeElement).not.toBe(screen.getByRole('status'))
  })

  it('keeps the settlement skin clipped until its text is painted', async () => {
    await page.viewport(900, 900)
    const SETTLED = 'The session ended'
    function Host() {
      const [settled, setSettled] = useState(false)
      return (
        <>
          <button type="button" onClick={() => setSettled(true)}>
            settle
          </button>
          <ImpersonationBanner
            impersonation={null}
            onEnd={() => undefined}
            successMessage={settled ? SETTLED : null}
          />
        </>
      )
    }

    renderBanner(<Host />)
    const status = screen.getByRole('status')
    const alert = status.closest('.MuiAlert-root') as HTMLElement
    expect(alert).not.toBeNull()
    const skin = alert.parentElement as HTMLElement
    expect(status.textContent).toBe('')
    expect(getComputedStyle(skin).clipPath).toBe('inset(50%)')

    fireEvent.click(screen.getByRole('button', { name: 'settle' }))
    expect(status.textContent).toBe('')
    expect(getComputedStyle(skin).clipPath).toBe('inset(50%)')

    await waitFor(() => {
      expect(status.textContent).toBe(SETTLED)
    })
    expect(getComputedStyle(skin).clipPath).toBe('none')
  })
})
