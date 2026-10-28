/**
 * AccountsPage: the account-administration page of the console.
 *
 * It lists accounts (GET /api/v1/system/auth/accounts, narrowed by an identifier
 * prefix of one identifier kind), creates an account, and opens one
 * account's detail: its read-only fields, its registered factors, its
 * service secrets and its sessions, with the write controls the platform
 * declares a request body for -- create an account; change its status,
 * MFA requirement, display name, attributes and protected flag; reset its
 * password; add a login name or email; create a service secret; and the
 * body-free writes delete the account, reset its factors, revoke its
 * sessions, revoke a secret, revoke a session and remove a login
 * identifier named by hand.
 *
 * The list read takes the identifier prefix with its kind, because the
 * module's index is keyed by the two together (it refuses a prefix
 * without a kind): the toolbar carries a kind selector beside the prefix
 * box, and the applied filter is committed on submit so a keystroke never
 * fires a request. The toolbar also narrows the list by a display-name
 * prefix, by account status and by account kind, the other query
 * parameters the module's list endpoint declares. The display-name prefix
 * is the beginning of the stored name, compared as written (case-sensitive
 * and un-normalized), so the box sends the operator's text unchanged. Each
 * unset filter is left out of the request rather than sent empty.
 *
 * The list read is cursor-paginated: it answers with the next page's
 * cursor only, so the footer advances through a stack of the cursors each
 * visited page was read by, and a filter submission returns to the first
 * page. A cursor page carries no total, so the footer shows the page's
 * range without a count.
 *
 * The detail's factor, secret and session sections exist only while the
 * process serves their features (authn.admin.factors, authn.admin.secrets
 * and authn.admin.sessions): an absent feature unmounts the section and the
 * read it carries, so the page neither renders the control nor issues the
 * request. The reset-MFA, MFA-requirement, password-reset and secret-create
 * controls follow the same answer. The page itself is gated on authn.admin
 * by its route declaration; no page-level read depends on the answer, and
 * the identity unit it shares with every other surface skips its account
 * read when the feature is absent.
 *
 * The host may extend the list and the attribute editors without the page
 * knowing what the columns or the attributes mean: `extraColumns` inserts
 * its own columns (each declaring the priority that sheds it as the table's
 * container narrows) before the actions column, and `attributeFields`
 * replaces the raw-JSON attribute editors -- on the create form and on the
 * detail's patch -- with one text field per declared key. The patch keeps
 * the attributes it did not declare, so a host field edits its own keys
 * without dropping another writer's. With neither supplied, the output is
 * the page's own.
 *
 * The list and the detail's reads each stand behind their own
 * AsyncSection, so every one of them shows its loading, empty and
 * retryable-error states rather than settling a failed read into a
 * silently empty table. The detail and the create form are carried by
 * ui-kit's SidePanel: a row's Manage control only opens that account's
 * detail panel (it reports `aria-haspopup="dialog"` and never toggles
 * closed), and the page header's New account control opens the create
 * panel. Both are `wide`. The panel is modal, so the list keeps its
 * data, filters and scroll position without being clickable while it is
 * open, and closing goes through the panel's own control. The selected
 * account stays until the panel has animated out (its `onExited`), so the
 * exit renders the account rather than an empty panel.
 *
 * Every write reports through one feedback scope per control group, and
 * each region renders directly under the group that fired it: the panel's
 * sections carry their own scopes so a failure lands beside the controls
 * instead of behind the open modal, the two action rows in the detail
 * share one scope, each table shares one with its row actions, and the
 * list's row delete and the create success share the list's scope below
 * the table. The panel is `busy` while any scope inside it has a write in
 * flight, so a write's result always lands in a region that is still
 * mounted. Every settled action without a failure confirms with a message
 * naming the account and, for a row action, the row.
 *
 * A created service secret is a value handed out once. Its state is held
 * by the page, not by the panel content that unmounts on close, so a scope
 * reset cannot drop it; the display has its own close control and its
 * accessible name names the account. While it is displayed, a detail close
 * request first asks through a ConfirmDialog whether to discard it.
 *
 * Each write request body is the shape the module's OpenAPI fragment
 * declares for its endpoint, which the handler decodes; the account view
 * carries no identifier list (the module exposes identifier add/remove
 * but no identifier read), so identifier removal and add name the
 * identifier by hand.
 *
 * Every destructive write is confirmed through ui-kit's ConfirmDialog
 * before it fires; a delete that takes other records with it -- the account
 * delete, like the authorization page's role removal -- adds the danger
 * variant's two-step guard. Every read
 * and write is an operation on the console operations bundle, so the page
 * builds no client and writes no query key. The page opens with ui-kit's
 * PageHeader.
 */

import { useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import Chip from '@mui/material/Chip'
import FormControlLabel from '@mui/material/FormControlLabel'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TablePagination from '@mui/material/TablePagination'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useForm } from 'react-hook-form'
import type { SubmitHandler, UseFormReturn } from 'react-hook-form'
import type { UseMutationResult } from '@tanstack/react-query'
import { useStepUpGate } from '@speed/auth-core'
import { formatInstant } from '@speed/i18n'
import {
  ConfirmDialog,
  DataTable,
  FormField,
  FormLayout,
  ListSkeleton,
  OneTimeValuePanel,
  PageHeader,
  PRIMARY_COLUMN_MAX_WIDTH,
  RefreshButton,
  SectionCard,
  SidePanel,
  errorCodeOf,
  pageRefreshRead,
  primaryColumnMaxWidthSx,
  usePageRefresh,
  useWriteFeedback,
} from '@speed/ui-kit'
import type {
  DataTableColumn,
  DataTableColumnPriority,
  WriteFeedback,
} from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import {
  accountIdentityValue,
  accountNotice,
  type AccountIdentityValue,
} from './internal/account-identity.js'
import {
  PendingConfirmDialog,
  usePendingConfirm,
} from './internal/confirm.js'
import type { PendingConfirmState } from './internal/confirm.js'
import { useConsoleHasFeature } from './internal/features.js'
import { identifierPrefix } from './internal/identifier-prefix.js'
import { useConsoleSession } from './internal/session.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import {
  useConsoleUiTranslation,
  useUiKitTranslation,
} from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'
import { useConsoleOperations } from './operations.js'
import { CONSOLE_UI_NAMESPACE } from './resources.js'
import type {
  ConsoleAccount,
  ConsoleAccountSecret,
  ConsoleAccountSecretCreated,
  ConsoleAccountSession,
  ConsoleAccountFactor,
  ConsoleCreateAccountInput,
} from './operations.js'

/** The account status's label, guarding the closed set. */
const ACCOUNT_STATUSES = new Set<string>(['active', 'disabled', 'deleted'])

/** The identifier kinds the module's index is keyed by, in the order the
 * selector offers them. */
const IDENTIFIER_KINDS = ['username', 'email', 'phone'] as const
type IdentifierKind = (typeof IDENTIFIER_KINDS)[number]

/** The account kinds the list read narrows by, in the order the filter offers them. */
const ACCOUNT_KINDS = ['user', 'service'] as const

/** The account statuses the list read narrows by, in the order the filter offers them. */
const FILTER_STATUSES = ['active', 'disabled', 'deleted'] as const

/** The list read's page size. Cursor pages carry no total, so the footer
 * shows this page's slot range and offers no size selector. */
const ACCOUNTS_PAGE_SIZE = 100

/** One service secret handed out once, held by the page across panel
 * closes so a scope reset cannot drop the only copy. */
interface OneTimeSecret {
  readonly accountId: string
  readonly accountName: string
  readonly label: string
  readonly secret: string
}

/** The detail's per-section feedback scopes, one per control group. */
interface AccountDetailFeedback {
  readonly status: WriteFeedback
  readonly mfa: WriteFeedback
  readonly patch: WriteFeedback
  readonly password: WriteFeedback
  readonly addIdentifier: WriteFeedback
  readonly createSecret: WriteFeedback
}

function statusLabel(status: string, t: (key: string) => string): string {
  return ACCOUNT_STATUSES.has(status)
    ? t(`accounts.status.${status}`)
    : t('accounts.status.other')
}

/**
 * One value of a closed set through this package's namespace, falling back
 * to the raw value when the bundle declares no leaf for it. The fallback
 * keeps a value the platform grows later readable instead of printing a
 * resource key.
 */
function closedSetLabel(
  prefix: string,
  value: string,
  t: (key: string) => string,
  i18n: { exists: (key: string, options?: { ns?: string }) => boolean },
): string {
  const key = `${prefix}.${value}`
  return i18n.exists(key, { ns: CONSOLE_UI_NAMESPACE }) ? t(key) : value
}

/** One host-supplied list column, inserted before the row-actions column.
 * The priority is required: a host column that repeats across rows takes
 * its own ui-kit priority so a narrow container sheds it in the host's
 * chosen order; the page does not choose for it. */
export type AccountsPageExtraColumn = DataTableColumn<ConsoleAccount> & {
  readonly priority: DataTableColumnPriority
}

/** One account attribute the host offers as a plain text field. */
export interface AccountsPageAttributeField {
  /** The attribute's key in the account's attribute map. */
  readonly key: string
  /** The field's visible label. */
  readonly label: ReactNode
}

export interface AccountsPageProps {
  /**
   * Host columns inserted after the page's own columns and before the
   * row-actions column, so the controls stay the last cell of every row.
   */
  readonly extraColumns?: readonly AccountsPageExtraColumn[]
  /**
   * The account attributes the host declares, each edited as one text
   * field in place of the raw-JSON attribute editor. Undeclared keys an
   * edited account already carries are preserved. Absent, the page keeps
   * the JSON editor.
   */
  readonly attributeFields?: readonly AccountsPageAttributeField[]
}

export function AccountsPage({
  extraColumns,
  attributeFields,
}: AccountsPageProps = {}) {
  const { t, i18n } = useConsoleUiTranslation()
  // The paging footer is ui-kit's own footer, rendered by this page (see
  // the list card's footer): its wording stays in the ui-kit namespace the
  // host already registers beside this one.
  const { t: tUiKit } = useUiKitTranslation()
  const operations = useConsoleOperations()
  const confirm = usePendingConfirm()
  const session = useConsoleSession()
  const hasFeature = useConsoleHasFeature()
  const factorsPresent = hasFeature('authn.admin.factors')
  const secretsPresent = hasFeature('authn.admin.secrets')
  const sessionsPresent = hasFeature('authn.admin.sessions')
  const gate = useStepUpGate<() => void>(
    (retry) => retry(),
    (error) => session !== null && session.isStepUpShortfall(error),
  )

  const [identifierDraft, setIdentifierDraft] = useState('')
  const [identifier, setIdentifier] = useState('')
  const [kindDraft, setKindDraft] = useState<IdentifierKind>('username')
  const [identifierKind, setIdentifierKind] = useState<IdentifierKind>('username')
  const [displayNameDraft, setDisplayNameDraft] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [statusDraft, setStatusDraft] = useState('')
  const [status, setStatus] = useState('')
  const [accountKindDraft, setAccountKindDraft] = useState('')
  const [accountKind, setAccountKind] = useState('')
  // The cursor stack: cursors[i] is the cursor that reads page i ('' is the
  // first page). The read answers with the next page's cursor only, so the
  // page keeps the cursor each visited page was read by; "previous" steps
  // back through the stack, and a fresh filter submission starts it over.
  const [cursors, setCursors] = useState<readonly string[]>([''])
  const [page, setPage] = useState(0)
  // The selected account outlives the close request: the detail panel keeps
  // rendering it through the exit transition, and `selectedId` is cleared
  // only once the panel reports `onExited`.
  const [selectedId, setSelectedId] = useState('')
  const [detailOpen, setDetailOpen] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [identifierToRemove, setIdentifierToRemove] = useState('')
  // The one-time service secret is page state, not scope state: a scope
  // reset must not drop the only copy of a value handed out once, and the
  // display outlives the panel content that unmounts on close.
  const [oneTimeSecret, setOneTimeSecret] = useState<OneTimeSecret | null>(null)
  // The control that opened each panel. A close that goes through the
  // discard confirmation closes the dialog and the panel in one commit;
  // the dialog's own focus restore then lands on the panel's close control,
  // which unmounts with the panel and drops focus to the document. The page
  // therefore returns focus to the opener itself once the panel has exited
  // (the ordinary close is already restored by the panel, to this same
  // control).
  const createTrigger = useRef<HTMLButtonElement | null>(null)
  const detailTrigger = useRef<HTMLButtonElement | null>(null)
  // A detail close request while the secret is still open asks first.
  const [discardSecretOpen, setDiscardSecretOpen] = useState(false)
  // The create panel's success belongs to the list's scope and may only be
  // written after `onExited`: while the modal is open the page behind it is
  // aria-hidden, so a notice written then would never be announced.
  const createSuccess = useRef<string | null>(null)

  // The list's feedback scope: the row-initiated delete and the create
  // success share it, and its region stands below the list.
  const listWrite = useWriteFeedback()
  // The detail's scopes, one per section. They live here rather than inside
  // the content so the panel can be `busy` while any of them is in flight
  // and `onExited` can clear them all.
  const detailStatusWrite = useWriteFeedback()
  const detailMfaWrite = useWriteFeedback()
  const detailPatchWrite = useWriteFeedback()
  const detailPasswordWrite = useWriteFeedback()
  const detailAddIdentifierWrite = useWriteFeedback()
  const detailCreateSecretWrite = useWriteFeedback()
  const detailActionWrite = useWriteFeedback()
  const detailSecretWrite = useWriteFeedback()
  const detailSessionWrite = useWriteFeedback()
  const detailIdentifierRemoveWrite = useWriteFeedback()

  // The applied filters, committed together on submit so a keystroke never
  // fires a request. The identifier prefix is sent with its kind (the
  // module's index is keyed by the two together); the status and kind
  // filters are each sent on their own, and an unset one is omitted. The
  // cursor is part of the read's parameters, so each visited page caches
  // under its own key and a filter submission (cursor back to '') reads the
  // first page.
  const cursor = cursors[page] ?? ''
  const accountParams = useMemo(
    () => ({
      ...(identifier === ''
        ? {}
        : { identifier_prefix: identifier, identifier_kind: identifierKind }),
      ...(displayName === '' ? {} : { display_name_prefix: displayName }),
      ...(status === '' ? {} : { status }),
      ...(accountKind === '' ? {} : { kind: accountKind }),
      ...(cursor === '' ? {} : { after: cursor }),
      limit: ACCOUNTS_PAGE_SIZE,
    }),
    [identifier, identifierKind, displayName, status, accountKind, cursor],
  )
  const accounts = operations.useAccounts(accountParams)

  // The footer pages through the cursor the read answered with. A cursor
  // page carries no total, so the footer cannot judge where the last page
  // is on its own: a read answered with no next cursor is the last page,
  // and the footer disables its forward control there. A page whose read
  // has not settled yet has no cursor to advance from either, so it
  // disables the control too and this guard backs that up.
  function changePage(nextPage: number): void {
    // A write in flight owns the list's context: paging away mid-write
    // would let a result land against a list it no longer describes.
    if (listWrite.pending) {
      return
    }
    if (nextPage === page - 1 && page > 0) {
      setPage(nextPage)
      return
    }
    if (nextPage !== page + 1) {
      return
    }
    const nextCursor = accounts.data?.next_cursor ?? ''
    if (nextCursor === '') {
      return
    }
    setCursors((previous) => {
      const grown = [...previous]
      grown[page + 1] = nextCursor
      return grown
    })
    setPage(nextPage)
  }
  const account = operations.useAccount({
    id: selectedId === '' ? undefined : selectedId,
  })
  // The selected account's readable identity, as a success notice names it.
  // A detail write can only be started from a settled panel, but the read's
  // type does not say so; an absent read resolves nothing and the notice
  // stays neutral instead of naming the recorded identifier.
  const selectedIdentity: AccountIdentityValue =
    account.data === undefined
      ? { name: undefined, identifier: undefined }
      : accountIdentityValue(account.data)
  // The factors and secrets reads live in their feature-gated sections, so
  // an absent feature takes the read with it. The sessions read stays here:
  // the bulk revoke's confirmation in the action row needs to know whether
  // the caller's own session is listed, and the read is disabled while the
  // feature is absent.
  const sessions = operations.useAccountSessions({
    id: selectedId === '' || !sessionsPresent ? undefined : selectedId,
  })
  // Whether the listed sessions include the caller's own, so the bulk revoke's
  // confirmation can say the caller is signed out too.
  const sessionsIncludeCurrent =
    sessions.data?.some((row) => row.current === true) ?? false

  const deleteAccount = operations.useDeleteAccount()
  const resetMFA = operations.useResetMFA()
  const revokeSessions = operations.useRevokeAccountSessions()
  const revokeSecret = operations.useRevokeSecret()
  const revokeSession = operations.useRevokeSession()
  const removeIdentifier = operations.useRemoveIdentifier()

  // The refresh action re-reads the list -- the one read describing the
  // page body; the detail panel's reads live in the modal and re-read when
  // it opens again -- and resets the list's feedback scope first.
  const pageRefresh = usePageRefresh(
    [pageRefreshRead('accounts', accounts)],
    () => listWrite.reset(),
  )

  // The write controls a detail section starts are disabled and marked busy
  // from the scope's own in-flight count, never from one mutation observer's
  // `isPending`: overlapping attempts of the same group would otherwise
  // disagree about which one is running.
  const detailFeedback: AccountDetailFeedback = {
    status: detailStatusWrite,
    mfa: detailMfaWrite,
    patch: detailPatchWrite,
    password: detailPasswordWrite,
    addIdentifier: detailAddIdentifierWrite,
    createSecret: detailCreateSecretWrite,
  }

  // Any write in flight inside the panel makes the panel busy, so a close
  // request cannot unmount the region the result is about to land in.
  const detailBusy =
    detailStatusWrite.pending ||
    detailMfaWrite.pending ||
    detailPatchWrite.pending ||
    detailPasswordWrite.pending ||
    detailAddIdentifierWrite.pending ||
    detailCreateSecretWrite.pending ||
    detailActionWrite.pending ||
    detailSecretWrite.pending ||
    detailSessionWrite.pending ||
    detailIdentifierRemoveWrite.pending

  function openDetail(id: string): void {
    setSelectedId(id)
    setDetailOpen(true)
  }

  // A close request while the one-time secret is still open asks before
  // discarding a value that cannot be shown again.
  function requestDetailClose(): void {
    if (oneTimeSecret !== null) {
      setDiscardSecretOpen(true)
      return
    }
    setDetailOpen(false)
  }

  // The panel has animated out: only now drop the account it rendered, and
  // reset every scope and draft the panel carried so opening it again (for
  // any account) starts clean. The one-time secret is page state and is
  // cleared only by its own close control or the discard confirmation.
  function detailExited(): void {
    setSelectedId('')
    setIdentifierToRemove('')
    detailStatusWrite.reset()
    detailMfaWrite.reset()
    detailPatchWrite.reset()
    detailPasswordWrite.reset()
    detailAddIdentifierWrite.reset()
    detailCreateSecretWrite.reset()
    detailActionWrite.reset()
    detailSecretWrite.reset()
    detailSessionWrite.reset()
    detailIdentifierRemoveWrite.reset()
    const trigger = detailTrigger.current
    detailTrigger.current = null
    if (trigger !== null && trigger.isConnected) {
      trigger.focus()
    }
  }

  // A created account closes the panel; the list's confirmation waits for
  // the exit so it is written into the page after it becomes reachable.
  function accountCreated(created: ConsoleAccount): void {
    createSuccess.current = accountNotice(
      t,
      { named: 'accounts.created', unnamed: 'accounts.createdUnnamed' },
      accountIdentityValue(created),
    )
    setCreateOpen(false)
  }

  function createPanelExited(): void {
    const message = createSuccess.current
    createSuccess.current = null
    if (message !== null) {
      listWrite.succeed(message)
    }
    const trigger = createTrigger.current
    createTrigger.current = null
    if (trigger !== null && trigger.isConnected) {
      trigger.focus()
    }
  }

  function deleteOne(row: ConsoleAccount): Promise<void> {
    return listWrite.run(async () => {
      try {
        await deleteAccount.mutateAsync({ id: row.id })
      } catch (error) {
        if (gate.intercept(error, () => void deleteOne(row))) {
          return null
        }
        throw error
      }
      if (selectedId === row.id) {
        setSelectedId('')
      }
      return accountNotice(
        t,
        { named: 'accounts.deleted', unnamed: 'accounts.deletedUnnamed' },
        accountIdentityValue(row),
      )
    })
  }

  // The delete confirmation names the account the way the roster does: its
  // display name, and the first login identifier beside it when the view
  // carries one. An account with no display name is named by that identifier
  // alone; neither gives the neutral sentence. The opaque id never enters the
  // copy -- an operator cannot match it to a row.
  function deleteMessageFor(row: ConsoleAccount): string {
    const identifier = row.identifiers[0]?.value
    if (row.display_name !== '') {
      return identifier === undefined
        ? t('accounts.deleteMessage', { name: row.display_name })
        : t('accounts.deleteMessageWithIdentifier', {
            name: row.display_name,
            identifier,
          })
    }
    return identifier === undefined
      ? t('accounts.deleteMessageUnnamed')
      : t('accounts.deleteMessageByIdentifier', { identifier })
  }

  function resetAllFactors(): Promise<void> {
    return detailActionWrite.run(async () => {
      try {
        await resetMFA.mutateAsync({ id: selectedId })
      } catch (error) {
        if (gate.intercept(error, () => void resetAllFactors())) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        { named: 'accounts.mfaReset', unnamed: 'accounts.mfaResetUnnamed' },
        selectedIdentity,
      )
    })
  }

  function revokeAllSessions(): Promise<void> {
    return detailActionWrite.run(async () => {
      try {
        await revokeSessions.mutateAsync({ id: selectedId })
      } catch (error) {
        if (gate.intercept(error, () => void revokeAllSessions())) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        {
          named: 'accounts.sessionsRevoked',
          unnamed: 'accounts.sessionsRevokedUnnamed',
        },
        selectedIdentity,
      )
    })
  }

  function removeOne(identifier: string): Promise<void> {
    return detailIdentifierRemoveWrite.run(async () => {
      try {
        await removeIdentifier.mutateAsync({ id: selectedId, identifier })
      } catch (error) {
        if (gate.intercept(error, () => void removeOne(identifier))) {
          return null
        }
        throw error
      }
      setIdentifierToRemove('')
      return accountNotice(
        t,
        {
          named: 'accounts.identifierRemoved',
          unnamed: 'accounts.identifierRemovedUnnamed',
        },
        selectedIdentity,
        { loginIdentifier: identifier },
      )
    })
  }

  function revokeOneSecret(row: ConsoleAccountSecret): Promise<void> {
    return detailSecretWrite.run(async () => {
      try {
        await revokeSecret.mutateAsync({ id: selectedId, secret: row.id })
      } catch (error) {
        if (gate.intercept(error, () => void revokeOneSecret(row))) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        {
          named: 'accounts.secretRevoked',
          unnamed: 'accounts.secretRevokedUnnamed',
        },
        selectedIdentity,
        { label: row.label },
      )
    })
  }

  function revokeOneSession(row: ConsoleAccountSession): Promise<void> {
    return detailSessionWrite.run(async () => {
      try {
        await revokeSession.mutateAsync({
          accountId: selectedId,
          sessionId: row.id,
        })
      } catch (error) {
        if (gate.intercept(error, () => void revokeOneSession(row))) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        {
          named: 'accounts.sessionRevoked',
          unnamed: 'accounts.sessionRevokedUnnamed',
        },
        selectedIdentity,
        { session: row.id },
      )
    })
  }

  const columns: DataTableColumn<ConsoleAccount>[] = [
    {
      id: 'id',
      header: t('accounts.column.id'),
      priority: 'low',
      cellWrap: 'nowrap',
      cellFullValue: (row) => row.id,
      // The prefix is short, but the cap counts the copy control beside
      // it, so the whole identifier cell stays inside the shared budget.
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (row) => (
        <Typography variant="body2">{identifierPrefix(row.id)}</Typography>
      ),
    },
    {
      id: 'kind',
      header: t('accounts.column.kind'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (row) =>
        closedSetLabel('accounts.create.kind', row.kind, t, i18n),
    },
    {
      id: 'displayName',
      header: t('accounts.column.displayName'),
      cellWrap: 'nowrap',
      // The complete identifier list, not only the one on the secondary
      // line: the cell's hover title and copy control hand over every
      // identifier an operator may need to match the row.
      cellFullValue: (row) =>
        row.identifiers.length === 0
          ? undefined
          : row.identifiers.map((identifier) => identifier.value).join(', '),
      // The page's primary identity: it never hides, and the shared
      // whole-cell cap -- which counts the copy control -- keeps it from
      // pushing the actions off screen in a narrow container. The detail
      // panel carries the full value.
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (row) => {
        const firstIdentifier = row.identifiers[0]
        const remaining = row.identifiers.length - 1
        return (
          <Box
            sx={{
              display: 'flex',
              flexDirection: 'column',
              rowGap: 0.25,
              minWidth: 0,
            }}
          >
            <Typography
              variant="body2"
              noWrap
              title={row.display_name}
            >
              {row.display_name}
            </Typography>
            {firstIdentifier !== undefined && (
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'baseline',
                  columnGap: 0.5,
                  minWidth: 0,
                }}
              >
                <Typography
                  variant="caption"
                  color="text.secondary"
                  noWrap
                  sx={{ minWidth: 0 }}
                >
                  {firstIdentifier.value}
                </Typography>
                {remaining > 0 && (
                  <Typography
                    variant="caption"
                    color="text.secondary"
                    sx={{ flexShrink: 0 }}
                  >
                    {t('accounts.identifiersMore', {
                      count: String(remaining),
                    })}
                  </Typography>
                )}
              </Box>
            )}
          </Box>
        )
      },
    },
    {
      id: 'status',
      header: t('accounts.column.status'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (row) => statusLabel(row.status, t),
    },
    {
      id: 'mfa',
      header: t('accounts.column.mfa'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (row) =>
        row.mfa_required ? t('accounts.mfaYes') : t('accounts.mfaNo'),
    },
    // Host columns sit between the page's own fields and the row's write
    // controls, so the controls stay the last cell of every row.
    ...(extraColumns ?? []),
    {
      id: 'actions',
      header: t('accounts.column.actions'),
      cellWrap: 'nowrap',
      cell: (row) => (
        <Box sx={{ display: 'flex', columnGap: 1 }}>
          <Button
            type="button"
            size="small"
            variant="outlined"
            aria-label={t('accounts.manageAria', { id: row.id })}
            // The panel is modal: this control only opens it. Closing is
            // the panel's own exit, never a second click here.
            aria-haspopup="dialog"
            onClick={(event) => {
              detailTrigger.current = event.currentTarget
              openDetail(row.id)
            }}
          >
            {t('accounts.manage')}
          </Button>
          <Button
            type="button"
            size="small"
            variant="outlined"
            color="error"
            aria-label={t('accounts.deleteAria', { id: row.id })}
            loading={listWrite.pending}
            disabled={listWrite.pending}
            onClick={() =>
              confirm.ask({
                title: t('accounts.deleteTitle'),
                // The roster names an account by its display name and, when
                // the view carries one, its first login identifier; an empty
                // display name with an identifier is named by that identifier
                // alone, and no identity at all keeps the sentence neutral.
                message: deleteMessageFor(row),
                confirmLabel: t('common.confirm'),
                // Deleting an account takes its identifiers, secrets and
                // sessions with it and cannot be undone from the interface:
                // the heavier two-step guard, never the plain single confirm.
                doubleConfirm: true,
                run: () => void deleteOne(row),
              })
            }
          >
            {t('accounts.delete')}
          </Button>
        </Box>
      ),
    },
  ]

  const sessionColumns: DataTableColumn<ConsoleAccountSession>[] = [
    {
      id: 'id',
      header: t('accounts.sessions.column.id'),
      priority: 'low',
      cellWrap: 'nowrap',
      cellFullValue: (row) => row.id,
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (row) => (
        <Typography variant="body2">{identifierPrefix(row.id)}</Typography>
      ),
    },
    {
      id: 'created',
      header: t('accounts.sessions.column.created'),
      cellWrap: 'nowrap',
      cell: (row) => {
        // The primary identity also carries the current-session mark:
        // this column never hides, so the safety hint survives every
        // tier instead of disappearing with the low-priority id column.
        const formatted = formatInstant(row.created_at, i18n.language, {
          timeZone: 'UTC',
        })
        return (
          <Box sx={{ display: 'flex', alignItems: 'center', columnGap: 1 }}>
            <Typography
              variant="body2"
              noWrap
              sx={primaryColumnMaxWidthSx}
              title={formatted === '' ? undefined : formatted}
            >
              {formatted}
            </Typography>
            {row.current === true && (
              <Chip
                size="small"
                variant="outlined"
                color="info"
                label={t('accounts.sessions.current')}
                sx={{ flexShrink: 0 }}
              />
            )}
          </Box>
        )
      },
    },
    {
      id: 'state',
      header: t('accounts.sessions.column.state'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (row) =>
        row.revoked ? t('accounts.sessions.revoked') : t('accounts.sessions.active'),
    },
    {
      id: 'actions',
      header: t('accounts.sessions.column.actions'),
      cellWrap: 'nowrap',
      cell: (row) => (
        <Button
          type="button"
          size="small"
          variant="outlined"
          color="error"
          aria-label={t('accounts.sessions.revokeAria', { id: row.id })}
          loading={detailSessionWrite.pending}
          disabled={row.revoked || detailSessionWrite.pending}
          onClick={() =>
            confirm.ask({
              title: t('accounts.sessions.revokeTitle'),
              message: t(
                row.current === true
                  ? 'accounts.sessions.revokeMessageCurrent'
                  : 'accounts.sessions.revokeMessage',
                { id: row.id },
              ),
              confirmLabel: t('common.confirm'),
              run: () => void revokeOneSession(row),
            })
          }
        >
          {t('accounts.sessions.revoke')}
        </Button>
      ),
    },
  ]

  const rows = accounts.data?.accounts
  const pending = accounts.isPending && rows === undefined
  // Only a settled page names the next one: while a page read is in flight
  // there is no cursor to advance from, and an empty one is the last page.
  const hasNextPage =
    accounts.data !== undefined && accounts.data.next_cursor !== ''

  return (
    <Box>
      <PageHeader
        title={t('accounts.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('accounts.title') },
        ]}
        actions={
          <Box sx={{ display: 'flex', gap: 1 }}>
            <RefreshButton
              // Refreshing changes the list scope's context: while one of
              // its writes is in flight it is natively disabled, so a late
              // result cannot land against a list it no longer describes.
              // While the list read is in flight it is marked busy.
              busy={pageRefresh.busy}
              writePending={listWrite.pending}
              label={t('common.refresh')}
              onClick={pageRefresh.refresh}
            />
            <Button
              type="button"
              variant="contained"
              size="small"
              aria-haspopup="dialog"
              onClick={(event) => {
                createTrigger.current = event.currentTarget
                setCreateOpen(true)
              }}
            >
              {t('accounts.create.title')}
            </Button>
          </Box>
        }
        sx={{ mb: 2 }}
      />

      <AccountCreatePanel
        open={createOpen}
        attributeFields={attributeFields}
        onClose={() => setCreateOpen(false)}
        onCreated={accountCreated}
        onExited={createPanelExited}
      />

      <SectionCard sx={{ mb: 3 }}>
        <Box
          sx={{
            display: 'flex',
            flexWrap: 'wrap',
            alignItems: 'flex-end',
            columnGap: 2,
            rowGap: 1,
          }}
        >
          <TextField
            select
            size="small"
            value={kindDraft}
            onChange={(event) => setKindDraft(event.target.value as IdentifierKind)}
            label={t('accounts.identifierKindLabel')}
            sx={{ minWidth: 140 }}
          >
            {IDENTIFIER_KINDS.map((kind) => (
              <MenuItem key={kind} value={kind}>
                {t(`accounts.identifierKind.${kind}`)}
              </MenuItem>
            ))}
          </TextField>
          <TextField
            size="small"
            value={identifierDraft}
            onChange={(event) => setIdentifierDraft(event.target.value)}
            label={t('accounts.identifierLabel')}
          />
          <TextField
            size="small"
            value={displayNameDraft}
            onChange={(event) => setDisplayNameDraft(event.target.value)}
            label={t('accounts.displayNamePrefixLabel')}
          />
          <TextField
            select
            size="small"
            value={statusDraft}
            onChange={(event) => setStatusDraft(event.target.value)}
            label={t('accounts.filter.status')}
            sx={{ minWidth: 140 }}
          >
            <MenuItem value="">{t('accounts.filter.all')}</MenuItem>
            {FILTER_STATUSES.map((value) => (
              <MenuItem key={value} value={value}>
                {t(`accounts.status.${value}`)}
              </MenuItem>
            ))}
          </TextField>
          <TextField
            select
            size="small"
            value={accountKindDraft}
            onChange={(event) => setAccountKindDraft(event.target.value)}
            label={t('accounts.filter.kind')}
            sx={{ minWidth: 140 }}
          >
            <MenuItem value="">{t('accounts.filter.all')}</MenuItem>
            {ACCOUNT_KINDS.map((value) => (
              <MenuItem key={value} value={value}>
                {t(`accounts.create.kind.${value}`)}
              </MenuItem>
            ))}
          </TextField>
          <Button
            type="button"
            variant="contained"
            disabled={listWrite.pending}
            onClick={() => {
              setIdentifier(identifierDraft.trim())
              setIdentifierKind(kindDraft)
              setDisplayName(displayNameDraft.trim())
              setStatus(statusDraft)
              setAccountKind(accountKindDraft)
              // A submitted filter is a new list: return to its first page,
              // which is read by the empty cursor.
              setCursors([''])
              setPage(0)
            }}
          >
            {t('accounts.search')}
          </Button>
        </Box>
      </SectionCard>

      <SectionCard flushContent>
        <ConsoleAsyncSection
          pending={pending}
          payload={rows}
          error={accounts.error}
          refreshing={accounts.isFetching}
          // Only the first page treats an empty answer as "no accounts": a
          // later page that comes back empty still renders the table (its
          // own empty state) so the footer, and the page before it, stay
          // reachable.
          empty={
            rows !== undefined && rows.length === 0 && page === 0
          }
          headingLevel="h2"
          loading={
            <ListSkeleton
              label={t('accounts.loading')}
              rows={[
                <Skeleton key="a" variant="text" width="58%" />,
                <Skeleton key="b" variant="text" width="46%" />,
              ]}
            />
          }
          errorState={{
            title: t('accounts.error.title'),
            description: t('accounts.error.description'),
            retryLabel: t('accounts.retry'),
            onRetry: () => void accounts.refetch(),
          }}
          emptyState={{
            title: t('accounts.empty.title'),
            description: t('accounts.empty.description'),
          }}
        >
          {(settled) => (
            <DataTable
              rows={settled}
              columns={columns}
              rowKey={(row) => row.id}
              // The row-actions column is last, and at the mobile floor
              // the primary cell and the actions together can be wider
              // than the page content. Pinning the actions keeps Manage
              // and Delete reachable while the main columns scroll under
              // them; where the table already fits, the pin is inert.
              pinLastColumn
              // The primary cell stacks the display name over its first
              // identifier, so the row is top-aligned: the copy control
              // sits beside the identity line instead of dropping to the
              // middle of a two-line cell.
              rowVerticalAlign="top"
              // The table stands in for the async section's empty branch on
              // a later page, so it carries the same wording and heading
              // level.
              emptyTitle={t('accounts.empty.title')}
              emptyDescription={t('accounts.empty.description')}
              emptyHeadingLevel="h2"
            />
          )}
        </ConsoleAsyncSection>
      </SectionCard>

      {/* The paging footer lives outside the async section's branches: it
          is the control that changes the list's page, so it must survive
          the page read it starts (a pending, failed or empty page would
          otherwise unmount the one control that could return to the page
          before it) and keep keyboard focus across the swap. */}
      <Box sx={{ mt: 2 }}>
        <TablePagination
          component="div"
          // The cursor read reports no total. -1 asks the footer for the
          // unknown-total wording instead of inventing a count.
          count={-1}
          page={page}
          rowsPerPage={ACCOUNTS_PAGE_SIZE}
          // One size, so the footer offers no size selector and the
          // callback below never fires.
          rowsPerPageOptions={[ACCOUNTS_PAGE_SIZE]}
          onPageChange={(_event, nextPage) => changePage(nextPage)}
          onRowsPerPageChange={() => undefined}
          labelRowsPerPage={tUiKit('dataTable.rowsPerPage')}
          labelDisplayedRows={({ from, to }) =>
            tUiKit('dataTable.displayedRowsUnknown', { from, to })
          }
          slotProps={{
            actions: {
              // Changing the page is a change of the list's context, so
              // both directions are disabled while one of the list's
              // writes is in flight (the same rule the toolbar's search
              // and the header's refresh follow).
              previousButton: { disabled: listWrite.pending || page === 0 },
              nextButton: { disabled: listWrite.pending || !hasNextPage },
            },
          }}
        />
      </Box>

      {/* The list's feedback: the row delete's result and the create
          panel's success, which is written here only after the panel's
          exit so the page is reachable when it appears. */}
      <WriteFeedbackRegion outcome={listWrite.outcome} sx={{ mb: 2 }} />

      <SidePanel
        open={detailOpen}
        size="wide"
        title={t('accounts.detail.title', { id: selectedId })}
        busy={detailBusy}
        onClose={requestDetailClose}
        onExited={detailExited}
      >
        {selectedId !== '' && (
          <>
            <ConsoleAsyncSection
              pending={account.isPending && account.data === undefined}
              payload={account.data}
              error={account.error}
              refreshing={account.isFetching}
              empty={false}
              headingLevel="h3"
              loading={
                <ListSkeleton
                  label={t('accounts.detail.loading')}
                  rows={[
                    <Skeleton key="a" variant="text" width="42%" />,
                    <Skeleton key="b" variant="text" width="34%" />,
                  ]}
                />
              }
              errorState={{
                title: t('accounts.detail.error.title'),
                description: t('accounts.detail.error.description'),
                retryLabel: t('accounts.detail.retry'),
                onRetry: () => void account.refetch(),
              }}
              notFoundState={{
                title: t('detail.notFound.title'),
                description: t('detail.notFound.description'),
                retryLabel: t('accounts.detail.retry'),
                onRetry: () => void account.refetch(),
              }}
            >
              {(settled) => (
                <Box sx={{ mb: 2 }}>
                  <Typography variant="body2">
                    {t('accounts.detail.displayNameLine', {
                      value: settled.display_name,
                    })}
                  </Typography>
                  <Typography variant="body2">
                    {t('accounts.detail.statusLine', {
                      value: statusLabel(settled.status, t),
                    })}
                  </Typography>
                  <Typography variant="body2">
                    {t('accounts.detail.kindLine', {
                      value: closedSetLabel(
                        'accounts.create.kind',
                        settled.kind,
                        t,
                        i18n,
                      ),
                    })}
                  </Typography>
                </Box>
              )}
            </ConsoleAsyncSection>

            {/* The action row's account-level actions share one scope: they
                are the same control group and each message names the
                account. Each control follows its own feature: resetting
                factors belongs to the factors face, the bulk revocation to
                the sessions face. */}
            {(factorsPresent || sessionsPresent) && (
              <>
                <Box
                  sx={{
                    display: 'flex',
                    flexWrap: 'wrap',
                    columnGap: 1,
                    rowGap: 1,
                  }}
                >
                  {factorsPresent && (
                    <Button
                      type="button"
                      variant="outlined"
                      loading={detailActionWrite.pending}
                      disabled={detailActionWrite.pending}
                      onClick={() =>
                        confirm.ask({
                          title: t('accounts.resetMFATitle'),
                          message: t('accounts.resetMFAMessage'),
                          confirmLabel: t('common.confirm'),
                          run: () => void resetAllFactors(),
                        })
                      }
                    >
                      {t('accounts.resetMFA')}
                    </Button>
                  )}
                  {sessionsPresent && (
                    <Button
                      type="button"
                      variant="outlined"
                      color="error"
                      loading={detailActionWrite.pending}
                      disabled={detailActionWrite.pending}
                      onClick={() =>
                        confirm.ask({
                          title: t('accounts.revokeSessionsTitle'),
                          message: t(
                            sessionsIncludeCurrent
                              ? 'accounts.revokeSessionsMessageCurrent'
                              : 'accounts.revokeSessionsMessage',
                          ),
                          confirmLabel: t('common.confirm'),
                          run: () => void revokeAllSessions(),
                        })
                      }
                    >
                      {t('accounts.revokeSessions')}
                    </Button>
                  )}
                </Box>
                <WriteFeedbackRegion
                  outcome={detailActionWrite.outcome}
                  sx={{ mb: 2 }}
                />
              </>
            )}

            {account.data !== undefined && (
              <AccountDetailWrites
                key={selectedId}
                account={account.data}
                feedback={detailFeedback}
                attributeFields={attributeFields}
                oneTimeSecret={oneTimeSecret}
                onSecretCreated={setOneTimeSecret}
                onCloseSecret={() => setOneTimeSecret(null)}
              />
            )}

            {factorsPresent && <AccountFactorsSection accountId={selectedId} />}

            {secretsPresent && (
              <AccountSecretsSection
                accountId={selectedId}
                feedback={detailSecretWrite}
                confirm={confirm}
                onRevoke={revokeOneSecret}
              />
            )}

            {sessionsPresent && (
              <>
                <Typography
                  component="h3"
                  variant="subtitle1"
                  sx={{ mt: 2, mb: 1 }}
                >
                  {t('accounts.sessions.title')}
                </Typography>
                <ConsoleAsyncSection
                  pending={sessions.isPending && sessions.data === undefined}
                  payload={sessions.data}
                  error={sessions.error}
                  refreshing={sessions.isFetching}
                  empty={sessions.data !== undefined && sessions.data.length === 0}
                  headingLevel="h3"
                  loading={
                    <ListSkeleton
                      label={t('accounts.sessions.loading')}
                      rows={[<Skeleton key="a" variant="text" width="48%" />]}
                    />
                  }
                  errorState={{
                    title: t('accounts.sessions.error.title'),
                    description: t('accounts.sessions.error.description'),
                    retryLabel: t('accounts.sessions.retry'),
                    onRetry: () => void sessions.refetch(),
                  }}
                  emptyState={{
                    title: t('accounts.sessions.empty.title'),
                    description: t('accounts.sessions.empty.description'),
                  }}
                >
                  {(settled) => (
                    <DataTable
                      rows={settled}
                      columns={sessionColumns}
                      rowKey={(row) => row.id}
                    />
                  )}
                </ConsoleAsyncSection>
                <WriteFeedbackRegion
                  outcome={detailSessionWrite.outcome}
                  sx={{ mb: 2 }}
                />
              </>
            )}

            <Box
              sx={{
                mt: 2,
                display: 'flex',
                flexWrap: 'wrap',
                alignItems: 'flex-end',
                columnGap: 2,
                rowGap: 1,
              }}
            >
              <TextField
                size="small"
                value={identifierToRemove}
                onChange={(event) => setIdentifierToRemove(event.target.value)}
                label={t('accounts.removeIdentifier.label')}
              />
              <Button
                type="button"
                variant="outlined"
                color="error"
                loading={detailIdentifierRemoveWrite.pending}
                disabled={
                  detailIdentifierRemoveWrite.pending ||
                  identifierToRemove.trim() === ''
                }
                onClick={() =>
                  confirm.ask({
                    title: t('accounts.removeIdentifier.confirmTitle'),
                    message: t('accounts.removeIdentifier.confirmMessage', {
                      identifier: identifierToRemove,
                    }),
                    confirmLabel: t('common.confirm'),
                    run: () => void removeOne(identifierToRemove),
                  })
                }
              >
                {t('accounts.removeIdentifier.submit')}
              </Button>
            </Box>
            <WriteFeedbackRegion
              outcome={detailIdentifierRemoveWrite.outcome}
              sx={{ mb: 2 }}
            />
          </>
        )}
      </SidePanel>

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />

      {/* A detail close request with an unclosed one-time secret confirms
          first: a secret shown once cannot be recovered once discarded. */}
      <ConfirmDialog
        open={discardSecretOpen}
        title={t('accounts.createSecret.discardTitle')}
        message={t('accounts.createSecret.discardMessage')}
        confirmLabel={t('accounts.createSecret.discardConfirm')}
        cancelLabel={t('common.cancel')}
        onConfirm={() => {
          setDiscardSecretOpen(false)
          setOneTimeSecret(null)
          setDetailOpen(false)
        }}
        onCancel={() => setDiscardSecretOpen(false)}
      />

      {session !== null && (
        <StepUpChallenge
          open={gate.open}
          reason={gate.reason}
          session={session}
          onSuccess={gate.succeed}
          onCancel={gate.cancel}
        />
      )}
    </Box>
  )
}

/**
 * AccountFactorsSection: one account's registered factors.
 *
 * The page mounts it only while authn.admin.factors is present, so the
 * factor read lives and dies with the section: an absent feature renders no
 * heading and no table, and issues no request.
 */
function AccountFactorsSection({ accountId }: { readonly accountId: string }) {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const factors = operations.useAccountFactors({ id: accountId })
  const columns: DataTableColumn<ConsoleAccountFactor>[] = [
    {
      id: 'kind',
      header: t('accounts.factors.column.kind'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (row) =>
        closedSetLabel('accounts.factors.kind', row.kind, t, i18n),
    },
    {
      id: 'label',
      header: t('accounts.factors.column.label'),
      cellWrap: 'nowrap',
      cell: (row) => (
        <Typography
          variant="body2"
          noWrap
          sx={primaryColumnMaxWidthSx}
          title={row.label}
        >
          {row.label}
        </Typography>
      ),
    },
    {
      id: 'status',
      header: t('accounts.factors.column.status'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (row) =>
        closedSetLabel('accounts.factors.status', row.status, t, i18n),
    },
  ]
  return (
    <>
      <Typography component="h3" variant="subtitle1" sx={{ mb: 1 }}>
        {t('accounts.factors.title')}
      </Typography>
      <ConsoleAsyncSection
        pending={factors.isPending && factors.data === undefined}
        payload={factors.data}
        error={factors.error}
        refreshing={factors.isFetching}
        empty={factors.data !== undefined && factors.data.length === 0}
        headingLevel="h3"
        loading={
          <ListSkeleton
            label={t('accounts.factors.loading')}
            rows={[<Skeleton key="a" variant="text" width="48%" />]}
          />
        }
        errorState={{
          title: t('accounts.factors.error.title'),
          description: t('accounts.factors.error.description'),
          retryLabel: t('accounts.factors.retry'),
          onRetry: () => void factors.refetch(),
        }}
        emptyState={{
          title: t('accounts.factors.empty.title'),
          description: t('accounts.factors.empty.description'),
        }}
      >
        {(settled) => (
          <DataTable
            rows={settled}
            columns={columns}
            rowKey={(row) => `${row.kind}:${row.label}`}
          />
        )}
      </ConsoleAsyncSection>
    </>
  )
}

/**
 * AccountSecretsSection: one account's service secrets.
 *
 * The page mounts it only while authn.admin.secrets is present, so the
 * secrets read, their table and the per-row revoke control all share the
 * feature's answer. The scope and the revoke action stay on the page: the
 * panel's busy state covers every scope inside it and the confirmation
 * dialog is the page's own.
 */
function AccountSecretsSection({
  accountId,
  feedback,
  confirm,
  onRevoke,
}: {
  readonly accountId: string
  readonly feedback: WriteFeedback
  readonly confirm: PendingConfirmState
  readonly onRevoke: (row: ConsoleAccountSecret) => Promise<void>
}) {
  const { t, i18n } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const secrets = operations.useAccountSecrets({ id: accountId })
  const columns: DataTableColumn<ConsoleAccountSecret>[] = [
    {
      id: 'label',
      header: t('accounts.secrets.column.label'),
      cellWrap: 'nowrap',
      cell: (row) => (
        <Typography
          variant="body2"
          noWrap
          sx={primaryColumnMaxWidthSx}
          title={row.label}
        >
          {row.label}
        </Typography>
      ),
    },
    {
      id: 'expires',
      header: t('accounts.secrets.column.expires'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (row) =>
        formatInstant(row.expires_at, i18n.language, { timeZone: 'UTC' }),
    },
    {
      id: 'state',
      header: t('accounts.secrets.column.state'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (row) =>
        row.revoked ? t('accounts.secrets.revoked') : t('accounts.secrets.active'),
    },
    {
      id: 'actions',
      header: t('accounts.secrets.column.actions'),
      cellWrap: 'nowrap',
      cell: (row) => (
        <Button
          type="button"
          size="small"
          variant="outlined"
          color="error"
          aria-label={t('accounts.secrets.revokeAria', { label: row.label })}
          loading={feedback.pending}
          disabled={row.revoked || feedback.pending}
          onClick={() =>
            confirm.ask({
              title: t('accounts.secrets.revokeTitle'),
              message: t('accounts.secrets.revokeMessage', { label: row.label }),
              confirmLabel: t('common.confirm'),
              run: () => void onRevoke(row),
            })
          }
        >
          {t('accounts.secrets.revoke')}
        </Button>
      ),
    },
  ]
  return (
    <>
      <Typography component="h3" variant="subtitle1" sx={{ mt: 2, mb: 1 }}>
        {t('accounts.secrets.title')}
      </Typography>
      <ConsoleAsyncSection
        pending={secrets.isPending && secrets.data === undefined}
        payload={secrets.data}
        error={secrets.error}
        refreshing={secrets.isFetching}
        empty={secrets.data !== undefined && secrets.data.length === 0}
        headingLevel="h3"
        loading={
          <ListSkeleton
            label={t('accounts.secrets.loading')}
            rows={[<Skeleton key="a" variant="text" width="48%" />]}
          />
        }
        errorState={{
          title: t('accounts.secrets.error.title'),
          description: t('accounts.secrets.error.description'),
          retryLabel: t('accounts.secrets.retry'),
          onRetry: () => void secrets.refetch(),
        }}
        emptyState={{
          title: t('accounts.secrets.empty.title'),
          description: t('accounts.secrets.empty.description'),
        }}
      >
        {(settled) => (
          <DataTable rows={settled} columns={columns} rowKey={(row) => row.id} />
        )}
      </ConsoleAsyncSection>
      <WriteFeedbackRegion outcome={feedback.outcome} sx={{ mb: 2 }} />
    </>
  )
}

/**
 * Parses an optional attribute map the operator typed as a JSON object with
 * string values. Returns null when the text is not such an object, so a form
 * can mark the field and keep the submit disabled.
 */
function parseStringMap(text: string): Record<string, string> | null {
  try {
    const parsed: unknown = JSON.parse(text)
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return null
    }
    const out: Record<string, string> = {}
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value !== 'string') {
        return null
      }
      out[key] = value
    }
    return out
  } catch {
    return null
  }
}

/**
 * The value one declared attribute key holds in the JSON attribute text.
 * Absent, empty or unparsable text reads as an empty value.
 */
function attributeValueOf(text: string, key: string): string {
  const parsed = text.trim() === '' ? null : parseStringMap(text)
  return parsed?.[key] ?? ''
}

/**
 * Replaces one declared attribute key in the JSON attribute text, leaving
 * every key the host did not declare untouched. A blank value removes the
 * declared key, so an emptied field leaves no empty-string attribute
 * behind.
 */
function withAttributeValue(text: string, key: string, value: string): string {
  const parsed = text.trim() === '' ? {} : (parseStringMap(text) ?? {})
  const next = { ...parsed }
  if (value === '') {
    delete next[key]
  } else {
    next[key] = value
  }
  return JSON.stringify(next)
}

/** The ways an administrator may state an identifier's verification. */
type VerifiedVia = '' | 'admin' | 'import-unverified'

/** The create form's fields, in the shape react-hook-form holds them. */
type CreateAccountFields = {
  kind: 'user' | 'service'
  displayName: string
  username: string
  email: string
  emailVia: VerifiedVia
  phone: string
  phoneVia: VerifiedVia
  password: string
  mfaRequired: boolean
  attributes: string
}

/** The values every opening of the create panel starts from. */
const CREATE_ACCOUNT_DEFAULTS: CreateAccountFields = {
  kind: 'user',
  displayName: '',
  username: '',
  email: '',
  emailVia: '',
  phone: '',
  phoneVia: '',
  password: '',
  mfaRequired: false,
  attributes: '',
}

/** The create mutation as the operations bundle returns it. */
type CreateAccountMutation = UseMutationResult<
  ConsoleAccount,
  unknown,
  ConsoleCreateAccountInput
>

/**
 * The bundle key of a create refusal the form words itself, or null for one
 * the scope's code text already covers. Only a user account carries a
 * username, so only its two username refusals are named here.
 */
function createFailureKey(
  kind: CreateAccountFields['kind'],
  code: string,
): string | null {
  if (kind !== 'user') {
    return null
  }
  switch (code) {
    case 'authn.username_taken':
      return 'accounts.create.usernameTaken'
    case 'authn.username_reserved':
      return 'accounts.create.usernameUnusable'
    default:
      return null
  }
}

/**
 * AccountCreateForm: the account-creation control (POST /api/v1/system/auth/accounts),
 * rendered inside the create panel. It names the fields the handler decodes
 * into its create request; a field left blank is not sent. A user account
 * requires a display name and a username: the username is the login name the
 * account is opened with, and the email address and phone number are
 * identifiers beside it, so neither of them can stand in for it. A service
 * account signs in with a service secret and so needs no username -- the
 * server exempts it from that requirement -- and only the display name is
 * required. The submit stays disabled until the form could produce a usable
 * account, and the submit handler itself re-checks, so no path sends a
 * request the server is known to refuse. The fields flow through ui-kit's
 * form family (FormLayout's responsive grid of FormField adapters), kept two
 * columns wide in the panel and collapsed to one when the panel itself is
 * narrow. The attribute set is typed as a JSON object and marks the field
 * when it does not parse; when the host declares its attribute fields, one
 * text field per declared key replaces the JSON editor and writes back into
 * the same JSON value, so the create path and its payload keep one shape.
 *
 * A refused create settles the create panel's own scope, whose region stands
 * under the form with the fields kept for correction. A username the server
 * refuses is named by the form instead: a name another account holds and a
 * name the system keeps for itself each get their own sentence, rendered in
 * the same position as the scope's failure and only when the attempt also
 * left the scope empty. A landed create closes
 * the panel through the page: its success belongs to the list's scope and is
 * written there after the exit, when the page behind the modal is reachable.
 */
function AccountCreateForm({
  form,
  feedback,
  createAccount,
  attributeFields,
  onCreated,
}: {
  readonly form: UseFormReturn<CreateAccountFields>
  readonly feedback: WriteFeedback
  readonly createAccount: CreateAccountMutation
  readonly attributeFields?: readonly AccountsPageAttributeField[]
  readonly onCreated: (created: ConsoleAccount) => void
}) {
  const { t } = useConsoleUiTranslation()
  // The create refusal the form words itself: a username already taken, or
  // one the system keeps for itself. The scope's slot answers every other
  // refusal; the two are mutually exclusive, because this one is raised only
  // for an attempt the slot settles empty.
  const [failureKey, setFailureKey] = useState<string | null>(null)

  // An array, not a fragment: MUI's Select walks its children to build the
  // options and warns when one of them is a Fragment.
  const viaOptions = [
    <MenuItem key="none" value="">
      {t('accounts.create.via.none')}
    </MenuItem>,
    <MenuItem key="admin" value="admin">
      {t('accounts.create.via.admin')}
    </MenuItem>,
    <MenuItem key="import-unverified" value="import-unverified">
      {t('accounts.create.via.importUnverified')}
    </MenuItem>,
  ]

  // The form names an account once it carries a display name and either a
  // service kind or a username; a service account signs in with a service
  // secret, so it needs no username, and a user account is opened with one
  // (an email address or phone number beside it does not stand in for it).
  // Before that the submit is disabled, and the handler below refuses to
  // send anyway.
  const watched = form.watch()
  const namesAnAccount =
    watched.displayName.trim() !== '' &&
    (watched.kind === 'service' || watched.username.trim() !== '')

  const onSubmit: SubmitHandler<CreateAccountFields> = (values) => {
    // The guard judges the trimmed values, so the request sends those same
    // values: a username or phone wrapped in spaces otherwise passes here
    // and is refused by the server. The email leg needs no local trim -- the
    // server trims it.
    const username = values.username.trim()
    const phone = values.phone.trim()
    if (
      values.displayName.trim() === '' ||
      (values.kind !== 'service' && username === '')
    ) {
      // A submit the client refused is still an attempt: the previous
      // result must not stand beside the reasons this one did not fire.
      feedback.reset()
      setFailureKey(null)
      return
    }
    const parsed =
      values.attributes.trim() === '' ? null : parseStringMap(values.attributes)
    void feedback.run(async () => {
      setFailureKey(null)
      let created: ConsoleAccount
      try {
        created = await createAccount.mutateAsync({
          kind: values.kind,
          ...(values.displayName === '' ? {} : { display_name: values.displayName }),
          ...(username === '' ? {} : { username }),
          ...(values.email === '' ? {} : { email: values.email }),
          ...(values.emailVia === '' ? {} : { email_verified_via: values.emailVia }),
          ...(phone === '' ? {} : { phone }),
          ...(values.phoneVia === '' ? {} : { phone_verified_via: values.phoneVia }),
          ...(values.password === '' ? {} : { password: values.password }),
          ...(parsed === null ? {} : { attributes: parsed }),
          ...(values.mfaRequired ? { mfa_required: true } : {}),
        })
      } catch (error) {
        // Only a user account is opened with a username, so only its two
        // username refusals get this form's own sentence; every other
        // refusal (and every refusal of a service account) settles the
        // scope's slot as before.
        const key = createFailureKey(values.kind, errorCodeOf(error))
        if (key === null) {
          throw error
        }
        setFailureKey(key)
        return null
      }
      // The form starts from the initial values on the next opening; the
      // page closes the panel and confirms in the list's scope after exit.
      form.reset()
      onCreated(created)
      return null
    })
  }

  return (
    <>
      <FormLayout
        form={form}
        onSubmit={onSubmit}
        columns={2}
        maxWidth={false}
        actions={
          <Button
            type="submit"
            variant="contained"
            loading={feedback.pending}
            disabled={feedback.pending || !namesAnAccount}
          >
            {t('accounts.create.submit')}
          </Button>
        }
      >
        <FormField
          name="kind"
          render={({ field }) => (
            <TextField
              {...field}
              select
              size="small"
              fullWidth
              label={t('accounts.create.kindLabel')}
            >
              <MenuItem value="user">{t('accounts.create.kind.user')}</MenuItem>
              <MenuItem value="service">{t('accounts.create.kind.service')}</MenuItem>
            </TextField>
          )}
        />
        <FormField
          name="displayName"
          render={({ field }) => (
            <TextField
              {...field}
              size="small"
              fullWidth
              label={t('accounts.create.displayNameLabel')}
            />
          )}
        />
        <FormField
          name="username"
          render={({ field }) => (
            <TextField
              {...field}
              size="small"
              fullWidth
              label={t('accounts.create.usernameLabel')}
              slotProps={{ htmlInput: { 'aria-required': true } }}
            />
          )}
        />
        <FormField
          name="email"
          render={({ field }) => (
            <TextField
              {...field}
              size="small"
              fullWidth
              label={t('accounts.create.emailLabel')}
            />
          )}
        />
        <FormField
          name="emailVia"
          render={({ field }) => (
            <TextField
              {...field}
              select
              size="small"
              fullWidth
              label={t('accounts.create.emailViaLabel')}
            >
              {viaOptions}
            </TextField>
          )}
        />
        <FormField
          name="phone"
          render={({ field }) => (
            <TextField
              {...field}
              size="small"
              fullWidth
              label={t('accounts.create.phoneLabel')}
            />
          )}
        />
        <FormField
          name="phoneVia"
          render={({ field }) => (
            <TextField
              {...field}
              select
              size="small"
              fullWidth
              label={t('accounts.create.phoneViaLabel')}
            >
              {viaOptions}
            </TextField>
          )}
        />
        <FormField
          name="password"
          render={({ field }) => (
            <TextField
              {...field}
              size="small"
              fullWidth
              type="password"
              label={t('accounts.create.passwordLabel')}
            />
          )}
        />
        {attributeFields === undefined ? (
          <FormField
            name="attributes"
            rules={{
              validate: (value) =>
                value.trim() === '' ||
                parseStringMap(value) !== null ||
                t('accounts.create.attributesHelper'),
            }}
            render={({ field, invalid, errorText }) => (
              <TextField
                {...field}
                size="small"
                fullWidth
                multiline
                minRows={2}
                label={t('accounts.create.attributesLabel')}
                error={invalid}
                helperText={errorText ?? t('accounts.create.attributesHelper')}
              />
            )}
          />
        ) : (
          attributeFields.map((declared) => (
            <FormField
              key={declared.key}
              name="attributes"
              render={({ field }) => (
                <TextField
                  size="small"
                  fullWidth
                  label={declared.label}
                  value={attributeValueOf(
                    typeof field.value === 'string' ? field.value : '',
                    declared.key,
                  )}
                  onChange={(event) =>
                    field.onChange(
                      withAttributeValue(
                        typeof field.value === 'string' ? field.value : '',
                        declared.key,
                        event.target.value,
                      ),
                    )
                  }
                  onBlur={field.onBlur}
                  name={field.name}
                />
              )}
            />
          ))
        )}
        <FormField
          name="mfaRequired"
          render={({ field }) => (
            <FormControlLabel
              control={
                <Checkbox
                  checked={field.value}
                  onChange={(event) => field.onChange(event.target.checked)}
                  onBlur={field.onBlur}
                  name={field.name}
                />
              }
              label={t('accounts.create.mfaLabel')}
            />
          )}
        />
      </FormLayout>
      <WriteFeedbackRegion outcome={feedback.outcome} />
      {failureKey === null ? null : (
        <Alert severity="error" role="alert" sx={{ width: '100%', mt: 1 }}>
          {t(failureKey)}
        </Alert>
      )}
    </>
  )
}

/**
 * AccountCreatePanel: the create form's `wide` side panel, opened from the
 * page header. The form starts from its initial values on every opening,
 * and a landed create closes the panel. A modified form is not closed
 * directly: a close request asks through a default-variant ConfirmDialog
 * whether to discard the changes. While the create request is in flight the
 * panel is busy, and SidePanel then reports no close request at all.
 *
 * The failing attempt settles the panel's own scope, whose region is under
 * the form. The success is not written here: `onExited` hands the page the
 * moment the page behind is reachable again, and the page writes it into the
 * list's scope there.
 *
 * The discarding confirmation is a dialog stacked above the panel (both
 * stack at the modal tier; the dialog mounts later), and closing it returns
 * focus to the panel's own close control, which asked.
 */
function AccountCreatePanel({
  open,
  attributeFields,
  onClose,
  onCreated,
  onExited,
}: {
  readonly open: boolean
  readonly attributeFields?: readonly AccountsPageAttributeField[]
  readonly onClose: () => void
  readonly onCreated: (created: ConsoleAccount) => void
  readonly onExited: () => void
}) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const createAccount = operations.useCreateAccount()
  const feedback = useWriteFeedback()
  const form = useForm<CreateAccountFields>({
    defaultValues: CREATE_ACCOUNT_DEFAULTS,
  })
  // Reading `isDirty` during render subscribes the form state to dirty
  // tracking, so the close guard below judges the current draft rather than
  // a value that was never computed.
  const dirty = form.formState.isDirty
  const [discardOpen, setDiscardOpen] = useState(false)

  // Opening starts from the initial values and without the previous
  // attempt's result, so a reopened panel never shows a draft or a failure
  // it already dismissed. `form` is react-hook-form's stable controller and
  // the scope's `reset` keeps its identity, so the effect keys on the open
  // flip alone.
  useEffect(() => {
    if (!open) {
      return
    }
    form.reset()
    feedback.reset()
    setDiscardOpen(false)
  }, [open, form, feedback.reset])

  function requestClose(): void {
    if (feedback.pending) {
      return
    }
    if (dirty) {
      setDiscardOpen(true)
      return
    }
    onClose()
  }

  return (
    <>
      <SidePanel
        open={open}
        title={t('accounts.create.title')}
        size="wide"
        busy={feedback.pending}
        onClose={requestClose}
        onExited={() => {
          form.reset()
          feedback.reset()
          onExited()
        }}
      >
        <AccountCreateForm
          form={form}
          feedback={feedback}
          createAccount={createAccount}
          attributeFields={attributeFields}
          onCreated={onCreated}
        />
      </SidePanel>
      <ConfirmDialog
        open={discardOpen}
        title={t('accounts.create.discardTitle')}
        message={t('accounts.create.discardMessage')}
        confirmLabel={t('accounts.create.discardConfirm')}
        cancelLabel={t('common.cancel')}
        onConfirm={() => {
          setDiscardOpen(false)
          onClose()
        }}
        onCancel={() => setDiscardOpen(false)}
      />
    </>
  )
}

/**
 * OneTimeSecretNotice: the display of a newly created service secret, a
 * value handed out once.
 *
 * Its state is the page's (`AccountDetailWrites` only renders it), so a
 * scope reset cannot drop the only copy. The notice is one polite live
 * region with an explicit close control whose accessible name names the
 * account and the secret's label; the page clears it on that control or on
 * the discard confirmation a detail close request raises.
 */
function OneTimeSecretNotice({
  notice,
  onClose,
}: {
  readonly notice: OneTimeSecret
  readonly onClose: () => void
}) {
  const { t } = useConsoleUiTranslation()
  return (
    <Box sx={{ mt: 1 }}>
      <OneTimeValuePanel
        message={t('accounts.createSecret.handedOver', {
          label: notice.label,
        })}
        value={notice.secret}
        copyLabel={t('accounts.createSecret.copy')}
        closeLabel={t('accounts.createSecret.close')}
        closeAriaLabel={t('accounts.createSecret.closeAria', {
          id: notice.accountId,
          name: notice.accountName,
          label: notice.label,
        })}
        onClose={onClose}
      />
    </Box>
  )
}

/**
 * AccountDetailWrites: the account-level write controls the platform contract
 * declares a body for -- the status change, the MFA requirement, the patch
 * (display name / attributes / protected), the password reset, the
 * identifier add and the service-secret creation. Each is one control group
 * with its own feedback scope and a region directly under it, so a failure
 * lands beside the controls that fired it inside the still-open panel. The
 * account is passed settled, and the component is keyed by the account id at
 * its call site so a newly selected account starts from its own values.
 *
 * The one-time secret's state is the page's, passed in and reported back:
 * this component only renders the display under the create-secret group, so
 * remounting the panel content never loses the value.
 *
 * Three groups follow features rather than the page: the MFA requirement and
 * the password reset live on the factors face (authn.admin.factors) and the
 * secret creation on the secrets face (authn.admin.secrets). Their scopes
 * stay with the page -- its busy state covers them -- but their controls and
 * the one-time display render only while the feature is present.
 */
function AccountDetailWrites({
  account,
  feedback,
  attributeFields,
  oneTimeSecret,
  onSecretCreated,
  onCloseSecret,
}: {
  readonly account: ConsoleAccount
  readonly feedback: AccountDetailFeedback
  readonly attributeFields?: readonly AccountsPageAttributeField[]
  readonly oneTimeSecret: OneTimeSecret | null
  readonly onSecretCreated: (notice: OneTimeSecret) => void
  readonly onCloseSecret: () => void
}) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const hasFeature = useConsoleHasFeature()
  const factorsPresent = hasFeature('authn.admin.factors')
  const secretsPresent = hasFeature('authn.admin.secrets')
  const session = useConsoleSession()
  const gate = useStepUpGate<() => void>(
    (retry) => retry(),
    (error) => session !== null && session.isStepUpShortfall(error),
  )
  const patchAccount = operations.usePatchAccount()
  const setStatus = operations.useSetAccountStatus()
  const setMfaRequired = operations.useSetMFARequired()
  const resetPassword = operations.useResetPassword()
  const addIdentifier = operations.useAddIdentifier()
  const createSecret = operations.useCreateSecret()

  const id = account.id
  // The account's readable identity, as this panel's success notices name it.
  const identity = accountIdentityValue(account)
  // Every control is seeded from the account as loaded, so a control shows
  // the account's own value and a write fires only for a field the operator
  // actually changed: a status or MFA change goes out only when it differs
  // from the account's, and the patch carries only the fields that moved.
  const currentStatus: 'active' | 'disabled' =
    account.status === 'disabled' ? 'disabled' : 'active'
  const currentAttributes =
    account.attributes === undefined ? '' : JSON.stringify(account.attributes)
  const currentProtected: 'true' | 'false' = account.protected ? 'true' : 'false'

  const [statusDraft, setStatusDraft] = useState<'active' | 'disabled'>(currentStatus)
  const [mfaDraft, setMfaDraft] = useState<'true' | 'false'>(
    account.mfa_required ? 'true' : 'false',
  )
  const [displayName, setDisplayName] = useState(account.display_name)
  const [attributes, setAttributes] = useState(currentAttributes)
  const [protectedDraft, setProtectedDraft] = useState<'unchanged' | 'true' | 'false'>(
    currentProtected,
  )
  const [tempPassword, setTempPassword] = useState('')
  const [identifierKind, setIdentifierKind] = useState<'username' | 'email'>('username')
  const [identifierValue, setIdentifierValue] = useState('')
  const [secretLabel, setSecretLabel] = useState('')
  const [secretExpires, setSecretExpires] = useState('')

  // Declared attribute fields always hold serialized JSON the page wrote
  // itself, so there is nothing to mark invalid there.
  const attributesInvalid =
    attributeFields === undefined &&
    attributes.trim() !== '' &&
    parseStringMap(attributes) === null
  const statusUnchanged = statusDraft === currentStatus
  const mfaUnchanged = (mfaDraft === 'true') === account.mfa_required
  // The protected control keeps its 'unchanged' sentinel: selecting it, or
  // leaving the control at the account's own value, is a no-op, and the patch
  // carries a protected field only for a real change.
  const protectedUnchanged =
    protectedDraft === 'unchanged' || protectedDraft === currentProtected
  const patchUnchanged =
    displayName === account.display_name &&
    attributes === currentAttributes &&
    protectedUnchanged

  // Each write runs as one attempt on its group's scope. A 401 refused for a
  // step-up shortfall is handed to the gate and settles `null` (the slot is
  // empty from the attempt's own reset); the replay after a won challenge is
  // a new attempt.

  function writeStatus(): void {
    void feedback.status.run(async () => {
      try {
        await setStatus.mutateAsync({ id, status: statusDraft })
      } catch (error) {
        if (gate.intercept(error, writeStatus)) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        { named: 'accounts.statusSaved', unnamed: 'accounts.statusSavedUnnamed' },
        identity,
      )
    })
  }

  function writeMfa(): void {
    void feedback.mfa.run(async () => {
      try {
        await setMfaRequired.mutateAsync({ id, required: mfaDraft === 'true' })
      } catch (error) {
        if (gate.intercept(error, writeMfa)) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        { named: 'accounts.mfaSaved', unnamed: 'accounts.mfaSavedUnnamed' },
        identity,
      )
    })
  }

  function writePatch(): void {
    const parsed = attributes.trim() === '' ? null : parseStringMap(attributes)
    void feedback.patch.run(async () => {
      try {
        await patchAccount.mutateAsync({
          id,
          ...(displayName === account.display_name ? {} : { display_name: displayName }),
          ...(attributes === currentAttributes || parsed === null
            ? {}
            : { attributes: parsed }),
          ...(protectedUnchanged ? {} : { protected: protectedDraft === 'true' }),
        })
      } catch (error) {
        if (gate.intercept(error, writePatch)) {
          return null
        }
        throw error
      }
      return accountNotice(
        t,
        { named: 'accounts.patchSaved', unnamed: 'accounts.patchSavedUnnamed' },
        identity,
      )
    })
  }

  function writePassword(): void {
    void feedback.password.run(async () => {
      try {
        await resetPassword.mutateAsync({ id, password: tempPassword })
      } catch (error) {
        if (gate.intercept(error, writePassword)) {
          return null
        }
        throw error
      }
      setTempPassword('')
      return accountNotice(
        t,
        {
          named: 'accounts.passwordReset',
          unnamed: 'accounts.passwordResetUnnamed',
        },
        identity,
      )
    })
  }

  function addOne(): void {
    void feedback.addIdentifier.run(async () => {
      try {
        await addIdentifier.mutateAsync({
          id,
          kind: identifierKind,
          value: identifierValue,
        })
      } catch (error) {
        if (gate.intercept(error, addOne)) {
          return null
        }
        throw error
      }
      setIdentifierValue('')
      return accountNotice(
        t,
        {
          named: 'accounts.identifierAdded',
          unnamed: 'accounts.identifierAddedUnnamed',
        },
        identity,
        { loginIdentifier: identifierValue },
      )
    })
  }

  function createOne(): void {
    void feedback.createSecret.run(async () => {
      let created: ConsoleAccountSecretCreated
      try {
        created = await createSecret.mutateAsync({
          id,
          label: secretLabel,
          ...(secretExpires === '' ? {} : { expires_at: secretExpires }),
        })
      } catch (error) {
        if (gate.intercept(error, createOne)) {
          return null
        }
        throw error
      }
      setSecretLabel('')
      setSecretExpires('')
      // The value is handed to the page, beside the slot: the display
      // survives this scope's reset and the panel content's remount.
      onSecretCreated({
        accountId: id,
        accountName: account.display_name === '' ? id : account.display_name,
        label: created.label,
        secret: created.secret,
      })
      return accountNotice(
        t,
        {
          named: 'accounts.secretCreated',
          unnamed: 'accounts.secretCreatedUnnamed',
        },
        identity,
        { label: created.label },
      )
    })
  }

  const rowSx = {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'flex-end',
    columnGap: 2,
    rowGap: 1,
    mb: 1,
  } as const

  return (
    <>
      <Typography component="h3" variant="subtitle1" sx={{ mb: 1 }}>
        {t('accounts.writes.title')}
      </Typography>

      <Box sx={rowSx}>
        <TextField
          select
          size="small"
          value={statusDraft}
          onChange={(event) =>
            setStatusDraft(event.target.value as 'active' | 'disabled')
          }
          label={t('accounts.status.label')}
          sx={{ minWidth: 160 }}
        >
          <MenuItem value="active">{t('accounts.status.active')}</MenuItem>
          <MenuItem value="disabled">{t('accounts.status.disabled')}</MenuItem>
        </TextField>
        <Button
          type="button"
          variant="outlined"
          loading={feedback.status.pending}
          disabled={feedback.status.pending || statusUnchanged}
          onClick={() => writeStatus()}
        >
          {t('accounts.status.submit')}
        </Button>
      </Box>
      <WriteFeedbackRegion outcome={feedback.status.outcome} />

      {factorsPresent && (
        <>
          <Box sx={{ ...rowSx, mt: 1 }}>
            <TextField
              select
              size="small"
              value={mfaDraft}
              onChange={(event) =>
                setMfaDraft(event.target.value as 'true' | 'false')
              }
              label={t('accounts.mfa.label')}
              sx={{ minWidth: 160 }}
            >
              <MenuItem value="true">{t('accounts.mfa.required')}</MenuItem>
              <MenuItem value="false">{t('accounts.mfa.notRequired')}</MenuItem>
            </TextField>
            <Button
              type="button"
              variant="outlined"
              loading={feedback.mfa.pending}
              disabled={feedback.mfa.pending || mfaUnchanged}
              onClick={() => writeMfa()}
            >
              {t('accounts.mfa.submit')}
            </Button>
          </Box>
          <WriteFeedbackRegion outcome={feedback.mfa.outcome} />
        </>
      )}

      <Box sx={{ ...rowSx, mt: 1 }}>
        <TextField
          size="small"
          value={displayName}
          onChange={(event) => setDisplayName(event.target.value)}
          label={t('accounts.patch.displayNameLabel')}
        />
        {attributeFields === undefined ? (
          <TextField
            size="small"
            value={attributes}
            onChange={(event) => setAttributes(event.target.value)}
            label={t('accounts.patch.attributesLabel')}
            error={attributesInvalid}
            helperText={t('accounts.patch.attributesHelper')}
            multiline
            minRows={1}
            sx={{ minWidth: 240 }}
          />
        ) : (
          attributeFields.map((declared) => (
            <TextField
              key={declared.key}
              size="small"
              value={attributeValueOf(attributes, declared.key)}
              onChange={(event) =>
                setAttributes(
                  withAttributeValue(
                    attributes,
                    declared.key,
                    event.target.value,
                  ),
                )
              }
              label={declared.label}
              sx={{ minWidth: 200 }}
            />
          ))
        )}
        <TextField
          select
          size="small"
          value={protectedDraft}
          onChange={(event) =>
            setProtectedDraft(event.target.value as 'unchanged' | 'true' | 'false')
          }
          label={t('accounts.patch.protectedLabel')}
          sx={{ minWidth: 180 }}
        >
          <MenuItem value="unchanged">
            {t('accounts.patch.protected.unchanged')}
          </MenuItem>
          <MenuItem value="true">{t('accounts.patch.protected.true')}</MenuItem>
          <MenuItem value="false">{t('accounts.patch.protected.false')}</MenuItem>
        </TextField>
        <Button
          type="button"
          variant="outlined"
          loading={feedback.patch.pending}
          disabled={
            feedback.patch.pending || attributesInvalid || patchUnchanged
          }
          onClick={() => writePatch()}
        >
          {t('accounts.patch.submit')}
        </Button>
      </Box>
      <WriteFeedbackRegion outcome={feedback.patch.outcome} />

      {factorsPresent && (
        <>
          <Box sx={{ ...rowSx, mt: 1 }}>
            <TextField
              size="small"
              type="password"
              value={tempPassword}
              onChange={(event) => setTempPassword(event.target.value)}
              label={t('accounts.password.label')}
            />
            <Button
              type="button"
              variant="outlined"
              loading={feedback.password.pending}
              disabled={feedback.password.pending || tempPassword === ''}
              onClick={() => writePassword()}
            >
              {t('accounts.password.submit')}
            </Button>
          </Box>
          <WriteFeedbackRegion outcome={feedback.password.outcome} />
        </>
      )}

      <Box sx={{ ...rowSx, mt: 1 }}>
        <TextField
          select
          size="small"
          value={identifierKind}
          onChange={(event) =>
            setIdentifierKind(event.target.value as 'username' | 'email')
          }
          label={t('accounts.addIdentifier.kindLabel')}
          sx={{ minWidth: 140 }}
        >
          <MenuItem value="username">
            {t('accounts.identifierKind.username')}
          </MenuItem>
          <MenuItem value="email">{t('accounts.identifierKind.email')}</MenuItem>
        </TextField>
        <TextField
          size="small"
          value={identifierValue}
          onChange={(event) => setIdentifierValue(event.target.value)}
          label={t('accounts.addIdentifier.valueLabel')}
        />
        <Button
          type="button"
          variant="outlined"
          loading={feedback.addIdentifier.pending}
          disabled={feedback.addIdentifier.pending || identifierValue === ''}
          onClick={() => addOne()}
        >
          {t('accounts.addIdentifier.submit')}
        </Button>
      </Box>
      <WriteFeedbackRegion outcome={feedback.addIdentifier.outcome} />

      {secretsPresent && (
        <>
          <Box sx={{ ...rowSx, mt: 1 }}>
            <TextField
              size="small"
              value={secretLabel}
              onChange={(event) => setSecretLabel(event.target.value)}
              label={t('accounts.createSecret.labelLabel')}
            />
            <TextField
              size="small"
              value={secretExpires}
              onChange={(event) => setSecretExpires(event.target.value)}
              label={t('accounts.createSecret.expiresLabel')}
              helperText={t('accounts.createSecret.expiresHelper')}
            />
            <Button
              type="button"
              variant="outlined"
              loading={feedback.createSecret.pending}
              disabled={feedback.createSecret.pending || secretLabel === ''}
              onClick={() => createOne()}
            >
              {t('accounts.createSecret.submit')}
            </Button>
          </Box>
          <WriteFeedbackRegion
            outcome={feedback.createSecret.outcome}
            sx={{ mb: 2 }}
          />
          {oneTimeSecret !== null && oneTimeSecret.accountId === id && (
            <OneTimeSecretNotice notice={oneTimeSecret} onClose={onCloseSecret} />
          )}
        </>
      )}

      {session !== null && (
        <StepUpChallenge
          open={gate.open}
          reason={gate.reason}
          session={session}
          onSuccess={gate.succeed}
          onCancel={gate.cancel}
        />
      )}
    </>
  )
}
