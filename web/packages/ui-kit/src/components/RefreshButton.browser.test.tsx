/**
 * RefreshButton in a real browser: a write starting in the surface's scope
 * must not take focus away from the control that holds it.
 *
 * jsdom implements no focus correction for a focused element that becomes
 * natively disabled, so the old `disabled` control kept focus there; only
 * the browser tier can show the browser dropping it to the document body.
 */

import { describe, expect, it } from 'vitest'
import { useState } from 'react'
import { act, screen } from '@testing-library/react'
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { RefreshButton } from './RefreshButton.js'

/** The surface's host state, driven from the test without a focus-taking control. */
const controls: { setWritePending: (pending: boolean) => void } = {
  setWritePending: () => undefined,
}

function WritePendingHarness() {
  const [writePending, setWritePending] = useState(false)
  controls.setWritePending = setWritePending
  return (
    <RefreshButton
      busy={false}
      writePending={writePending}
      onClick={() => undefined}
    />
  )
}

describe('RefreshButton in Chromium', () => {
  it('keeps focus on the control when a write starts pending', async () => {
    renderWithProviders(<WritePendingHarness />, { language: 'en-US' })

    const refresh = await screen.findByRole('button', {
      name: enUS.refreshButton.label,
    })
    refresh.focus()
    expect(refresh).toHaveFocus()

    // A write in the surface's scope begins: the control becomes
    // unavailable. A natively disabled control loses the focus it had.
    act(() => {
      controls.setWritePending(true)
    })
    expect(refresh).toHaveFocus()
  })
})
