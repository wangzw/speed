/**
 * SettingsPage in a real browser: the settings table at the sm/md/lg
 * floor widths with fixed data (keys and values of representative
 * length, including a long dotted key).
 */
import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import axe from 'axe-core'
import { useTheme } from '@mui/material/styles'
import { page, userEvent } from 'vitest/browser'
import { SettingsPage } from './SettingsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import { createAdminOperations, makeAdminBackend } from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { uiKitResources } from '@speed/ui-kit'
import type { AdminSetting } from './operations.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const

/** The key of the first scripted setting, the row the overflow case looks up. */
const FIRST_SETTING_KEY = 'mail.retry'

const SETTINGS: readonly AdminSetting[] = [
  {
    key: FIRST_SETTING_KEY,
    namespace: 'mail',
    type: 'int',
    description: 'Retry count',
    domain: '',
    sensitive: false,
    default: '3',
    written: '5',
    has_row: true,
    value: '5',
  },
  {
    key: 'authn.session.absolute-lifetime',
    namespace: 'authn',
    type: 'duration',
    description: 'Absolute session lifetime',
    domain: '',
    sensitive: false,
    default: '24h',
    written: '',
    has_row: false,
    value: '24h',
  },
]

describe('SettingsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the settings table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      const operations = createAdminOperations(
        makeAdminBackend({
          listSettings: () => Promise.resolve(SETTINGS),
        }),
      )
      renderWithProviders(<SettingsPage />, { operations, language: 'en-US' })

      await screen.findByText('mail.retry')
      await expectNoHorizontalOverflow()
    })
  }
})

/**
 * A prose description of the length the settings schema really declares
 * (the longest ones run past 300 characters). Inside the primary
 * identity's 160/200px cap it wraps into a very tall column of thin text;
 * the declaration must get the cell's own width instead.
 */
const LONG_DESCRIPTION =
  'Whether the mail carrier retries a message it refused, measured against the delivery attempts already recorded for that message and against the retry window this deployment configures, so a message is not retried forever and a transient refusal is still retried.'

const LONG_DESCRIPTION_ITEM: AdminSetting = {
  key: 'mail.retry',
  namespace: 'mail',
  type: 'bool',
  description: LONG_DESCRIPTION,
  domain: '',
  sensitive: false,
  default: 'true',
  written: '',
  has_row: false,
  value: 'true',
}

/** The shipped en-US copy-control label for this row's key. */
const COPY_LABEL = (
  uiKitResources['en-US'] as unknown as {
    readonly dataTable: { readonly copyValue: string }
  }
).dataTable.copyValue.replace('{{value}}', LONG_DESCRIPTION_ITEM.key)

/** The vertical centre of a rendered element. */
function centreY(element: Element): number {
  const rect = element.getBoundingClientRect()
  return (rect.top + rect.bottom) / 2
}

describe('SettingsPage declaration layout in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`wraps the declaration across the cell in a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      const operations = createAdminOperations(
        makeAdminBackend({
          listSettings: () => Promise.resolve([LONG_DESCRIPTION_ITEM]),
        }),
      )
      renderWithProviders(<SettingsPage />, {
        operations,
        language: 'en-US',
      })

      const description = await screen.findByText(LONG_DESCRIPTION)
      const row = description.closest('tr')
      expect(row).not.toBeNull()
      await waitFor(() =>
        expect(
          description.getBoundingClientRect().width,
        ).toBeGreaterThan(0),
      )

      // The declaration gets the cell's own width, not the primary
      // identity's 160/200px cap it used to be held to (where this text
      // measured ~130/170px). The key line keeps that cap alone.
      expect(description.getBoundingClientRect().width).toBeGreaterThan(200)

      // Wrapping across the cell keeps the row a handful of prose lines
      // tall instead of stacking the description down a narrow column.
      expect(row?.getBoundingClientRect().height ?? Infinity).toBeLessThan(
        300,
      )

      // The tall key cell does not drop the controls to its middle: the
      // copy control sits on the key line, and the value input starts at
      // the row's top beside the key. Before the row was top-aligned the
      // copy control measured ~83px below the key line here.
      const keyLine = screen.getByText(LONG_DESCRIPTION_ITEM.key)
      const copy = screen.getByRole('button', { name: COPY_LABEL })
      expect(Math.abs(centreY(copy) - centreY(keyLine))).toBeLessThan(12)

      const input = screen.getByRole('textbox', {
        name: LONG_DESCRIPTION_ITEM.key,
      })
      expect(
        Math.abs(
          input.getBoundingClientRect().top -
            keyLine.getBoundingClientRect().top,
        ),
      ).toBeLessThan(24)
    })
  }
})

/** The shipped Remove label for the overridden row the case renders. */
const REMOVE_KEY = 'mail.retry'
const REMOVE_LABEL = enUS.settings.removeAria.replace('{{key}}', REMOVE_KEY)

/**
 * The row's Remove control carries the visible text "Remove" beside its
 * accessible name; WCAG 2.5.3 requires that text be part of the name. The
 * rule measures the visible text through a canvas, which jsdom cannot
 * provide, so this tier settles it for the one row shape that offers the
 * control.
 */
/**
 * The theme's container surface painted onto a reference element, so the
 * cases compare against the role the page is supposed to use rather than
 * against values written into the test.
 */
function ContainerSurfaceProbe() {
  const theme = useTheme()
  const role = theme.surface.container
  return (
    <div
      data-testid="container-surface-probe"
      style={{
        backgroundColor: role.fill,
        borderColor: role.borderColor,
        borderStyle: 'solid',
        borderWidth: role.borderWidth,
        borderRadius: theme.shape.borderRadius,
      }}
    />
  )
}

/**
 * The settings page's two blocks -- the namespace navigation and the
 * settings register -- are content sections: each renders through
 * `SectionCard` as a `<section>` painted with the theme's container role.
 * The register is a flush table section, so the table reaches the
 * section's rim. A hand-written outlined `Card` paints the same colors,
 * so only the section element and the flush geometry distinguish the two.
 */
describe('SettingsPage content surfaces in Chromium', () => {
  it('renders both blocks on the container surface, the register flush', async () => {
    await page.viewport(1200, 900)
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve(SETTINGS) }),
    )
    renderWithProviders(
      <>
        <ContainerSurfaceProbe />
        <SettingsPage />
      </>,
      { operations, language: 'en-US' },
    )

    const key = await screen.findByText(FIRST_SETTING_KEY)
    const table = key.closest('table')
    expect(table).not.toBeNull()
    const register = table?.closest('section')
    expect(register, 'the settings register is a SectionCard section').not.toBeNull()

    const navigation = screen.getByRole('navigation', {
      name: enUS.settings.nav.label,
    })
    const navigationSection = navigation.querySelector('section')
    expect(
      navigationSection,
      'the namespace navigation is a SectionCard section',
    ).not.toBeNull()

    const expected = getComputedStyle(
      screen.getByTestId('container-surface-probe'),
    )
    for (const block of [navigationSection, register] as Element[]) {
      const paint = getComputedStyle(block)
      expect(paint.backgroundColor).toBe(expected.backgroundColor)
      expect(paint.borderTopWidth).toBe(expected.borderTopWidth)
      expect(paint.borderTopStyle).toBe(expected.borderTopStyle)
      expect(paint.borderTopColor).toBe(expected.borderTopColor)
      expect(paint.borderTopLeftRadius).toBe(expected.borderTopLeftRadius)
    }

    // Flush content: outside the 1px rim, the table touches every edge of
    // the section, so the surface clips its corners rather than the table
    // floating on a padded card.
    const tableRect = table!.getBoundingClientRect()
    const registerRect = register!.getBoundingClientRect()
    expect(Math.abs(tableRect.left - registerRect.left)).toBeLessThanOrEqual(1.5)
    expect(Math.abs(tableRect.right - registerRect.right)).toBeLessThanOrEqual(1.5)
    expect(Math.abs(tableRect.top - registerRect.top)).toBeLessThanOrEqual(1.5)
  })
})

/** One row the layer holds, so it carries a remove control. */
function overridden(key: string): AdminSetting {
  const namespace = key.slice(0, key.indexOf('.'))
  return {
    key,
    namespace,
    type: 'int',
    description: `Description of ${key}`,
    domain: '',
    sensitive: false,
    default: '3',
    written: '5',
    has_row: true,
    value: '5',
  }
}

/** The row Remove control's accessible name for one key. */
function removeLabel(key: string): string {
  return enUS.settings.removeAria.replace('{{key}}', key)
}

/** A promise the case settles by hand, so one write can be held in flight. */
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
  const parts = [active.tagName.toLowerCase()]
  const role = active.getAttribute('role')
  const label = active.getAttribute('aria-label')
  const text = (active.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 80)
  if (role !== null) {
    parts.push(`role="${role}"`)
  }
  if (label !== null) {
    parts.push(`aria-label="${label}"`)
  }
  if (text !== '') {
    parts.push(`text="${text}"`)
  }
  return parts.join(' ')
}

/** Clicks a row's Remove control and confirms the dialog. */
async function confirmRemove(key: string): Promise<HTMLElement> {
  const remove = await screen.findByRole('button', { name: removeLabel(key) })
  await userEvent.click(remove)
  const dialog = await screen.findByRole('dialog')
  await userEvent.click(
    within(dialog).getByRole('button', { name: enUS.common.confirm }),
  )
  return remove
}

describe('SettingsPage write focus in Chromium', () => {
  it('keeps focus on the save action while the batch is in flight and after it settles', async () => {
    await page.viewport(1200, 900)
    const written = deferred<void>()
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([overridden('mail.retry')]),
        setSetting: () => written.promise,
      }),
    )
    renderWithProviders(<SettingsPage />, { operations, language: 'en-US' })
    const input = await screen.findByDisplayValue('5')
    await userEvent.clear(input)
    await userEvent.type(input, '9')
    const save = screen.getByRole('button', {
      name: enUS.settings.saveBar.save,
    })
    await userEvent.click(save)
    await waitFor(() =>
      expect(within(save).getByRole('progressbar')).toBeInTheDocument(),
    )
    await nextFrame()

    expect(
      save,
      `focus while the batch is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    written.resolve()
    await waitFor(() =>
      expect(within(save).queryByRole('progressbar')).not.toBeInTheDocument(),
    )

    // The landed batch cleared the drafts, so the action's precondition is
    // gone: it stays where the operator left it, unavailable and explained.
    await waitFor(() => {
      expect(
        save,
        `focus after the batch settled; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).toHaveAccessibleDescription(enUS.settings.saveBar.unchanged)
  })

  it('moves focus to the next removable row when a removal takes its row away', async () => {
    await page.viewport(1200, 900)
    const written = deferred<void>()
    let removedKey = ''
    const rows = [
      overridden('mail.a'),
      overridden('mail.b'),
      overridden('mail.c'),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () =>
          Promise.resolve(
            rows.map((row) =>
              row.key === removedKey
                ? { ...row, written: '', has_row: false, value: row.default }
                : row,
            ),
          ),
        removeSetting: (input) =>
          written.promise.then(() => {
            removedKey = `${input.namespace}.${input.key}`
          }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations, language: 'en-US' })

    const removeB = await confirmRemove('mail.b')
    await waitFor(() => {
      expect(
        removeB,
        `focus after the confirmation closed; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
    // The row's own removal is in flight: its control says so and still holds
    // the focus the confirmation returned to it.
    expect(removeB).toHaveAttribute('aria-busy', 'true')
    expect(removeB).toHaveAttribute('aria-disabled', 'true')
    // Wait out the confirmation's exit transition: while the dialog stands,
    // the page behind it is hidden from role queries.
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    written.resolve()
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: removeLabel('mail.b') }),
      ).toBeNull(),
    )
    const removeC = screen.getByRole('button', { name: removeLabel('mail.c') })
    await waitFor(() => {
      expect(
        removeC,
        `focus after the row was removed; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
  })

  it('moves focus to the previous removable row when the last row is removed', async () => {
    await page.viewport(1200, 900)
    const written = deferred<void>()
    let removedKey = ''
    const rows = [overridden('mail.a'), overridden('mail.b')]
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () =>
          Promise.resolve(
            rows.map((row) =>
              row.key === removedKey
                ? { ...row, written: '', has_row: false, value: row.default }
                : row,
            ),
          ),
        removeSetting: (input) =>
          written.promise.then(() => {
            removedKey = `${input.namespace}.${input.key}`
          }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations, language: 'en-US' })

    const removeB = await confirmRemove('mail.b')
    await waitFor(() => {
      expect(
        removeB,
        `focus after the confirmation closed; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
    expect(removeB).toHaveAttribute('aria-busy', 'true')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    written.resolve()
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: removeLabel('mail.b') }),
      ).toBeNull(),
    )
    const removeA = screen.getByRole('button', { name: removeLabel('mail.a') })
    await waitFor(() => {
      expect(
        removeA,
        `focus after the last row was removed; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
  })

  it('moves focus to the table container when the only row is removed', async () => {
    await page.viewport(1200, 900)
    const written = deferred<void>()
    let removedKey = ''
    const rows = [overridden('mail.retry')]
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () =>
          Promise.resolve(
            rows.map((row) =>
              row.key === removedKey
                ? { ...row, written: '', has_row: false, value: row.default }
                : row,
            ),
          ),
        removeSetting: (input) =>
          written.promise.then(() => {
            removedKey = `${input.namespace}.${input.key}`
          }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations, language: 'en-US' })

    const remove = await confirmRemove('mail.retry')
    await waitFor(() => {
      expect(
        remove,
        `focus after the confirmation closed; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
    expect(remove).toHaveAttribute('aria-busy', 'true')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    written.resolve()
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: removeLabel('mail.retry') }),
      ).toBeNull(),
    )

    // Nothing removable is left on the page, so focus lands on the settings
    // table's named group container rather than the document body.
    await waitFor(() => {
      const active = document.activeElement as HTMLElement | null
      expect(
        active,
        `focus after the only row was removed; document.activeElement is ${describeActiveElement()}`,
      ).not.toBe(document.body)
      expect(active).toHaveAttribute('role', 'group')
      expect(active).toHaveAccessibleName(enUS.settings.tableLabel)
      expect(active?.querySelector('table')).not.toBeNull()
    })
  })

  it('keeps focus where the operator moved it while a removal is in flight', async () => {
    await page.viewport(1200, 900)
    const written = deferred<void>()
    let removedKey = ''
    const rows = [
      overridden('mail.a'),
      overridden('mail.b'),
      overridden('mail.c'),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () =>
          Promise.resolve(
            rows.map((row) =>
              row.key === removedKey
                ? { ...row, written: '', has_row: false, value: row.default }
                : row,
            ),
          ),
        removeSetting: (input) =>
          written.promise.then(() => {
            removedKey = `${input.namespace}.${input.key}`
          }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations, language: 'en-US' })

    const removeB = await confirmRemove('mail.b')
    await waitFor(() => {
      expect(
        removeB,
        `focus after the confirmation closed; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    // The operator leaves the confirmation's landing place and settles on a
    // control of their own while the removal is still in flight.
    const elsewhere = screen.getByRole('textbox', { name: 'mail.a' })
    elsewhere.focus()
    await nextFrame()
    expect(elsewhere).toHaveFocus()

    written.resolve()
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: removeLabel('mail.b') }),
      ).toBeNull(),
    )
    await nextFrame()

    // The landed removal does not take the focus the operator chose.
    expect(
      elsewhere,
      `focus after the row was removed; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })
})

describe('SettingsPage row Remove Label in Name in Chromium', () => {
  it('keeps the visible Remove text inside the accessible name', async () => {
    await page.viewport(1200, 900)
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve(SETTINGS) }),
    )
    renderWithProviders(<SettingsPage />, { operations, language: 'en-US' })

    const remove = await screen.findByRole('button', { name: REMOVE_LABEL })
    const result = await axe.run(remove, {
      runOnly: { type: 'rule', values: ['label-content-name-mismatch'] },
    })
    expect(result.incomplete).toEqual([])
    expect(result.violations).toEqual([])
  })
})
