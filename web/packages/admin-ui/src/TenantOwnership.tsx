/**
 * TenantOwnership: the owner's transfer and delete controls on the profile, with
 * ownership moving only on confirmation and local 403/404/409 text.
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
  useInertSelectMenu,
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
 * The local failure codes this surface resolves: UI facts, not server codes
 * (the tenant admin answers 403, 404 and one conflict type).
 */
const OWNERSHIP_FORBIDDEN = 'ownership.forbidden'
const OWNERSHIP_NOT_FOUND = 'ownership.not_found'
const OWNERSHIP_CONFLICT = 'ownership.conflict'
const OWNERSHIP_PENDING = 'ownership.pending'
const OWNERSHIP_EXPIRED = 'ownership.expired'
const OWNERSHIP_NOT_PENDING = 'ownership.not_pending'
const OWNERSHIP_OWNER_CHANGED = 'ownership.owner_changed'
const OWNERSHIP_UNKNOWN = 'ownership.unknown'

function ownershipFailure(code: string): Error & { readonly code: string } {
  return Object.assign(new Error(code), { code })
}

/**
 * The readable case behind a refusal: 403 is a caller no longer accepted, 404 a
 * gone organization, and 409 every conflict the tenant admin reports under one
 * type (the page re-reads pending state to separate what it can).
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
  /** The organization's display name, falling back to its name, for the dialog and notices. */
  readonly displayName: string
  /** The host's action when a deletion and no departure action is supplied. */
  readonly onTenantUpdated?: () => void | Promise<unknown>
  /**
   * The host's action after a deletion ends the caller's membership: leave,
   * reload the caller tenant list and show `message` on the destination.
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
  // A landed deletion withdraws the controls even without a departure action;
  // a confirmed transfer does not set it, since the previous owner stays a member.
  const [departed, setDeparted] = useState(false)

  const currentAccount =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.account_id ?? '')
      : ''
  // The kind is the roster's answer, never a guess from the name.
  const personal = profile.kind === 'personal'

  // Only the current owner sees the controls; a personal organization, a
  // tombstone and a departed caller carry none. A refusal can leave the profile
  // naming another owner while the attempt's message stays visible.
  const controlsAllowed =
    !departed &&
    !personal &&
    profile.status !== 'deleted' &&
    currentAccount !== '' &&
    currentAccount === profile.owner_account
  // The pending read is the owner's; a hidden caller never issues it.
  const pendingRead = operations.useTenantOwnerPending(tenant, controlsAllowed)
  const pending: AdminTenantTransfer | null = pendingRead.data ?? null
  // A settled cancel or delete hands focus to the card's heading, but only when
  // the removal left focus on the body.
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
    // The handed-back confirmation card mounts only when the attempt settles
    // with its message; until then there is no heading to take the focus.
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

  // One scope, one region: rendered beside the controls or alone after a refusal.
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

  // A deletion ends the caller's membership: hand the confirmation to the host's
  // departure action, or show it in the card after the fallback reload. A refused
  // departure leaves the message here rather than dropping it.
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
        // A conflict can also mean a request is already pending; re-read so the
        // card names that case instead of the generic conflict sentence.
        if (httpStatusOf(error) === 409) {
          const reread = await pendingRead.refetch()
          if (reread.data !== null && reread.data !== undefined) {
            throw ownershipFailure(OWNERSHIP_PENDING)
          }
        }
        throw ownershipFailure(ownershipFailureCode(error))
      }
      // The request is recorded but ownership is unchanged until the target confirms.
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
        // A conflict on cancel means the request was already settled unseen;
        // re-read so the card drops the panel and the failure names the state.
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
 * How many matches the dialog's member search asks for. An organization larger
 * than this is reached by narrowing the search, not by paging.
 */
const MEMBER_SEARCH_LIMIT = 100

/**
 * The request dialog: search one active member, choose one, confirm twice.
 * Mounted only while open; the list holds the first `MEMBER_SEARCH_LIMIT` matches.
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
  const targetMenu = useInertSelectMenu()
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
        // A typed field holding focus keeps it while the request runs, so a
        // write locks it by read-only rather than by removing it from the Tab order.
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
        // The member read keeps the native disable (focus is elsewhere when it
        // starts); the dialog's own write turns it read-only to keep focus.
        disabled={loading}
        slotProps={{ input: { readOnly: busy }, select: targetMenu }}
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
 * The delete dialog: type the organization's name, then confirm twice. Deletion
 * is terminal, so both guards are required.
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
