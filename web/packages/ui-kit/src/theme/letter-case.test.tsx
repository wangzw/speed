/**
 * The theme-layer letter case, sampled by button variant.
 *
 * Material's `button` typography asks for capitals. A filled or outlined
 * button is a button and keeps them; a text button carries a link-shaped
 * action whose sentence should read as written ("Create an account", "Forgot
 * your password?"), so only that variant is exempted. The decorative
 * `overline` role keeps its capitals either way.
 */

import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import Button from '@mui/material/Button'
import Typography from '@mui/material/Typography'
import { renderWithProviders } from '../../test-utils/render.js'

const textTransformOf = (element: Element): string =>
  getComputedStyle(element).textTransform

describe('theme letter case', () => {
  it('leaves a text button as it was written', () => {
    renderWithProviders(<Button variant="text">Create an account</Button>)
    expect(
      textTransformOf(screen.getByRole('button', { name: 'Create an account' })),
    ).toBe('none')
  })

  it('keeps the stock capitals on filled and outlined buttons', () => {
    renderWithProviders(
      <>
        <Button variant="contained">Sign in</Button>
        <Button variant="outlined">Cancel</Button>
      </>,
    )
    expect(
      textTransformOf(screen.getByRole('button', { name: 'Sign in' })),
    ).toBe('uppercase')
    expect(
      textTransformOf(screen.getByRole('button', { name: 'Cancel' })),
    ).toBe('uppercase')
  })

  it('keeps the overline role in capitals', () => {
    renderWithProviders(<Typography variant="overline">section</Typography>)
    expect(textTransformOf(screen.getByText('section'))).toBe('uppercase')
  })
})
