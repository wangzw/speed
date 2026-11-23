/**
 * SidePanel in a real browser: turning the panel busy must not take focus
 * away from the close button that holds it.
 *
 * jsdom implements no focus correction for a focused element that becomes
 * natively disabled, so the old `disabled` close button kept focus there;
 * only the browser tier can show the browser dropping it to the document
 * body and the panel's focus trap then pulling it back to the paper.
 */

import { describe, expect, it } from 'vitest'
import { useState } from 'react'
import { act, screen } from '@testing-library/react'
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { SidePanel } from './SidePanel.js'

/** The panel's host state, driven from the test without a focus-taking control. */
const controls: { setBusy: (busy: boolean) => void } = {
  setBusy: () => undefined,
}

function BusyHarness() {
  const [busy, setBusy] = useState(false)
  controls.setBusy = setBusy
  return (
    <SidePanel
      open
      title="Account details"
      busy={busy}
      onClose={() => undefined}
      onExited={() => undefined}
    >
      <p>Panel body</p>
    </SidePanel>
  )
}

describe('SidePanel in Chromium', () => {
  it('keeps focus on the close button when the panel becomes busy', async () => {
    renderWithProviders(<BusyHarness />, { language: 'en-US' })

    const close = await screen.findByRole('button', {
      name: enUS.sidePanel.closeLabel,
    })
    close.focus()
    expect(close).toHaveFocus()

    // The panel turns busy while its close button holds focus. A natively
    // disabled close button loses that focus to the body, and the panel's
    // focus trap then moves it back to the paper.
    act(() => {
      controls.setBusy(true)
    })
    expect(close).toHaveFocus()
  })
})
