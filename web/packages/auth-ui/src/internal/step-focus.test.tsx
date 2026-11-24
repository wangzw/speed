import { describe, expect, it } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useStepFocus } from './step-focus.js'

function Surface({ step }: { step: string }) {
  const ref = useStepFocus(step)
  return (
    <div>
      <button type="button">elsewhere</button>
      <div ref={ref}>
        <button key={step} type="button">
          {`first ${step}`}
        </button>
      </div>
    </div>
  )
}

describe('useStepFocus', () => {
  it('moves nothing when the first surface renders', () => {
    render(<Surface step="credentials" />)

    expect(document.body).toHaveFocus()
  })

  it('takes the focus that unmounted with the replaced step', async () => {
    const { rerender } = render(<Surface step="credentials" />)
    const replaced = screen.getByRole('button', {
      name: 'first credentials',
    })
    replaced.focus()
    expect(replaced).toHaveFocus()

    rerender(<Surface step="factor" />)

    const next = screen.getByRole('button', { name: 'first factor' })
    await waitFor(() => expect(next).toHaveFocus())
  })

  it('takes the focus when the body held it before the switch', async () => {
    const { rerender } = render(<Surface step="credentials" />)

    rerender(<Surface step="factor" />)

    const next = screen.getByRole('button', { name: 'first factor' })
    await waitFor(() => expect(next).toHaveFocus())
  })

  it('leaves focus where the viewer moved it', async () => {
    const user = userEvent.setup()
    const { rerender } = render(<Surface step="credentials" />)
    const elsewhere = screen.getByRole('button', { name: 'elsewhere' })
    await user.click(elsewhere)
    expect(elsewhere).toHaveFocus()

    rerender(<Surface step="factor" />)

    expect(elsewhere).toHaveFocus()
    expect(
      screen.getByRole('button', { name: 'first factor' }),
    ).not.toHaveFocus()
  })
})
