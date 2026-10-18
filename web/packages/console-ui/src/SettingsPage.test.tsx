/**
 * SettingsPage: the settings read-write page.
 *
 * Its read renders the settled items as a width-limited form (a sensitive
 * item's value arriving already masked from the server renders as-is), the
 * secondary navigation groups them by namespace and narrows the read, and
 * its sticky save bar drives the writes -- one PUT per changed item, a
 * remove calling the remove operation. A row whose layer holds no override
 * is marked as the declared default and carries no remove control; only an
 * overridden row can be removed, which `has_row` decides for a row written
 * as the empty string and for a sensitive row whose text is masked. A
 * refused write renders the
 * answer's code text (a value outside the domain reading the invalid-value
 * text); a landed save clears the item's draft; a refused save keeps it. A
 * refused read (403) renders the permission placeholder without a retry,
 * every other failure the retryable error placeholder.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { zhCN as muiZhCN } from '@mui/material/locale'
import { SettingsPage } from './SettingsPage.js'
import type { ConsoleSetting } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  containerMaxWidthOf,
  emittedStyleText,
  hasCoarsePointerTouchTarget,
} from '@speed/test-utils/emitted-css'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The pagination action labels MUI's zh-CN locale gives its own controls. */
const NEXT_PAGE_LABEL =
  muiZhCN.components?.MuiTablePagination?.defaultProps.getItemAriaLabel?.(
    'next',
  ) ?? ''

const item: ConsoleSetting = {
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

/** A declared item with no row on the selected layer: it resolves to its default. */
const defaultItem: ConsoleSetting = {
  ...item,
  key: 'mail.limit',
  written: '',
  has_row: false,
  value: '3',
}

/**
 * jsdom implements no layout, so `scrollIntoView` does not exist on
 * elements. The tests that observe the region's scroll install this spy;
 * the ones that only drive a failure rely on the call site guarding a
 * missing method.
 */
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

/** Escapes an emotion class name for use inside a regular expression. */
function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/**
 * The `align-items` `element` declares inside its
 * `@media (min-width:<minWidth>px)` block (`0` for the `xs` branch, which
 * MUI emits under `@media (min-width:0px)`). jsdom renders no layout and
 * evaluates no media query, so a viewport-keyed `sx` value is only
 * observable in the generated stylesheet; scoping to this element's own
 * emotion class keeps the assertion a proof for this box.
 */
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

  it('keeps the state column on one line', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve([defaultItem, item]),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByText('默认')

    // A short state label must not split across two rows when the table is
    // squeezed. jsdom does no layout, so the proof is the declared
    // `white-space: nowrap` on the rendered cells.
    for (const text of ['默认', '已覆盖']) {
      const cell = screen.getByText(text).closest('td')
      expect(cell).not.toBeNull()
      expect(cell).toHaveStyle({ whiteSpace: 'nowrap' })
    }
  })

  it('stretches the content column across the cross axis below md', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const nav = await screen.findByRole('navigation', {
      name: '命名空间导航',
    })
    const container = nav.parentElement
    expect(container).not.toBeNull()

    // Below md the skeleton is a column, so the content area takes its
    // cross size from the container's alignment: `flex-start` would size it
    // to its min-content width and leave the table scrolling inside a
    // sliver. jsdom lays nothing out, so the proof is the generated
    // cross-axis alignment -- stretch at the base width, flex-start from md
    // up where the row layout starts; the pixel-level measurement belongs
    // to the browser pass.
    const css = emittedStyleText()
    expect(alignItemsAt(css, container as Element, 0)).toBe('stretch')
    expect(alignItemsAt(css, container as Element, 900)).toBe('flex-start')
  })

  it('caps the key column with the shared primary-column width', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve([item]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const key = await screen.findByText('mail.retry')
    const cell = key.closest('td')
    expect(cell).not.toBeNull()

    // The cap bounds the whole cell -- the key and its copy control -- at
    // the shared primary-identity widths: 160 below md, 200 from md up.
    const wrapper = cell?.firstElementChild
    expect(wrapper).not.toBeNull()
    expect(containerMaxWidthOf(wrapper as Element)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )

    // The key truncates inside the cap instead of widening the cell; the
    // complete value stays on the copy control.
    expect(key).toHaveStyle({
      whiteSpace: 'nowrap',
      overflow: 'hidden',
      textOverflow: 'ellipsis',
    })
  })

  it('marks a row written as the empty string as an override', async () => {
    // An explicitly written empty string and a missing row both carry an
    // empty `written`; only `has_row` tells them apart. The row is a real
    // override, and the page must offer the way to remove it.
    const emptyText: ConsoleSetting = {
      ...item,
      key: 'authn.sms.allowed-prefixes',
      type: 'string',
      default: '[]',
      written: '',
      has_row: true,
      value: '',
    }
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve([emptyText]),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    expect(await screen.findByText('已覆盖')).toBeInTheDocument()
    expect(
      screen.getByRole('button', {
        name: '移除设置项 authn.sms.allowed-prefixes',
      }),
    ).toBeInTheDocument()
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
      has_row: true,
      value: '********',
    }
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve([masked]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    expect(await screen.findByDisplayValue('********')).toBeInTheDocument()

    // The server masks the text but reports the row: the page states the
    // row is overridden and keeps its remove control.
    expect(screen.getByText('已覆盖')).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: '移除设置项 mail.password' }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))

    // The success confirmation names the setting that was removed.
    expect(
      await screen.findByText('设置项「mail.password」已移除'),
    ).toBeInTheDocument()
  })

  it('marks a sensitive item with no row as the default and offers no removal', async () => {
    const masked: ConsoleSetting = {
      ...item,
      key: 'mail.password',
      sensitive: true,
      default: '',
      written: '********',
      has_row: false,
      value: '********',
    }
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve([masked]) }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    expect(await screen.findByDisplayValue('********')).toBeInTheDocument()
    expect(screen.getByText('默认')).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: '移除设置项 mail.password' }),
    ).toBeNull()
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

  it('saves a whole batch and confirms once that the settings landed', async () => {
    const user = userEvent.setup()
    const writes: string[] = []
    const second: ConsoleSetting = {
      ...item,
      key: 'mail.limit',
      value: '3',
      written: '3',
    }
    const operations = createConsoleOperations(
      makeConsoleBackend({
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
    await user.click(screen.getByRole('button', { name: '保存改动' }))

    // One attempt for the whole batch: both PUTs are issued, and one
    // confirmation names the kind of object that was saved -- never a
    // success per item.
    await vi.waitFor(() => {
      expect(writes).toEqual(['retry=9', 'limit=7'])
    })
    expect(await screen.findByText('设置已保存')).toBeInTheDocument()
    expect(screen.getAllByText('设置已保存')).toHaveLength(1)
  })

  it('reports only the first batch failure, keeps its drafts and pages to it', async () => {
    const user = userEvent.setup()
    // Thirteen rows: ten on the first page, three on the second, so moving
    // the table to the first failed item is observable.
    const items: ConsoleSetting[] = Array.from({ length: 13 }, (_, index) => ({
      ...item,
      key: `mail.key-${index}`,
      value: `${index}`,
      written: `${index}`,
    }))
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve(items),
        setSetting: (input) => {
          // Submission order follows the list order: the first failure is
          // key-11, and key-12 fails too behind it with a different code.
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

    // Edit the two later rows on the second page...
    await user.click(await screen.findByRole('button', { name: '下一页' }))
    const eleventh = await screen.findByRole('textbox', { name: 'mail.key-11' })
    await user.clear(eleventh)
    await user.type(eleventh, 'x11')
    const twelfth = screen.getByRole('textbox', { name: 'mail.key-12' })
    await user.clear(twelfth)
    await user.type(twelfth, 'x12')
    // ...and the first row back on the first page, then save from there.
    await user.click(screen.getByRole('button', { name: '上一页' }))
    const first = await screen.findByRole('textbox', { name: 'mail.key-0' })
    await user.clear(first)
    await user.type(first, 'x0')
    await user.click(screen.getByRole('button', { name: '保存改动' }))

    // The first failure in submission order is the one reported; the
    // later failure's text stays out of the banner. The same text also
    // stands in the failing row's helper, so the banner is addressed by
    // its role rather than by text.
    expect(await screen.findByRole('alert')).toHaveTextContent(
      '该值不在该设置项声明的取值范围内。',
    )
    expect(
      screen.queryByText('服务端无法读取该请求，请检查表单后重试。'),
    ).toBeNull()
    // A partial landing is not a success.
    expect(screen.queryByText('设置已保存')).toBeNull()

    // The table moved to the page holding the first failed item, where
    // both failed drafts are still on screen; the landed item's draft was
    // cleared and dropped out of the pending count.
    expect(
      screen.getByRole('textbox', { name: 'mail.key-11' }),
    ).toHaveValue('x11')
    expect(
      screen.getByRole('textbox', { name: 'mail.key-12' }),
    ).toHaveValue('x12')
    expect(screen.queryByRole('textbox', { name: 'mail.key-0' })).toBeNull()
    expect(screen.getByText('2 项待保存')).toBeInTheDocument()

    // Only the first failed row carries the marker, and its described-by
    // target is the resolved reason -- the same text the region shows.
    const firstFailed = screen.getByRole('textbox', { name: 'mail.key-11' })
    expect(firstFailed).toHaveAttribute('aria-invalid', 'true')
    const hintId = firstFailed.getAttribute('aria-describedby')
    expect(hintId).not.toBeNull()
    const hint = document.getElementById(hintId as string)
    expect(hint).toHaveTextContent('该值不在该设置项声明的取值范围内。')
    expect(hint?.textContent).toBe(
      screen.getByRole('alert').textContent,
    )
    // The later failure behind it is not the row the result names.
    expect(
      screen.getByRole('textbox', { name: 'mail.key-12' }),
    ).toHaveAttribute('aria-invalid', 'false')
  })

  it('marks the first failed row inline and brings the region into view', async () => {
    // No reduced-motion preference, so the scroll is animated.
    mockMatchMedia(false)
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    // The failing item sits on the table's third page: scheduling a batch
    // from there is exactly the case where the region is far below the
    // viewport. Changing a valid item on the same page proves the marker
    // follows the failure, not the whole batch.
    const items: ConsoleSetting[] = Array.from({ length: 30 }, (_, index) => ({
      ...item,
      key: `mail.key-${index}`,
      value: `${index}`,
      written: `${index}`,
    }))
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve(items),
        setSetting: (input) =>
          input.key === 'key-24'
            ? Promise.reject({ code: 'settings.invalid_value' })
            : Promise.resolve(),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    await user.click(await screen.findByRole('button', { name: '下一页' }))
    await user.click(await screen.findByRole('button', { name: '下一页' }))
    const failed = await screen.findByRole('textbox', { name: 'mail.key-24' })
    const valid = screen.getByRole('textbox', { name: 'mail.key-25' })
    await user.clear(failed)
    await user.type(failed, 'bad')
    await user.clear(valid)
    await user.type(valid, 'ok')
    await user.click(screen.getByRole('button', { name: '保存改动' }))

    // The row marker: the failing field is invalid and describes itself
    // with the resolved message (never the raw code).
    await waitFor(() => expect(failed).toHaveAttribute('aria-invalid', 'true'))
    const describedBy = failed.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    const hint = document.getElementById(describedBy as string)
    expect(hint).not.toBeNull()
    expect(hint).toHaveTextContent('该值不在该设置项声明的取值范围内。')

    // The region carries that same text -- one resolved source, two places.
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('该值不在该设置项声明的取值范围内。')
    expect(hint?.textContent).toBe(alert.textContent)

    // The valid row in the same batch is not marked, and a partial landing
    // is never a success.
    expect(valid).toHaveAttribute('aria-invalid', 'false')
    expect(screen.queryByText('设置已保存')).toBeNull()

    // The region was scrolled into view, clear of the sticky save bar.
    expect(scrollIntoView).toHaveBeenCalledWith({
      behavior: 'smooth',
      block: 'center',
    })

    // The failed row itself is on screen: the table paged to it.
    expect(screen.getByRole('textbox', { name: 'mail.key-24' })).toHaveValue(
      'bad',
    )

    // The described-by target is real and reachable, so the scan adds no
    // finding the page did not already have.
    await expectNoAxeViolations()
  })

  it('jumps to the region instead of animating under reduced motion', async () => {
    mockMatchMedia(true)
    const scrollIntoView = stubScrollIntoView()
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

    await screen.findByRole('alert')
    expect(scrollIntoView).toHaveBeenCalledWith({
      behavior: 'auto',
      block: 'center',
    })
  })

  it('does not move the page when the batch lands', async () => {
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve([item]),
        setSetting: () => Promise.resolve(),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    const input = await screen.findByDisplayValue('5')
    await user.clear(input)
    await user.type(input, '9')
    await user.click(screen.getByRole('button', { name: '保存改动' }))

    expect(await screen.findByText('设置已保存')).toBeInTheDocument()
    expect(scrollIntoView).not.toHaveBeenCalled()
  })

  it('clears the failed marker when the corrected batch lands', async () => {
    const user = userEvent.setup()
    let attempts = 0
    const operations = createConsoleOperations(
      makeConsoleBackend({
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
    await user.click(screen.getByRole('button', { name: '保存改动' }))
    await screen.findByRole('alert')
    expect(input).toHaveAttribute('aria-invalid', 'true')

    // The marker belongs to one attempt's result: correcting the draft and
    // landing the next attempt leaves neither the marker nor a failure
    // banner behind.
    await user.clear(input)
    await user.type(input, '6')
    await user.click(screen.getByRole('button', { name: '保存改动' }))
    expect(await screen.findByText('设置已保存')).toBeInTheDocument()
    expect(input).toHaveAttribute('aria-invalid', 'false')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('keeps paging, the page size and the header refresh inert while a batch save is in flight', async () => {
    const user = userEvent.setup()
    const items: ConsoleSetting[] = Array.from({ length: 12 }, (_, index) => ({
      ...item,
      key: `mail.key-${index}`,
      value: `${index}`,
      written: `${index}`,
    }))
    let release: (() => void) | undefined
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: () => Promise.resolve(items),
        setSetting: () =>
          new Promise<void>((resolve) => {
            release = resolve
          }),
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })

    const first = await screen.findByRole('textbox', { name: 'mail.key-0' })
    await user.clear(first)
    await user.type(first, 'x')
    await user.click(screen.getByRole('button', { name: '保存改动' }))
    await waitFor(() =>
      expect(screen.getByRole('button', { name: '刷新' })).toBeDisabled(),
    )

    // Paging is part of the save's context: the next page does not open.
    await user.click(screen.getByRole('button', { name: '下一页' }))
    expect(
      screen.getByRole('textbox', { name: 'mail.key-0' }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('textbox', { name: 'mail.key-10' })).toBeNull()

    // Nor does the page-size selector change the slice under the save.
    await user.click(screen.getByRole('combobox'))
    await user.click(await screen.findByRole('option', { name: '25' }))
    expect(screen.getAllByRole('textbox')).toHaveLength(10)
    expect(screen.queryByRole('textbox', { name: 'mail.key-10' })).toBeNull()

    await act(async () => {
      release?.()
    })
  })

  it('clears the batch marker when the row it names is left behind by a later write', async () => {
    const user = userEvent.setup()
    const items: ConsoleSetting[] = [
      { ...item, key: 'mail.a', value: '1', written: '1' },
      { ...item, key: 'mail.b', value: '2', written: '2' },
    ]
    const operations = createConsoleOperations(
      makeConsoleBackend({
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
    await user.click(screen.getByRole('button', { name: '保存改动' }))
    await waitFor(() =>
      expect(screen.getByRole('textbox', { name: 'mail.a' })).toHaveAttribute(
        'aria-invalid',
        'true',
      ),
    )

    // The row removal shares the batch's scope. Its success replaces the
    // result the marker describes, so the old marker must go with it
    // rather than leave a red row with no reason.
    await user.click(
      screen.getByRole('button', { name: '移除设置项 mail.b' }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    expect(
      await screen.findByText('设置项「mail.b」已移除'),
    ).toBeInTheDocument()
    // The page behind the confirmation is reachable again only once the
    // dialog has left; the role query waits for that.
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(
      screen.getByRole('textbox', { name: 'mail.a' }),
    ).toHaveAttribute('aria-invalid', 'false')
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
    // The landed remove fills the page's status region and names the row.
    expect(
      await screen.findByText('设置项「mail.retry」已移除'),
    ).toBeInTheDocument()
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

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '该值不在该设置项声明的取值范围内。',
    )
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
    expect(screen.getByText('设置面未能完成这次请求。')).toBeInTheDocument()
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

  it('re-reads the two namespace reads once when both name all namespaces', async () => {
    const user = userEvent.setup()
    const seen: (string | undefined)[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listSettings: (namespace) => {
          seen.push(namespace)
          return Promise.resolve([item])
        },
      }),
    )
    renderWithProviders(<SettingsPage />, { operations })
    await screen.findByDisplayValue('5')

    // The selected read and the navigation read resolve to one query key
    // with "all namespaces" chosen, so the mount issues one request.
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
    const operations = createConsoleOperations(
      makeConsoleBackend({
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
    // The selected namespace's read and the navigation's whole-set read are
    // distinct keys, so both go out.
    expect(seen.slice(before)).toEqual(['mail', undefined])
  })

  it('keeps unsaved drafts and the current page across a refresh', async () => {
    const user = userEvent.setup()
    const many: ConsoleSetting[] = Array.from({ length: 12 }, (_, index) => ({
      ...item,
      key: `mail.key-${index}`,
      value: `${index}`,
      written: `${index}`,
    }))
    const operations = createConsoleOperations(
      makeConsoleBackend({ listSettings: () => Promise.resolve(many) }),
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

    // The refresh re-reads the settings but does not start a fresh form:
    // the draft and the page the operator was reading stay.
    await waitFor(() => {
      expect(screen.getByRole('textbox', { name: 'mail.key-10' })).toHaveValue(
        '99',
      )
    })
    expect(screen.queryByDisplayValue('0')).toBeNull()
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
