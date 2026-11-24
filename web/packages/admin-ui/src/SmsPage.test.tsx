import { afterEach, describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import { createClient } from '@speed/api-client'
import { SmsPage } from './SmsPage.js'
import type {
  AdminSmsTemplate,
  AdminSmsTemplatePage,
} from './operations.js'
import { renderWithProviders, createAdminUiI18n } from '../test-utils/render.js'
import { ADMIN_UI_NAMESPACE } from './resources.js'
import {
  containerDeclarationsOf,
  containerMaxWidthOf,
  hasCoarsePointerTouchTarget,
} from '@speed/test-utils/emitted-css'
import {
  createAdminOperations,
  makeAdminBackend,
  type AdminBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

function text(
  template: string,
  values: Record<string, string>,
): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
    values[key] ?? match,
  )
}

const SOURCE_LABEL = 'sms-source-runtime'
const APPROVAL_LABEL = 'sms-approval-approved'

function template(overrides: Partial<AdminSmsTemplate> = {}): AdminSmsTemplate {
  return {
    name: 'login-code',
    carrier: 'log',
    kind: 'notification',
    source: 'runtime',
    params: ['code'],
    locales: ['zh-CN'],
    bodies: {},
    attributes: {},
    approvals: { 'zh-CN': { state: 'approved' } },
    enabled: true,
    version: 1,
    created_at: '2026-10-15T12:00:00Z',
    updated_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function page(templates: readonly AdminSmsTemplate[]) {
  return { templates: [...templates], next_cursor: '' }
}

function withTemplates(
  overrides: Partial<AdminBackend> = {},
): ReturnType<typeof createAdminOperations> {
  return createAdminOperations(
    makeAdminBackend({
      listSmsTemplates: () => Promise.resolve(page([template()])),
      ...overrides,
    }),
  )
}

function section(name: string): HTMLElement {
  const element = screen.getByRole('heading', { name }).closest('section')
  if (element === null) {
    throw new Error(`no section headed ${name}`)
  }
  return element as HTMLElement
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((settle, fail) => {
    resolve = settle
    reject = fail
  })
  return { promise, resolve, reject }
}

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
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(<SmsPage />, { operations })
    expect(await screen.findByText(zhCN.sms.empty.title)).toBeInTheDocument()
  })

  it('lists templates with their approval state and enablement', async () => {
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    expect(await screen.findByText('login-code')).toBeInTheDocument()
    expect(
      screen.getByText(`zh-CN: ${zhCN.sms.approval.approved}`),
    ).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: text(zhCN.sms.disableAria, { name: 'login-code' }) })).toBeInTheDocument()
  })

  it('caps the primary name column at the shared primary-column width', async () => {
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    const cell = (await screen.findByText('login-code')).closest(
      'td',
    ) as HTMLElement
    expect(containerMaxWidthOf(cell.firstElementChild as Element)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )
  })

  it('keeps the status column visible from the sm container tier', async () => {
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    const row = (
      await screen.findByRole('switch', { name: text(zhCN.sms.disableAria, { name: 'login-code' }) })
    ).closest('tr') as HTMLElement
    const enabled = within(row).getByText(zhCN.sms.enabledYes).closest('td') as HTMLElement
    const declarations = containerDeclarationsOf(enabled)
    expect(declarations.get(0)).toContain('display:none')
    expect(declarations.get(600)).toContain('display:table-cell')
  })

  it('disables the refresh while the register scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withTemplates({
      registerSmsTemplate: () => pending.promise,
    })
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')
    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    expect(refresh).not.toHaveAttribute('aria-disabled', 'true')

    await user.type(screen.getAllByLabelText(zhCN.sms.register.nameLabel)[0]!, 'welcome')
    await user.click(screen.getAllByRole('button', { name: zhCN.sms.register.submit })[0]!)
    expect(refresh).toHaveAttribute('aria-disabled', 'true')

    pending.resolve()
    await waitForCondition(() =>
      expect(refresh).not.toHaveAttribute('aria-disabled', 'true'),
    )
  })

  it('disables the refresh while the offer scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withTemplates({
      offerSmsTemplate: () => pending.promise,
    })
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')
    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })

    await user.type(screen.getByLabelText(zhCN.sms.offer.contentLabel), 'your code is {code}')
    await user.type(screen.getAllByLabelText(zhCN.sms.offer.nameLabel)[1]!, 'welcome')
    await user.type(screen.getByLabelText(zhCN.sms.offer.kindLabel), 'verification')
    await user.type(screen.getByLabelText(zhCN.sms.offer.localeLabel), 'zh-CN')
    await user.click(screen.getByRole('button', { name: zhCN.sms.offer.submit }))
    expect(refresh).toHaveAttribute('aria-disabled', 'true')

    pending.resolve()
    await waitForCondition(() =>
      expect(refresh).not.toHaveAttribute('aria-disabled', 'true'),
    )
  })

  it('disables the refresh while a table scope write is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withTemplates({
      setSmsTemplateEnabled: () => pending.promise,
    })
    renderWithProviders(<SmsPage />, { operations })
    const toggle = await screen.findByRole('switch', {
      name: text(zhCN.sms.disableAria, { name: 'login-code' }),
    })
    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })

    await user.click(toggle)
    expect(refresh).toHaveAttribute('aria-disabled', 'true')

    pending.resolve()
    await waitForCondition(() =>
      expect(refresh).not.toHaveAttribute('aria-disabled', 'true'),
    )
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    const toggle = await screen.findByRole('switch', {
      name: text(zhCN.sms.disableAria, { name: 'login-code' }),
    })
    const toggleRoot = toggle.closest('.MuiSwitch-root')
    expect(toggleRoot).not.toBeNull()
    const remove = screen.getByRole('button', {
      name: text(zhCN.sms.removeAria, { name: 'login-code' }),
    })
    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    for (const control of [toggleRoot as Element, remove, refresh]) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('keeps each create submit unavailable until its required fields carry text', async () => {
    const user = userEvent.setup()
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')

    const register = section(zhCN.sms.register.title)
    const offer = section(zhCN.sms.offer.title)
    const registerSubmit = within(register).getByRole('button', {
      name: zhCN.sms.register.submit,
    })
    const offerSubmit = within(offer).getByRole('button', {
      name: zhCN.sms.offer.submit,
    })
    expect(registerSubmit).toHaveAttribute('aria-disabled', 'true')
    expect(offerSubmit).toHaveAttribute('aria-disabled', 'true')

    await user.type(
      within(register).getByLabelText(zhCN.sms.register.nameLabel),
      '   ',
    )
    expect(registerSubmit).toHaveAttribute('aria-disabled', 'true')
    await user.type(
      within(register).getByLabelText(zhCN.sms.register.nameLabel),
      'welcome',
    )
    expect(registerSubmit).not.toHaveAttribute('aria-disabled', 'true')

    await user.type(within(offer).getByLabelText(zhCN.sms.offer.nameLabel), 'welcome')
    expect(offerSubmit).toHaveAttribute('aria-disabled', 'true')
    await user.type(within(offer).getByLabelText(zhCN.sms.offer.contentLabel), '   ')
    expect(offerSubmit).toHaveAttribute('aria-disabled', 'true')
    await user.type(
      within(offer).getByLabelText(zhCN.sms.offer.contentLabel),
      'your code is ${code}',
    )
    expect(offerSubmit).toHaveAttribute('aria-disabled', 'true')
    await user.type(
      within(offer).getByLabelText(zhCN.sms.offer.kindLabel),
      'verification',
    )
    expect(offerSubmit).toHaveAttribute('aria-disabled', 'true')
    await user.type(
      within(offer).getByLabelText(zhCN.sms.offer.localeLabel),
      'zh-CN',
    )
    expect(offerSubmit).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('keeps the template toggle focused while its write is in flight and ignores a second activation', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const writes: boolean[] = []
    const operations = withTemplates({
      setSmsTemplateEnabled: (input) => {
        writes.push(input.enabled)
        return pending.promise
      },
    })
    renderWithProviders(<SmsPage />, { operations })
    const toggle = await screen.findByRole('switch', {
      name: text(zhCN.sms.disableAria, { name: 'login-code' }),
    })

    await user.click(toggle)

    expect(toggle).toHaveFocus()
    expect(toggle).toHaveAttribute('aria-disabled', 'true')
    expect(toggle).toBeChecked()

    await user.keyboard(' ')
    expect(writes).toEqual([false])

    pending.resolve()
    await waitForCondition(() =>
      expect(toggle).not.toHaveAttribute('aria-disabled', 'true'),
    )
    expect(toggle).toHaveFocus()
  })

  it('marks only the row whose removal runs busy and holds its siblings back', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const removed: string[] = []
    const operations = withTemplates({
      listSmsTemplates: () =>
        Promise.resolve(
          page([
            template({ name: 'alpha' }),
            template({ name: 'bravo' }),
            template({ name: 'charlie' }),
          ]),
        ),
      removeSmsTemplate: (name) => {
        removed.push(name)
        return pending.promise
      },
    })
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('bravo')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.sms.removeAria, { name: 'bravo' }),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )
    await waitForCondition(() => expect(removed).toEqual(['bravo']))

    const active = screen.getByRole('button', {
      name: text(zhCN.sms.removeAria, { name: 'bravo' }),
    })
    expect(active).toHaveAttribute('aria-busy', 'true')
    expect(active).toHaveAttribute('aria-disabled', 'true')
    const sibling = screen.getByRole('button', {
      name: text(zhCN.sms.removeAria, { name: 'alpha' }),
    })
    expect(sibling).toHaveAttribute('aria-disabled', 'true')
    expect(sibling).not.toHaveAttribute('aria-busy', 'true')
    expect(sibling).not.toHaveAccessibleDescription()

    await user.click(sibling)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(removed).toEqual(['bravo'])

    await act(async () => {
      pending.resolve()
    })
  })

  it('holds every row’s remove control back while a toggle is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const writes: boolean[] = []
    const operations = withTemplates({
      listSmsTemplates: () =>
        Promise.resolve(
          page([template({ name: 'alpha' }), template({ name: 'bravo' })]),
        ),
      setSmsTemplateEnabled: (input) => {
        writes.push(input.enabled)
        return pending.promise
      },
    })
    renderWithProviders(<SmsPage />, { operations })
    const toggle = await screen.findByRole('switch', {
      name: text(zhCN.sms.disableAria, { name: 'alpha' }),
    })
    await user.click(toggle)
    await waitForCondition(() => expect(writes).toEqual([false]))

    for (const name of ['alpha', 'bravo']) {
      const remove = screen.getByRole('button', {
        name: text(zhCN.sms.removeAria, { name }),
      })
      expect(remove).toHaveAttribute('aria-disabled', 'true')
      expect(remove).not.toHaveAttribute('aria-busy', 'true')
      expect(remove).not.toHaveAccessibleDescription()
    }

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.sms.removeAria, { name: 'bravo' }),
      }),
    )
    expect(screen.queryByRole('dialog')).toBeNull()

    await act(async () => {
      pending.resolve()
    })
  })

  it('keeps an unavailable create submit focusable and explains the missing fields', async () => {
    const user = userEvent.setup()
    const registered: string[] = []
    const offered: string[] = []
    const operations = withTemplates({
      registerSmsTemplate: (input) => {
        registered.push(input.name)
        return Promise.resolve()
      },
      offerSmsTemplate: (input) => {
        offered.push(input.name)
        return Promise.resolve()
      },
    })
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')
    const register = section(zhCN.sms.register.title)
    const offer = section(zhCN.sms.offer.title)
    const registerSubmit = within(register).getByRole('button', {
      name: zhCN.sms.register.submit,
    })
    const offerSubmit = within(offer).getByRole('button', {
      name: zhCN.sms.offer.submit,
    })

    expect(registerSubmit).toHaveAttribute('aria-disabled', 'true')
    expect(registerSubmit).toHaveAccessibleDescription(
      text(zhCN.sms.missingFields, { fields: zhCN.sms.register.nameLabel }),
    )

    registerSubmit.focus()
    expect(registerSubmit).toHaveFocus()
    await user.click(registerSubmit)
    expect(registerSubmit).toHaveFocus()
    expect(registered).toEqual([])

    expect(offerSubmit).toHaveAccessibleDescription(
      text(zhCN.sms.missingFields, {
        fields: [
          zhCN.sms.offer.nameLabel,
          zhCN.sms.offer.contentLabel,
          zhCN.sms.offer.kindLabel,
          zhCN.sms.offer.localeLabel,
        ].join(zhCN.sms.listSeparator),
      }),
    )
  })

  it('keeps the offer submit focused through an in-flight write and ignores Enter', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const offered: string[] = []
    const operations = withTemplates({
      offerSmsTemplate: (input) => {
        offered.push(input.name)
        return pending.promise
      },
    })
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')
    const offer = section(zhCN.sms.offer.title)
    for (const [label, value] of [
      [zhCN.sms.offer.nameLabel, 'welcome'],
      [zhCN.sms.offer.contentLabel, 'your code is {code}'],
      [zhCN.sms.offer.kindLabel, 'verification'],
      [zhCN.sms.offer.localeLabel, 'zh-CN'],
    ] as const) {
      await user.type(within(offer).getByLabelText(label), value)
    }
    const submit = within(offer).getByRole('button', {
      name: zhCN.sms.offer.submit,
    })
    await user.click(submit)

    expect(submit).toHaveFocus()
    expect(submit).toHaveAttribute('aria-busy', 'true')
    await user.keyboard('{Enter}')
    expect(offered).toEqual(['welcome'])

    pending.resolve()
    await waitForCondition(() =>
      expect(submit).not.toHaveAttribute('aria-busy', 'true'),
    )
    expect(submit).toHaveFocus()
  })

  it('returns both create forms to their initial state after a landed write', async () => {
    const user = userEvent.setup()
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')

    const register = section(zhCN.sms.register.title)
    await user.type(
      within(register).getByLabelText(zhCN.sms.register.nameLabel),
      'welcome',
    )
    await user.type(
      within(register).getByLabelText(zhCN.sms.register.paramsLabel),
      'code, name',
    )
    await user.type(
      within(register).getByLabelText(zhCN.sms.register.codesLabel),
      'zh-CN:1001',
    )
    await user.click(
      within(register).getByRole('button', { name: zhCN.sms.register.submit }),
    )
    expect(
      await screen.findByText(text(zhCN.sms.registered, { name: 'welcome' })),
    ).toBeInTheDocument()
    expect(
      within(register).getByLabelText(zhCN.sms.register.nameLabel),
    ).toHaveValue('')
    expect(
      within(register).getByLabelText(zhCN.sms.register.paramsLabel),
    ).toHaveValue('')
    expect(
      within(register).getByLabelText(zhCN.sms.register.codesLabel),
    ).toHaveValue('')

    const offer = section(zhCN.sms.offer.title)
    await user.type(within(offer).getByLabelText(zhCN.sms.offer.nameLabel), 'welcome')
    await user.type(
      within(offer).getByLabelText(zhCN.sms.offer.contentLabel),
      'your code is ${code}',
    )
    await user.type(
      within(offer).getByLabelText(zhCN.sms.offer.kindLabel),
      'verification',
    )
    await user.type(
      within(offer).getByLabelText(zhCN.sms.offer.localeLabel),
      'zh-CN',
    )
    await user.type(
      within(offer).getByLabelText(zhCN.sms.offer.paramsLabel),
      'code',
    )
    await user.type(
      within(offer).getByLabelText(zhCN.sms.offer.remarkLabel),
      'for login',
    )
    await user.click(
      within(offer).getByRole('button', { name: zhCN.sms.offer.submit }),
    )
    expect(
      await screen.findByText(text(zhCN.sms.offered, { name: 'welcome' })),
    ).toBeInTheDocument()
    for (const label of [
      zhCN.sms.offer.nameLabel,
      zhCN.sms.offer.contentLabel,
      zhCN.sms.offer.kindLabel,
      zhCN.sms.offer.localeLabel,
      zhCN.sms.offer.paramsLabel,
      zhCN.sms.offer.remarkLabel,
    ]) {
      expect(within(offer).getByLabelText(label)).toHaveValue('')
    }
  })

  it('registers a template through the bundle', async () => {
    const user = userEvent.setup()
    const registered: string[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        registerSmsTemplate: (input) => {
          registered.push(input.name)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SmsPage />, { operations })
    await user.type(screen.getAllByLabelText(zhCN.sms.register.nameLabel)[0]!, 'welcome')
    await user.type(screen.getAllByLabelText(zhCN.sms.register.paramsLabel)[0]!, 'code, name')
    await user.type(screen.getByLabelText(zhCN.sms.register.codesLabel), 'zh-CN:1001')
    await user.click(screen.getAllByRole('button', { name: zhCN.sms.register.submit })[0]!)
    await waitForCondition(() => {
      expect(registered).toEqual(['welcome'])
    })
  })

  it('offers a template for review through the bundle', async () => {
    const user = userEvent.setup()
    const offered: string[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        offerSmsTemplate: (input) => {
          offered.push(input.name)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SmsPage />, { operations })
    await user.type(screen.getByLabelText(zhCN.sms.offer.contentLabel), 'your code is {code}')
    await user.type(screen.getAllByLabelText(zhCN.sms.offer.nameLabel)[1]!, 'welcome')
    await user.type(screen.getByLabelText(zhCN.sms.offer.kindLabel), 'verification')
    await user.type(screen.getByLabelText(zhCN.sms.offer.localeLabel), 'zh-CN')
    await user.click(screen.getByRole('button', { name: zhCN.sms.offer.submit }))
    await waitForCondition(() => {
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
    await user.click(screen.getByRole('switch', { name: text(zhCN.sms.disableAria, { name: 'login-code' }) }))
    await user.click(screen.getByRole('button', { name: text(zhCN.sms.removeAria, { name: 'login-code' }) }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    await waitForCondition(() => {
      expect(toggles).toEqual([false])
      expect(removed).toEqual(['login-code'])
    })
  })

  it('renders the generic failure placeholder and keeps both write forms when the read answers 404', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listSmsTemplates: () =>
          Promise.reject({ status: 404, code: 'client.http.404' }),
      }),
    )
    renderWithProviders(<SmsPage />, { operations })

    expect(await screen.findByText(zhCN.sms.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.error.description)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.sms.retry })).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.register.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.offer.title)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.sms.register.submit })).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByRole('button', { name: zhCN.sms.offer.submit })).toHaveAttribute('aria-disabled', 'true')
  })

  it('renders the generic failure placeholder and keeps both write forms when the entry document answers the read', async () => {
    // A deployment serving the console from the API's own endpoint answers an
    // unclaimed path with the entry document: HTTP 200 and text/html.
    const api = createClient({
      baseUrl: 'https://console.example.test',
      fetch: async () =>
        new Response('<!doctype html><html><body></body></html>', {
          status: 200,
          headers: { 'content-type': 'text/html; charset=utf-8' },
        }),
    })
    const operations = createAdminOperations(
      makeAdminBackend({
        listSmsTemplates: () =>
          api<AdminSmsTemplatePage>('/api/v1/system/sms/templates'),
      }),
    )
    renderWithProviders(<SmsPage />, { operations })

    expect(await screen.findByText(zhCN.sms.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.error.description)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.sms.retry })).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.register.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.offer.title)).toBeInTheDocument()
  })

  it('keeps both write forms when a mounted surface fails temporarily', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listSmsTemplates: () =>
          Promise.reject({ status: 503, code: 'client.http.503' }),
      }),
    )
    renderWithProviders(<SmsPage />, { operations })

    expect(await screen.findByText(zhCN.sms.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.error.description)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.register.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.offer.title)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.sms.register.submit })).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByRole('button', { name: zhCN.sms.offer.submit })).toHaveAttribute('aria-disabled', 'true')
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listSmsTemplates: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<SmsPage />, { operations })
    expect(await screen.findByText(zhCN.sms.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.error.description)).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listSmsTemplates: () =>
          Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<SmsPage />, { operations })
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: zhCN.sms.retry })).toBeNull()
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

    await user.type(screen.getAllByLabelText(zhCN.sms.register.nameLabel)[0]!, 'welcome')
    await user.click(screen.getAllByRole('button', { name: zhCN.sms.register.submit })[0]!)
    expect(
      await within(section(zhCN.sms.register.title)).findByRole('alert'),
    ).toHaveTextContent(zhCN.errors.sms.bad_request)

    await user.click(
      screen.getByRole('switch', { name: text(zhCN.sms.disableAria, { name: 'login-code' }) }),
    )
    await waitFor(() =>
      expect(
        screen.getAllByRole('alert').map((alert) => alert.textContent),
      ).toEqual([zhCN.errors.sms.bad_request, zhCN.errors.sms.conflict]),
    )
    expect(
      within(section(zhCN.sms.register.title)).queryByText(zhCN.errors.sms.conflict),
    ).toBeNull()
    expect(
      within(section(zhCN.sms.offer.title)).queryByText(zhCN.errors.sms.conflict),
    ).toBeNull()
  })

  it('marks the refused template row inline and brings the table region into view', async () => {
    mockMatchMedia(true)
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    const operations = withTemplates({
      setSmsTemplateEnabled: () => Promise.reject({ code: 'sms.conflict' }),
    })
    renderWithProviders(<SmsPage />, { operations })
    const toggle = await screen.findByRole('switch', {
      name: text(zhCN.sms.disableAria, { name: 'login-code' }),
    })
    const remove = screen.getByRole('button', { name: text(zhCN.sms.removeAria, { name: 'login-code' }) })
    await user.click(toggle)

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.sms.conflict,
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

    await user.type(screen.getAllByLabelText(zhCN.sms.register.nameLabel)[0]!, 'welcome')
    await user.click(screen.getAllByRole('button', { name: zhCN.sms.register.submit })[0]!)
    expect(
      await screen.findByText(text(zhCN.sms.registered, { name: 'welcome' })),
    ).toBeInTheDocument()

    await user.type(screen.getByLabelText(zhCN.sms.offer.contentLabel), 'your code is {code}')
    await user.type(screen.getAllByLabelText(zhCN.sms.offer.nameLabel)[1]!, 'welcome')
    await user.type(screen.getByLabelText(zhCN.sms.offer.kindLabel), 'verification')
    await user.type(screen.getByLabelText(zhCN.sms.offer.localeLabel), 'zh-CN')
    await user.click(screen.getByRole('button', { name: zhCN.sms.offer.submit }))
    expect(
      await screen.findByText(text(zhCN.sms.offered, { name: 'welcome' })),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('switch', { name: text(zhCN.sms.disableAria, { name: 'login-code' }) }),
    )
    expect(
      await screen.findByText(text(zhCN.sms.disabledToast, { name: 'login-code' })),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: text(zhCN.sms.removeAria, { name: 'login-code' }) }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    expect(
      await screen.findByText(text(zhCN.sms.removed, { name: 'login-code' })),
    ).toBeInTheDocument()
  })

  it('drops the section results and the row marker when the operator refreshes', async () => {
    const user = userEvent.setup()
    const operations = withTemplates({
      setSmsTemplateEnabled: () => Promise.reject({ code: 'sms.conflict' }),
    })
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')

    await user.type(screen.getAllByLabelText(zhCN.sms.register.nameLabel)[0]!, 'welcome')
    await user.click(screen.getAllByRole('button', { name: zhCN.sms.register.submit })[0]!)
    const registered = text(zhCN.sms.registered, { name: 'welcome' })
    expect(await screen.findByText(registered)).toBeInTheDocument()

    await user.type(screen.getByLabelText(zhCN.sms.offer.contentLabel), 'your code is {code}')
    await user.type(screen.getAllByLabelText(zhCN.sms.offer.nameLabel)[1]!, 'welcome')
    await user.type(screen.getByLabelText(zhCN.sms.offer.kindLabel), 'verification')
    await user.type(screen.getByLabelText(zhCN.sms.offer.localeLabel), 'zh-CN')
    await user.click(screen.getByRole('button', { name: zhCN.sms.offer.submit }))
    const offered = text(zhCN.sms.offered, { name: 'welcome' })
    expect(await screen.findByText(offered)).toBeInTheDocument()

    await user.click(
      screen.getByRole('switch', {
        name: text(zhCN.sms.disableAria, { name: 'login-code' }),
      }),
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.sms.conflict,
    )
    const toggleName = text(zhCN.sms.disableAria, { name: 'login-code' })
    expect(screen.getByRole('switch', { name: toggleName })).toHaveAttribute(
      'aria-invalid',
      'true',
    )

    await user.click(screen.getByRole('button', { name: zhCN.common.refresh }))
    await waitFor(() => expect(screen.queryByText(registered)).toBeNull())
    expect(screen.queryByText(offered)).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(
      screen.getByRole('switch', { name: toggleName }),
    ).toHaveAttribute('aria-invalid', 'false')
  })

  it('translates the sources and approval states it knows and leaves an unknown one as recorded', async () => {
    const i18n = createAdminUiI18n()
    i18n.addResourceBundle(
      'zh-CN',
      ADMIN_UI_NAMESPACE,
      {
        sms: {
          source: { runtime: SOURCE_LABEL },
          approval: { approved: APPROVAL_LABEL },
        },
      },
      true,
      true,
    )
    const operations = withTemplates({
      listSmsTemplates: () =>
        Promise.resolve(
          page([
            template({
              name: 'known',
              source: 'runtime',
              approvals: { 'zh-CN': { state: 'approved' } },
            }),
            template({
              name: 'unknown',
              source: 'other',
              approvals: { 'zh-CN': { state: 'other' } },
            }),
          ]),
        ),
    })
    renderWithProviders(<SmsPage />, { operations, i18n })

    expect(await screen.findByText(SOURCE_LABEL)).toBeInTheDocument()
    expect(
      screen.getByText(`zh-CN: ${APPROVAL_LABEL}`),
    ).toBeInTheDocument()
    expect(screen.getByText('other')).toBeInTheDocument()
    expect(screen.getByText('zh-CN: other')).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    await screen.findByText('login-code')
    await expectNoAxeViolations()
  })
})
