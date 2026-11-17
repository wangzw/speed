/**
 * SmsPage: the SMS-template administration page.
 *
 * Its read lists the runtime templates (with their approval states), the
 * register and offer forms drive the bundle, and the row's enable/disable
 * toggle and remove drive it too. Every read failure settles into the
 * page's generic failure surface -- the retryable placeholder, or the
 * permission placeholder for a 403 -- and the write forms stay in place,
 * because a write may still land once the read recovers.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { screen, waitFor, within } from '@testing-library/react'
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

/** Fills a locale template's `{{name}}` placeholders. */
function text(
  template: string,
  values: Record<string, string>,
): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
    values[key] ?? match,
  )
}

/** The source and approval labels a test injects into the namespace. */
const SOURCE_LABEL = 'sms-source-runtime'
const APPROVAL_LABEL = 'sms-approval-approved'

function template(overrides: Partial<AdminSmsTemplate> = {}): AdminSmsTemplate {
  return {
    name: 'login-code',
    carrier: 'review',
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

/** The titled section a heading opens, which holds its group's region. */
function section(name: string): HTMLElement {
  const element = screen.getByRole('heading', { name }).closest('section')
  if (element === null) {
    throw new Error(`no section headed ${name}`)
  }
  return element as HTMLElement
}

/** A promise the test settles by hand, for observing an in-flight write. */
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((settle, fail) => {
    resolve = settle
    reject = fail
  })
  return { promise, resolve, reject }
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
    // The cap counts the copy control beside the name: 160px below the
    // table container's md, 200px from it up.
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
    expect(refresh).not.toBeDisabled()

    await user.type(screen.getAllByLabelText(zhCN.sms.register.nameLabel)[0]!, 'welcome')
    await user.click(screen.getAllByRole('button', { name: zhCN.sms.register.submit })[0]!)
    expect(refresh).toBeDisabled()

    pending.resolve()
    await waitForCondition(() => expect(refresh).not.toBeDisabled())
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
    expect(refresh).toBeDisabled()

    pending.resolve()
    await waitForCondition(() => expect(refresh).not.toBeDisabled())
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
    expect(refresh).toBeDisabled()

    pending.resolve()
    await waitForCondition(() => expect(refresh).not.toBeDisabled())
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const operations = withTemplates()
    renderWithProviders(<SmsPage />, { operations })
    const toggle = await screen.findByRole('switch', {
      name: text(zhCN.sms.disableAria, { name: 'login-code' }),
    })
    // The coarse floor is the theme's rule on the switch root, not the
    // visually hidden input the role sits on.
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

  it('keeps each create submit disabled until its required fields carry text', async () => {
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
    expect(registerSubmit).toBeDisabled()
    expect(offerSubmit).toBeDisabled()

    // Whitespace alone is not a name.
    await user.type(
      within(register).getByLabelText(zhCN.sms.register.nameLabel),
      '   ',
    )
    expect(registerSubmit).toBeDisabled()
    await user.type(
      within(register).getByLabelText(zhCN.sms.register.nameLabel),
      'welcome',
    )
    expect(registerSubmit).toBeEnabled()

    // An offer names a template, its content, its kind and its language.
    await user.type(within(offer).getByLabelText(zhCN.sms.offer.nameLabel), 'welcome')
    expect(offerSubmit).toBeDisabled()
    await user.type(within(offer).getByLabelText(zhCN.sms.offer.contentLabel), '   ')
    expect(offerSubmit).toBeDisabled()
    await user.type(
      within(offer).getByLabelText(zhCN.sms.offer.contentLabel),
      'your code is ${code}',
    )
    expect(offerSubmit).toBeDisabled()
    await user.type(
      within(offer).getByLabelText(zhCN.sms.offer.kindLabel),
      'verification',
    )
    expect(offerSubmit).toBeDisabled()
    await user.type(
      within(offer).getByLabelText(zhCN.sms.offer.localeLabel),
      'zh-CN',
    )
    expect(offerSubmit).toBeEnabled()
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
    // Two name fields exist (register + offer); the register one is first.
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
    // Two name fields exist (register + offer); the offer one is second.
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

    // A 404 is one more failed read: the page falls back to its generic
    // retryable placeholder, and both forms stay in place because a write
    // may still land once the read recovers.
    expect(await screen.findByText(zhCN.sms.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.error.description)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.sms.retry })).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.register.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.offer.title)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.sms.register.submit })).toBeDisabled()
    expect(screen.getByRole('button', { name: zhCN.sms.offer.submit })).toBeDisabled()
  })

  it('renders the generic failure placeholder and keeps both write forms when the entry document answers the read', async () => {
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
    const operations = createAdminOperations(
      makeAdminBackend({
        listSmsTemplates: () =>
          api<AdminSmsTemplatePage>('/api/v1/system/sms/templates'),
      }),
    )
    renderWithProviders(<SmsPage />, { operations })

    // The entry document is one more failed read: the same generic
    // retryable placeholder, and both forms stay in place.
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

    // A 503 is the mounted surface failing, not its absence: the forms stay
    // available because a write may still land once the read recovers.
    expect(await screen.findByText(zhCN.sms.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.error.description)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.register.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sms.offer.title)).toBeInTheDocument()
    // Both forms are still there; their submits start disabled because
    // neither has a required field filled yet.
    expect(screen.getByRole('button', { name: zhCN.sms.register.submit })).toBeDisabled()
    expect(screen.getByRole('button', { name: zhCN.sms.offer.submit })).toBeDisabled()
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
    await user.type(screen.getAllByLabelText(zhCN.sms.register.nameLabel)[0]!, 'welcome')
    await user.click(screen.getAllByRole('button', { name: zhCN.sms.register.submit })[0]!)
    expect(
      await within(section(zhCN.sms.register.title)).findByRole('alert'),
    ).toHaveTextContent(zhCN.errors.sms.bad_request)

    // The table's row action lands in the table's scope, not the form's
    // and not the offer form's.
    await user.click(
      screen.getByRole('switch', { name: text(zhCN.sms.disableAria, { name: 'login-code' }) }),
    )
    // The table's own region carries the refusal, and its row marker
    // repeats the one message.
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
    // Reduced motion: the region jumps instead of animating.
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

    // Register names the template.
    await user.type(screen.getAllByLabelText(zhCN.sms.register.nameLabel)[0]!, 'welcome')
    await user.click(screen.getAllByRole('button', { name: zhCN.sms.register.submit })[0]!)
    expect(
      await screen.findByText(text(zhCN.sms.registered, { name: 'welcome' })),
    ).toBeInTheDocument()

    // Offer names the template it hands to the vendor.
    await user.type(screen.getByLabelText(zhCN.sms.offer.contentLabel), 'your code is {code}')
    await user.type(screen.getAllByLabelText(zhCN.sms.offer.nameLabel)[1]!, 'welcome')
    await user.type(screen.getByLabelText(zhCN.sms.offer.kindLabel), 'verification')
    await user.type(screen.getByLabelText(zhCN.sms.offer.localeLabel), 'zh-CN')
    await user.click(screen.getByRole('button', { name: zhCN.sms.offer.submit }))
    expect(
      await screen.findByText(text(zhCN.sms.offered, { name: 'welcome' })),
    ).toBeInTheDocument()

    // The toggle names the template and the state it now has.
    await user.click(
      screen.getByRole('switch', { name: text(zhCN.sms.disableAria, { name: 'login-code' }) }),
    )
    expect(
      await screen.findByText(text(zhCN.sms.disabledToast, { name: 'login-code' })),
    ).toBeInTheDocument()

    // The removal names the template it dropped.
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

    // One result in each of the three scopes: a register success, an offer
    // success and a refused row action with its inline marker.
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

    // A refresh changes the context those results describe; all of them go
    // before the page re-reads.
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
    // Values outside the closed sets keep the recorded text.
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
