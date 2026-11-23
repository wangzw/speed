/**
 * Step-switch focus in jsdom: a step that replaces the surface the viewer
 * was working in takes the focus that unmounted with it, while a step the
 * viewer had already tabbed or clicked away from is left alone. The old
 * surface's focused control is removed from the document, so focus has
 * fallen to the body by the time the new step commits; focus still resting
 * on another element is the viewer's and is never pulled away.
 */
import { describe, expect, it } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useStepFocus } from './step-focus.js'

/**
 * One surface of a channel plus a control outside it. The step's inner
 * element is keyed by the step name so a switch replaces it, exactly as
 * two different surfaces do.
 */
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
