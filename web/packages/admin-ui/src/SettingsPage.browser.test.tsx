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

// The settings schema's longest descriptions run past 300 characters.
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

const COPY_LABEL = (
  uiKitResources['en-US'] as unknown as {
    readonly dataTable: { readonly copyValue: string }
  }
).dataTable.copyValue.replace('{{value}}', LONG_DESCRIPTION_ITEM.key)

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

      expect(description.getBoundingClientRect().width).toBeGreaterThan(200)

      expect(row?.getBoundingClientRect().height ?? Infinity).toBeLessThan(
        300,
      )

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

const REMOVE_KEY = 'mail.retry'
const REMOVE_LABEL = enUS.settings.removeAria.replace('{{key}}', REMOVE_KEY)

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

// Both blocks render through SectionCard as a section painted with the container
// role; the register is flush, so its table reaches the rim.
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

    const tableRect = table!.getBoundingClientRect()
    const registerRect = register!.getBoundingClientRect()
    expect(Math.abs(tableRect.left - registerRect.left)).toBeLessThanOrEqual(1.5)
    expect(Math.abs(tableRect.right - registerRect.right)).toBeLessThanOrEqual(1.5)
    expect(Math.abs(tableRect.top - registerRect.top)).toBeLessThanOrEqual(1.5)
  })
})

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

function removeLabel(key: string): string {
  return enUS.settings.removeAria.replace('{{key}}', key)
}

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

function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

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
    expect(removeB).toHaveAttribute('aria-busy', 'true')
    expect(removeB).toHaveAttribute('aria-disabled', 'true')
    // While the dialog stands the page behind it is hidden from role queries.
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
