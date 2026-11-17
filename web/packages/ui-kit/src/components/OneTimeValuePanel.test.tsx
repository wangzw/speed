/**
 * OneTimeValuePanel contract: a presentation-only panel for a value handed
 * out once -- the caller's message, the value itself, a copy control
 * named by the caller, and an explicit close control whose accessible name
 * is the caller's. The panel holds no state: closing it is the caller's
 * state change.
 */

import { createRef } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
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

const DETAILS = [
  {
    label: 'Record name',
    value: '_speed-authn.acme.example',
    copyLabel: 'Copy record name',
  },
  {
    label: 'Record value',
    value: 'speed-authn=abc',
    copyLabel: 'Copy record value',
  },
] as const

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
    await waitForCondition(() => expect(writeText).toHaveBeenCalledWith(VALUE))
    expect(await screen.findByText(zhCN.oneTimeValuePanel.copied)).toHaveAttribute(
      'role',
      'status',
    )
  })

  it('renders labelled details with their own copy controls and values', async () => {
    const user = userEvent.setup()
    const writeText = stubClipboard()
    const { container } = renderWithProviders(
      <OneTimeValuePanel
        message={MESSAGE}
        value={VALUE}
        copyLabel={COPY_LABEL}
        details={DETAILS}
        closeLabel={CLOSE_LABEL}
        closeAriaLabel={CLOSE_ARIA}
        onClose={() => undefined}
      />,
    )

    // Every detail renders its label and its complete value inside the
    // panel's one live region, so the hand-off is announced as a whole.
    const panel = container.querySelector(':scope > [role="status"]')
    expect(panel).not.toBeNull()
    for (const detail of DETAILS) {
      expect(panel).toHaveTextContent(detail.label)
      expect(panel).toHaveTextContent(detail.value)
      expect(
        screen.getByRole('button', { name: detail.copyLabel }),
      ).toBeInTheDocument()
    }

    // Each copy control hands over exactly its own value, not the primary
    // value and not another detail's.
    await user.click(screen.getByRole('button', { name: DETAILS[0].copyLabel }))
    await waitForCondition(() =>
      expect(writeText).toHaveBeenLastCalledWith(DETAILS[0].value),
    )
    await user.click(screen.getByRole('button', { name: DETAILS[1].copyLabel }))
    await waitForCondition(() =>
      expect(writeText).toHaveBeenLastCalledWith(DETAILS[1].value),
    )
  })

  it('renders a notice with no value and no copy control when nothing is handed out', async () => {
    const onClose = vi.fn()
    const user = userEvent.setup()
    const { container } = renderWithProviders(
      <OneTimeValuePanel
        message={MESSAGE}
        closeLabel={CLOSE_LABEL}
        closeAriaLabel={CLOSE_ARIA}
        onClose={onClose}
      />,
    )
    // The message is the whole hand-off; there is no empty value line and no
    // control that would copy nothing.
    expect(container.querySelector(':scope > [role="status"]')).toHaveTextContent(
      MESSAGE,
    )
    expect(screen.queryByRole('button', { name: COPY_LABEL })).toBeNull()
    expect(screen.queryByText(VALUE)).toBeNull()
    // The close control remains.
    await user.click(screen.getByRole('button', { name: CLOSE_ARIA }))
    expect(onClose).toHaveBeenCalledTimes(1)
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

  it('forwards sx, className and ref to the panel root', () => {
    const ref = createRef<HTMLDivElement>()
    const { container } = renderWithProviders(
      <OneTimeValuePanel
        ref={ref}
        className="host-panel"
        sx={{ mt: 4 }}
        message={MESSAGE}
        value={VALUE}
        copyLabel={COPY_LABEL}
        closeLabel={CLOSE_LABEL}
        closeAriaLabel={CLOSE_ARIA}
        onClose={() => undefined}
      />,
    )
    const root = container.firstChild as HTMLElement
    expect(root).toHaveClass('host-panel')
    expect(root).toBe(ref.current)
    expect(root).toHaveStyle({ marginTop: '32px' })
  })
})
