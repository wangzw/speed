/**
 * WriteFeedbackRegion contract: one slot rendered as the scope's result
 * area -- a failure code through the caller's resolver and the success
 * message through the always-mounted status region. A rendered banner
 * carries one default top margin on the region's root; an empty slot's
 * root takes no height, so the resident live region never widens its
 * control group's rhythm. The caller's `sx` applies after the margin.
 */

import { useState } from 'react'
import userEvent from '@testing-library/user-event'
import { act, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
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

/** A host holding the slot, so the region re-renders in place. */
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

/** A host settling the slot with the empty string through the hook. */
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

/**
 * Renders one outcome twice -- without caller `sx` and with `marginTop: 2`
 * -- and checks that only the root's spacing moved. A caller's `sx` copied
 * into a banner would add its margin to the root's own and double the gap.
 * The default margin belongs to a rendered outcome; an empty slot starts
 * at zero.
 */
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
  // The caller's spacing lands on the root ...
  expect(styled.container.firstElementChild).toHaveStyle({ marginTop: '16px' })
  // ... and on nothing inside it: the status region (silent or filled) and
  // the failure alert keep exactly the margin they had without caller `sx`.
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
    // The success region stays mounted (empty and clipped) so the next
    // message fills a region the screen reader already knows.
    expect(view.getByRole('status')).toHaveTextContent('')
  })

  it('renders no visible feedback for an empty slot', () => {
    const view = renderWithProviders(
      <WriteFeedbackRegion outcome={null} resolve={resolve} />,
    )
    expect(view.container).toHaveTextContent('')
    expect(view.queryByRole('alert')).toBeNull()
    // The always-mounted region is still present for the next message.
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
    // The failure occupies the slot through its alert; the success region
    // beside it is silent and clipped.
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
    // The two banners add the same (zero) spacing of their own: the one gap
    // is the root's default margin in both states.
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
    // The live region stays mounted, but the empty slot adds no gap: the
    // margin belongs to a banner that actually renders.
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
    // The caller's spacing wins over the default, and the success banner
    // renders inside that root.
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
    // `''` is a settled success, not silent: the region drops the clip and
    // paints an empty banner, exactly as a direct `InlineSuccess` does.
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

    // Another success replaces the text on the same node.
    await userEvent.click(view.getByRole('button', { name: 'second success' }))
    expect(view.getByRole('status')).toBe(status)
    expect(status).toHaveTextContent('second success')

    // A failure fills the alert beside the same, now clipped, status node.
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

/** A resolver that spells out both the code and the wait it was handed,
 * so a test can tell the live sentence from the stable one. */
const waitResolver = (code: string, seconds?: number): string =>
  seconds === undefined ? `${code}:no-wait` : `${code}:${seconds}s`

describe('WriteFeedbackRegion wait countdown', () => {
  afterEach(() => {
    vi.useRealTimers()
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
    // The sentence assistive technology reads is fixed at the stated wait,
    // clipped from view.
    const [stable] = within(alert).getAllByText('authn.throttled:3s')
    expect(stable).not.toHaveAttribute('aria-hidden')
    expect(stable).toHaveStyle(CLIPPED)
    // The painted sentence is hidden from assistive technology and follows
    // the remaining time.
    const visible = alert.querySelector('span[aria-hidden="true"]')
    expect(visible).toHaveTextContent('authn.throttled:3s')

    act(() => {
      vi.advanceTimersByTime(1_000)
    })
    expect(visible).toHaveTextContent('authn.throttled:2s')
    // The accessible text did not change with the tick: the same node, same
    // sentence, so role="alert" does not re-announce every second.
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
    // The banner is not auto-cleared: only a new attempt or a context change
    // replaces the result, never a timer.
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
