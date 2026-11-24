
import { afterEach, describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { zhCN as muiZhCN } from '@mui/material/locale'
import { SettingsPage } from './SettingsPage.js'
import type { AdminSetting } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { uiKitResources } from '@speed/ui-kit'
import {
  containerMaxWidthOf,
  emittedStyleText,
  hasCoarsePointerTouchTarget,
} from '@speed/test-utils/emitted-css'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import {
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const NEXT_PAGE_LABEL =
  muiZhCN.components?.MuiTablePagination?.defaultProps.getItemAriaLabel?.(
    'next',
  ) ?? ''
const PREVIOUS_PAGE_LABEL =
  muiZhCN.components?.MuiTablePagination?.defaultProps.getItemAriaLabel?.(
    'previous',
  ) ?? ''

function text(
  template: string,
  values: Record<string, string>,
): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
    values[key] ?? match,
  )
}

const COPY_VALUE_TEMPLATE = (
  uiKitResources['zh-CN'] as unknown as {
    readonly dataTable: { readonly copyValue: string }
  }
).dataTable.copyValue

function copyLabel(value: string): string {
  return text(COPY_VALUE_TEMPLATE, { value })
}

const item: AdminSetting = {
  key: 'mail.retry',
  namespace: 'mail',
  type: 'int',
  description: 'Retry count',
  domain: '',
  sensitive: false,
  default: '3',
  written: '5',
  has_row: true,
  value: '5',
}

const defaultItem: AdminSetting = {
  ...item,
  key: 'mail.limit',
  written: '',
  has_row: false,
  value: '3',
}

const sessionItem: AdminSetting = {
  ...item,
  key: 'authn.session.absolute-lifetime',
  namespace: 'authn',
  type: 'duration',
  description: 'Absolute session lifetime',
  default: '24h',
  written: '',
  has_row: false,
  value: '24h',
}

// jsdom implements no layout, so elements have no scrollIntoView; the tests that
// observe the region's scroll install this spy.
function stubScrollIntoView(): ReturnType<typeof vi.fn> {
  const spy = vi.fn()
  Object.defineProperty(Element.prototype, 'scrollIntoView', {
    configurable: true,
    writable: true,
    value: spy,
  })
  return spy
}

afterEach(() => {
  delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView
})

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

// jsdom renders no layout and evaluates no media query, so a viewport-keyed sx
// value is only observable in the generated stylesheet.
function alignItemsAt(
  css: string,
  element: Element,
  minWidth: number,
): string | undefined {
  const emotionClass = Array.from(element.classList).find((name) =>
    name.startsWith('css-'),
  )
  if (emotionClass === undefined) {
    return undefined
  }
  const rule = new RegExp(
    `@media \\(min-width:${minWidth}px\\)\\{\\.${escapeRegExp(emotionClass)}\\{([^}]*)\\}\\}`,
  ).exec(css)?.[1]
  return rule === undefined
    ? undefined
    : /(?:^|;)align-items:([^;]+)/.exec(rule)?.[1]
}

describe('SettingsPage', () => {
  it('renders the settled empty state when no item is declared', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByText(zhCN.settings.empty.title)).toBeInTheDocument()
  })

  it('raises its secondary navigation and compact actions to the floor', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const all = await screen.findByRole('button', { name: zhCN.settings.nav.all })
    const namespace = await screen.findByRole('button', { name: 'mail' })
    const remove = screen.getByRole('button', {
      name: text(zhCN.settings.removeAria, { key: 'mail.retry' }),
    })
    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    for (const control of [all, namespace, remove, refresh]) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('renders a declared item with its resolved value', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByDisplayValue('5')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'mail.retry' })).toBeInTheDocument()
  })

  it('marks the declared default apart from an override and offers removal only on the override', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([defaultItem, item]),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    expect(await screen.findByDisplayValue('3')).toBeInTheDocument()
    expect(screen.getByText(zhCN.settings.state.default)).toBeInTheDocument()
    expect(screen.getByText(zhCN.settings.state.overridden)).toBeInTheDocument()

    expect(
      screen.getByRole('button', { name: text(zhCN.settings.removeAria, { key: 'mail.retry' }) }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: text(zhCN.settings.removeAria, { key: 'mail.limit' }) }),
    ).toBeNull()

    expect(screen.getByRole('textbox', { name: 'mail.limit' })).toBeEnabled()
  })

  it('shows each row its own description and declared default', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([item, sessionItem]),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByText('mail.retry')

    const rowOf = (key: string): HTMLElement => {
      const row = screen.getByText(key).closest('tr')
      expect(row).not.toBeNull()
      return row as HTMLElement
    }

    expect(within(rowOf('mail.retry')).getByText('Retry count')).toBeInTheDocument()
    expect(
      within(rowOf('mail.retry')).getByText(
        text(zhCN.settings.field.defaultValue, { value: '3' }),
      ),
    ).toBeInTheDocument()

    expect(
      within(rowOf('authn.session.absolute-lifetime')).getByText(
        'Absolute session lifetime',
      ),
    ).toBeInTheDocument()
    expect(
      within(rowOf('authn.session.absolute-lifetime')).getByText(
        text(zhCN.settings.field.defaultValue, { value: '24h' }),
      ),
    ).toBeInTheDocument()
  })

  it('shows the declared default on an overridden row, not its current value', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByText('mail.retry')

    const row = screen.getByText('mail.retry').closest('tr') as HTMLElement
    expect(within(row).getByText(zhCN.settings.state.overridden)).toBeInTheDocument()
    expect(
      within(row).getByText(text(zhCN.settings.field.defaultValue, { value: '3' })),
    ).toBeInTheDocument()
    expect(
      within(row).queryByText(
        text(zhCN.settings.field.defaultValue, { value: '5' }),
      ),
    ).toBeNull()
  })

  it('leaves the default line out when the setting declares no default', async () => {
    const masked: AdminSetting = {
      ...item,
      key: 'mail.password',
      sensitive: true,
      description: 'SMTP password',
      default: '',
      written: '********',
      has_row: true,
      value: '********',
    }
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve([masked]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByText('mail.password')

    const row = screen.getByText('mail.password').closest('tr') as HTMLElement
    expect(within(row).getByText('SMTP password')).toBeInTheDocument()
    const labelPrefix = zhCN.settings.field.defaultValue.replace('{{value}}', '')
    expect(
      within(row).queryByText(new RegExp(`^${escapeRegExp(labelPrefix)}`)),
    ).toBeNull()
  })

  it('keeps the state column on one line', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([defaultItem, item]),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByText(zhCN.settings.state.default)

    for (const label of [
      zhCN.settings.state.default,
      zhCN.settings.state.overridden,
    ]) {
      const cell = screen.getByText(label).closest('td')
      expect(cell).not.toBeNull()
      expect(cell).toHaveStyle({ whiteSpace: 'nowrap' })
    }
  })

  it('stretches the content column across the cross axis below md', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const nav = await screen.findByRole('navigation', {
      name: zhCN.settings.nav.label,
    })
    const container = nav.parentElement
    expect(container).not.toBeNull()

    const css = emittedStyleText()
    expect(alignItemsAt(css, container as Element, 0)).toBe('stretch')
    expect(alignItemsAt(css, container as Element, 900)).toBe('flex-start')
  })

  it('caps the key line without capping the declaration beneath it', async () => {
    const longDescription =
      'Whether the mail carrier retries a refused message, measured against the delivery attempts recorded for that message and the configured retry window for this deployment.'
    const described: AdminSetting = { ...item, description: longDescription }
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve([described]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const key = await screen.findByText('mail.retry')
    const cell = key.closest('td')
    expect(cell).not.toBeNull()

    expect(containerMaxWidthOf(key)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )
    expect(key).toHaveStyle({
      whiteSpace: 'nowrap',
      overflow: 'hidden',
      textOverflow: 'ellipsis',
    })
    expect(
      within(cell as HTMLElement).getByRole('button', {
        name: copyLabel('mail.retry'),
      }),
    ).toBeInTheDocument()

    const wrapper = cell?.firstElementChild
    expect(wrapper).not.toBeNull()
    expect(containerMaxWidthOf(wrapper as Element)).toEqual(new Map())
    expect(
      containerMaxWidthOf(
        within(cell as HTMLElement).getByText(longDescription),
      ),
    ).toEqual(new Map())
  })

  it('top-aligns the row so the value and copy control sit on the key line', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const key = await screen.findByText('mail.retry')
    const row = key.closest('tr')
    expect(row).not.toBeNull()

    expect(row).toHaveStyle({ verticalAlign: 'top' })

    const copy = within(row as HTMLElement).getByRole('button', {
      name: copyLabel('mail.retry'),
    })
    const wrapper = copy.parentElement
    expect(wrapper).not.toBeNull()
    expect(wrapper).toHaveStyle({ alignItems: 'flex-start' })
  })

  it('marks a row written as the empty string as an override', async () => {
    const emptyText: AdminSetting = {
      ...item,
      key: 'authn.sms.allowed-prefixes',
      type: 'string',
      default: '[]',
      written: '',
      has_row: true,
      value: '',
    }
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([emptyText]),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    expect(await screen.findByText(zhCN.settings.state.overridden)).toBeInTheDocument()
    expect(
      screen.getByRole('button', {
        name: text(zhCN.settings.removeAria, { key: 'authn.sms.allowed-prefixes' }),
      }),
    ).toBeInTheDocument()
  })

  it('renders the items in a table and pages a large declared set', async () => {
    const many: AdminSetting[] = Array.from({ length: 12 }, (_, index) => ({
      ...item,
      key: `mail.key-${index}`,
      value: `${index}`,
      written: `${index}`,
    }))
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve(many) }),
    )
    const { container } = renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('0')

    expect(container.querySelector('table')).not.toBeNull()
    expect(screen.getAllByRole('textbox')).toHaveLength(10)
    expect(screen.queryByDisplayValue('11')).toBeNull()
  })

  it("renders a sensitive item's masked value as the server sent it", async () => {
    const user = userEvent.setup()
    const masked: AdminSetting = {
      ...item,
      key: 'mail.password',
      sensitive: true,
      default: '',
      written: '********',
      has_row: true,
      value: '********',
    }
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve([masked]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByDisplayValue('********')).toBeInTheDocument()

    expect(screen.getByText(zhCN.settings.state.overridden)).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: text(zhCN.settings.removeAria, { key: 'mail.password' }) }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))

    expect(
      await screen.findByText(text(zhCN.settings.removed, { key: 'mail.password' })),
    ).toBeInTheDocument()
  })

  it('marks a sensitive item with no row as the default and offers no removal', async () => {
    const masked: AdminSetting = {
      ...item,
      key: 'mail.password',
      sensitive: true,
      default: '',
      written: '********',
      has_row: false,
      value: '********',
    }
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve([masked]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    expect(await screen.findByDisplayValue('********')).toBeInTheDocument()
    expect(screen.getByText(zhCN.settings.state.default)).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: text(zhCN.settings.removeAria, { key: 'mail.password' }) }),
    ).toBeNull()
  })

  it('lists the namespaces in the secondary navigation and narrows the read on selection', async () => {
    const user = userEvent.setup()
    const seen: (string | undefined)[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: (namespace) => {
          seen.push(namespace)
          return Promise.resolve(namespace === 'mail' ? [item] : [item])
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('5')

    const nav = screen.getByRole('navigation', { name: zhCN.settings.nav.label })
    expect(within(nav).getByText(zhCN.settings.nav.all)).toBeInTheDocument()
    await user.click(within(nav).getByText('mail'))

    await waitFor(() => expect(seen).toContain('mail'))
  })

  it('writes every changed item from the sticky save bar', async () => {
    const user = userEvent.setup()
    const writes: string[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: (input) => {
          writes.push(`set:${input.namespace}.${input.key}=${input.value}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const input = await screen.findByDisplayValue('5')

    const save = screen.getByRole('button', { name: zhCN.settings.saveBar.save })
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).toHaveAccessibleDescription(zhCN.settings.saveBar.unchanged)

    await user.clear(input)
    await user.type(input, '9')
    await user.click(save)
    expect(writes).toContain('set:mail.retry=9')
    expect(await screen.findByText(zhCN.settings.saved)).toBeInTheDocument()
  })

  it('keeps the save action focusable and explained while nothing changed', async () => {
    const user = userEvent.setup()
    const writes: string[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: (input) => {
          writes.push(`${input.key}=${input.value}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('5')
    const save = screen.getByRole('button', { name: zhCN.settings.saveBar.save })

    save.focus()
    expect(save).toHaveFocus()
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).toHaveAccessibleDescription(zhCN.settings.saveBar.unchanged)

    await user.click(save)
    expect(fireEvent.click(save)).toBe(false)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(writes).toHaveLength(0)
    expect(screen.queryByText(zhCN.settings.saved)).toBeNull()
  })

  it('keeps the save bar out of the progress state while a row removal runs', async () => {
    const user = userEvent.setup()
    let release: (() => void) | undefined
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([item]),
        removeSetting: () =>
          new Promise<void>((resolve) => {
            release = resolve
          }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('5')

    const remove = screen.getByRole('button', {
      name: text(zhCN.settings.removeAria, { key: 'mail.retry' }),
    })
    await user.click(remove)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )

    const save = screen.getByRole('button', {
      name: zhCN.settings.saveBar.save,
    })
    expect(remove).toHaveAttribute('aria-busy', 'true')
    expect(save).not.toHaveAttribute('aria-busy', 'true')
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).not.toHaveAccessibleDescription()

    await act(async () => {
      release?.()
    })
  })

  it('saves a whole batch and confirms once that the settings landed', async () => {
    const user = userEvent.setup()
    const writes: string[] = []
    const second: AdminSetting = {
      ...item,
      key: 'mail.limit',
      value: '3',
      written: '3',
    }
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([item, second]),
        setSetting: (input) => {
          writes.push(`${input.key}=${input.value}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const retry = await screen.findByRole('textbox', { name: 'mail.retry' })
    const limit = screen.getByRole('textbox', { name: 'mail.limit' })
    await user.clear(retry)
    await user.type(retry, '9')
    await user.clear(limit)
    await user.type(limit, '7')
    await user.click(screen.getByRole('button', { name: zhCN.settings.saveBar.save }))

    await waitForCondition(() => {
      expect(writes).toEqual(['retry=9', 'limit=7'])
    })
    expect(await screen.findByText(zhCN.settings.saved)).toBeInTheDocument()
    expect(screen.getAllByText(zhCN.settings.saved)).toHaveLength(1)
  })

  it('reports only the first batch failure, keeps its drafts and pages to it', async () => {
    const user = userEvent.setup()
    const items: AdminSetting[] = Array.from({ length: 13 }, (_, index) => ({
      ...item,
      key: `mail.key-${index}`,
      value: `${index}`,
      written: `${index}`,
    }))
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve(items),
        setSetting: (input) => {
          if (input.key === 'key-11') {
            return Promise.reject({ code: 'settings.invalid_value' })
          }
          if (input.key === 'key-12') {
            return Promise.reject({ code: 'settings.bad_request' })
          }
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    await user.click(await screen.findByRole('button', { name: NEXT_PAGE_LABEL }))
    const eleventh = await screen.findByRole('textbox', { name: 'mail.key-11' })
    await user.clear(eleventh)
    await user.type(eleventh, 'x11')
    const twelfth = screen.getByRole('textbox', { name: 'mail.key-12' })
    await user.clear(twelfth)
    await user.type(twelfth, 'x12')
    await user.click(screen.getByRole('button', { name: PREVIOUS_PAGE_LABEL }))
    const first = await screen.findByRole('textbox', { name: 'mail.key-0' })
    await user.clear(first)
    await user.type(first, 'x0')
    await user.click(screen.getByRole('button', { name: zhCN.settings.saveBar.save }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.settings.invalid_value,
    )
    expect(
      screen.queryByText(zhCN.errors.settings.bad_request),
    ).toBeNull()
    expect(screen.queryByText(zhCN.settings.saved)).toBeNull()

    expect(
      screen.getByRole('textbox', { name: 'mail.key-11' }),
    ).toHaveValue('x11')
    expect(
      screen.getByRole('textbox', { name: 'mail.key-12' }),
    ).toHaveValue('x12')
    expect(screen.queryByRole('textbox', { name: 'mail.key-0' })).toBeNull()
    expect(screen.getByText(text(zhCN.settings.saveBar.pending, { count: '2' }))).toBeInTheDocument()

    const firstFailed = screen.getByRole('textbox', { name: 'mail.key-11' })
    expect(firstFailed).toHaveAttribute('aria-invalid', 'true')
    const hintId = firstFailed.getAttribute('aria-describedby')
    expect(hintId).not.toBeNull()
    const hint = document.getElementById(hintId as string)
    expect(hint).toHaveTextContent(zhCN.errors.settings.invalid_value)
    expect(hint?.textContent).toBe(
      screen.getByRole('alert').textContent,
    )
    expect(
      screen.getByRole('textbox', { name: 'mail.key-12' }),
    ).toHaveAttribute('aria-invalid', 'false')
  })

  it('marks the first failed row inline and brings the region into view', async () => {
    mockMatchMedia(false)
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    const items: AdminSetting[] = Array.from({ length: 30 }, (_, index) => ({
      ...item,
      key: `mail.key-${index}`,
      value: `${index}`,
      written: `${index}`,
    }))
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve(items),
        setSetting: (input) =>
          input.key === 'key-24'
            ? Promise.reject({ code: 'settings.invalid_value' })
            : Promise.resolve(),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    await user.click(await screen.findByRole('button', { name: NEXT_PAGE_LABEL }))
    await user.click(await screen.findByRole('button', { name: NEXT_PAGE_LABEL }))
    const failed = await screen.findByRole('textbox', { name: 'mail.key-24' })
    const valid = screen.getByRole('textbox', { name: 'mail.key-25' })
    await user.clear(failed)
    await user.type(failed, 'bad')
    await user.clear(valid)
    await user.type(valid, 'ok')
    await user.click(screen.getByRole('button', { name: zhCN.settings.saveBar.save }))

    await waitFor(() => expect(failed).toHaveAttribute('aria-invalid', 'true'))
    const describedBy = failed.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    const hint = document.getElementById(describedBy as string)
    expect(hint).not.toBeNull()
    expect(hint).toHaveTextContent(zhCN.errors.settings.invalid_value)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.settings.invalid_value)
    expect(hint?.textContent).toBe(alert.textContent)

    expect(valid).toHaveAttribute('aria-invalid', 'false')
    expect(screen.queryByText(zhCN.settings.saved)).toBeNull()

    expect(scrollIntoView).toHaveBeenCalledWith({
      behavior: 'smooth',
      block: 'center',
    })

    expect(screen.getByRole('textbox', { name: 'mail.key-24' })).toHaveValue(
      'bad',
    )

    await expectNoAxeViolations()
  })

  it('jumps to the region instead of animating under reduced motion', async () => {
    mockMatchMedia(true)
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: () => Promise.reject({ code: 'settings.invalid_value' }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const input = await screen.findByDisplayValue('5')
    await user.clear(input)
    await user.type(input, 'x')
    await user.click(screen.getByRole('button', { name: zhCN.settings.saveBar.save }))

    await screen.findByRole('alert')
    expect(scrollIntoView).toHaveBeenCalledWith({
      behavior: 'auto',
      block: 'center',
    })
  })

  it('does not move the page when the batch lands', async () => {
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: () => Promise.resolve(),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const input = await screen.findByDisplayValue('5')
    await user.clear(input)
    await user.type(input, '9')
    await user.click(screen.getByRole('button', { name: zhCN.settings.saveBar.save }))

    expect(await screen.findByText(zhCN.settings.saved)).toBeInTheDocument()
    expect(scrollIntoView).not.toHaveBeenCalled()
  })

  it('clears the failed marker when the corrected batch lands', async () => {
    const user = userEvent.setup()
    let attempts = 0
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: () => {
          attempts += 1
          return attempts === 1
            ? Promise.reject({ code: 'settings.invalid_value' })
            : Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const input = await screen.findByDisplayValue('5')
    await user.clear(input)
    await user.type(input, 'x')
    await user.click(screen.getByRole('button', { name: zhCN.settings.saveBar.save }))
    await screen.findByRole('alert')
    expect(input).toHaveAttribute('aria-invalid', 'true')

    await user.clear(input)
    await user.type(input, '6')
    await user.click(screen.getByRole('button', { name: zhCN.settings.saveBar.save }))
    expect(await screen.findByText(zhCN.settings.saved)).toBeInTheDocument()
    expect(input).toHaveAttribute('aria-invalid', 'false')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('keeps paging, the page size and the header refresh inert while a batch save is in flight', async () => {
    const user = userEvent.setup()
    const items: AdminSetting[] = Array.from({ length: 30 }, (_, index) => ({
      ...item,
      key: `mail.key-${index}`,
      value: `${index}`,
      written: `${index}`,
    }))
    let release: (() => void) | undefined
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve(items),
        setSetting: () =>
          new Promise<void>((resolve) => {
            release = resolve
          }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    // A middle page: after the save settles, neither direction is at a
    // boundary, so each one's recovery is the save's alone.
    await screen.findByRole('textbox', { name: 'mail.key-0' })
    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    const first = await screen.findByRole('textbox', { name: 'mail.key-10' })
    await user.clear(first)
    await user.type(first, 'x')
    await user.click(screen.getByRole('button', { name: zhCN.settings.saveBar.save }))
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: zhCN.common.refresh }),
      ).toHaveAttribute('aria-disabled', 'true'),
    )

    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    expect(
      screen.getByRole('textbox', { name: 'mail.key-10' }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('textbox', { name: 'mail.key-20' })).toBeNull()

    // Nor can the page-size selector change the slice under the save: the
    // write disables it outright.
    const sizeSelect = screen.getByRole('combobox')
    expect(sizeSelect).toHaveAttribute('aria-disabled', 'true')
    expect(sizeSelect).toHaveClass('Mui-disabled')
    expect(screen.getAllByRole('textbox')).toHaveLength(10)
    expect(screen.queryByRole('textbox', { name: 'mail.key-20' })).toBeNull()

    await act(async () => {
      release?.()
    })

    // The save no longer owns the pager: both directions and the size
    // selector are usable again.
    const previous = screen.getByRole('button', { name: PREVIOUS_PAGE_LABEL })
    const next = screen.getByRole('button', { name: NEXT_PAGE_LABEL })
    await waitFor(() => {
      expect(previous).not.toHaveAttribute('aria-disabled')
      expect(next).not.toHaveAttribute('aria-disabled')
    })
    expect(sizeSelect).not.toHaveAttribute('aria-disabled')
    expect(sizeSelect).not.toHaveClass('Mui-disabled')
    await user.click(sizeSelect)
    expect(await screen.findByRole('listbox')).toBeInTheDocument()
  })

  it('marks the pager directions unavailable and keeps them focusable while a batch save is in flight', async () => {
    const user = userEvent.setup()
    const items: AdminSetting[] = Array.from({ length: 30 }, (_, index) => ({
      ...item,
      key: `mail.key-${index}`,
      value: `${index}`,
      written: `${index}`,
    }))
    let release: (() => void) | undefined
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve(items),
        setSetting: () =>
          new Promise<void>((resolve) => {
            release = resolve
          }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    await screen.findByRole('textbox', { name: 'mail.key-0' })
    // A middle page: the save alone, not a boundary, makes both unavailable.
    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    const current = await screen.findByRole('textbox', { name: 'mail.key-10' })

    await user.clear(current)
    await user.type(current, 'x')

    const previous = screen.getByRole('button', { name: PREVIOUS_PAGE_LABEL })
    const next = screen.getByRole('button', { name: NEXT_PAGE_LABEL })
    // A pointer click would move focus to the save action, so activate it
    // without one and keep the direction focused.
    next.focus()
    fireEvent.click(screen.getByRole('button', { name: zhCN.settings.saveBar.save }))
    await waitFor(() =>
      expect(next).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(previous).toHaveAttribute('aria-disabled', 'true')
    expect(next).toHaveFocus()
    // Both stay in the tab order: the attribute, not a native disable.
    expect(next).not.toBeDisabled()
    expect(previous).not.toBeDisabled()
    const sizeSelect = screen.getByRole('combobox')
    expect(sizeSelect).toHaveAttribute('aria-disabled', 'true')
    expect(sizeSelect).toHaveClass('Mui-disabled')
    await user.click(sizeSelect)
    expect(screen.queryByRole('listbox')).toBeNull()

    await user.click(next)
    expect(screen.getByRole('textbox', { name: 'mail.key-10' })).toBeInTheDocument()
    expect(screen.queryByRole('textbox', { name: 'mail.key-20' })).toBeNull()

    await act(async () => {
      release?.()
    })

    // The write no longer owns the pager: both directions are usable again
    // and the size selector opens.
    await waitFor(() => {
      expect(previous).not.toHaveAttribute('aria-disabled')
      expect(next).not.toHaveAttribute('aria-disabled')
    })
    expect(sizeSelect).not.toHaveAttribute('aria-disabled')
    expect(sizeSelect).not.toHaveClass('Mui-disabled')
    await user.click(sizeSelect)
    expect(await screen.findByRole('listbox')).toBeInTheDocument()
  })

  it('keeps the namespace navigation focusable and inert while a batch save is in flight', async () => {
    const user = userEvent.setup()
    const seen: (string | undefined)[] = []
    let release: (() => void) | undefined
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: (namespace) => {
          seen.push(namespace)
          return Promise.resolve([item])
        },
        setSetting: () =>
          new Promise<void>((resolve) => {
            release = resolve
          }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const input = await screen.findByDisplayValue('5')
    await user.clear(input)
    await user.type(input, '9')
    await user.click(
      screen.getByRole('button', { name: zhCN.settings.saveBar.save }),
    )

    const nav = screen.getByRole('navigation', { name: zhCN.settings.nav.label })
    const mailButton = within(nav).getByRole('button', { name: 'mail' })
    await waitFor(() =>
      expect(mailButton).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(mailButton).not.toBeDisabled()
    mailButton.focus()
    expect(mailButton).toHaveFocus()
    await user.click(mailButton)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(seen).toEqual([undefined])

    await act(async () => {
      release?.()
    })
  })

  it('clears the batch marker when the row it names is left behind by a later write', async () => {
    const user = userEvent.setup()
    const items: AdminSetting[] = [
      { ...item, key: 'mail.a', value: '1', written: '1' },
      { ...item, key: 'mail.b', value: '2', written: '2' },
    ]
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve(items),
        setSetting: (input) =>
          input.key === 'a'
            ? Promise.reject({ code: 'settings.invalid_value' })
            : Promise.resolve(),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    const failed = await screen.findByRole('textbox', { name: 'mail.a' })
    const other = screen.getByRole('textbox', { name: 'mail.b' })
    await user.clear(failed)
    await user.type(failed, 'bad')
    await user.clear(other)
    await user.type(other, 'ok')
    await user.click(screen.getByRole('button', { name: zhCN.settings.saveBar.save }))
    await waitFor(() =>
      expect(screen.getByRole('textbox', { name: 'mail.a' })).toHaveAttribute(
        'aria-invalid',
        'true',
      ),
    )

    await user.click(
      screen.getByRole('button', { name: text(zhCN.settings.removeAria, { key: 'mail.b' }) }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    expect(
      await screen.findByText(text(zhCN.settings.removed, { key: 'mail.b' })),
    ).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(
      screen.getByRole('textbox', { name: 'mail.a' }),
    ).toHaveAttribute('aria-invalid', 'false')
  })

  it('removes a setting through the row control after confirmation', async () => {
    const user = userEvent.setup()
    const writes: string[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([item]),
        removeSetting: (input) => {
          writes.push(`remove:${input.namespace}.${input.key}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('5')

    await user.click(screen.getByRole('button', { name: text(zhCN.settings.removeAria, { key: 'mail.retry' }) }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    expect(writes).toContain('remove:mail.retry')
    expect(
      await screen.findByText(text(zhCN.settings.removed, { key: 'mail.retry' })),
    ).toBeInTheDocument()
  })

  it('renders the invalid-value text when a write is refused', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: () => Promise.reject({ code: 'settings.invalid_value' }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const input = await screen.findByDisplayValue('5')

    await user.clear(input)
    await user.type(input, 'x')
    await user.click(screen.getByRole('button', { name: zhCN.settings.saveBar.save }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.settings.invalid_value,
    )
    expect(input).toHaveValue('x')
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByText(zhCN.settings.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.settings.error.description)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.settings.retry })).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: zhCN.settings.retry })).toBeNull()
  })

  it('re-reads the two namespace reads once when both name all namespaces', async () => {
    const user = userEvent.setup()
    const seen: (string | undefined)[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: (namespace) => {
          seen.push(namespace)
          return Promise.resolve([item])
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('5')

    expect(seen).toEqual([undefined])

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitFor(() => expect(seen).toHaveLength(2))
    expect(seen).toEqual([undefined, undefined])
  })

  it('re-reads the selected namespace and the navigation read on refresh', async () => {
    const user = userEvent.setup()
    const seen: (string | undefined)[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listSettings: (namespace) => {
          seen.push(namespace)
          return Promise.resolve([item])
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('5')
    const nav = screen.getByRole('navigation', {
      name: zhCN.settings.nav.label,
    })
    await user.click(within(nav).getByText('mail'))
    await waitFor(() => expect(seen).toContain('mail'))

    const before = seen.length
    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitFor(() => expect(seen.length).toBe(before + 2))
    expect(seen.slice(before)).toEqual(['mail', undefined])
  })

  it('keeps unsaved drafts and the current page across a refresh', async () => {
    const user = userEvent.setup()
    const many: AdminSetting[] = Array.from({ length: 12 }, (_, index) => ({
      ...item,
      key: `mail.key-${index}`,
      value: `${index}`,
      written: `${index}`,
    }))
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve(many) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('0')

    await user.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    const field = await screen.findByRole('textbox', { name: 'mail.key-10' })
    await user.clear(field)
    await user.type(field, '99')

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )

    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: 'mail.key-10' })).toHaveValue(
        '99',
      )
    })
    expect(screen.queryByDisplayValue('0')).toBeNull()
  })

  it('has no axe violations', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('5')
    await expectNoAxeViolations()
  })
})
