/**
 * SmsPage: the SMS-template administration page.
 *
 * Its read lists the runtime templates (with their approval states), the
 * register and offer forms drive the bundle, the row's enable/disable
 * toggle and remove drive it too, and a failed read settles into the
 * retryable error placeholder. A 404 (the surface is not mounted) also
 * collapses both write forms, and so does the entry document a shared
 * endpoint answers an unclaimed path with; any other failure keeps them.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import { createClient } from '@speed/api-client'
import { SmsPage } from './SmsPage.js'
import type {
  ConsoleSmsTemplate,
  ConsoleSmsTemplatePage,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
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

/** The titled section a heading opens, which holds its group's region. */
function section(name: string): HTMLElement {
  const element = screen.getByRole('heading', { name }).closest('section')
  if (element === null) {
    throw new Error(`no section headed ${name}`)
  }
  return element as HTMLElement
}

/** jsdom implements no layout; a scroll observer installs this stub. */
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
    expect(screen.getByRole('button', { name: '停用模板 login-code' })).toBeInTheDocument()
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    const toggle = await screen.findByRole('button', {
      name: '停用模板 login-code',
    })
    const remove = screen.getByRole('button', {
      name: '移除模板 login-code',
    })
    const refresh = screen.getByRole('button', { name: '刷新' })
    for (const control of [toggle, remove, refresh]) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
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
    await user.click(screen.getByRole('button', { name: '停用模板 login-code' }))
    await user.click(screen.getByRole('button', { name: '移除模板 login-code' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    await vi.waitFor(() => {
      expect(toggles).toEqual([false])
      expect(removed).toEqual(['login-code'])
    })
  })

  it('collapses both write forms when the administration surface is not mounted', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSmsTemplates: () =>
          Promise.reject({ status: 404, code: 'client.http.404' }),
      }),
    )
    renderWithProviders(<SmsPage />, { operations })

    // The surface has no route on this process: the page says so and keeps
    // the retry, and neither form is in the document -- every submission
    // would reach the same missing route.
    expect(await screen.findByText('短信管理面未装载')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
    expect(screen.queryByText('登记模板')).toBeNull()
    expect(screen.queryByText('提交审核')).toBeNull()
    expect(screen.queryByRole('button', { name: '登记' })).toBeNull()
    expect(screen.queryByRole('button', { name: '提交' })).toBeNull()
  })

  it('collapses both write forms when the entry document answers the read', async () => {
    // A deployment that serves this console from the API's own endpoint
    // answers a path no API route claims with the front-end entry document:
    // HTTP 200 and text/html, not the 404 an API-only deployment gives.
    // The read travels through the real transport, so the refusal under
    // test is the one the deployment produces, not a hand-shaped error.
    const api = createClient({
      baseUrl: 'https://console.example.test',
      fetch: async () =>
        new Response('<!doctype html><html><body></body></html>', {
          status: 200,
          headers: { 'content-type': 'text/html; charset=utf-8' },
        }),
    })
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSmsTemplates: () =>
          api<ConsoleSmsTemplatePage>('/sms/admin/templates'),
      }),
    )
    renderWithProviders(<SmsPage />, { operations })

    // The surface is absent here too: the page says so and keeps the retry,
    // and neither form is rendered.
    expect(await screen.findByText('短信管理面未装载')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
    expect(screen.queryByText('登记模板')).toBeNull()
    expect(screen.queryByText('提交审核')).toBeNull()
  })

  it('keeps both write forms when a mounted surface fails temporarily', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSmsTemplates: () =>
          Promise.reject({ status: 503, code: 'client.http.503' }),
      }),
    )
    renderWithProviders(<SmsPage />, { operations })

    // A 503 is the mounted surface failing, not its absence: the forms stay
    // available because a write may still land once the read recovers.
    expect(await screen.findByText('短信模板加载失败')).toBeInTheDocument()
    expect(screen.getByText('登记模板')).toBeInTheDocument()
    expect(screen.getByText('提交审核')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '登记' })).toBeEnabled()
    expect(screen.getByRole('button', { name: '提交' })).toBeEnabled()
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
    // A read's refusal never settles into a write scope's slot.
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it("reports the form failures and the table failure in their own regions", async () => {
    const user = userEvent.setup()
    const operations = withTemplates({
      registerSmsTemplate: () => Promise.reject({ code: 'sms.bad_request' }),
      setSmsTemplateEnabled: () => Promise.reject({ code: 'sms.conflict' }),
    })
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')

    // The register form's scope carries its own failure.
    await user.type(screen.getAllByLabelText('名称')[0]!, 'welcome')
    await user.click(screen.getAllByRole('button', { name: '登记' })[0]!)
    expect(
      await within(section('登记模板')).findByRole('alert'),
    ).toHaveTextContent('服务端无法读取该请求，请检查表单后重试。')

    // The table's row action lands in the table's scope, not the form's
    // and not the offer form's.
    await user.click(
      screen.getByRole('button', { name: '停用模板 login-code' }),
    )
    // The table's own region carries the refusal, and its row marker
    // repeats the one message.
    const alerts = await screen.findAllByRole('alert')
    expect(alerts.map((alert) => alert.textContent)).toEqual([
      '服务端无法读取该请求，请检查表单后重试。',
      '同名模板已存在。',
    ])
    expect(
      within(section('登记模板')).queryByText('同名模板已存在。'),
    ).toBeNull()
    expect(
      within(section('提交审核')).queryByText('同名模板已存在。'),
    ).toBeNull()
  })

  it('marks the refused template row inline and brings the table region into view', async () => {
    // Reduced motion: the region jumps instead of animating.
    mockMatchMedia(true)
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    const operations = withTemplates({
      setSmsTemplateEnabled: () => Promise.reject({ code: 'sms.conflict' }),
    })
    renderWithProviders(<SmsPage />, { operations })
    const toggle = await screen.findByRole('button', {
      name: '停用模板 login-code',
    })
    const remove = screen.getByRole('button', { name: '移除模板 login-code' })
    await user.click(toggle)

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '同名模板已存在。',
    )
    expect(toggle).toHaveAttribute('aria-invalid', 'true')
    expect(remove).toHaveAttribute('aria-invalid', 'false')
    const hintId = toggle.getAttribute('aria-describedby')
    expect(hintId).not.toBeNull()
    expect(document.getElementById(hintId as string)?.textContent).toBe(
      screen.getByRole('alert').textContent,
    )
    expect(scrollIntoView).toHaveBeenCalledWith({
      behavior: 'auto',
      block: 'center',
    })
    await expectNoAxeViolations()
  })

  it('names the changed template on each landed write', async () => {
    const user = userEvent.setup()
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')

    // Register names the template.
    await user.type(screen.getAllByLabelText('名称')[0]!, 'welcome')
    await user.click(screen.getAllByRole('button', { name: '登记' })[0]!)
    expect(
      await screen.findByText('模板「welcome」已登记'),
    ).toBeInTheDocument()

    // Offer names the template it hands to the vendor.
    await user.type(screen.getByLabelText('内容'), 'your code is {code}')
    await user.type(screen.getAllByLabelText('名称')[1]!, 'welcome')
    await user.click(screen.getByRole('button', { name: '提交' }))
    expect(
      await screen.findByText('模板「welcome」已提交审核'),
    ).toBeInTheDocument()

    // The toggle names the template and the state it now has.
    await user.click(
      screen.getByRole('button', { name: '停用模板 login-code' }),
    )
    expect(
      await screen.findByText('模板「login-code」已停用发送'),
    ).toBeInTheDocument()

    // The removal names the template it dropped.
    await user.click(screen.getByRole('button', { name: '移除模板 login-code' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    expect(
      await screen.findByText('模板「login-code」已移除'),
    ).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')
    await expectNoAxeViolations()
  })
})
