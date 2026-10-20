/**
 * SectionCard contract: the card is a `<section>` carrying the container
 * surface, its title renders as a real heading at the requested level
 * (h2 by default) and names the section through `aria-labelledby`, an
 * untitled card renders no heading block and no accessible name, the
 * content area is padded unless `flushContent` removes it, and `sx`
 * reaches the card root.
 */

import { createRef } from 'react'
import { describe, expect, it } from 'vitest'
import { cardClasses } from '@mui/material/Card'
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { SectionCard } from './SectionCard.js'

describe('SectionCard', () => {
  it('renders the title as an h2 heading and names the section with it', () => {
    const { getByRole } = renderWithProviders(
      <SectionCard title="Billing">Content</SectionCard>,
    )
    const heading = getByRole('heading', { level: 2 })
    expect(heading).toHaveTextContent('Billing')
    const section = getByRole('region', { name: 'Billing' })
    expect(section).toHaveAttribute('aria-labelledby', heading.id)
  })

  it('renders the title at the requested heading level', () => {
    const { getByRole } = renderWithProviders(
      <SectionCard title="Billing" headingLevel="h4">
        Content
      </SectionCard>,
    )
    expect(getByRole('heading', { level: 4 })).toHaveTextContent('Billing')
  })

  it('renders no heading block and no aria-labelledby without a title', () => {
    const { container, queryByRole } = renderWithProviders(
      <SectionCard>Content</SectionCard>,
    )
    expect(queryByRole('heading')).not.toBeInTheDocument()
    const section = container.querySelector('section')
    expect(section).not.toBeNull()
    expect(section).not.toHaveAttribute('aria-labelledby')
  })

  it('renders the description and actions inside the heading block', () => {
    const { getByRole, getByText } = renderWithProviders(
      <SectionCard
        title="Members"
        description="Everyone with access to this workspace."
        actions={<button type="button">Invite</button>}
      >
        Content
      </SectionCard>,
    )
    expect(getByText('Everyone with access to this workspace.')).toBeInTheDocument()
    expect(getByRole('button', { name: 'Invite' })).toBeInTheDocument()
  })

  it('pads the content area by default', () => {
    const { getByText } = renderWithProviders(
      <SectionCard title="Billing">Body</SectionCard>,
    )
    expect(getByText('Body')).toHaveStyle({ padding: '24px' })
  })

  it('removes the content padding for a flush child and keeps the surface', () => {
    const { container, getByText } = renderWithProviders(
      <SectionCard title="Billing" flushContent>
        Body
      </SectionCard>,
    )
    expect(getByText('Body')).not.toHaveStyle({ padding: '24px' })
    expect(container.querySelector('section')).toHaveClass(cardClasses.root)
  })

  it('forwards sx to the card root', () => {
    const { container } = renderWithProviders(
      <SectionCard title="Billing" sx={{ mt: 4 }}>
        Body
      </SectionCard>,
    )
    expect(container.querySelector('section')).toHaveStyle({ marginTop: '32px' })
  })

  it('renders the surface as a section carrying the card root class', () => {
    const { container } = renderWithProviders(
      <SectionCard title="Billing">Body</SectionCard>,
    )
    const section = container.querySelector('section')
    expect(section).toHaveClass(cardClasses.root)
  })

  it('forwards className and ref to the card root alongside sx', () => {
    const ref = createRef<HTMLElement>()
    const { container } = renderWithProviders(
      <SectionCard
        ref={ref}
        title="Billing"
        className="host-card"
        sx={{ mt: 4 }}
      >
        Body
      </SectionCard>,
    )
    const section = container.querySelector('section')
    expect(section).toBe(ref.current)
    expect(section).toHaveClass('host-card')
    expect(section).toHaveStyle({ marginTop: '32px' })
  })

  it('passes axe over a titled card', async () => {
    renderWithProviders(
      <div>
        <h1>Page title</h1>
        <SectionCard
          title="Members"
          description="Everyone with access to this workspace."
          actions={<button type="button">Invite</button>}
        >
          Content
        </SectionCard>
      </div>,
    )
    await expectNoAxeViolations()
  })
})
