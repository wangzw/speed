/**
 * ActionRow and ActionButton contract: the row is one right-aligned group
 * whose adjacent controls sit exactly one spacing unit apart, the button
 * takes its weight and colour from `emphasis`/`tone`, and its size from
 * the enclosing row unless given directly. The assertions read the theme
 * spacing value from the shipped tokens rather than inlining it.
 */

import { describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import { act, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { defaultTokens } from '@speed/tokens'
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { ActionButton, ActionRow } from './ActionRow.js'

const SPACING_UNIT = `${defaultTokens.spacing.unit}px`

describe('ActionRow', () => {
  it('lays the actions out as one group against the right edge, one spacing unit apart', () => {
    renderWithProviders(
      <ActionRow>
        <ActionButton>Cancel</ActionButton>
        <ActionButton emphasis="primary">Save</ActionButton>
      </ActionRow>,
    )

    const row = screen
      .getByRole('button', { name: 'Save' })
      .closest('[data-action-row]')
    expect(row).not.toBeNull()
    // The row is marked so tests and call sites can find it as a row
    // rather than treating one action's parent as the row.
    expect(row).toHaveAttribute('data-action-row', '')
    expect(row).toHaveStyle({
      display: 'flex',
      flexWrap: 'wrap',
      justifyContent: 'flex-end',
      alignItems: 'center',
      gap: SPACING_UNIT,
    })
  })

  it('lets a host rule override its own layout', () => {
    renderWithProviders(
      <ActionRow sx={{ justifyContent: 'space-between' }}>
        <ActionButton>Cancel</ActionButton>
      </ActionRow>,
    )

    const row = screen
      .getByRole('button', { name: 'Cancel' })
      .closest('[data-action-row]')
    expect(row).toHaveStyle({ justifyContent: 'space-between' })
  })

  it('has no axe violations', async () => {
    // Rendered under a real page h1: page-has-heading-one is determinate
    // in jsdom now, and a widget-only document would fail it rather than
    // pass by indeterminacy.
    renderWithProviders(
      <div>
        <h1>Members</h1>
        <ActionRow>
          <ActionButton>Cancel</ActionButton>
          <ActionButton emphasis="primary">Save</ActionButton>
          <ActionButton tone="danger">Delete</ActionButton>
        </ActionRow>
      </div>,
    )
    await expectNoAxeViolations()
  })
})

describe('ActionButton', () => {
  it('renders the secondary face as text and the primary face as contained', () => {
    renderWithProviders(
      <ActionRow>
        <ActionButton>Cancel</ActionButton>
        <ActionButton emphasis="primary">Save</ActionButton>
      </ActionRow>,
    )

    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveClass(
      'MuiButton-text',
    )
    expect(screen.getByRole('button', { name: 'Save' })).toHaveClass(
      'MuiButton-contained',
    )
  })

  it('paints a danger action from the error role, filled when primary and outlined when secondary', () => {
    renderWithProviders(
      <ActionRow>
        <ActionButton tone="danger">Discard</ActionButton>
        <ActionButton emphasis="primary" tone="danger">
          Delete
        </ActionButton>
      </ActionRow>,
    )

    expect(screen.getByRole('button', { name: 'Discard' })).toHaveClass(
      'MuiButton-outlined',
      'MuiButton-colorError',
    )
    expect(screen.getByRole('button', { name: 'Delete' })).toHaveClass(
      'MuiButton-contained',
      'MuiButton-colorError',
    )
  })

  it('hands the row size to every action and lets one override it', () => {
    renderWithProviders(
      <ActionRow size="small">
        <ActionButton>Cancel</ActionButton>
        <ActionButton size="medium" emphasis="primary">
          Save
        </ActionButton>
      </ActionRow>,
    )

    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveClass(
      'MuiButton-sizeSmall',
    )
    expect(screen.getByRole('button', { name: 'Save' })).toHaveClass(
      'MuiButton-sizeMedium',
    )
  })

  it('defaults to medium outside a row', () => {
    renderWithProviders(<ActionButton emphasis="primary">Save</ActionButton>)

    expect(screen.getByRole('button', { name: 'Save' })).toHaveClass(
      'MuiButton-sizeMedium',
    )
  })

  it('forwards the button props the host owns', async () => {
    const onClick = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <ActionRow>
        <ActionButton type="submit" onClick={onClick} aria-describedby="hint">
          Save
        </ActionButton>
      </ActionRow>,
    )

    const save = screen.getByRole('button', { name: 'Save' })
    expect(save).toHaveAttribute('type', 'submit')
    expect(save).toHaveAttribute('aria-describedby', 'hint')
    await user.click(save)
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('renders the disabled and loading states the host gives it', () => {
    renderWithProviders(
      <ActionRow>
        <ActionButton disabled>Cancel</ActionButton>
        <ActionButton emphasis="primary" loading>
          Save
        </ActionButton>
      </ActionRow>,
    )

    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  })
})

describe('ActionButton busy and unavailable states', () => {
  it('keeps a busy button focusable, marks it aria-busy and shows a progress indicator', () => {
    renderWithProviders(<ActionButton busy>Save status</ActionButton>)

    const save = screen.getByRole('button', { name: 'Save status' })
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).toHaveAttribute('aria-busy', 'true')
    expect(save).toBeEnabled()
    save.focus()
    expect(save).toHaveFocus()
    expect(save).toHaveAccessibleName('Save status')
    expect(within(save).getByRole('progressbar')).toBeInTheDocument()
  })

  it('keeps an unavailable button focusable and carries no busy state', () => {
    renderWithProviders(<ActionButton unavailable>Save status</ActionButton>)

    const save = screen.getByRole('button', { name: 'Save status' })
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).not.toHaveAttribute('aria-busy')
    expect(save).toBeEnabled()
    save.focus()
    expect(save).toHaveFocus()
    expect(within(save).queryByRole('progressbar')).not.toBeInTheDocument()
  })

  it('keeps focus when the focused button becomes busy', async () => {
    function BusyOnActivate() {
      const [busy, setBusy] = useState(false)
      return (
        <ActionButton busy={busy} onClick={() => setBusy(true)}>
          Save
        </ActionButton>
      )
    }
    const user = userEvent.setup()
    renderWithProviders(<BusyOnActivate />)

    const save = screen.getByRole('button', { name: 'Save' })
    await user.click(save)

    expect(save).toHaveAttribute('aria-busy', 'true')
    expect(save).toHaveFocus()
    expect(within(save).getByRole('progressbar')).toBeInTheDocument()
  })

  it('keeps focus when the focused button becomes unavailable', async () => {
    function UnavailableOnActivate() {
      const [unavailable, setUnavailable] = useState(false)
      return (
        <ActionButton
          unavailable={unavailable}
          onClick={() => setUnavailable(true)}
        >
          Save
        </ActionButton>
      )
    }
    const user = userEvent.setup()
    renderWithProviders(<UnavailableOnActivate />)

    const save = screen.getByRole('button', { name: 'Save' })
    await user.click(save)

    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).toBeEnabled()
    expect(save).toHaveFocus()
  })

  it('keeps the same button element, and its focus, through every availability state', () => {
    type Availability = 'reason' | 'available' | 'busy' | 'noReason'
    let change: (state: Availability) => void = () => {}
    function Harness() {
      const [state, setState] = useState<Availability>('reason')
      change = setState
      return (
        <ActionButton
          unavailable={state === 'reason' || state === 'noReason'}
          unavailableReason={state === 'reason' ? 'Pick a status' : undefined}
          busy={state === 'busy'}
        >
          Save
        </ActionButton>
      )
    }
    renderWithProviders(<Harness />)

    const save = screen.getByRole('button', { name: 'Save' })
    save.focus()
    expect(save).toHaveFocus()

    // The reason's presence must not decide the button's place in the
    // tree: switching availability states reconciles this element, it
    // never replaces it, so the focused button keeps focus.
    const states: readonly Availability[] = [
      'available',
      'busy',
      'reason',
      'noReason',
      'reason',
      'available',
    ]
    for (const state of states) {
      act(() => change(state))
      expect(document.activeElement).toBe(save)
      expect(save).toBeInTheDocument()
    }
  })

  it('swallows click, Enter and Space activation while unavailable', async () => {
    const onClick = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <ActionButton unavailable onClick={onClick}>
        Save
      </ActionButton>,
    )

    const save = screen.getByRole('button', { name: 'Save' })
    await user.click(save)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')

    expect(onClick).not.toHaveBeenCalled()
  })

  it('leaves an unavailable click visible to the ancestor that holds it', async () => {
    const onAncestorClick = vi.fn()
    const onClick = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <div onClick={onAncestorClick}>
        <ActionButton unavailable onClick={onClick}>
          Save
        </ActionButton>
      </div>,
    )

    await user.click(screen.getByRole('button', { name: 'Save' }))

    // Swallowing the activation must not swallow the event: a host that
    // listens on an ancestor still sees the click.
    expect(onClick).not.toHaveBeenCalled()
    expect(onAncestorClick).toHaveBeenCalledTimes(1)
  })

  it('blocks both a click and an input Enter from submitting the form while unavailable', async () => {
    const onSubmit = vi.fn((event: { preventDefault: () => void }) => {
      event.preventDefault()
    })
    const user = userEvent.setup()
    renderWithProviders(
      <form onSubmit={onSubmit}>
        <input aria-label="Name" />
        <ActionButton type="submit" unavailable>
          Save
        </ActionButton>
      </form>,
    )

    const save = screen.getByRole('button', { name: 'Save' })
    await user.click(save)
    expect(onSubmit).not.toHaveBeenCalled()

    await user.type(screen.getByLabelText('Name'), 'Ada{Enter}')
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('associates an unavailable reason once, after the caller description, without changing the name', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <>
        <span id="hint">A status must be chosen</span>
        <ActionButton
          unavailable
          unavailableReason="Pick a status"
          aria-describedby="hint"
        >
          Save
        </ActionButton>
      </>,
    )

    const save = screen.getByRole('button', { name: 'Save' })
    const reason = screen.getByText('Pick a status')
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).toHaveAttribute('aria-describedby', `hint ${reason.id}`)
    expect(save).toHaveAccessibleDescription(
      'A status must be chosen Pick a status',
    )
    expect(save).toHaveAccessibleName('Save')

    await user.hover(save)
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Pick a status')
    expect(save).toHaveAccessibleName('Save')
    expect(save).toHaveAccessibleDescription(
      'A status must be chosen Pick a status',
    )
  })

  it('points at existing explanatory text without rendering or tooltipping its own copy', () => {
    renderWithProviders(
      <>
        <span id="why">Only an administrator may change this</span>
        <ActionButton unavailable unavailableDescribedBy="why">
          Save
        </ActionButton>
      </>,
    )

    const save = screen.getByRole('button', { name: 'Save' })
    expect(save).toHaveAttribute('aria-describedby', 'why')
    expect(save).toHaveAccessibleDescription(
      'Only an administrator may change this',
    )
    expect(save).not.toHaveAttribute('title')
  })

  it('does not render an unavailable reason while busy', () => {
    renderWithProviders(
      <ActionButton busy unavailable unavailableReason="Pick a status">
        Save
      </ActionButton>,
    )

    const save = screen.getByRole('button', { name: 'Save' })
    expect(save).toHaveAttribute('aria-busy', 'true')
    expect(screen.queryByText('Pick a status')).not.toBeInTheDocument()
    expect(save).not.toHaveAttribute('aria-describedby')
    expect(save).not.toHaveAttribute('title')
  })

  it('labels the progress indicator from the button id, preferring a caller id', () => {
    const first = renderWithProviders(
      <ActionButton busy id="save-status">
        Save status
      </ActionButton>,
    )
    const namedSave = screen.getByRole('button', { name: 'Save status' })
    expect(namedSave).toHaveAttribute('id', 'save-status')
    expect(
      within(namedSave).getByRole('progressbar'),
    ).toHaveAttribute('aria-labelledby', 'save-status')
    first.unmount()

    renderWithProviders(<ActionButton busy>Save status</ActionButton>)
    const save = screen.getByRole('button', { name: 'Save status' })
    const progress = within(save).getByRole('progressbar')
    expect(save.id).not.toBe('')
    expect(progress).toHaveAttribute('aria-labelledby', save.id)
  })
})
