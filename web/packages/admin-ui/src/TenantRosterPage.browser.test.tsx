/**
 * TenantRosterPage in a real browser: the roster table at the sm/md/lg
 * floor widths with fixed data. The invariant is horizontal containment
 * -- the page and the table's own scroll container stay inside their
 * widths -- which only a browser can measure (container queries, column
 * priorities and real font metrics all take part). A row's status switch
 * keeps the focus it was activated with while its write is in flight,
 * which a browser settles by its own focus rules.
 */
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { TenantRosterPage } from './TenantRosterPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const

/** A promise the case settles by hand, so one request can be held in
 * flight across frames. */
function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
} {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

/** One animation frame: the updated state has reached the DOM and the
 * browser has had its chance to react to it. */
function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

/** What currently holds focus, for a failure message. */
function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const label = active.getAttribute('aria-label')
  const textContent = (active.textContent ?? '').replace(/\s+/g, ' ').trim()
  return [active.tagName.toLowerCase(), label, textContent]
    .filter((part) => part !== null && part !== '')
    .join(' ')
}

const TENANTS = [
  tenantFixture({
    name: 'acme-corporation',
    display_name: 'Acme Corporation',
    status: 'active',
  }),
  tenantFixture({
    name: 'beta-industries',
    display_name: 'Beta Industries',
    status: 'disabled',
    disabled_by: 'operator',
  }),
]

describe('TenantRosterPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the roster table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      const operations = createAdminOperations(
        makeAdminBackend({
          listTenants: () => Promise.resolve(TENANTS),
        }),
      )
      renderWithProviders(<TenantRosterPage />, { operations, language: 'en-US' })

      await screen.findByText('Acme Corporation')
      await expectNoHorizontalOverflow()
    })
  }
})

describe('TenantRosterPage write focus in Chromium', () => {
  it('keeps focus on the row status switch through an in-flight write', async () => {
    await page.viewport(1200, 900)
    const written = deferred<void>()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([
            tenantFixture({
              name: 'acme-corporation',
              display_name: 'Acme Corporation',
              status: 'active',
            }),
          ]),
        setTenantStatus: () => written.promise,
      }),
    )
    renderWithProviders(<TenantRosterPage />, {
      operations,
      language: 'en-US',
    })
    await screen.findByText('Acme Corporation')

    const user = userEvent.setup()
    const toggle = screen.getByRole('switch', {
      name: enUS.tenants.disableAria.replace('{{name}}', 'acme-corporation'),
    })
    await user.click(toggle)

    // A frame passes, so the browser has had its chance to move a focused
    // control whose availability changed.
    await nextFrame()
    expect(
      toggle,
      `focus while the status write is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    expect(toggle).toHaveAttribute('aria-busy', 'true')
    expect(toggle).toHaveAttribute('aria-disabled', 'true')
    // The controlled value does not move until the write's refetch settles.
    expect(toggle).toBeChecked()

    // A second activation is swallowed, in a real browser as in jsdom.
    await user.click(toggle)
    expect(toggle).toBeChecked()
    expect(toggle).toHaveFocus()

    written.resolve()
    await waitFor(() => expect(toggle).not.toHaveAttribute('aria-busy'))
    expect(toggle).toHaveFocus()
  })
})
