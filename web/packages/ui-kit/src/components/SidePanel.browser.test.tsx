
import { describe, expect, it } from 'vitest'
import { useState } from 'react'
import { act, screen } from '@testing-library/react'
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { SidePanel } from './SidePanel.js'

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

    act(() => {
      controls.setBusy(true)
    })
    expect(close).toHaveFocus()
  })
})
