/**
 * SmsPage: the SMS-template administration page.
 *
 * Its read lists the runtime templates (with their approval states), the
 * register and offer forms drive the bundle, the row's enable/disable
 * toggle and remove drive it too, and a failed read settles into the
 * retryable error placeholder.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SmsPage } from './SmsPage.js'
import type { ConsoleSmsTemplate } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
  type ConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

function template(overrides: Partial<ConsoleSmsTemplate> = {}): ConsoleSmsTemplate {
  return {
    name: 'login-code',
    source: 'runtime',
    params: ['code'],
    locales: ['zh-CN'],
    approvals: { 'zh-CN': { state: 'approved' } },
    enabled: true,
    version: 1,
    created_at: '2026-10-15T12:00:00Z',
    updated_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function page(templates: readonly ConsoleSmsTemplate[]) {
  return { templates: [...templates], next_cursor: '' }
}

function withTemplates(
  overrides: Partial<ConsoleBackend> = {},
): ReturnType<typeof createConsoleOperations> {
  return createConsoleOperations(
    makeConsoleBackend({
      listSmsTemplates: () => Promise.resolve(page([template()])),
      ...overrides,
    }),
  )
}

describe('SmsPage', () => {
  it('renders the empty state when no template exists', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<SmsPage />, { operations })
    expect(await screen.findByText('暂无模板')).toBeInTheDocument()
  })

  it('lists templates with their approval state and enablement', async () => {
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    expect(await screen.findByText('login-code')).toBeInTheDocument()
    expect(screen.getByText('zh-CN: approved')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '停用' })).toBeInTheDocument()
  })

  it('registers a template through the bundle', async () => {
    const user = userEvent.setup()
    const registered: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        registerSmsTemplate: (input) => {
          registered.push(input.name)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SmsPage />, { operations })
    // Two name fields exist (register + offer); the register one is first.
    await user.type(screen.getAllByLabelText('名称')[0]!, 'welcome')
    await user.type(screen.getAllByLabelText('参数')[0]!, 'code, name')
    await user.type(screen.getByLabelText('厂商编码'), 'zh-CN:1001')
    await user.click(screen.getAllByRole('button', { name: '登记' })[0]!)
    await vi.waitFor(() => {
      expect(registered).toEqual(['welcome'])
    })
  })

  it('offers a template for review through the bundle', async () => {
    const user = userEvent.setup()
    const offered: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        offerSmsTemplate: (input) => {
          offered.push(input.name)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SmsPage />, { operations })
    await user.type(screen.getByLabelText('内容'), 'your code is {code}')
    // Two name fields exist (register + offer); the offer one is second.
    await user.type(screen.getAllByLabelText('名称')[1]!, 'welcome')
    await user.click(screen.getByRole('button', { name: '提交' }))
    await vi.waitFor(() => {
      expect(offered).toEqual(['welcome'])
    })
  })

  it('toggles a template and removes it through the bundle', async () => {
    const user = userEvent.setup()
    const toggles: boolean[] = []
    const removed: string[] = []
    const operations = withTemplates({
      setSmsTemplateEnabled: (input) => {
        toggles.push(input.enabled)
        return Promise.resolve()
      },
      removeSmsTemplate: (name) => {
        removed.push(name)
        return Promise.resolve()
      },
    })
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')
    await user.click(screen.getByRole('button', { name: '停用' }))
    await user.click(screen.getByRole('button', { name: '移除' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    await vi.waitFor(() => {
      expect(toggles).toEqual([false])
      expect(removed).toEqual(['login-code'])
    })
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSmsTemplates: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<SmsPage />, { operations })
    expect(await screen.findByText('短信模板加载失败')).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSmsTemplates: () =>
          Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<SmsPage />, { operations })
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('has no axe violations', async () => {
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')
    await expectNoAxeViolations()
  })
})
