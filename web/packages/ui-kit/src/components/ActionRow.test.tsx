/**
 * ActionRow and ActionButton contract: the row is one right-aligned group
 * whose adjacent controls sit exactly one spacing unit apart, the button
 * takes its weight and colour from `emphasis`/`tone`, and its size from
 * the enclosing row unless given directly. The assertions read the theme
 * spacing value from the shipped tokens rather than inlining it.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
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
