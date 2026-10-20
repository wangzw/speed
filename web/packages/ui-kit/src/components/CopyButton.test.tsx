/**
 * CopyButton contract: the control writes the complete value to the
 * clipboard, announces the copy through a polite live region, carries the
 * caller's label or the namespace default, and stays silent when the
 * Clipboard API is unavailable. The visible face renders the caller's
 * text button; the icon face renders the icon control.
 */

import { createRef } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CopyButton } from './CopyButton.js'
import { renderWithProviders } from '../../test-utils/render.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

function renderTemplate(
  template: string,
  vars: Record<string, string | number>,
): string {
  return Object.entries(vars).reduce(
    (out, [key, value]) => out.replaceAll(`{{${key}}}`, String(value)),
    template,
  )
}

function stubClipboard() {
  const writeText = vi.fn().mockResolvedValue(undefined)
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText },
  })
  return writeText
}

describe('CopyButton', () => {
  afterEach(() => {
    Reflect.deleteProperty(navigator, 'clipboard')
  })

  it('copies the value and announces it with the namespace default', async () => {
    const user = userEvent.setup()
    const writeText = stubClipboard()
    renderWithProviders(<CopyButton value="member-1" />)

    const button = screen.getByRole('button', {
      name: renderTemplate(zhCN.copyButton.copyLabel, { value: 'member-1' }),
    })
    await user.click(button)

    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith('member-1'))
    const announcement = await screen.findByText(
      renderTemplate(zhCN.copyButton.copiedLabel, { value: 'member-1' }),
    )
    expect(announcement).toHaveAttribute('role', 'status')
  })

  it('takes the caller label and announcement over the defaults', async () => {
    const user = userEvent.setup()
    stubClipboard()
    renderWithProviders(
      <CopyButton
        value="token-1"
        label="Copy the token"
        copiedLabel="Token copied"
      />,
    )

    const button = screen.getByRole('button', { name: 'Copy the token' })
    await user.click(button)
    expect(await screen.findByText('Token copied')).toHaveAttribute(
      'role',
      'status',
    )
  })

  it('renders a text button when a visible label is given', () => {
    renderWithProviders(
      <CopyButton
        value="token-1"
        label="Copy the token"
        visibleLabel="Copy"
      />,
    )
    const button = screen.getByRole('button', { name: 'Copy the token' })
    expect(button).toHaveTextContent('Copy')
  })

  it('stays silent when the Clipboard API is unavailable', async () => {
    const user = userEvent.setup()
    Reflect.deleteProperty(navigator, 'clipboard')
    renderWithProviders(<CopyButton value="member-1" />)

    const button = screen.getByRole('button', {
      name: renderTemplate(zhCN.copyButton.copyLabel, { value: 'member-1' }),
    })
    await user.click(button)
    expect(
      screen.queryByText(
        renderTemplate(zhCN.copyButton.copiedLabel, { value: 'member-1' }),
      ),
    ).toBeNull()
  })

  it('refuses to shrink when pinned', () => {
    renderWithProviders(<CopyButton value="member-1" pinned />)
    expect(
      screen.getByRole('button', {
        name: renderTemplate(zhCN.copyButton.copyLabel, { value: 'member-1' }),
      }),
    ).toHaveStyle({ flexShrink: '0' })
  })

  it('forwards sx, className and ref to the copy control', () => {
    const ref = createRef<HTMLButtonElement>()
    renderWithProviders(
      <CopyButton
        value="member-1"
        ref={ref}
        className="host-copy"
        sx={{ mt: 4 }}
      />,
    )
    const button = screen.getByRole('button', {
      name: renderTemplate(zhCN.copyButton.copyLabel, { value: 'member-1' }),
    })
    expect(button).toHaveClass('host-copy')
    expect(button).toBe(ref.current)
    expect(button).toHaveStyle({ marginTop: '32px' })
  })
})
