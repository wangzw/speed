/**
 * ImpersonationBanner in a real browser: the live region is not a
 * scrollable keyboard stop, and focus reaches the banner's controls.
 *
 * The component mounts one visually hidden live region for its whole
 * life. MUI's alert message slot carries `overflow: auto`; squeezed into
 * the 1x1 clipped box a browser can treat it as a scroll container and
 * put it in the Tab order, which jsdom's layout-free DOM never shows.
 * These cases assert the browser fact directly -- axe's
 * `scrollable-region-focusable` rule finds nothing, and the first Tab
 * reaches the banner's own end control rather than a hidden region --
 * in the three states the region lives through: idle, announcing, and
 * after the impersonation disappears.
 */
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

/** The shipped en-US strings the cases assert on. */
const END_LABEL = enUS.impersonation.end
const ENDED_TEXT = enUS.impersonation.ended

/** Render in en-US so the live region's measured text is Latin-only. */
function renderBanner(ui: ReactElement) {
  return renderWithProviders(ui, { language: 'en-US' })
}

const SUBJECT = 'Ada Lovelace'
const TENANT = 'tenant-1'
const EXPIRES_AT = new Date('2026-10-18T12:30:00Z')

/** Runs only the focusability rule the tier is about, against the real
 * layout: the full page-oriented set would fail an isolated component
 * render for reasons this tier does not own. */
async function scrollableRegionViolations(): Promise<readonly axe.Result[]> {
  const result = await axe.run(document, {
    runOnly: { type: 'rule', values: ['scrollable-region-focusable'] },
  })
  expect(result.incomplete).toEqual([])
  return result.violations
}

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

    // The announcement is committed a tick after mount; wait for the
    // event, not the clock.
    await waitFor(() => {
      expect(screen.getByRole('status').textContent).toContain(SUBJECT)
    })
    expect(await scrollableRegionViolations()).toEqual([])

    ;(document.activeElement as HTMLElement | null)?.blur()
    document.body.focus()
    await userEvent.keyboard('{Tab}')

    expect(document.activeElement?.textContent).toBe(END_LABEL)
    // The hidden live region precedes the visible banner in DOM order and
    // must not have taken that Tab stop.
    expect(document.activeElement).not.toBe(screen.getByRole('status'))
  })

  it('keeps the live region mounted and quiet-looking after the impersonation disappears', async () => {
    await page.viewport(900, 900)

    // The host shape the component is designed for: the impersonation is
    // host state, and ending it clears that state. Clicking the banner's
    // own control drives the real end edge.
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
    // No visible control remains; the hidden region still must not enter
    // the tab order.
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
    // The element carrying the skin is the Box the component styles around
    // the alert; it is the one that clips or paints.
    const skin = alert.parentElement as HTMLElement
    expect(status.textContent).toBe('')
    expect(getComputedStyle(skin).clipPath).toBe('inset(50%)')

    // The commit set the settlement. The region is still empty for two
    // animation frames, and the skin must not paint an empty box in them.
    fireEvent.click(screen.getByRole('button', { name: 'settle' }))
    expect(status.textContent).toBe('')
    expect(getComputedStyle(skin).clipPath).toBe('inset(50%)')

    await waitFor(() => {
      expect(status.textContent).toBe(SETTLED)
    })
    // The skin unclips in the same frame the text is painted.
    expect(getComputedStyle(skin).clipPath).toBe('none')
  })
})
