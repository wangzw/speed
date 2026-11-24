import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { renderWithProviders } from '../../test-utils/render.js'
import { RetryAction } from './retry-action.js'

describe('RetryAction', () => {
  it('renders the one mapped retry face and reports the click', async () => {
    const onClick = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(<RetryAction label="Retry" onClick={onClick} />)

    const retry = screen.getByRole('button', { name: 'Retry' })
    expect(retry).toHaveClass('MuiButton-contained')
    expect(retry.parentElement).toHaveStyle({
      display: 'flex',
      justifyContent: 'flex-end',
    })

    await user.click(retry)
    expect(onClick).toHaveBeenCalledTimes(1)
  })
})
