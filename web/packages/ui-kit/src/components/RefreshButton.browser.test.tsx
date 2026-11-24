
import { describe, expect, it } from 'vitest'
import { useState } from 'react'
import { act, screen } from '@testing-library/react'
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { RefreshButton } from './RefreshButton.js'

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

    act(() => {
      controls.setWritePending(true)
    })
    expect(refresh).toHaveFocus()
  })
})
