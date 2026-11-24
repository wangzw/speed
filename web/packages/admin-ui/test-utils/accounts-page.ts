/** Shared fixtures and helpers for the AccountsPage suite. */

import { expect } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { zhCN as muiZhCN } from '@mui/material/locale'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
import type { AdminAccountSession } from '../src/operations.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
  type AdminBackend,
} from './operations.js'
import zhCN from '../src/locales/zh-CN.json' with { type: 'json' }

export function fill(
  template: string,
  values: Readonly<Record<string, string>>,
): string {
  return Object.entries(values).reduce(
    (text, [name, value]) => text.replaceAll(`{{${name}}}`, value),
    template,
  )
}

export function byName(name: string): string {
  return fill(zhCN.common.accountReference.name, { name })
}

export function byIdentifier(identifier: string): string {
  return fill(zhCN.common.accountReference.identifier, { identifier })
}

export function byNameAndIdentifier(name: string, identifier: string): string {
  return fill(zhCN.common.accountReference.nameWithIdentifier, {
    name,
    identifier,
  })
}

export const NEXT_PAGE_LABEL =
  muiZhCN.components?.MuiTablePagination?.defaultProps.getItemAriaLabel?.(
    'next',
  ) ?? ''
export const PREVIOUS_PAGE_LABEL =
  muiZhCN.components?.MuiTablePagination?.defaultProps.getItemAriaLabel?.(
    'previous',
  ) ?? ''

export const ACCOUNT_ID = 'acc-1'

/** The detail panel's accessible name (the fixture display name). */
export const DETAIL_PANEL_NAME = 'Operator'

export const MANAGE_ACCOUNT_NAME = fill(zhCN.accounts.manageAria, {
  name: 'Ada Lovelace',
})

export const DELETE_ACCOUNT_NAME = fill(zhCN.accounts.deleteAria, {
  name: 'Ada Lovelace',
})

export const REVOKE_SECRET_CI_NAME = fill(zhCN.accounts.secrets.revokeAria, {
  label: 'ci',
})

export const REVOKE_SESSION_SESS_1_NAME = fill(zhCN.accounts.sessions.revokeAria, {
  id: 'sess-1',
})

export const CREATED_GRACE = fill(zhCN.accounts.created, { account: byName('Grace') })

export const DELETED_ACC_1 = fill(zhCN.accounts.deleted, {
  account: byName('Ada Lovelace'),
})

export const SECRET_REVOKED_CI = fill(zhCN.accounts.secretRevoked, {
  account: byName('Operator'),
  label: 'ci',
})

export const SECRET_CREATED_CI = fill(zhCN.accounts.secretCreated, {
  account: byName('Operator'),
  label: 'ci',
})

export const SESSIONS_REVOKED = fill(zhCN.accounts.sessionsRevoked, {
  account: byName('Operator'),
})

export const MFA_SAVED = fill(zhCN.accounts.mfaSaved, {
  account: byName('Operator'),
})

export const STATUS_SAVED = fill(zhCN.accounts.statusSaved, {
  account: byName('Operator'),
})

export const HANDED_OVER_CI = fill(zhCN.accounts.createSecret.handedOver, {
  label: 'ci',
  secret: 'one-time-value',
})

export const DISPLAY_NAME_OPERATOR = fill(zhCN.accounts.detail.displayNameLine, {
  value: 'Operator',
})

export const DISCARD_SECRET_TITLE = zhCN.accounts.createSecret.discardTitle

export const COPY_VALUE_TEMPLATE = (
  uiKitResources['zh-CN'] as unknown as {
    readonly dataTable: { readonly copyValue: string }
  }
).dataTable.copyValue

export function withAccounts(
  overrides: Partial<AdminBackend> = {},
): ReturnType<typeof createAdminOperations> {
  return createAdminOperations(
    makeAdminBackend({
      listAccounts: () =>
        Promise.resolve({
          accounts: [accountFixture({ display_name: 'Ada Lovelace' })],
          next_cursor: '',
        }),
      ...overrides,
    }),
  )
}

export function sessionFixture(id: string): AdminAccountSession {
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

export function revokeSessionName(id: string): string {
  return fill(zhCN.accounts.sessions.revokeAria, { id })
}

export function revokeSecretName(label: string): string {
  return fill(zhCN.accounts.secrets.revokeAria, { label })
}

export function sessionsWithoutRevoked(
  rows: readonly AdminAccountSession[],
  revoked: readonly string[],
): () => Promise<readonly AdminAccountSession[]> {
  return () => Promise.resolve(rows.filter((row) => !revoked.includes(row.id)))
}

export async function search(
  user: ReturnType<typeof userEvent.setup>,
  value = 'ada',
): Promise<void> {
  await user.type(screen.getByLabelText(zhCN.accounts.identifierLabel), value)
  await user.click(screen.getByRole('button', { name: zhCN.accounts.search }))
}

export async function openDetail(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await search(user)
  await screen.findByText('Ada Lovelace')
  await user.click(screen.getByRole('button', { name: MANAGE_ACCOUNT_NAME }))
}

/** Opens and confirms the fixture account's two-step delete and waits for the dialog to close. */
export async function deleteThroughConfirm(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  await user.click(screen.getByRole('button', { name: DELETE_ACCOUNT_NAME }))
  const dialog = await screen.findByRole('dialog')
  await user.click(within(dialog).getByRole('button', { name: CONFIRM_LABEL }))
  await act(async () => {
    await new Promise((resolve) =>
      setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
    )
  })
  await user.click(
    within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
  )
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
}

/** Confirms one named row's two-step delete and waits for the dialog to leave. */
export async function deleteRowThroughConfirm(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
): Promise<void> {
  await user.click(
    screen.getByRole('button', {
      name: fill(zhCN.accounts.deleteAria, { name }),
    }),
  )
  const dialog = await screen.findByRole('dialog')
  await user.click(within(dialog).getByRole('button', { name: CONFIRM_LABEL }))
  await act(async () => {
    await new Promise((resolve) =>
      setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
    )
  })
  await user.click(
    within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
  )
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
}

export const CONFIRM_AGAIN_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmAgainLabel: string }
  }
).confirmDialog.confirmAgainLabel

export const PANEL_CLOSE_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly sidePanel: { readonly closeLabel: string }
  }
).sidePanel.closeLabel

export const UNKNOWN_TOTAL_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly dataTable: { readonly displayedRowsUnknown: string }
  }
).dataTable.displayedRowsUnknown

export const CONFIRM_LABEL = zhCN.common.confirm

export const CREATE_PANEL_NAME = zhCN.accounts.create.title

export const DISCARD_DIALOG_NAME = zhCN.accounts.create.discardTitle

/** The accessible name an in-page collapse control would carry on the fixture account. */
export const COLLAPSE_ACCOUNT_NAME = `${zhCN.accountSelector.close}${zhCN.accounts.title} ${ACCOUNT_ID}`

export async function openCreate(
  user: ReturnType<typeof userEvent.setup>,
): Promise<HTMLElement> {
  await user.click(screen.getByRole('button', { name: CREATE_PANEL_NAME }))
  return screen.findByRole('dialog', { name: CREATE_PANEL_NAME })
}

/** The body of the `@container (min-width:600px)` rule for `element`'s own class. */
export function elementContainerRule(
  css: string,
  element: Element,
): string | undefined {
  const emotionClass = Array.from(element.classList).find((name) =>
    name.startsWith('css-'),
  )
  if (emotionClass === undefined) {
    return undefined
  }
  const escaped = emotionClass.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return new RegExp(
    `@container \\(min-width:600px\\)\\{\\.${escaped}\\{([^}]*)\\}\\}`,
  ).exec(css)?.[1]
}

export function elementWidthAt(
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
  const escaped = emotionClass.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const rule = new RegExp(
    `@media \\(min-width:${minWidth}px\\)\\{\\.${escaped}\\{([^}]*)\\}\\}`,
  ).exec(css)?.[1]
  return rule === undefined ? undefined : /(?:^|;)width:([^;]+)/.exec(rule)?.[1]
}

/** Every declaration block emotion emitted for `element`'s own class. */
export function elementDeclarations(css: string, element: Element): string {
  const emotionClass = Array.from(element.classList).find((name) =>
    name.startsWith('css-'),
  )
  if (emotionClass === undefined) {
    return ''
  }
  const escaped = emotionClass.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const matches = css.matchAll(new RegExp(`\\.${escaped}\\{([^}]*)\\}`, 'g'))
  return Array.from(matches, (match) => match[1] ?? '').join('\n')
}

/** Confirms the open danger dialog, then waits for it to leave the tree. */
export async function confirmDialog(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  // The page may carry an open side panel (itself a dialog), so the
  // confirmation is picked out by its own confirm button rather than by
  // being the only dialog in the tree.
  const dialog = await waitFor(() => {
    const candidate = screen
      .getAllByRole('dialog')
      .find(
        (entry) =>
          within(entry).queryByRole('button', { name: CONFIRM_LABEL }) !== null,
      )
    expect(candidate).toBeDefined()
    return candidate as HTMLElement
  })
  await user.click(within(dialog).getByRole('button', { name: CONFIRM_LABEL }))
  await waitFor(() => {
    expect(
      screen.queryByRole('button', { name: CONFIRM_LABEL }),
    ).not.toBeInTheDocument()
  })
}

/** A 401 step-up refusal as the api-client delivers it (`client.http.401`). */
export function stepUpRefusal(): {
  readonly status: number
  readonly code: string
  readonly message: string
  readonly auth: boolean
} {
  return { status: 401, code: 'client.http.401', message: 'refused', auth: true }
}

/** A 401 SSO step-up refusal: the dialog must offer the sign-in exit. */
export const SSO_REFUSAL = {
  status: 401,
  code: 'authn.sso_required',
  message: 'refused',
  auth: true,
}

/** A 403 ApiError the error-text whitelist lists. */
export const FORBIDDEN = {
  status: 403,
  code: 'authn.forbidden',
  message: 'forbidden',
}

export const FORBIDDEN_TEXT = zhCN.errors.authn.forbidden

/** A 409 refusal: the email address is already taken. */
export const EMAIL_TAKEN = {
  status: 409,
  code: 'authn.email_taken',
  message: 'email taken',
}

export const EMAIL_TAKEN_TEXT = zhCN.errors.authn.email_taken

export const USERNAME_TAKEN = {
  status: 409,
  code: 'authn.username_taken',
  message: 'username taken',
}

export const USERNAME_TAKEN_TEXT = zhCN.accounts.create.usernameTaken

export const USERNAME_RESERVED = {
  status: 400,
  code: 'authn.username_reserved',
  message: 'username reserved',
}

export const USERNAME_RESERVED_TEXT = zhCN.accounts.create.usernameReserved

export const USERNAME_INVALID = {
  status: 400,
  code: 'authn.username_invalid',
  message: 'username invalid',
}

export const USERNAME_INVALID_TEXT = zhCN.accounts.create.usernameInvalid

export const BAD_REQUEST = {
  status: 400,
  code: 'authn.bad_request',
  message: 'bad request',
}

export const BAD_REQUEST_TEXT = zhCN.errors.authn.bad_request

/** A 409 refusal of a username removal (the name is permanent). */
export const USERNAME_IMMUTABLE = {
  status: 409,
  code: 'authn.username_immutable',
  message: 'username immutable',
}

export const USERNAME_IMMUTABLE_TEXT = zhCN.errors.authn.username_immutable

export const SECRET_ROW = {
  id: 'sec-1',
  label: 'ci',
  expires_at: null,
  revoked: false,
}

export const SESSION_ROW = {
  id: 'sess-1',
  kind: 'refresh',
  assurance: 1,
  auth_time: '2026-10-15T12:00:00Z',
  created_at: '2026-10-15T12:00:00Z',
  last_seen_at: '2026-10-15T12:00:00Z',
  expires_at: '2026-10-16T12:00:00Z',
  revoked: false,
  current: false,
}

/** Every element matching `selector` outside the panel. */
export function outsideOf(panel: HTMLElement, selector: string): Element[] {
  return Array.from(document.querySelectorAll(selector)).filter(
    (element) => !panel.contains(element),
  )
}

export type WriteDriver = (
  user: ReturnType<typeof userEvent.setup>,
  panel: HTMLElement,
) => Promise<void>

/** The five detail writes whose feedback lives in the panel. */
export const DETAIL_WRITES: readonly {
  readonly label: string
  readonly success: string
  readonly operations: (fail: boolean) => Partial<AdminBackend>
  readonly drive: WriteDriver
}[] = [
  {
    label: 'reset all MFA factors',
    success: fill(zhCN.accounts.mfaReset, { account: byName('Operator') }),
    operations: (fail) => ({
      resetMFA: () => (fail ? Promise.reject(FORBIDDEN) : Promise.resolve()),
    }),
    drive: async (user, panel) => {
      await user.click(
        within(panel).getByRole('button', { name: zhCN.accounts.resetMFA }),
      )
      await confirmDialog(user)
    },
  },
  {
    label: 'revoke all sessions',
    success: SESSIONS_REVOKED,
    operations: (fail) => ({
      revokeAccountSessions: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(),
    }),
    drive: async (user, panel) => {
      await user.click(
        within(panel).getByRole('button', {
          name: zhCN.accounts.revokeSessions,
        }),
      )
      await confirmDialog(user)
    },
  },
  {
    label: 'revoke one service secret',
    success: SECRET_REVOKED_CI,
    operations: (fail) => ({
      listAccountSecrets: () => Promise.resolve([SECRET_ROW]),
      revokeSecret: () => (fail ? Promise.reject(FORBIDDEN) : Promise.resolve()),
    }),
    drive: async (user, panel) => {
      await within(panel).findByText('ci')
      await user.click(
        within(panel).getByRole('button', { name: REVOKE_SECRET_CI_NAME }),
      )
      await confirmDialog(user)
    },
  },
  {
    label: 'revoke one session',
    success: fill(zhCN.accounts.sessionRevoked, {
      account: byName('Operator'),
      session: 'sess-1',
    }),
    operations: (fail) => ({
      listAccountSessions: () => Promise.resolve([SESSION_ROW]),
      revokeSession: () => (fail ? Promise.reject(FORBIDDEN) : Promise.resolve()),
    }),
    drive: async (user, panel) => {
      await within(panel).findByText('sess-1')
      await user.click(
        within(panel).getByRole('button', {
          name: REVOKE_SESSION_SESS_1_NAME,
        }),
      )
      await confirmDialog(user)
    },
  },
  {
    label: 'remove one login identifier by name',
    success: fill(zhCN.accounts.identifierRemoved, {
      account: byName('Operator'),
      loginIdentifier: 'ada@example.com',
    }),
    operations: (fail) => ({
      removeIdentifier: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(),
    }),
    drive: async (user, panel) => {
      await user.type(
        within(panel).getByLabelText(zhCN.accounts.removeIdentifier.label),
        'ada@example.com',
      )
      await user.click(
        within(panel).getByRole('button', {
          name: zhCN.accounts.removeIdentifier.submit,
        }),
      )
      await confirmDialog(user)
    },
  },
]

/** The six field-level writes of the detail's account-level section. */
export const FIELD_WRITES: readonly {
  readonly label: string
  readonly success: string
  readonly operations: (fail: boolean) => Partial<AdminBackend>
  readonly drive: WriteDriver
}[] = [
  {
    label: 'change the account status',
    success: STATUS_SAVED,
    operations: (fail) => ({
      setAccountStatus: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(accountFixture()),
    }),
    drive: async (user, panel) => {
      await user.click(within(panel).getByLabelText(zhCN.accounts.status.label))
      await user.click(
        await screen.findByRole('option', {
          name: zhCN.accounts.status.disabled,
        }),
      )
      await user.click(
        within(panel).getByRole('button', {
          name: zhCN.accounts.status.submit,
        }),
      )
    },
  },
  {
    label: 'change the MFA requirement',
    success: MFA_SAVED,
    operations: (fail) => ({
      setMFARequired: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(),
    }),
    drive: async (user, panel) => {
      await user.click(within(panel).getByLabelText(zhCN.accounts.mfa.label))
      await user.click(
        await screen.findByRole('option', { name: zhCN.accounts.mfa.required }),
      )
      await user.click(
        within(panel).getByRole('button', { name: zhCN.accounts.mfa.submit }),
      )
    },
  },
  {
    label: 'patch the account',
    success: fill(zhCN.accounts.patchSaved, { account: byName('Operator') }),
    operations: (fail) => ({
      patchAccount: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(accountFixture()),
    }),
    drive: async (user, panel) => {
      const name = within(panel).getByLabelText(
        zhCN.accounts.patch.displayNameLabel,
      )
      await user.clear(name)
      await user.type(name, 'Ada II')
      await user.click(
        within(panel).getByRole('button', {
          name: zhCN.accounts.patch.submit,
        }),
      )
    },
  },
  {
    label: 'reset the password',
    success: fill(zhCN.accounts.passwordReset, { account: byName('Operator') }),
    operations: (fail) => ({
      resetPassword: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(),
    }),
    drive: async (user, panel) => {
      await user.type(
        within(panel).getByLabelText(zhCN.accounts.password.label),
        'secret123',
      )
      await user.click(
        within(panel).getByRole('button', {
          name: zhCN.accounts.password.submit,
        }),
      )
    },
  },
  {
    label: 'add a login identifier',
    success: fill(zhCN.accounts.identifierAdded, {
      account: byName('Operator'),
      loginIdentifier: 'ada@example.com',
    }),
    operations: (fail) => ({
      addIdentifier: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(),
    }),
    drive: async (user, panel) => {
      await user.type(
        within(panel).getByLabelText(zhCN.accounts.addIdentifier.valueLabel),
        'ada@example.com',
      )
      await user.click(
        within(panel).getByRole('button', {
          name: zhCN.accounts.addIdentifier.submit,
        }),
      )
    },
  },
  {
    label: 'create a service secret',
    success: SECRET_CREATED_CI,
    operations: (fail) => ({
      createSecret: () =>
        fail
          ? Promise.reject(FORBIDDEN)
          : Promise.resolve({
              id: 'secret-9',
              label: 'ci',
              secret: 'one-time-value',
              expires_at: null,
            }),
    }),
    drive: async (user, panel) => {
      await user.type(
        within(panel).getByLabelText(zhCN.accounts.createSecret.labelLabel),
        'ci',
      )
      await user.click(
        within(panel).getByRole('button', {
          name: zhCN.accounts.createSecret.submit,
        }),
      )
    },
  },
]

export const ALL_DETAIL_WRITES = [...DETAIL_WRITES, ...FIELD_WRITES]
