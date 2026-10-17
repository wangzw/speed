/**
 * WriteFeedbackRegion contract: one slot rendered as the scope's result
 * area -- a failure code through the caller's resolver and the success
 * message through the always-mounted status region, with `sx` landing on
 * the region's own root. An empty slot renders no visible feedback while
 * the status region stays in the tree for the next message.
 */

import { describe, expect, it } from 'vitest'
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { WriteFeedbackRegion } from './WriteFeedbackRegion.js'

const resolve = (code: string): string => `resolved:${code}`

describe('WriteFeedbackRegion', () => {
  it('renders a success message in the polite status region', async () => {
    const view = renderWithProviders(
      <div>
        <h1>Page title</h1>
        <WriteFeedbackRegion
          outcome={{ kind: 'success', message: 'Tenant renamed' }}
          resolve={resolve}
        />
      </div>,
    )
    expect(view.getByRole('status')).toHaveTextContent('Tenant renamed')
    expect(view.queryByRole('alert')).toBeNull()
    await expectNoAxeViolations()
  })

  it('renders a failure code through the resolver as an alert', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'failure', code: 'tenant.not_found' }}
        resolve={resolve}
      />,
    )
    expect(view.getByRole('alert')).toHaveTextContent(
      'resolved:tenant.not_found',
    )
    // The success region stays mounted (empty and clipped) so the next
    // message fills a region the screen reader already knows.
    expect(view.getByRole('status')).toHaveTextContent('')
  })

  it('renders no visible feedback for an empty slot', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion outcome={null} resolve={resolve} />,
    )
    expect(view.container).toHaveTextContent('')
    expect(view.queryByRole('alert')).toBeNull()
    // The always-mounted region is still present for the next message.
    expect(view.getByRole('status')).toBeInTheDocument()
  })

  it('applies sx to its own root container', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'success', message: 'Saved' }}
        resolve={resolve}
        sx={{ marginTop: 2 }}
      />,
    )
    const root = view.container.firstElementChild
    expect(root).not.toBeNull()
    expect(root).toHaveStyle({ marginTop: '16px' })
    // The success banner renders inside that root.
    expect(root).toContainElement(view.getByRole('status'))
  })

  it('replaces a failure with a success when the slot changes', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'failure', code: 'write_failed' }}
        resolve={resolve}
      />,
    )
    expect(view.getByRole('alert')).toHaveTextContent(
      'resolved:write_failed',
    )

    view.rerender(
      <WriteFeedbackRegion
        outcome={{ kind: 'success', message: 'Saved' }}
        resolve={resolve}
      />,
    )
    expect(view.queryByRole('alert')).toBeNull()
    expect(view.getByRole('status')).toHaveTextContent('Saved')
  })
})
