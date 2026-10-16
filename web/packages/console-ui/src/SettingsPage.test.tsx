/**
 * SettingsPage: the settings read-write page.
 *
 * Its read renders the settled items as a table (a sensitive item's value
 * arriving already masked from the server renders as-is), the namespace
 * filter narrows the read, and its writes drive the operations bundle -- a
 * set and a remove each call the matching operation, and a refused write
 * renders the answer's code text (a value outside the domain reading the
 * invalid-value text). A save that lands clears the row's draft input; a
 * refused save keeps it.
 */

import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SettingsPage } from './SettingsPage.js'
import type { ConsoleSetting } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
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

describe('SettingsPage', () => {
  it('renders the settled empty state when no item is declared', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByText('暂无设置')).toBeInTheDocument()
  })

  it('renders a declared item with its resolved value', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByText('mail.retry')).toBeInTheDocument()
    expect(screen.getByText('5')).toBeInTheDocument()
  })

  it('renders a sensitive item\'s masked value as the server sent it', async () => {
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
    expect(await screen.findByText('mail.password')).toBeInTheDocument()
    expect(screen.getByText('********')).toBeInTheDocument()
    expect(screen.getByText('是')).toBeInTheDocument()
  })

  it('applies the namespace filter to the read', async () => {
    const user = userEvent.setup()
    const seen: (string | undefined)[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: (namespace) => {
          seen.push(namespace)
          return Promise.resolve([])
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByText('暂无设置')

    await user.type(screen.getByLabelText('命名空间'), 'mail')
    await user.click(screen.getByRole('button', { name: '加载' }))

    expect(seen).toContain('mail')
  })

  it('writes and removes a setting through the row controls', async () => {
    const user = userEvent.setup()
    const writes: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: (input) => {
          writes.push(`set:${input.namespace}.${input.key}=${input.value}`)
          return Promise.resolve()
        },
        removeSetting: (input) => {
          writes.push(`remove:${input.namespace}.${input.key}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByText('mail.retry')

    await user.type(screen.getByLabelText('新值'), '9')
    await user.click(screen.getByRole('button', { name: '保存' }))
    // The route addresses the item by the namespace and the rest of the
    // key under it (the full key is mail.retry).
    expect(writes).toContain('set:mail.retry=9')

    await user.click(screen.getByRole('button', { name: '移除' }))
    expect(writes).toContain('remove:mail.retry')
  })

  it('renders the invalid-value text when a write is refused', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: () =>
          Promise.reject({ code: 'settings.invalid_value' }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByText('mail.retry')

    await user.type(screen.getByLabelText('新值'), 'x')
    await user.click(screen.getByRole('button', { name: '保存' }))

    expect(
      await screen.findByText('该值不在该设置项声明的取值范围内。'),
    ).toBeInTheDocument()
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

  it('renders the declared items as a table with column headers', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByText('mail.retry')
    for (const name of ['键', '类型', '当前值', '操作']) {
      expect(
        screen.getByRole('columnheader', { name }),
      ).toBeInTheDocument()
    }
    expect(screen.getByRole('cell', { name: 'int' })).toBeInTheDocument()
  })

  it('clears the draft value once a save lands', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: () => Promise.resolve(),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByText('mail.retry')

    const input = screen.getByLabelText('新值')
    await user.type(input, '9')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => expect(input).toHaveValue(''))
  })

  it('keeps the draft value when a save is refused', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: () =>
          Promise.reject({ code: 'settings.invalid_value' }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByText('mail.retry')

    const input = screen.getByLabelText('新值')
    await user.type(input, 'x')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await screen.findByText('该值不在该设置项声明的取值范围内。')
    expect(input).toHaveValue('x')
  })

  it('has no axe violations', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByText('mail.retry')
    await expectNoAxeViolations()
  })
})
