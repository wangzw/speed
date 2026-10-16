/**
 * InlineSuccess contract: one polite status region, mounted whether or not
 * there is anything to confirm, visually silent (clipped, never
 * display:none -- a hidden region would not be live) while the message is
 * null and filled when it arrives.
 */

import { describe, expect, it } from 'vitest'
import { renderWithProviders } from '../../test-utils/render.js'
import { InlineSuccess } from './InlineSuccess.js'

describe('InlineSuccess', () => {
  it('mounts a status region carrying the message', () => {
    const { getByRole } = renderWithProviders(<InlineSuccess message="Saved" />)
    expect(getByRole('status')).toHaveTextContent('Saved')
  })

  it('mounts the region empty (and clipped) when there is no message', () => {
    const { getByRole } = renderWithProviders(<InlineSuccess message={null} />)
    const status = getByRole('status')
    expect(status).toHaveTextContent('')
    // Still in the accessibility tree -- clipped, never display:none: a
    // display:none live region announces nothing when its text later
    // changes.
    expect(status).toBeInTheDocument()
    expect(status).not.toHaveStyle({ display: 'none' })
  })

  it('announces a message that fills a region already in the tree', () => {
    const { getByRole, queryAllByRole, rerender } = renderWithProviders(
      <InlineSuccess message={null} />,
    )
    expect(getByRole('status')).toHaveTextContent('')
    rerender(<InlineSuccess message="Setting saved" />)
    // One region throughout (never a second born with its text), now
    // carrying the message -- the change is what a screen reader announces.
    expect(queryAllByRole('status')).toHaveLength(1)
    expect(getByRole('status')).toHaveTextContent('Setting saved')
  })
})
