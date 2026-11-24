
import { useState } from 'react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { alertClasses } from '@mui/material/Alert'
import { renderWithProviders } from '../../test-utils/render.js'
import { InlineSuccess } from './InlineSuccess.js'

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
    expect(getByRole('status').className).toMatch(
      new RegExp(alertClasses.colorSuccess),
    )
  })

  it('mounts the region empty and clipped when there is no message', () => {
    const { getByRole } = renderWithProviders(<InlineSuccess message={null} />)
    const status = getByRole('status')
    expect(status).toHaveTextContent('')
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
    const { getByRole } = renderWithProviders(<InlineSuccess message="" />)
    const status = getByRole('status')
    expect(status).toHaveTextContent('')
    expect(status).not.toHaveStyle(CLIPPED)
    expect(status).toHaveStyle({ width: '100%' })
  })

  it('forwards sx to the alert root, after the region’s own styles', () => {
    const { getByRole } = renderWithProviders(
      <InlineSuccess message="Saved" sx={{ marginTop: '37px' }} />,
    )
    const status = getByRole('status')
    expect(status).toHaveStyle({ marginTop: '37px' })
    expect(status).toHaveStyle({ width: '100%' })
  })

  it('fills and clears the same mounted region as the message changes', async () => {
    const view = renderWithProviders(<SuccessHost />)
    const silent = view.getByRole('status')
    expect(silent).toHaveTextContent('')
    expect(silent).toHaveStyle(CLIPPED)

    await userEvent.click(view.getByRole('button', { name: 'fill' }))
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
