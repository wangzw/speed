/**
 * AccountsPage in a real browser: the account table at the sm/md/lg
 * floor widths with fixed data. The columns shed by priority and cap the
 * identity cell, so the table must stay inside both the page and its own
 * scroll container. At the 375px mobile floor the table is narrower than
 * its content, and the row-actions column is pinned to the table's
 * scroll edge so Manage and Delete stay visible while the main columns
 * scroll.
 */
import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import type { AuthSession } from '@speed/auth-core'
import { CONFIRM_ARM_LOCKOUT_MS } from '@speed/ui-kit'
import Box from '@mui/material/Box'
import { page, userEvent } from 'vitest/browser'
import { AccountsPage } from './AccountsPage.js'
import type {
  AdminAccount,
  AdminAccountPage,
  AdminAccountSecretCreated,
} from './operations.js'
import { createStepUpSession } from '../test-utils/session.js'
import {
  accountUsername,
  identityLabel,
} from './internal/account-identity.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import {
  CONFIRM_AGAIN_LABEL,
  CONFIRM_LABEL,
  DELETE_ACCOUNT_NAME,
  NEXT_PAGE_LABEL,
  PREVIOUS_PAGE_LABEL,
} from '../test-utils/accounts-page.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const

const ACCOUNTS = [
  accountFixture({
    id: 'account-identifier-with-a-long-value-0001',
    display_name: 'Ada Lovelace',
  }),
  accountFixture({
    id: 'account-identifier-with-a-long-value-0002',
    display_name: 'Grace Hopper',
    status: 'disabled',
  }),
]

/** One account whose primary cell and row actions together are wider
 * than the page content at 375px. */
const NARROW_ACCOUNT = accountFixture({
  id: 'account-identifier-with-a-long-value-0003',
  display_name: 'Ada Lovelace the first programmer of recorded history',
  identifiers: [
    { kind: 'username', value: 'ada.lovelace@example.com', proven: false, sign_in: true, contact: false, default_contact: false },
  ],
})

/** The identity the row shows and its controls name: the display name with
 * the login username, never the account id. */
const NARROW_LABEL = identityLabel(
  NARROW_ACCOUNT.display_name,
  accountUsername(NARROW_ACCOUNT.identifiers),
) as string

/** One pixel of tolerance for subpixel layout rounding. */
const PIN_TOLERANCE = 0.5

/** A 401 the session classifies as a step-up shortfall: an ApiError-shaped
 * object while the snapshot stays authenticated. */
const STEP_UP_REFUSAL = {
  status: 401,
  code: 'client.http.401',
  message: 'refused',
  auth: true,
}

/** A promise the case settles by hand, so one request can be held in
 * flight across frames. */
function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
  readonly reject: (reason: unknown) => void
} {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((settle, fail) => {
    resolve = settle
    reject = fail
  })
  return { promise, resolve, reject }
}

/** One animation frame: the component's updated state has reached the DOM
 * and the browser has had its chance to react to it. */
function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

/** A step-up flow that never advances, so the challenge parks on its
 * opening step until the case cancels it. */
function parkedFlow(): Promise<never> {
  return new Promise<never>(() => {
    // Never settles.
  })
}

/** What currently holds focus, for a failure message: tag, role and the
 * label or text a reader can map back to a control. */
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

describe('AccountsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the account table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      const operations = createAdminOperations(
        makeAdminBackend({
          listAccounts: () =>
            Promise.resolve({ accounts: ACCOUNTS, next_cursor: '' }),
        }),
      )
      renderWithProviders(<AccountsPage />, { operations, language: 'en-US' })

      await screen.findByText('Ada Lovelace')
      await expectNoHorizontalOverflow()
    })
  }
})

describe('AccountsPage row actions at 375 in Chromium', () => {
  it('pins the account row actions to the table edge while the main columns scroll', async () => {
    await page.viewport(375, 900)
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({
            accounts: [NARROW_ACCOUNT],
            next_cursor: '',
          }),
      }),
    )
    // The console shell gives page content the theme's spacing-3 inset on
    // each side (AdminShell's content box); reproduce it so the narrow
    // arm sees the same content width the running console does.
    renderWithProviders(
      <Box sx={{ p: 3 }}>
        <AccountsPage />
      </Box>,
      { operations, language: 'en-US' },
    )

    await screen.findByText(NARROW_LABEL)
    await document.fonts.ready

    const manage = screen.getByRole('button', {
      name: enUS.accounts.manageAria.replace('{{name}}', NARROW_LABEL),
    })
    const remove = screen.getByRole('button', {
      name: enUS.accounts.deleteAria.replace('{{name}}', NARROW_LABEL),
    })
    const container = manage.closest<HTMLElement>('.MuiTableContainer-root')
    expect(container).not.toBeNull()
    // The account table is wider than the page content at this width, so
    // the container can scroll and the pin is observable.
    expect(container?.scrollWidth).toBeGreaterThan(container?.clientWidth ?? 0)

    const box = container?.getBoundingClientRect() as DOMRect
    for (const control of [manage, remove]) {
      const rect = control.getBoundingClientRect()
      expect(rect.left).toBeGreaterThanOrEqual(box.left - PIN_TOLERANCE)
      expect(rect.right).toBeLessThanOrEqual(box.right + PIN_TOLERANCE)
    }

    const before = manage.getBoundingClientRect().left
    if (container !== null) {
      container.scrollLeft = 30
    }
    await waitFor(() => {
      expect(container?.scrollLeft ?? 0).toBeGreaterThan(0)
    })

    await waitFor(() => {
      expect(
        Math.abs(manage.getBoundingClientRect().left - before),
      ).toBeLessThanOrEqual(PIN_TOLERANCE)
    })
    const after = manage.getBoundingClientRect()
    expect(after.left).toBeGreaterThanOrEqual(box.left - PIN_TOLERANCE)
    expect(after.right).toBeLessThanOrEqual(box.right + PIN_TOLERANCE)
  })
})

/** The list's first and second page, so a case can name the row on each. */
const FIRST_PAGE = [
  accountFixture({ id: 'acc-1', display_name: 'Ada Lovelace' }),
]
const SECOND_PAGE = [
  accountFixture({ id: 'acc-2', display_name: 'Grace Hopper' }),
]

describe('AccountsPage pager focus in Chromium', () => {
  it('keeps focus on the next-page direction while the page it started is read', async () => {
    await page.viewport(900, 700)
    const secondPage = deferred<AdminAccountPage>()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: (params) =>
          params?.after === undefined
            ? Promise.resolve({ accounts: FIRST_PAGE, next_cursor: 'cursor-1' })
            : secondPage.promise,
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await screen.findByText('Ada Lovelace')

    const previous = screen.getByRole('button', { name: PREVIOUS_PAGE_LABEL })
    const next = screen.getByRole('button', { name: NEXT_PAGE_LABEL })
    await userEvent.click(next)

    await waitFor(() =>
      expect(
        next,
        `the activated direction while its page read runs; document.activeElement is ${describeActiveElement()}`,
      ).toHaveAttribute('aria-busy', 'true'),
    )
    expect(next).toHaveAttribute('aria-disabled', 'true')
    expect(previous).toHaveAttribute('aria-disabled', 'true')
    expect(next).not.toBeDisabled()
    expect(previous).not.toBeDisabled()
    expect(document.activeElement).toBe(next)

    secondPage.resolve({ accounts: SECOND_PAGE, next_cursor: '' })
    await screen.findByText('Grace Hopper')
    await waitFor(() => expect(next).not.toHaveAttribute('aria-busy'))
    expect(document.activeElement).toBe(next)
  })

  it('keeps focus on the previous-page direction when the page it returns to is read', async () => {
    await page.viewport(900, 700)
    const returning = deferred<AdminAccountPage>()
    let firstPageReads = 0
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: (params) => {
          if (params?.after === 'cursor-1') {
            return Promise.resolve({
              accounts: SECOND_PAGE,
              next_cursor: 'cursor-2',
            })
          }
          firstPageReads += 1
          return firstPageReads === 1
            ? Promise.resolve({ accounts: FIRST_PAGE, next_cursor: 'cursor-1' })
            : returning.promise
        },
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await screen.findByText('Ada Lovelace')
    await userEvent.click(screen.getByRole('button', { name: NEXT_PAGE_LABEL }))
    await screen.findByText('Grace Hopper')

    const previous = screen.getByRole('button', { name: PREVIOUS_PAGE_LABEL })
    await userEvent.click(previous)

    await waitFor(() =>
      expect(
        previous,
        `the activated direction while its page read runs; document.activeElement is ${describeActiveElement()}`,
      ).toHaveAttribute('aria-busy', 'true'),
    )
    expect(previous).toHaveAttribute('aria-disabled', 'true')
    expect(previous).not.toBeDisabled()
    expect(document.activeElement).toBe(previous)

    returning.resolve({ accounts: FIRST_PAGE, next_cursor: 'cursor-1' })
  })

  it('keeps focus on the last page direction and starts no read there', async () => {
    await page.viewport(900, 700)
    let reads = 0
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: (params) => {
          reads += 1
          return params?.after === undefined
            ? Promise.resolve({ accounts: FIRST_PAGE, next_cursor: 'cursor-1' })
            : Promise.resolve({ accounts: SECOND_PAGE, next_cursor: '' })
        },
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await screen.findByText('Ada Lovelace')

    const next = screen.getByRole('button', { name: NEXT_PAGE_LABEL })
    await userEvent.click(next)
    await screen.findByText('Grace Hopper')

    await waitFor(() =>
      expect(
        next,
        `the last page direction; document.activeElement is ${describeActiveElement()}`,
      ).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(next).not.toBeDisabled()
    expect(document.activeElement).toBe(next)

    const before = reads
    await userEvent.keyboard('{Enter}')
    await nextFrame()
    expect(reads).toBe(before)
    expect(document.activeElement).toBe(next)
  })

  it('keeps a focused pager direction and starts no read while a row write runs', async () => {
    await page.viewport(900, 700)
    const pendingDelete = deferred<void>()
    let reads = 0
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: (params) => {
          reads += 1
          return Promise.resolve(
            params?.after === undefined
              ? { accounts: FIRST_PAGE, next_cursor: 'cursor-1' }
              : { accounts: SECOND_PAGE, next_cursor: 'cursor-2' },
          )
        },
        deleteAccount: () => pendingDelete.promise,
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await screen.findByText('Ada Lovelace')

    await userEvent.click(
      screen.getByRole('button', { name: DELETE_ACCOUNT_NAME }),
    )
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(
      within(dialog).getByRole('button', { name: CONFIRM_LABEL }),
    )
    await new Promise((resolve) =>
      setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
    )
    await userEvent.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    // The dialog's exit hands focus back to the row control; let that land
    // before this case moves focus to the pager.
    await nextFrame()

    const previous = screen.getByRole('button', { name: PREVIOUS_PAGE_LABEL })
    const next = screen.getByRole('button', { name: NEXT_PAGE_LABEL })
    next.focus()
    await waitFor(() =>
      expect(
        next,
        `the focused direction while the write runs; document.activeElement is ${describeActiveElement()}`,
      ).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(previous).toHaveAttribute('aria-disabled', 'true')
    expect(next).not.toBeDisabled()
    expect(previous).not.toBeDisabled()
    expect(document.activeElement).toBe(next)

    const before = reads
    await userEvent.keyboard('{Enter}')
    await nextFrame()
    expect(reads).toBe(before)
    expect(document.activeElement).toBe(next)

    pendingDelete.resolve()
  })
})

describe('AccountsPage focus traps in Chromium', () => {
  /** Renders the page over the fixture accounts and opens the detail panel. */
  async function openDetail(
    overrides: Partial<Parameters<typeof makeAdminBackend>[0]> = {},
    session?: AuthSession,
  ): Promise<HTMLElement> {
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({ accounts: ACCOUNTS, next_cursor: '' }),
        ...overrides,
      }),
    )
    renderWithProviders(<AccountsPage />, {
      operations,
      language: 'en-US',
      session,
    })
    await screen.findByText('Ada Lovelace')
    await userEvent.click(
      screen.getByRole('button', {
        name: enUS.accounts.manageAria.replace('{{name}}', 'Ada Lovelace'),
      }),
    )
    return screen.findByRole('dialog')
  }

  /** The account the detail panel reads: the row the helper manages. */
  const TARGET_ACCOUNT = accountFixture({
    id: 'account-identifier-with-a-long-value-0001',
    display_name: 'Ada Lovelace',
  })

  /** Selects Disabled in the panel's status control and returns the
   * "Save status" button, which is now enabled because the draft differs
   * from the account's own status. */
  async function chooseDisabled(panel: HTMLElement): Promise<HTMLElement> {
    await userEvent.click(
      within(panel).getByLabelText(enUS.accounts.status.label),
    )
    await userEvent.click(
      await screen.findByRole('option', {
        name: enUS.accounts.status.disabled,
      }),
    )
    return within(panel).getByRole('button', {
      name: enUS.accounts.status.submit,
    })
  }

  /** Clicks the save button and holds the request in flight: the button is
   * unavailable and shows its loading indicator, and at least one frame
   * passes so the browser has reacted to the unavailable state before the
   * request settles. */
  async function saveAndHold(save: HTMLElement): Promise<void> {
    await userEvent.click(save)
    await waitFor(() => {
      expect(within(save).getByRole('progressbar')).toBeInTheDocument()
    })
    await nextFrame()
  }

  it('keeps focus on the status save button while the write is in flight and after it settles', async () => {
    const written = deferred<AdminAccount>()
    const panel = await openDetail({
      getAccount: () => Promise.resolve(TARGET_ACCOUNT),
      setAccountStatus: () => written.promise,
    })
    const save = await chooseDisabled(panel)
    await saveAndHold(save)

    written.resolve(TARGET_ACCOUNT)
    await waitFor(() => {
      expect(within(save).queryByRole('progressbar')).not.toBeInTheDocument()
    })

    await waitFor(() => {
      expect(
        save,
        `focus after the write settled; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
  })

  it('returns focus to the status save button when the step-up challenge is cancelled', async () => {
    const refused = deferred<AdminAccount>()
    const session: AuthSession = {
      ...createStepUpSession(),
      // The challenge parks on its opening step: this case cancels it
      // rather than completing it.
      startAuthenticatedFlow: () => parkedFlow(),
    }
    const panel = await openDetail(
      {
        getAccount: () => Promise.resolve(TARGET_ACCOUNT),
        setAccountStatus: () => refused.promise,
      },
      session,
    )
    const save = await chooseDisabled(panel)
    await saveAndHold(save)

    refused.reject(STEP_UP_REFUSAL)
    const challenge = await screen.findByRole('dialog', {
      name: enUS.stepUp.title,
    })
    // The refused attempt settled and left the status group's slot empty,
    // so the save button is available again when the challenge is cancelled.
    await waitFor(() =>
      expect(save).not.toHaveAttribute('aria-disabled', 'true'),
    )

    await userEvent.click(
      within(challenge).getByRole('button', { name: enUS.common.cancel }),
    )
    await waitFor(() => {
      expect(
        save,
        `focus after the step-up challenge closed; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
  })

  it('keeps focus on the create-secret control while the request is in flight', async () => {
    const written = deferred<AdminAccountSecretCreated>()
    const panel = await openDetail({
      getAccount: () => Promise.resolve(TARGET_ACCOUNT),
      createSecret: () => written.promise,
    })
    await userEvent.type(
      within(panel).getByLabelText(enUS.accounts.createSecret.labelLabel),
      'ci',
    )
    const submit = within(panel).getByRole('button', {
      name: enUS.accounts.createSecret.submit,
    })
    await userEvent.click(submit)
    await waitFor(() => {
      expect(within(submit).getByRole('progressbar')).toBeInTheDocument()
    })
    await nextFrame()

    expect(
      submit,
      `focus while the write is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    written.resolve({
      id: 'secret-9',
      label: 'ci',
      secret: 'one-time',
      expires_at: null,
    })
    await waitFor(() => {
      expect(within(submit).queryByRole('progressbar')).not.toBeInTheDocument()
    })
  })

  it('keeps focus on the create-secret control when a landed secret clears its label', async () => {
    const panel = await openDetail({
      getAccount: () => Promise.resolve(TARGET_ACCOUNT),
      createSecret: () =>
        Promise.resolve({
          id: 'secret-9',
          label: 'ci',
          secret: 'one-time',
          expires_at: null,
        }),
    })
    const label = within(panel).getByLabelText(
      enUS.accounts.createSecret.labelLabel,
    )
    await userEvent.type(label, 'ci')
    const submit = within(panel).getByRole('button', {
      name: enUS.accounts.createSecret.submit,
    })
    await userEvent.click(submit)

    await waitFor(() => expect(label).toHaveValue(''))
    await waitFor(() =>
      expect(submit).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(submit).toHaveAccessibleDescription(
      enUS.accounts.createSecret.unavailable,
    )
    await waitFor(() => {
      expect(
        submit,
        `focus after the write settled; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
  })

  it('returns focus to a detail row control after its confirmation closes while the write runs', async () => {
    const revoked = deferred<void>()
    const panel = await openDetail({
      getAccount: () => Promise.resolve(TARGET_ACCOUNT),
      listAccountSessions: () =>
        Promise.resolve([
          {
            id: 'sess-1',
            kind: 'refresh',
            assurance: 1,
            auth_time: '2026-10-15T12:00:00Z',
            created_at: '2026-10-15T12:00:00Z',
            last_seen_at: '2026-10-15T12:00:00Z',
            expires_at: '2026-10-16T12:00:00Z',
            revoked: false,
            current: false,
          },
        ]),
      revokeSession: () => revoked.promise,
    })
    const revoke = await within(panel).findByRole('button', {
      name: enUS.accounts.sessions.revokeAria.replace('{{id}}', 'sess-1'),
    })
    await userEvent.click(revoke)
    const confirm = await screen.findByRole('dialog', {
      name: enUS.accounts.sessions.revokeTitle,
    })
    await userEvent.click(
      within(confirm).getByRole('button', { name: enUS.common.confirm }),
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: enUS.accounts.sessions.revokeTitle }),
      ).not.toBeInTheDocument(),
    )
    await waitFor(() => expect(revoke).toHaveAttribute('aria-busy', 'true'))
    await waitFor(() => {
      expect(
        revoke,
        `focus after the confirmation closed; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })

    revoked.resolve()
  })

  /** A session row as the fixture read answers it. */
  function sessionRow(id: string) {
    return {
      id,
      kind: 'refresh',
      assurance: 1,
      auth_time: '2026-10-15T12:00:00Z',
      created_at: '2026-10-15T12:00:00Z',
      last_seen_at: '2026-10-15T12:00:00Z',
      expires_at: '2026-10-16T12:00:00Z',
      revoked: false,
      current: false,
    }
  }

  it("moves focus to the next session's revoke control when a middle session leaves", async () => {
    // The revoked row drops out of the read, so its control unmounts. Focus
    // must land on the next row's revoke control inside the drawer rather
    // than fall to the body, where the panel's focus trap pulls it back to
    // the paper a frame later.
    const revoked: string[] = []
    const rows = [sessionRow('sess-a'), sessionRow('sess-b'), sessionRow('sess-c')]
    const panel = await openDetail({
      getAccount: () => Promise.resolve(TARGET_ACCOUNT),
      listAccountSessions: () =>
        Promise.resolve(rows.filter((row) => !revoked.includes(row.id))),
      revokeSession: (input) => {
        revoked.push(input.sessionId)
        return Promise.resolve()
      },
    })
    const next = await within(panel).findByRole('button', {
      name: enUS.accounts.sessions.revokeAria.replace('{{id}}', 'sess-c'),
    })
    await userEvent.click(
      within(panel).getByRole('button', {
        name: enUS.accounts.sessions.revokeAria.replace('{{id}}', 'sess-b'),
      }),
    )
    const confirm = await screen.findByRole('dialog', {
      name: enUS.accounts.sessions.revokeTitle,
    })
    await userEvent.click(
      within(confirm).getByRole('button', { name: enUS.common.confirm }),
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', {
          name: enUS.accounts.sessions.revokeTitle,
        }),
      ).not.toBeInTheDocument(),
    )

    await waitFor(() => {
      expect(
        next,
        `focus after the revoked row left; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
  })

  it('keeps the detail panel focus trap holding over a hidden page', async () => {
    // The unit suite's open-panel scan cannot press Tab, so aria-hidden-focus
    // is indeterminate there; the helper's browser pass exercises the trap
    // and decides it (@speed/test-utils/axe).
    const panel = await openDetail()
    expect(panel).toBeInTheDocument()
    await expectNoAxeViolations({
      decidedInBrowser: 'accountsDetailPanel',
    })
  })

  it('keeps the one-time secret focus trap holding over a hidden page', async () => {
    const panel = await openDetail()
    await userEvent.type(
      within(panel).getByLabelText(enUS.accounts.createSecret.labelLabel),
      'ci',
    )
    const submit = within(panel).getByRole('button', {
      name: enUS.accounts.createSecret.submit,
    })
    await userEvent.click(submit)
    await within(panel).findByText(
      enUS.accounts.createSecret.handedOver
        .replace('{{label}}', 'label')
        .replace('{{secret}}', 'one-time'),
    )
    // The landed control stays focused and becomes unavailable, which opens
    // its reason tooltip; that popper is portaled to the body, outside the
    // modal's aria-hidden chain. The shared helper exempts a tooltip portal
    // that carries no focusable control, so the scan runs with it open.
    expect(await screen.findByRole('tooltip')).toBeInTheDocument()
    await expectNoAxeViolations({
      decidedInBrowser: 'accountsOneTimeSecret',
    })
  })
})
