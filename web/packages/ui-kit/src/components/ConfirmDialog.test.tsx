
import { createRef, useState } from 'react'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { buttonClasses } from '@mui/material/Button'
import { switchLanguage } from '@speed/i18n'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import enUS from '../locales/en-US.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { ConfirmDialog, CONFIRM_ARM_LOCKOUT_MS } from './ConfirmDialog.js'

/** Reuses one mounted dialog for a sequence of targets, with `open` true across the whole sequence. */
function BatchDeleteHarness({ onItemBConfirmed }: { onItemBConfirmed: () => void }) {
  const [current, setCurrent] = useState<'itemA' | 'itemB' | 'done'>('itemA')

  const handleConfirmed = () => {
    if (current === 'itemA') {
      setCurrent('itemB')
    } else {
      setCurrent('done')
      onItemBConfirmed()
    }
  }

  return (
    <ConfirmDialog
      open={current !== 'done'}
      variant="danger"
      doubleConfirm
      title={current === 'itemA' ? 'Delete item A?' : 'Delete item B?'}
      onConfirm={handleConfirmed}
      onCancel={() => {}}
    />
  )
}

function OpenFromPage() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open dialog
      </button>
      <ConfirmDialog
        open={open}
        onConfirm={() => undefined}
        onCancel={() => setOpen(false)}
      />
    </>
  )
}

function setup(overrides: Record<string, unknown> = {}) {
  const onConfirm = vi.fn()
  const onCancel = vi.fn()
  const utils = renderWithProviders(
    <ConfirmDialog open onConfirm={onConfirm} onCancel={onCancel} {...overrides} />,
  )
  return { onConfirm, onCancel, ...utils }
}

describe('ConfirmDialog', () => {
  it('renders nothing while closed', () => {
    const { queryByRole } = renderWithProviders(
      <ConfirmDialog open={false} onConfirm={vi.fn()} onCancel={vi.fn()} />,
    )
    expect(queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders the built-in zh-CN title, message and button labels', () => {
    const { getByRole, getByText } = setup()
    expect(getByRole('dialog')).toBeInTheDocument()
    expect(getByText(zhCN.confirmDialog.title)).toBeInTheDocument()
    expect(getByText(zhCN.confirmDialog.message)).toBeInTheDocument()
    expect(
      getByRole('button', { name: zhCN.confirmDialog.confirmLabel }),
    ).toBeInTheDocument()
    expect(
      getByRole('button', { name: zhCN.confirmDialog.cancelLabel }),
    ).toBeInTheDocument()
  })

  it('switches built-in texts to en-US when the language changes', async () => {
    const { i18n, getByRole, getByText, queryByText } = setup()
    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })
    expect(getByText(enUS.confirmDialog.title)).toBeInTheDocument()
    expect(getByText(enUS.confirmDialog.message)).toBeInTheDocument()
    expect(
      getByRole('button', { name: enUS.confirmDialog.confirmLabel }),
    ).toBeInTheDocument()
    expect(queryByText(zhCN.confirmDialog.title)).not.toBeInTheDocument()
  })

  it('lets title, message and both labels be overridden', () => {
    const { getByRole, getByText, queryByText } = setup({
      title: 'Remove patient record?',
      message: 'All scans and attachments are deleted permanently.',
      confirmLabel: 'Delete permanently',
      cancelLabel: 'Keep record',
    })
    expect(getByText('Remove patient record?')).toBeInTheDocument()
    expect(
      getByText('All scans and attachments are deleted permanently.'),
    ).toBeInTheDocument()
    expect(getByRole('button', { name: 'Delete permanently' })).toBeInTheDocument()
    expect(getByRole('button', { name: 'Keep record' })).toBeInTheDocument()
    expect(queryByText(zhCN.confirmDialog.title)).not.toBeInTheDocument()
  })

  it('fires onConfirm once on the confirm click and never onCancel', async () => {
    const { onConfirm, onCancel, getByRole } = setup()
    const user = userEvent.setup()
    await user.click(getByRole('button', { name: zhCN.confirmDialog.confirmLabel }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onCancel).not.toHaveBeenCalled()
  })

  it('fires onCancel on the cancel button and never onConfirm', async () => {
    const { onConfirm, onCancel, getByRole } = setup()
    const user = userEvent.setup()
    await user.click(getByRole('button', { name: zhCN.confirmDialog.cancelLabel }))
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('fires onCancel on Escape and never onConfirm', () => {
    const { onConfirm, onCancel, getByRole } = setup()
    fireEvent.keyDown(getByRole('dialog'), { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('paints the danger variant confirm button with the error role', () => {
    const { getByRole } = setup({ variant: 'danger' })
    expect(getByRole('button', { name: zhCN.confirmDialog.confirmLabel })).toHaveClass(
      buttonClasses.colorError,
    )
  })

  it('keeps the default variant confirm button in the primary role', () => {
    const { getByRole } = setup()
    expect(getByRole('button', { name: zhCN.confirmDialog.confirmLabel })).toHaveClass(
      buttonClasses.colorPrimary,
    )
  })

  it('doubleConfirm: the first click only arms, the second fires onConfirm once', async () => {
    const { onConfirm, onCancel, getByRole, queryByRole } = setup({
      variant: 'danger',
      doubleConfirm: true,
    })
    const user = userEvent.setup()
    await user.click(getByRole('button', { name: zhCN.confirmDialog.confirmLabel }))
    expect(onConfirm).not.toHaveBeenCalled()
    expect(
      getByRole('button', { name: zhCN.confirmDialog.confirmAgainLabel }),
    ).toBeInTheDocument()
    expect(queryByRole('button', { name: zhCN.confirmDialog.confirmLabel })).not.toBeInTheDocument()

    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(getByRole('button', { name: zhCN.confirmDialog.confirmAgainLabel }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onCancel).not.toHaveBeenCalled()
  })

  it('doubleConfirm: a double click cannot skip the guard -- the armed confirm stays inert through the arming lockout (P1 regression)', async () => {
    const { onConfirm, getByRole } = setup({
      variant: 'danger',
      doubleConfirm: true,
    })
    const confirm = getByRole('button', { name: zhCN.confirmDialog.confirmLabel })
    const again = () => getByRole('button', { name: zhCN.confirmDialog.confirmAgainLabel })

    vi.useFakeTimers()
    try {
      fireEvent.click(confirm)
      fireEvent.click(confirm)
      expect(onConfirm).not.toHaveBeenCalled()

      expect(again()).toHaveAttribute('aria-disabled', 'true')
      await act(async () => {
        await vi.advanceTimersByTimeAsync(CONFIRM_ARM_LOCKOUT_MS - 1)
      })
      fireEvent.click(again())
      expect(onConfirm).not.toHaveBeenCalled()

      await act(async () => {
        await vi.advanceTimersByTimeAsync(1)
      })
      expect(again()).not.toHaveAttribute('aria-disabled', 'true')
      fireEvent.click(again())
      expect(onConfirm).toHaveBeenCalledTimes(1)
    } finally {
      await restoreRealTimers()
    }
  })

  it('doubleConfirm: cancel and close stay available during the arming lockout', async () => {
    const { onConfirm, onCancel, getByRole, rerender } = setup({
      variant: 'danger',
      doubleConfirm: true,
    })
    const confirm = getByRole('button', { name: zhCN.confirmDialog.confirmLabel })
    const cancel = getByRole('button', { name: zhCN.confirmDialog.cancelLabel })
    const again = () => getByRole('button', { name: zhCN.confirmDialog.confirmAgainLabel })

    vi.useFakeTimers()
    try {
      fireEvent.click(confirm)
      expect(again()).toHaveAttribute('aria-disabled', 'true')
      fireEvent.click(cancel)
      expect(onCancel).toHaveBeenCalledTimes(1)
      expect(onConfirm).not.toHaveBeenCalled()

      rerender(
        <ConfirmDialog
          open={false}
          variant="danger"
          doubleConfirm
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      )
      rerender(
        <ConfirmDialog
          open
          variant="danger"
          doubleConfirm
          onConfirm={onConfirm}
          onCancel={onCancel}
        />,
      )
      await act(async () => {
        await vi.advanceTimersByTimeAsync(CONFIRM_ARM_LOCKOUT_MS)
      })
      const reopened = getByRole('button', { name: zhCN.confirmDialog.confirmLabel })
      expect(reopened).not.toHaveAttribute('aria-disabled', 'true')
      fireEvent.click(reopened)
      expect(onConfirm).not.toHaveBeenCalled()
    } finally {
      await restoreRealTimers()
    }
  })

  it('doubleConfirm: arming the confirm is announced through a live region (P2-9 regression)', async () => {
    const { getByRole, queryByRole } = setup({
      variant: 'danger',
      doubleConfirm: true,
    })
    const region = getByRole('status')
    expect(region).toHaveTextContent('')
    const user = userEvent.setup()
    await user.click(getByRole('button', { name: zhCN.confirmDialog.confirmLabel }))
    expect(region).toHaveTextContent(zhCN.confirmDialog.confirmAgainLabel)
    expect(
      queryByRole('button', { name: zhCN.confirmDialog.confirmAgainLabel }),
    ).toBeInTheDocument()
  })

  it('doubleConfirm: the override confirmLabel does not leak into the re-armed step', async () => {
    const { getByRole } = setup({
      variant: 'danger',
      doubleConfirm: true,
      confirmLabel: 'Delete permanently',
    })
    const user = userEvent.setup()
    await user.click(getByRole('button', { name: 'Delete permanently' }))
    expect(
      getByRole('button', { name: zhCN.confirmDialog.confirmAgainLabel }),
    ).toBeInTheDocument()
  })

  it('doubleConfirm: cancelling while armed does not confirm, and a reopen re-arms', async () => {
    const { onConfirm, onCancel, getByRole, rerender } = setup({
      variant: 'danger',
      doubleConfirm: true,
    })
    const user = userEvent.setup()
    await user.click(getByRole('button', { name: zhCN.confirmDialog.confirmLabel }))
    await user.click(getByRole('button', { name: zhCN.confirmDialog.cancelLabel }))
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()

    rerender(
      <ConfirmDialog
        open={false}
        variant="danger"
        doubleConfirm
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    )
    rerender(
      <ConfirmDialog
        open
        variant="danger"
        doubleConfirm
        onConfirm={onConfirm}
        onCancel={onCancel}
      />,
    )
    await user.click(getByRole('button', { name: zhCN.confirmDialog.confirmLabel }))
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('doubleConfirm: a batched close+reopen for the next item inside the onConfirm that just fired does not inherit the armed state (regression: web-base-packages.md P2-3)', async () => {
    const onItemBConfirmed = vi.fn()
    const { getByRole, queryByRole } = renderWithProviders(
      <BatchDeleteHarness onItemBConfirmed={onItemBConfirmed} />,
    )
    const user = userEvent.setup()
    const waitOutLockout = () =>
      act(async () => {
        await new Promise((resolve) =>
          setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
        )
      })

    await user.click(getByRole('button', { name: zhCN.confirmDialog.confirmLabel }))
    expect(
      getByRole('button', { name: zhCN.confirmDialog.confirmAgainLabel }),
    ).toBeInTheDocument()
    expect(onItemBConfirmed).not.toHaveBeenCalled()

    await waitOutLockout()
    await user.click(getByRole('button', { name: zhCN.confirmDialog.confirmAgainLabel }))
    expect(getByRole('button', { name: zhCN.confirmDialog.confirmLabel })).toBeInTheDocument()
    expect(
      queryByRole('button', { name: zhCN.confirmDialog.confirmAgainLabel }),
    ).not.toBeInTheDocument()
    expect(onItemBConfirmed).not.toHaveBeenCalled()

    await user.click(getByRole('button', { name: zhCN.confirmDialog.confirmLabel }))
    expect(onItemBConfirmed).not.toHaveBeenCalled()
    expect(
      getByRole('button', { name: zhCN.confirmDialog.confirmAgainLabel }),
    ).toBeInTheDocument()

    await waitOutLockout()
    await user.click(getByRole('button', { name: zhCN.confirmDialog.confirmAgainLabel }))
    expect(onItemBConfirmed).toHaveBeenCalledTimes(1)
  })

  it('non-danger variant still confirms on exactly one click, unaffected by the armed-reset change', async () => {
    const { onConfirm, onCancel, getByRole } = setup({ variant: 'default' })
    const user = userEvent.setup()
    await user.click(getByRole('button', { name: zhCN.confirmDialog.confirmLabel }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onCancel).not.toHaveBeenCalled()
  })

  it('danger variant without doubleConfirm still confirms on exactly one click, unaffected by the armed-reset change', async () => {
    const { onConfirm, onCancel, getByRole } = setup({ variant: 'danger' })
    const user = userEvent.setup()
    await user.click(getByRole('button', { name: zhCN.confirmDialog.confirmLabel }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onCancel).not.toHaveBeenCalled()
  })

  it('confirmLoading: both exits are inert, focusable and report unavailable', () => {
    const { onConfirm, onCancel, getByRole } = setup({ confirmLoading: true })
    const confirm = getByRole('button', { name: zhCN.confirmDialog.confirmLabel })
    expect(confirm).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(confirm)
    expect(onConfirm).not.toHaveBeenCalled()

    const cancel = getByRole('button', { name: zhCN.confirmDialog.cancelLabel })
    expect(cancel).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(cancel)
    expect(onCancel).not.toHaveBeenCalled()
  })

  it('confirmLoading: Escape does not cancel while busy', () => {
    const { onCancel, getByRole } = setup({ confirmLoading: true })
    fireEvent.keyDown(getByRole('dialog'), { key: 'Escape' })
    expect(onCancel).not.toHaveBeenCalled()
  })

  it('confirmDisabled: makes only the confirm button unavailable, leaving cancel live', () => {
    const { onConfirm, onCancel, getByRole } = setup({ confirmDisabled: true })
    const confirm = getByRole('button', { name: zhCN.confirmDialog.confirmLabel })
    expect(confirm).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(confirm)
    expect(onConfirm).not.toHaveBeenCalled()

    const cancel = getByRole('button', { name: zhCN.confirmDialog.cancelLabel })
    expect(cancel).not.toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(cancel)
    expect(onCancel).toHaveBeenCalledTimes(1)
  })

  it('confirmDisabled: carries the host-supplied reason as its accessible description', () => {
    const REASON = 'Accept the terms first.'
    const { getByRole } = setup({
      confirmDisabled: true,
      confirmDisabledReason: REASON,
    })
    const confirm = getByRole('button', {
      name: zhCN.confirmDialog.confirmLabel,
    })
    expect(confirm).toHaveAccessibleDescription(REASON)
  })

  it('confirmDisabled: clearing it starts the doubleConfirm guard untouched', async () => {
    const user = userEvent.setup()
    const onConfirm = vi.fn()
    function Harness() {
      const [disabled, setDisabled] = useState(true)
      return (
        <ConfirmDialog
          open
          variant="danger"
          doubleConfirm
          confirmDisabled={disabled}
          onConfirm={onConfirm}
          onCancel={() => undefined}
        >
          <button type="button" onClick={() => setDisabled(false)}>
            enable
          </button>
        </ConfirmDialog>
      )
    }
    renderWithProviders(<Harness />)
    const confirm = screen.getByRole('button', {
      name: zhCN.confirmDialog.confirmLabel,
    })
    expect(confirm).toHaveAttribute('aria-disabled', 'true')

    await user.click(screen.getByRole('button', { name: 'enable' }))
    const enabled = screen.getByRole('button', {
      name: zhCN.confirmDialog.confirmLabel,
    })
    await user.click(enabled)
    expect(onConfirm).not.toHaveBeenCalled()
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(
      screen.getByRole('button', {
        name: zhCN.confirmDialog.confirmAgainLabel,
      }),
    )
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it('renders host children below the message, outside aria-describedby', () => {
    const CHILD_TEXT = 'I understand this cannot be undone'
    const utils = setup({ children: <p>{CHILD_TEXT}</p> })
    const dialog = utils.getByRole('dialog')
    const describedBy = dialog.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    const description = document.getElementById(describedBy!)
    expect(description).toHaveTextContent(zhCN.confirmDialog.message)
    expect(description).not.toHaveTextContent(CHILD_TEXT)
    expect(utils.getByText(CHILD_TEXT)).toBeInTheDocument()
  })

  it('wires the dialog to its title and message ids', () => {
    const { getByRole } = setup()
    const dialog = getByRole('dialog')
    expect(dialog).toHaveAttribute('aria-labelledby')
    expect(dialog).toHaveAttribute('aria-describedby')
  })

  it('marks the page behind the dialog inert while open and lifts it on close', async () => {
    const user = userEvent.setup()
    const { container, getByRole } = renderWithProviders(<OpenFromPage />)
    expect(container).not.toHaveAttribute('inert')

    await user.click(getByRole('button', { name: 'Open dialog' }))
    await waitFor(() => expect(container).toHaveAttribute('inert'))
    expect(document.querySelector('.MuiModal-root')).not.toHaveAttribute('inert')

    await user.click(getByRole('button', { name: zhCN.confirmDialog.cancelLabel }))
    await waitFor(() => expect(container).not.toHaveAttribute('inert'))
  })

  it('passes axe over an open dialog', async () => {
    setup()
    await expectNoAxeViolations()
  })

  it('forwards sx, className and ref to the dialog root', () => {
    const ref = createRef<HTMLDivElement>()
    renderWithProviders(
      <ConfirmDialog
        open
        ref={ref}
        className="host-dialog"
        sx={{ mt: 4 }}
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
    )
    expect(ref.current).not.toBeNull()
    expect(ref.current).toHaveClass('host-dialog')
    expect(ref.current).toHaveStyle({ marginTop: '32px' })
  })
})
