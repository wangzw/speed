/**
 * SettingsPage: the settings read-write page.
 *
 * Its read renders the settled items as a width-limited form (a sensitive
 * item's value arriving already masked from the server renders as-is), the
 * secondary navigation groups them by namespace and narrows the read, and
 * its sticky save bar drives the writes -- one PUT per changed item, a
 * remove calling the remove operation. A row whose layer holds no override
 * is marked as the declared default and carries no remove control; only an
 * overridden row can be removed. A refused write renders the
 * answer's code text (a value outside the domain reading the invalid-value
 * text); a landed save clears the item's draft; a refused save keeps it. A
 * refused read (403) renders the permission placeholder without a retry,
 * every other failure the retryable error placeholder.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SettingsPage } from './SettingsPage.js'
import type { ConsoleSetting } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

const item: ConsoleSetting = {
  key: 'mail.retry',
  namespace: 'mail',
  type: 'int',
  description: 'Retry count',
  domain: '',
  sensitive: false,
  default: '3',
  written: '5',
  value: '5',
}

/** A declared item with no row on the selected layer: it resolves to its default. */
const defaultItem: ConsoleSetting = {
  ...item,
  key: 'mail.limit',
  written: '',
  value: '3',
}

describe('SettingsPage', () => {
  it('renders the settled empty state when no item is declared', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByText('暂无设置')).toBeInTheDocument()
  })

  it('raises its secondary navigation and compact actions to the floor', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const all = await screen.findByRole('button', { name: '全部命名空间' })
    const namespace = await screen.findByRole('button', { name: 'mail' })
    const remove = screen.getByRole('button', {
      name: '移除设置项 mail.retry',
    })
    const refresh = screen.getByRole('button', { name: '刷新' })
    // The namespace navigation and the row/toolbar actions are the same
    // class of compact target; each gates up to the 44x44 floor on a
    // coarse pointer.
    for (const control of [all, namespace, remove, refresh]) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('renders a declared item with its resolved value', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByDisplayValue('5')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'mail.retry' })).toBeInTheDocument()
  })

  it('marks the declared default apart from an override and offers removal only on the override', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve([defaultItem, item]),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    // The default row resolves to its declared value and says so in words;
    // the overridden row is marked as such.
    expect(await screen.findByDisplayValue('3')).toBeInTheDocument()
    expect(screen.getByText('默认')).toBeInTheDocument()
    expect(screen.getByText('已覆盖')).toBeInTheDocument()

    // Only the override has something to take back, so only its row carries
    // a remove control; a default row renders none rather than a no-op.
    expect(
      screen.getByRole('button', { name: '移除设置项 mail.retry' }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: '移除设置项 mail.limit' }),
    ).toBeNull()

    // Both rows stay editable: writing a value over a default is the way a
    // default row becomes overridden.
    expect(screen.getByRole('textbox', { name: 'mail.limit' })).toBeEnabled()
  })

  it('renders the items in a table and pages a large declared set', async () => {
    const many: ConsoleSetting[] = Array.from({ length: 12 }, (_, index) => ({
      ...item,
      key: `mail.key-${index}`,
      value: `${index}`,
      written: `${index}`,
    }))
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve(many) }),
    )
    const { container } = renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('0')

    // A data table, not a single unbounded column...
    expect(container.querySelector('table')).not.toBeNull()
    // ...whose first page holds the default ten rows, leaving the rest for
    // the next page rather than running the whole set down the viewport.
    expect(screen.getAllByRole('textbox')).toHaveLength(10)
    expect(screen.queryByDisplayValue('11')).toBeNull()
  })

  it("renders a sensitive item's masked value as the server sent it", async () => {
    const user = userEvent.setup()
    const masked: ConsoleSetting = {
      ...item,
      key: 'mail.password',
      sensitive: true,
      default: '',
      written: '********',
      value: '********',
    }
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve([masked]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByDisplayValue('********')).toBeInTheDocument()

    // The server masks `written` as well, so the answer cannot say whether
    // the layer holds a row: the row is marked masked and keeps its remove
    // control -- hiding it would take away the only way to clear a real
    // override, and calling it default or overridden would be a claim the
    // answer does not carry.
    expect(screen.getByText('已遮蔽')).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: '移除设置项 mail.password' }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))

    // The answer cannot say whether a row was there to clear, so the notice
    // does not claim one was removed.
    expect(await screen.findByText('移除请求已提交')).toBeInTheDocument()
  })

  it('lists the namespaces in the secondary navigation and narrows the read on selection', async () => {
    const user = userEvent.setup()
    const seen: (string | undefined)[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: (namespace) => {
          seen.push(namespace)
          return Promise.resolve(namespace === 'mail' ? [item] : [item])
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('5')

    // The navigation offers the "all namespaces" entry plus each declared
    // namespace (derived from the whole-set read).
    const nav = screen.getByRole('navigation', { name: '命名空间导航' })
    expect(within(nav).getByText('全部命名空间')).toBeInTheDocument()
    await user.click(within(nav).getByText('mail'))

    await waitFor(() => expect(seen).toContain('mail'))
  })

  it('writes every changed item from the sticky save bar', async () => {
    const user = userEvent.setup()
    const writes: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: (input) => {
          writes.push(`set:${input.namespace}.${input.key}=${input.value}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const input = await screen.findByDisplayValue('5')

    // Nothing changed yet, so the save bar's action is inert.
    const save = screen.getByRole('button', { name: '保存改动' })
    expect(save).toBeDisabled()

    await user.clear(input)
    await user.type(input, '9')
    await user.click(save)
    // The route addresses the item by the namespace and the key rest.
    expect(writes).toContain('set:mail.retry=9')
    // The landed save confirms itself in the page's standing status region.
    expect(await screen.findByText('设置已保存')).toBeInTheDocument()
  })

  it('removes a setting through the row control after confirmation', async () => {
    const user = userEvent.setup()
    const writes: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve([item]),
        removeSetting: (input) => {
          writes.push(`remove:${input.namespace}.${input.key}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('5')

    // The row's remove control names its own setting key.
    await user.click(screen.getByRole('button', { name: '移除设置项 mail.retry' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    expect(writes).toContain('remove:mail.retry')
    // The landed remove fills the page's standing status region.
    expect(await screen.findByText('设置项已移除')).toBeInTheDocument()
  })

  it('renders the invalid-value text when a write is refused', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: () => Promise.reject({ code: 'settings.invalid_value' }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const input = await screen.findByDisplayValue('5')

    await user.clear(input)
    await user.type(input, 'x')
    await user.click(screen.getByRole('button', { name: '保存改动' }))

    expect(
      await screen.findByText('该值不在该设置项声明的取值范围内。'),
    ).toBeInTheDocument()
    // A refused save keeps the draft for a retry.
    expect(input).toHaveValue('x')
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByText('设置加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('has no axe violations', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('5')
    await expectNoAxeViolations()
  })
})
