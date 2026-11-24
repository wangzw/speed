
import { useState } from 'react'
import userEvent from '@testing-library/user-event'
import { act, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { alertClasses } from '@mui/material/Alert'
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'
import { useWriteFeedback, type WriteOutcome } from '../write-feedback.js'
import { WriteFeedbackRegion } from './WriteFeedbackRegion.js'

const resolve = (code: string): string => `resolved:${code}`

const CLIPPED = {
  position: 'absolute',
  width: '1px',
  height: '1px',
} as const

function RegionHost() {
  const [outcome, setOutcome] = useState<WriteOutcome | null>(null)
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          setOutcome({ kind: 'success', message: 'first success' })
        }}
      >
        first success
      </button>
      <button
        type="button"
        onClick={() => {
          setOutcome({ kind: 'success', message: 'second success' })
        }}
      >
        second success
      </button>
      <button
        type="button"
        onClick={() => {
          setOutcome({ kind: 'failure', code: 'write_failed' })
        }}
      >
        failure
      </button>
      <button
        type="button"
        onClick={() => {
          setOutcome(null)
        }}
      >
        clear
      </button>
      <WriteFeedbackRegion outcome={outcome} resolve={resolve} />
    </div>
  )
}

function EmptyMessageHost() {
  const feedback = useWriteFeedback()
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          feedback.succeed('')
        }}
      >
        succeed empty
      </button>
      <WriteFeedbackRegion outcome={feedback.outcome} resolve={resolve} />
    </div>
  )
}

/** jsdom reports an unset margin as '' or '0'; both mean no gap. */
function normalizeMargin(element: Element): string {
  const value = getComputedStyle(element).marginTop
  return value === '' || value === '0' ? '0px' : value
}

function expectCallerSxToStayOnRoot(outcome: WriteOutcome | null): void {
  const plain = renderWithProviders(
    <WriteFeedbackRegion outcome={outcome} resolve={resolve} />,
  )
  const plainRoot = plain.container.firstElementChild
  expect(normalizeMargin(plainRoot as Element)).toBe(
    outcome === null ? '0px' : '8px',
  )
  const plainStatusMargin = getComputedStyle(
    plain.getByRole('status'),
  ).marginTop
  const plainAlert =
    outcome?.kind === 'failure' ? plain.getByRole('alert') : null
  const plainAlertMargin =
    plainAlert === null ? null : getComputedStyle(plainAlert).marginTop
  plain.unmount()

  const styled = renderWithProviders(
    <WriteFeedbackRegion
      outcome={outcome}
      resolve={resolve}
      sx={{ marginTop: 2 }}
    />,
  )
  expect(styled.container.firstElementChild).toHaveStyle({ marginTop: '16px' })
  expect(getComputedStyle(styled.getByRole('status')).marginTop).toBe(
    plainStatusMargin,
  )
  if (plainAlertMargin !== null) {
    expect(getComputedStyle(styled.getByRole('alert')).marginTop).toBe(
      plainAlertMargin,
    )
  }
}

describe('WriteFeedbackRegion', () => {
  it('renders a success message in the polite status region', async () => {
    const view = renderWithProviders(
      <div>
        <h1>Page title</h1>
        <WriteFeedbackRegion
          outcome={{ kind: 'success', message: 'Tenant renamed' }}
          resolve={resolve}
        />
      </div>,
    )
    expect(view.getByRole('status')).toHaveTextContent('Tenant renamed')
    expect(view.queryByRole('alert')).toBeNull()
    await expectNoAxeViolations()
  })

  it('renders a failure code through the resolver as an alert', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'failure', code: 'tenant.not_found' }}
        resolve={resolve}
      />,
    )
    expect(view.getByRole('alert')).toHaveTextContent(
      'resolved:tenant.not_found',
    )
    expect(view.getByRole('status')).toHaveTextContent('')
  })

  it('renders no visible feedback for an empty slot', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion outcome={null} resolve={resolve} />,
    )
    expect(view.container).toHaveTextContent('')
    expect(view.queryByRole('alert')).toBeNull()
    expect(view.getByRole('status')).toBeInTheDocument()
  })

  it('clips the silent status region and leaves the painted banners unclipped', () => {
    const empty = renderWithProviders(
      <WriteFeedbackRegion outcome={null} resolve={resolve} />,
    )
    expect(empty.getByRole('status')).toHaveStyle(CLIPPED)
    empty.unmount()

    const failure = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'failure', code: 'write_failed' }}
        resolve={resolve}
      />,
    )
    expect(failure.getByRole('status')).toHaveStyle(CLIPPED)
    expect(failure.getByRole('alert')).not.toHaveStyle(CLIPPED)
    failure.unmount()

    const success = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'success', message: 'Saved' }}
        resolve={resolve}
      />,
    )
    expect(success.getByRole('status')).not.toHaveStyle(CLIPPED)
    expect(success.getByRole('status')).toHaveStyle({ width: '100%' })
  })

  it('renders the success severity', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'success', message: 'Saved' }}
        resolve={resolve}
      />,
    )
    expect(view.getByRole('status').className).toMatch(
      new RegExp(alertClasses.colorSuccess),
    )
  })

  it('carries one default top margin on the root that both parts share', () => {
    const success = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'success', message: 'Saved' }}
        resolve={resolve}
      />,
    )
    const successRoot = success.container.firstElementChild
    expect(successRoot).toHaveStyle({ marginTop: '8px' })
    expect(successRoot).toContainElement(success.getByRole('status'))
    const successSpacing = getComputedStyle(
      success.getByRole('status'),
    ).marginTop
    success.unmount()

    const failure = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'failure', code: 'write_failed' }}
        resolve={resolve}
      />,
    )
    const failureRoot = failure.container.firstElementChild
    expect(failureRoot).toHaveStyle({ marginTop: '8px' })
    expect(failureRoot).toContainElement(failure.getByRole('alert'))
    expect(getComputedStyle(failure.getByRole('alert')).marginTop).toBe(
      successSpacing,
    )
  })

  it('takes no top margin for an empty slot, while a settled outcome does', () => {
    const empty = renderWithProviders(
      <WriteFeedbackRegion outcome={null} resolve={resolve} />,
    )
    const emptyRoot = empty.container.firstElementChild
    expect(emptyRoot).not.toBeNull()
    expect(normalizeMargin(emptyRoot as Element)).toBe('0px')
    expect(empty.getByRole('status')).toBeInTheDocument()
    empty.unmount()

    const success = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'success', message: 'Saved' }}
        resolve={resolve}
      />,
    )
    expect(
      getComputedStyle(success.container.firstElementChild!).marginTop,
    ).toBe('8px')
  })

  it('applies an array sx to the root container', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={null}
        resolve={resolve}
        sx={[{ marginTop: 2 }]}
      />,
    )
    expect(view.container.firstElementChild).toHaveStyle({ marginTop: '16px' })
  })

  it('applies an array sx containing false to the root container', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={null}
        resolve={resolve}
        sx={[false, { marginTop: 2 }]}
      />,
    )
    expect(view.container.firstElementChild).toHaveStyle({ marginTop: '16px' })
  })

  it('applies a function sx to the root container', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={null}
        resolve={resolve}
        sx={(theme) => ({ marginTop: theme.spacing(3) })}
      />,
    )
    expect(view.container.firstElementChild).toHaveStyle({ marginTop: '24px' })
  })

  it('applies the caller sx after the default root margin', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'success', message: 'Saved' }}
        resolve={resolve}
        sx={{ marginTop: 2 }}
      />,
    )
    const root = view.container.firstElementChild
    expect(root).not.toBeNull()
    expect(root).toHaveStyle({ marginTop: '16px' })
    expect(root).toContainElement(view.getByRole('status'))
  })

  it('applies the caller sx only to the root for a success', () => {
    expectCallerSxToStayOnRoot({ kind: 'success', message: 'Saved' })
  })

  it('applies the caller sx only to the root for an empty slot', () => {
    expectCallerSxToStayOnRoot(null)
  })

  it('applies the caller sx only to the root for a failure', () => {
    expectCallerSxToStayOnRoot({ kind: 'failure', code: 'write_failed' })
  })

  it('renders a success carrying the empty string as a painted, empty banner', async () => {
    const view = renderWithProviders(<EmptyMessageHost />)
    expect(view.getByRole('status')).toHaveStyle(CLIPPED)

    await userEvent.click(view.getByRole('button', { name: 'succeed empty' }))
    const status = view.getByRole('status')
    expect(status).toHaveTextContent('')
    expect(status).not.toHaveStyle(CLIPPED)
    expect(status).toHaveStyle({ width: '100%' })
  })

  it('keeps the success banner as one node across slot changes', async () => {
    const view = renderWithProviders(<RegionHost />)
    const status = view.getByRole('status')
    expect(status).toHaveTextContent('')
    expect(status).toHaveStyle(CLIPPED)

    await userEvent.click(view.getByRole('button', { name: 'first success' }))
    expect(view.getByRole('status')).toBe(status)
    expect(status).toHaveTextContent('first success')

    await userEvent.click(view.getByRole('button', { name: 'second success' }))
    expect(view.getByRole('status')).toBe(status)
    expect(status).toHaveTextContent('second success')

    await userEvent.click(view.getByRole('button', { name: 'failure' }))
    expect(view.getByRole('status')).toBe(status)
    expect(status).toHaveTextContent('')
    expect(status).toHaveStyle(CLIPPED)
    expect(view.getByRole('alert')).toHaveTextContent('resolved:write_failed')

    await userEvent.click(view.getByRole('button', { name: 'clear' }))
    expect(view.getByRole('status')).toBe(status)
    expect(view.queryByRole('alert')).toBeNull()
  })

  it('replaces a failure with a success when the slot changes', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'failure', code: 'write_failed' }}
        resolve={resolve}
      />,
    )
    expect(view.getByRole('alert')).toHaveTextContent(
      'resolved:write_failed',
    )

    view.rerender(
      <WriteFeedbackRegion
        outcome={{ kind: 'success', message: 'Saved' }}
        resolve={resolve}
      />,
    )
    expect(view.queryByRole('alert')).toBeNull()
    expect(view.getByRole('status')).toHaveTextContent('Saved')
  })
})

const waitResolver = (code: string, seconds?: number): string =>
  seconds === undefined ? `${code}:no-wait` : `${code}:${seconds}s`

describe('WriteFeedbackRegion wait countdown', () => {
  afterEach(async () => {
    await restoreRealTimers()
  })

  it('counts the visible sentence down against the deadline while the accessible sentence stays fixed', () => {
    vi.useFakeTimers()
    const waitUntil = Date.now() + 3_000
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{
          kind: 'failure',
          code: 'authn.throttled',
          retryAfterSeconds: 3,
          waitUntil,
        }}
        resolve={waitResolver}
      />,
    )
    const alert = view.getByRole('alert')
    const [stable] = within(alert).getAllByText('authn.throttled:3s')
    expect(stable).not.toHaveAttribute('aria-hidden')
    expect(stable).toHaveStyle(CLIPPED)
    const visible = alert.querySelector('span[aria-hidden="true"]')
    expect(visible).toHaveTextContent('authn.throttled:3s')

    act(() => {
      vi.advanceTimersByTime(1_000)
    })
    expect(visible).toHaveTextContent('authn.throttled:2s')
    expect(within(alert).getByText('authn.throttled:3s')).toBe(stable)
  })

  it('drops the number at expiry without clearing the result', () => {
    vi.useFakeTimers()
    const waitUntil = Date.now() + 2_000
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{
          kind: 'failure',
          code: 'authn.throttled',
          retryAfterSeconds: 2,
          waitUntil,
        }}
        resolve={waitResolver}
      />,
    )
    const alert = view.getByRole('alert')
    const [stable] = within(alert).getAllByText('authn.throttled:2s')
    const visible = alert.querySelector('span[aria-hidden="true"]')

    act(() => {
      vi.advanceTimersByTime(2_000)
    })
    expect(visible).toHaveTextContent('authn.throttled:no-wait')
    expect(view.getByRole('alert')).toBe(alert)
    expect(within(alert).getByText('authn.throttled:2s')).toBe(stable)
  })

  it('renders a failure with no stated wait unchanged', () => {
    vi.useFakeTimers()
    const view = renderWithProviders(
      <WriteFeedbackRegion
        outcome={{ kind: 'failure', code: 'write_failed' }}
        resolve={waitResolver}
      />,
    )
    const alert = view.getByRole('alert')
    expect(alert).toHaveTextContent('write_failed:no-wait')
    expect(alert.querySelector('span[aria-hidden="true"]')).toBeNull()
    expect(within(alert).getAllByText('write_failed:no-wait')).toHaveLength(1)
  })
})
