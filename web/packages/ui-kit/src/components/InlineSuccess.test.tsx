/**
 * InlineSuccess contract: one polite status region, mounted whether or not
 * there is anything to confirm, visually silent (clipped, never
 * display:none -- a hidden region would not be live) while the message is
 * null and filled when it arrives. The region is never keyed to its
 * message: the same node is filled, cleared and filled again.
 */

import { useState } from 'react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { renderWithProviders } from '../../test-utils/render.js'
import { InlineSuccess } from './InlineSuccess.js'

/** A host holding the message, so changes re-render the banner in place. */
function SuccessHost() {
  const [message, setMessage] = useState<string | null>(null)
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          setMessage('Setting saved')
        }}
      >
        fill
      </button>
      <button
        type="button"
        onClick={() => {
          setMessage(null)
        }}
      >
        clear
      </button>
      <InlineSuccess message={message} />
    </div>
  )
}

const CLIPPED = {
  position: 'absolute',
  width: '1px',
  height: '1px',
} as const

describe('InlineSuccess', () => {
  it('mounts a status region carrying the message', () => {
    const { getByRole } = renderWithProviders(<InlineSuccess message="Saved" />)
    expect(getByRole('status')).toHaveTextContent('Saved')
  })

  it('renders the success severity', () => {
    const { getByRole } = renderWithProviders(<InlineSuccess message="Saved" />)
    expect(getByRole('status').className).toMatch(/MuiAlert-colorSuccess/)
  })

  it('mounts the region empty and clipped when there is no message', () => {
    const { getByRole } = renderWithProviders(<InlineSuccess message={null} />)
    const status = getByRole('status')
    expect(status).toHaveTextContent('')
    // Still in the accessibility tree -- clipped, never display:none: a
    // display:none live region announces nothing when its text later
    // changes. The clip is the 1x1 absolute box, not a painted banner.
    expect(status).toBeInTheDocument()
    expect(status).not.toHaveStyle({ display: 'none' })
    expect(status).toHaveStyle(CLIPPED)
  })

  it('drops the clip and fills its width when the message arrives', () => {
    const { getByRole } = renderWithProviders(<InlineSuccess message="Saved" />)
    const status = getByRole('status')
    expect(status).not.toHaveStyle(CLIPPED)
    expect(status).toHaveStyle({ width: '100%' })
  })

  it('renders an empty string literally as a banner, not as silent', () => {
    // The design ruling holds `''` and `null` apart: only `null` means
    // "nothing to confirm" and clips the region. An empty string is a
    // success carrying no text -- a painted, empty banner.
    const { getByRole } = renderWithProviders(<InlineSuccess message="" />)
    const status = getByRole('status')
    expect(status).toHaveTextContent('')
    expect(status).not.toHaveStyle(CLIPPED)
    expect(status).toHaveStyle({ width: '100%' })
  })

  it('fills and clears the same mounted region as the message changes', async () => {
    const view = renderWithProviders(<SuccessHost />)
    const silent = view.getByRole('status')
    expect(silent).toHaveTextContent('')
    expect(silent).toHaveStyle(CLIPPED)

    await userEvent.click(view.getByRole('button', { name: 'fill' }))
    // One region throughout (never a second born with its text), now
    // carrying the message -- the change is what a screen reader announces.
    expect(view.getAllByRole('status')).toHaveLength(1)
    expect(view.getByRole('status')).toBe(silent)
    expect(silent).toHaveTextContent('Setting saved')
    expect(silent).not.toHaveStyle(CLIPPED)

    await userEvent.click(view.getByRole('button', { name: 'clear' }))
    expect(view.getByRole('status')).toBe(silent)
    expect(silent).toHaveTextContent('')
    expect(silent).toHaveStyle(CLIPPED)
  })
})
