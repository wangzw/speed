/**
 * OneTimeValuePanel contract: a presentation-only panel for a value handed
 * out once -- the caller's message, the value itself, a copy control
 * named by the caller, and an explicit close control whose accessible name
 * is the caller's. The panel holds no state: closing it is the caller's
 * state change.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { OneTimeValuePanel } from './OneTimeValuePanel.js'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const MESSAGE = 'Invitation for ada@example.com is ready.'
const VALUE = 'https://example.test/accept?token=abc'
const COPY_LABEL = 'Copy link'
const CLOSE_LABEL = 'Close'
const CLOSE_ARIA = 'Close the invitation link for ada@example.com'

function stubClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText },
  })
  return writeText
}

describe('OneTimeValuePanel', () => {
  afterEach(() => {
    Reflect.deleteProperty(navigator, 'clipboard')
  })

  it('renders the message, the value and one polite live region', () => {
    const utils = renderWithProviders(
      <OneTimeValuePanel
        message={MESSAGE}
        value={VALUE}
        copyLabel={COPY_LABEL}
        closeLabel={CLOSE_LABEL}
        closeAriaLabel={CLOSE_ARIA}
        onClose={() => undefined}
      />,
    )
    expect(utils.getByText(MESSAGE)).toBeInTheDocument()
    expect(utils.getByText(VALUE)).toBeInTheDocument()
    // The panel root is the live region that announces the hand-off; the
    // copy control carries its own nested announcement region.
    const panel = utils.container.querySelector(':scope > [role="status"]')
    expect(panel).not.toBeNull()
    expect(panel).toHaveTextContent(MESSAGE)
    expect(panel).toHaveTextContent(VALUE)
  })

  it('copies the value through the labelled control', async () => {
    const user = userEvent.setup()
    const writeText = stubClipboard()
    renderWithProviders(
      <OneTimeValuePanel
        message={MESSAGE}
        value={VALUE}
        copyLabel={COPY_LABEL}
        closeLabel={CLOSE_LABEL}
        closeAriaLabel={CLOSE_ARIA}
        onClose={() => undefined}
      />,
    )
    await user.click(screen.getByRole('button', { name: COPY_LABEL }))
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(VALUE))
    expect(await screen.findByText(zhCN.oneTimeValuePanel.copied)).toHaveAttribute(
      'role',
      'status',
    )
  })

  it('reports the close control by its accessible name and visible text', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    renderWithProviders(
      <OneTimeValuePanel
        message={MESSAGE}
        value={VALUE}
        copyLabel={COPY_LABEL}
        closeLabel={CLOSE_LABEL}
        closeAriaLabel={CLOSE_ARIA}
        onClose={onClose}
      />,
    )
    const close = screen.getByRole('button', { name: CLOSE_ARIA })
    expect(close).toHaveTextContent(CLOSE_LABEL)
    await user.click(close)
    expect(onClose).toHaveBeenCalledTimes(1)
  })
})
