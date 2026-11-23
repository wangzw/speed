/**
 * TenantOwnership: the organization owner's hand-over and termination
 * controls on the organization profile page.
 *
 * The organization profile is read by every tenant administrator, but only
 * the account named in the profile's `owner_account` may ask for ownership to
 * be handed on or delete the organization. A personal organization has no
 * owner controls at all: it is the account's own space, not an organization
 * with a transferable owner. The package hides the controls for every other
 * caller; the server remains the authority on every write.
 *
 * Ownership moves only after the target confirms. The owner's control sends a
 * request to an active member; until that member confirms, the caller stays
 * the owner. The card renders the pending request (target, request time,
 * deadline) and lets the current owner cancel it. Only one request can be
 * pending at a time and it expires after a fixed window, so the transfer
 * control is unavailable while one is pending; the delete control stays
 * available, because deletion settles the request in the same transaction.
 *
 * The write controls stay focusable throughout: one whose write is in flight
 * is busy, and one whose precondition is unmet or whose context another write
 * in this scope is changing is unavailable. A control that is also a button
 * may hold focus at the moment either state arrives, so neither takes the
 * native disable that would move focus to the document body.
 *
 * The transfer dialog searches the organization's active members (the server
 * grants the target tenant-admin when it confirms) and asks for the
 * deliberate second confirmation. The delete dialog requires the operator to
 * type the organization's name and also asks for the second confirmation;
 * deletion is a tombstone with no restore route, so both guards are the heavy
 * ones.
 *
 * A refusal is resolved locally rather than through the console's server-code
 * whitelist, from the write's own answer. A removed or unauthorized caller
 * is answered 403, a vanished organization 404, and a conflict -- an
 * ownership change that beat this attempt, a target that stopped being an
 * active member, a tombstone, a request already pending or already settled --
 * with the one problem type the tenant admin uses. The answer cannot separate
 * those conflicts, so the page re-reads the pending state and the profile to
 * word the case it can observe; the rest share one sentence.
 *
 * A confirmed transfer changes ownership only: the previous owner stays an
 * active member and keeps every binding it held, so the caller stays on the
 * page and the confirmation stays in the card. The profile re-read that the
 * confirmation invalidates shows the new owner, and the controls withdraw
 * because the caller no longer owns the organization.
 *
 * A landed deletion, by contrast, ends the caller's membership. The host's
 * departure action leaves the organization and shows the success message on
 * the destination, so the confirmation never renders inside a page the
 * server is about to refuse. A host that supplies no departure action gets
 * the message in the card, with the controls withdrawn.
 */

import { useEffect, useId, useRef, useState } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import MenuItem from '@mui/material/MenuItem'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useAuthState } from '@speed/auth-core'
import {
  ActionButton,
  ConfirmDialog,
  httpStatusOf,
  SectionCard,
  useFormatInstant,
  useWriteFeedback,
  WriteFeedbackRegion,
} from '@speed/ui-kit'
import { identityLabel } from './internal/account-identity.js'
import { PermissionsIcon } from './internal/icons.js'
import { useSectionHeadingFocus, removedControlHeldFocus } from './internal/section-heading-focus.js'
import { useAdminUiTranslation } from './internal/translation.js'
import { useOrganizationAdminOperations } from './organization-operations.js'
import type {
  AdminTenantProfile,
  AdminTenantTransfer,
} from './organization-operations.js'
import type { AdminMember } from './operations.js'

/**
 * The local failure codes this surface resolves. They are UI facts, not
 * server codes: the tenant admin answers 403, 404 and one conflict type, so
 * these are the cases the page can name from the answer and the pending
 * state it re-reads beside it.
 */
const OWNERSHIP_FORBIDDEN = 'ownership.forbidden'
const OWNERSHIP_NOT_FOUND = 'ownership.not_found'
const OWNERSHIP_CONFLICT = 'ownership.conflict'
const OWNERSHIP_PENDING = 'ownership.pending'
const OWNERSHIP_EXPIRED = 'ownership.expired'
const OWNERSHIP_NOT_PENDING = 'ownership.not_pending'
const OWNERSHIP_OWNER_CHANGED = 'ownership.owner_changed'
const OWNERSHIP_UNKNOWN = 'ownership.unknown'

/** The failure code a synthetic error carries into the feedback scope. */
function ownershipFailure(code: string): Error & { readonly code: string } {
  return Object.assign(new Error(code), { code })
}

/**
 * The readable case behind a refusal, read from the write's own answer. A
 * 403 is a caller the server no longer accepts, a 404 an organization that
 * is gone, and a 409 every conflict the tenant admin reports under its one
 * conflict type -- an ownership change that beat this attempt, a target
 * that stopped being a member, a tombstone, a request already pending or
 * already settled. The page re-reads its pending state where that can
 * separate one case; the rest share the conflict sentence.
 */
function ownershipFailureCode(error: unknown): string {
  const status = httpStatusOf(error)
  if (status === 403) {
    return OWNERSHIP_FORBIDDEN
  }
  if (status === 404) {
    return OWNERSHIP_NOT_FOUND
  }
  if (status === 409) {
    return OWNERSHIP_CONFLICT
  }
  return OWNERSHIP_UNKNOWN
}

/** The host callback's own refusal never overturns a landed write. */
async function settleHost(
  callback: (() => void | Promise<unknown>) | undefined,
): Promise<void> {
  if (callback === undefined) {
    return
  }
  try {
    await callback()
  } catch {
    // The write landed; how the host reacts to it is the host's concern.
  }
}

export interface TenantOwnershipProps {
  /** The organization every request names. */
  readonly tenant: string
  /** The settled profile, carrying the owner account. */
  readonly profile: AdminTenantProfile
  /** The organization's display name, falling back to its name, for the
   * dialog and notices. */
  readonly displayName: string
  /** The host's action when a deletion and no departure action is supplied:
   * reload the caller tenant list the account menu and breadcrumb read. */
  readonly onTenantUpdated?: () => void | Promise<unknown>
  /**
   * The host's action after a deletion ends the caller's membership. The
   * host leaves the organization, reloads the caller tenant list, and shows
   * `message` on the destination, so the confirmation outlives this page.
   * A transfer changes ownership only and never departs.
   */
  readonly onOrganizationLeft?: (message: string) => void | Promise<unknown>
}

/** The owner-only controls on the organization profile page. */
export function TenantOwnership({
  tenant,
  profile,
  displayName,
  onTenantUpdated,
  onOrganizationLeft,
}: TenantOwnershipProps): ReactElement | null {
  const { t } = useAdminUiTranslation()
  const operations = useOrganizationAdminOperations()
  const formatTime = useFormatInstant()
  const snapshot = useAuthState()
  const request = operations.useTenantOwnerRequest()
  const cancel = operations.useTenantOwnerCancel()
  const remove = operations.useTenantOwnerDelete()
  const feedback = useWriteFeedback()
  const pendingDescriptionId = useId()
  const [transferOpen, setTransferOpen] = useState(false)
  const [cancelOpen, setCancelOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  // A landed deletion ends the caller's membership. The local flag withdraws
  // the controls even when the host supplies no departure action (the card
  // then carries the confirmation alone). A confirmed transfer does not set
  // it: the previous owner stays an active member.
  const [departed, setDeparted] = useState(false)

  const currentAccount =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.account_id ?? '')
      : ''
  // The kind is the roster's answer, never a guess from the name: a personal
  // tenant's name is its owner's username, which says nothing about the kind.
  const personal = profile.kind === 'personal'

  // Only the current owner sees the controls; a personal organization, a
  // tombstone and a caller who already left carry none. The server decides
  // every write regardless. A refusal can leave the profile naming another
  // owner underneath this component; the message of the attempt that
  // observed the change stays visible even though the controls are gone.
  const controlsAllowed =
    !departed &&
    !personal &&
    profile.status !== 'deleted' &&
    currentAccount !== '' &&
    currentAccount === profile.owner_account
  // The pending read is the owner's; a caller the controls are hidden from
  // never issues it, so an administrator who is not the owner makes no
  // request the server would refuse.
  const pendingRead = operations.useTenantOwnerPending(tenant, controlsAllowed)
  const pending: AdminTenantTransfer | null = pendingRead.data ?? null
  // A settled cancel or delete withdraws the control that started it; the
  // card's own heading is the stable landmark focus hands off to, and it
  // only takes the focus when the removal left that focus on the body.
  const ownershipFocus = useSectionHeadingFocus()
  const cancelLanded = useRef(false)
  const deleteLanded = useRef(false)
  useEffect(() => {
    if (!cancelLanded.current) {
      return
    }
    if (pending !== null) {
      return
    }
    cancelLanded.current = false
    if (removedControlHeldFocus()) {
      ownershipFocus.focusHeading()
    }
  }, [pending, ownershipFocus.focusHeading])
  useEffect(() => {
    if (!deleteLanded.current) {
      return
    }
    if (controlsAllowed) {
      return
    }
    // The card that carries the handed-back confirmation mounts only when
    // the attempt settles with its message; until then there is no heading
    // to take the focus.
    if (feedback.outcome === null) {
      return
    }
    deleteLanded.current = false
    if (removedControlHeldFocus()) {
      ownershipFocus.focusHeading()
    }
  }, [controlsAllowed, feedback.outcome, ownershipFocus.focusHeading])

  const resolve = (code: string): string => {
    switch (code) {
      case OWNERSHIP_FORBIDDEN:
        return t('tenantProfile.ownership.error.forbidden')
      case OWNERSHIP_NOT_FOUND:
        return t('tenantProfile.ownership.error.notFound')
      case OWNERSHIP_PENDING:
        return t('tenantProfile.ownership.error.pending')
      case OWNERSHIP_EXPIRED:
        return t('tenantProfile.ownership.error.expired')
      case OWNERSHIP_NOT_PENDING:
        return t('tenantProfile.ownership.error.notPending')
      case OWNERSHIP_OWNER_CHANGED:
        return t('tenantProfile.ownership.error.ownerChanged')
      case OWNERSHIP_CONFLICT:
        return t('tenantProfile.ownership.error.conflict')
      default:
        return t('tenantProfile.ownership.error.unknown')
    }
  }

  // One scope, one region: the same element is rendered either beside the
  // controls or alone when a refusal left no controls to show.
  const feedbackRegion = (
    <WriteFeedbackRegion outcome={feedback.outcome} resolve={resolve} />
  )
  if (!controlsAllowed) {
    return feedback.outcome === null ? null : (
      <SectionCard
        ref={ownershipFocus.sectionRef}
        title={t('tenantProfile.ownership.title')}
        icon={<PermissionsIcon size={24} />}
        description={t('tenantProfile.ownership.description')}
        sx={{ mb: 3 }}
      >
        {feedbackRegion}
      </SectionCard>
    )
  }

  const openTransfer = (): void => {
    feedback.reset()
    setTransferOpen(true)
  }

  const openDelete = (): void => {
    feedback.reset()
    setDeleteOpen(true)
  }

  // A deletion ends the caller's membership: hand the confirmation to the
  // host's departure action, which leaves the organization and shows it
  // there. A host with no departure action gets the message in the card
  // instead, after the fallback tenant-list reload; either way the controls
  // are withdrawn. A departure action that refuses leaves the message here
  // rather than dropping the confirmation with it.
  const leave = async (message: string): Promise<string | null> => {
    setDeparted(true)
    if (onOrganizationLeft === undefined) {
      await settleHost(onTenantUpdated)
      return message
    }
    try {
      await onOrganizationLeft(message)
      return null
    } catch {
      return message
    }
  }

  const runRequest = (account: string, label: string): void => {
    void feedback.run(async () => {
      try {
        await request.mutateAsync({ tenant, account })
      } catch (error) {
        // A conflict can also mean the organization already has a pending
        // request. Re-read it so the card names that case instead of the
        // generic conflict sentence; the card's own pending panel then
        // renders the request the read found.
        if (httpStatusOf(error) === 409) {
          const reread = await pendingRead.refetch()
          if (reread.data !== null && reread.data !== undefined) {
            throw ownershipFailure(OWNERSHIP_PENDING)
          }
        }
        throw ownershipFailure(ownershipFailureCode(error))
      }
      // The request is recorded but ownership is unchanged: the caller stays
      // the owner, the card shows the pending request, and the transfer
      // control is held until the target confirms, declines or the window
      // closes.
      return t('tenantProfile.ownership.transfer.requested', {
        account: label,
      })
    })
  }

  const runCancel = (): void => {
    if (pending === null) {
      return
    }
    void feedback.run(async () => {
      try {
        await cancel.mutateAsync({ tenant, request: pending.id })
        cancelLanded.current = true
      } catch (error) {
        // A conflict on cancel means the request was settled by someone the
        // caller cannot see -- confirmed, declined, expired or already
        // cancelled. Re-read it: the card drops the pending panel, and the
        // failure names the state that can be observed.
        if (httpStatusOf(error) === 409) {
          const reread = await pendingRead.refetch()
          if (reread.data === null || reread.data === undefined) {
            throw ownershipFailure(OWNERSHIP_NOT_PENDING)
          }
        }
        throw ownershipFailure(ownershipFailureCode(error))
      }
      return t('tenantProfile.ownership.pending.cancelled')
    })
  }

  const runDelete = (): void => {
    void feedback.run(async () => {
      try {
        await remove.mutateAsync({ tenant })
        deleteLanded.current = true
      } catch (error) {
        throw ownershipFailure(ownershipFailureCode(error))
      }
      return leave(
        t('tenantProfile.ownership.delete.saved', { name: displayName }),
      )
    })
  }

  const pendingTarget =
    pending === null
      ? ''
      : (identityLabel(pending.target_display_name, pending.target_username) ??
        t('common.accountUnavailable'))

  return (
    <SectionCard
      ref={ownershipFocus.sectionRef}
      title={t('tenantProfile.ownership.title')}
      icon={<PermissionsIcon size={24} />}
      description={t('tenantProfile.ownership.description')}
      sx={{ mb: 3 }}
    >
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'flex-start',
          columnGap: 2,
          rowGap: 1,
        }}
      >
        <ActionButton
          type="button"
          busy={request.isPending}
          unavailable={
            pending !== null || (feedback.pending && !request.isPending)
          }
          unavailableDescribedBy={
            pending !== null ? pendingDescriptionId : undefined
          }
          onClick={openTransfer}
        >
          {t('tenantProfile.ownership.transfer.button')}
        </ActionButton>
        <ActionButton
          type="button"
          tone="danger"
          busy={remove.isPending}
          unavailable={feedback.pending && !remove.isPending}
          onClick={openDelete}
        >
          {t('tenantProfile.ownership.delete.button')}
        </ActionButton>
      </Box>
      {pending !== null ? (
        <Box
          component="section"
          aria-label={t('tenantProfile.ownership.pending.title')}
          sx={{
            mt: 2,
            p: 2,
            border: 1,
            borderColor: 'divider',
            borderRadius: 1,
          }}
        >
          <Typography variant="subtitle1" component="h3">
            {t('tenantProfile.ownership.pending.title')}
          </Typography>
          <Typography
            id={pendingDescriptionId}
            variant="body2"
            color="text.secondary"
            sx={{ mt: 0.5 }}
          >
            {t('tenantProfile.ownership.pending.description')}
          </Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', columnGap: 3, rowGap: 0.5, mt: 1 }}>
            <Typography variant="body2">
              {t('tenantProfile.ownership.pending.target', {
                target: pendingTarget,
              })}
            </Typography>
            <Typography variant="body2">
              {t('tenantProfile.ownership.pending.requested', {
                requested: formatTime(pending.requested_at),
              })}
            </Typography>
            <Typography variant="body2">
              {t('tenantProfile.ownership.pending.expires', {
                expires: formatTime(pending.expires_at),
              })}
            </Typography>
          </Box>
          <ActionButton
            type="button"
            size="small"
            sx={{ mt: 1.5 }}
            busy={cancel.isPending}
            unavailable={feedback.pending && !cancel.isPending}
            onClick={() => {
              feedback.reset()
              setCancelOpen(true)
            }}
          >
            {t('tenantProfile.ownership.pending.cancel')}
          </ActionButton>
        </Box>
      ) : null}
      {feedbackRegion}
      {transferOpen && (
        <TransferOwnershipDialog
          tenant={tenant}
          organizationName={displayName}
          ownerAccount={profile.owner_account}
          busy={feedback.pending}
          onCancel={() => setTransferOpen(false)}
          onConfirm={(account, label) => {
            setTransferOpen(false)
            runRequest(account, label)
          }}
        />
      )}
      <ConfirmDialog
        open={cancelOpen}
        title={t('tenantProfile.ownership.pending.cancelTitle')}
        message={t('tenantProfile.ownership.pending.cancelMessage')}
        variant="default"
        confirmLabel={t('tenantProfile.ownership.pending.cancelConfirm')}
        cancelLabel={t('common.cancel')}
        confirmLoading={feedback.pending}
        onCancel={() => setCancelOpen(false)}
        onConfirm={() => {
          setCancelOpen(false)
          runCancel()
        }}
      />
      {deleteOpen && (
        <DeleteOrganizationDialog
          name={profile.name}
          organizationName={displayName}
          busy={feedback.pending}
          onCancel={() => setDeleteOpen(false)}
          onConfirm={() => {
            setDeleteOpen(false)
            runDelete()
          }}
        />
      )}
    </SectionCard>
  )
}

interface TransferOwnershipDialogProps {
  readonly tenant: string
  readonly organizationName: string
  readonly ownerAccount: string
  readonly busy: boolean
  readonly onCancel: () => void
  readonly onConfirm: (account: string, label: string) => void
}

/**
 * The member the transfer names, as the select and the notice show it: the
 * display name with the login username beside it, either alone, or the
 * neutral copy when the member record carries neither. The account id never
 * becomes a label.
 */
function memberLabel(
  t: ReturnType<typeof useAdminUiTranslation>['t'],
  member: AdminMember,
): string {
  return (
    identityLabel(member.display_name, member.username) ??
    t('common.accountUnavailable')
  )
}

/**
 * How many matches the dialog's member search asks the server for. The
 * read is a search, not a paging walk of the roster: an organization larger
 * than this is reached by narrowing the search, not by scrolling, so the
 * dialog never needs the member who would fall beyond a page it stopped at.
 */
const MEMBER_SEARCH_LIMIT = 100

/**
 * The request dialog: search for one active member, choose one and confirm
 * the request twice. Mounted only while open, so the member read is issued
 * when the operator asks for it and not on every profile view. The search
 * is a prefix over the members' display names and login identifiers; the
 * list holds the first `MEMBER_SEARCH_LIMIT` matches, which the operator
 * narrows with the search field.
 */
function TransferOwnershipDialog({
  tenant,
  organizationName,
  ownerAccount,
  busy,
  onCancel,
  onConfirm,
}: TransferOwnershipDialogProps): ReactElement {
  const { t } = useAdminUiTranslation()
  const operations = useOrganizationAdminOperations()
  const [search, setSearch] = useState('')
  const [target, setTarget] = useState('')
  const query = search.trim()
  const members = operations.useMembers(tenant, {
    ...(query === '' ? {} : { query }),
    limit: MEMBER_SEARCH_LIMIT,
  })

  const options = (members.data?.members ?? []).filter(
    (member) => member.active && member.account !== ownerAccount,
  )
  const selected = options.find((member) => member.account === target)
  const loading = members.isPending && members.data === undefined
  const helperText = loading
    ? t('tenantProfile.ownership.transfer.loading')
    : members.isError
      ? t('tenantProfile.ownership.transfer.membersError')
      : options.length === 0
        ? query === ''
          ? t('tenantProfile.ownership.transfer.noMembers')
          : t('tenantProfile.ownership.transfer.noMatches')
        : t('tenantProfile.ownership.transfer.targetHint')

  return (
    <ConfirmDialog
      open
      variant="danger"
      doubleConfirm
      title={t('tenantProfile.ownership.transfer.title', {
        name: organizationName,
      })}
      message={t('tenantProfile.ownership.transfer.message')}
      confirmLabel={t('tenantProfile.ownership.transfer.confirmLabel')}
      cancelLabel={t('common.cancel')}
      confirmLoading={busy}
      confirmDisabled={selected === undefined}
      confirmDisabledReason={t(
        'tenantProfile.ownership.transfer.targetRequired',
      )}
      onCancel={onCancel}
      onConfirm={() => {
        if (selected !== undefined) {
          onConfirm(selected.account, memberLabel(t, selected))
        }
      }}
    >
      <TextField
        size="small"
        fullWidth
        label={t('tenantProfile.ownership.transfer.searchLabel')}
        value={search}
        onChange={(event) => {
          setSearch(event.target.value)
          // The previous selection may not survive the narrowed list.
          setTarget('')
        }}
        // A typed field holding focus keeps it while the request runs, so the
        // dialog's write locks it by turning it read-only rather than by
        // taking it out of the Tab sequence.
        slotProps={{ input: { readOnly: busy } }}
        helperText={t('tenantProfile.ownership.transfer.searchHint', {
          limit: MEMBER_SEARCH_LIMIT,
        })}
        autoComplete="off"
        sx={{ mt: 2 }}
      />
      <TextField
        select
        size="small"
        fullWidth
        label={t('tenantProfile.ownership.transfer.targetLabel')}
        value={target}
        onChange={(event) => setTarget(event.target.value)}
        // The member read is a selection-shaped context control whose focus
        // sits on the search field or the confirming button when it starts,
        // so it keeps the native disable; the dialog's own write instead
        // turns it read-only, keeping a select that held focus focusable.
        disabled={loading}
        slotProps={{ input: { readOnly: busy } }}
        helperText={helperText}
        sx={{ mt: 2 }}
      >
        {options.map((member) => (
          <MenuItem key={member.account} value={member.account}>
            {memberLabel(t, member)}
          </MenuItem>
        ))}
      </TextField>
    </ConfirmDialog>
  )
}

interface DeleteOrganizationDialogProps {
  readonly name: string
  readonly organizationName: string
  readonly busy: boolean
  readonly onCancel: () => void
  readonly onConfirm: () => void
}

/**
 * The delete dialog: type the organization's name, then confirm twice.
 * Deletion is terminal and has no restore route, so the name typing and the
 * double confirmation are both required.
 */
function DeleteOrganizationDialog({
  name,
  organizationName,
  busy,
  onCancel,
  onConfirm,
}: DeleteOrganizationDialogProps): ReactElement {
  const { t } = useAdminUiTranslation()
  const [typed, setTyped] = useState('')
  return (
    <ConfirmDialog
      open
      variant="danger"
      doubleConfirm
      title={t('tenantProfile.ownership.delete.title', {
        name: organizationName,
      })}
      message={t('tenantProfile.ownership.delete.message')}
      confirmLabel={t('tenantProfile.ownership.delete.confirmLabel')}
      cancelLabel={t('common.cancel')}
      confirmLoading={busy}
      confirmDisabled={typed !== name}
      confirmDisabledReason={t(
        'tenantProfile.ownership.delete.nameMismatch',
      )}
      onCancel={onCancel}
      onConfirm={onConfirm}
    >
      <TextField
        size="small"
        fullWidth
        label={t('tenantProfile.ownership.delete.nameLabel')}
        value={typed}
        onChange={(event) => setTyped(event.target.value)}
        helperText={t('tenantProfile.ownership.delete.nameHint', { name })}
        autoComplete="off"
        sx={{ mt: 2 }}
      />
    </ConfirmDialog>
  )
}
