/**
 * TenantOwnership: the organization owner's hand-over and termination
 * controls on the organization profile page.
 *
 * The organization profile is read by every tenant administrator, but only
 * the account named in the profile's `ownerAccount` may hand ownership on or
 * delete the organization. A personal organization has no owner controls at
 * all: it is the account's own space, not an organization with a transferable
 * owner. The package hides the controls for every other caller; the server
 * remains the authority on every write.
 *
 * The transfer dialog searches the organization's active members (the server
 * grants the target tenant-admin when it does not hold it) and asks for the
 * deliberate second confirmation. The delete dialog requires the operator to
 * type the organization's name and also asks for the second confirmation;
 * deletion is a tombstone with no restore route, so both guards are the heavy
 * ones.
 *
 * A refusal is resolved locally rather than through the console's server-code
 * whitelist, from the write's own answer. A removed or unauthorized caller
 * is answered 403, a vanished organization 404, and every conflict -- a
 * target that stopped being an active member, an ownership change that beat
 * this attempt, a tombstone another request wrote -- with the one problem
 * type the tenant admin uses. The answer cannot separate those conflicts, so
 * they share one sentence; nothing is re-read to guess which one it was.
 *
 * A landed transfer or deletion ends the caller's membership. The host's
 * departure action leaves the organization and shows the success message on
 * the destination, so the confirmation never renders inside a page the
 * server is about to refuse. A host that supplies no departure action gets
 * the message in the card, with the controls withdrawn.
 */

import { useState } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import MenuItem from '@mui/material/MenuItem'
import TextField from '@mui/material/TextField'
import { useAuthState } from '@speed/auth-core'
import {
  ConfirmDialog,
  httpStatusOf,
  SectionCard,
  useWriteFeedback,
  WriteFeedbackRegion,
} from '@speed/ui-kit'
import { identityLabel } from './internal/account-identity.js'
import { PermissionsIcon } from './internal/icons.js'
import { useAdminUiTranslation } from './internal/translation.js'
import { useOrganizationAdminOperations } from './organization-operations.js'
import type { AdminTenantProfile } from './organization-operations.js'
import type { AdminMember } from './operations.js'

/**
 * The local failure codes this surface resolves. They are UI facts, not
 * server codes: the tenant admin answers 403, 404 and one conflict type, so
 * these are the cases the page can name without inventing a distinction the
 * answer does not carry.
 */
const OWNERSHIP_FORBIDDEN = 'ownership.forbidden'
const OWNERSHIP_NOT_FOUND = 'ownership.not_found'
const OWNERSHIP_CONFLICT = 'ownership.conflict'
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
 * that stopped being a member, a tombstone. Those conflicts cannot be told
 * apart from the answer, so they share the conflict sentence; nothing is
 * re-read to guess which one it was.
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
  /** The host's action when no departure action is supplied: reload the
   * caller tenant list the account menu and breadcrumb read. */
  readonly onTenantUpdated?: () => void | Promise<unknown>
  /**
   * The host's action after a write ends the caller's membership -- a
   * transfer hands the organization on, a deletion ends it. The host leaves
   * the organization, reloads the caller tenant list, and shows `message`
   * on the destination, so the confirmation outlives this page.
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
  const snapshot = useAuthState()
  const transfer = operations.useTenantOwnerTransfer()
  const remove = operations.useTenantOwnerDelete()
  const feedback = useWriteFeedback()
  const [transferOpen, setTransferOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  // A landed transfer or deletion ends the caller's membership. The local
  // flag withdraws the controls even when the host supplies no departure
  // action (the card then carries the confirmation alone).
  const [departed, setDeparted] = useState(false)

  const currentAccount =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.account_id ?? '')
      : ''
  // The kind is the roster's answer, never a guess from the name: a personal
  // tenant's name is its owner's username, which says nothing about the kind.
  const personal = profile.kind === 'personal'

  const resolve = (code: string): string => {
    switch (code) {
      case OWNERSHIP_FORBIDDEN:
        return t('tenantProfile.ownership.error.forbidden')
      case OWNERSHIP_NOT_FOUND:
        return t('tenantProfile.ownership.error.notFound')
      case OWNERSHIP_CONFLICT:
        return t('tenantProfile.ownership.error.conflict')
      default:
        return t('tenantProfile.ownership.error.unknown')
    }
  }

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
    currentAccount === profile.ownerAccount
  // One scope, one region: the same element is rendered either beside the
  // controls or alone when a refusal left no controls to show.
  const feedbackRegion = (
    <WriteFeedbackRegion outcome={feedback.outcome} resolve={resolve} />
  )
  if (!controlsAllowed) {
    return feedback.outcome === null ? null : (
      <SectionCard
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

  // The caller has left: hand the confirmation to the host's departure
  // action, which leaves the organization and shows it there. A host with no
  // departure action gets the message in the card instead, after the
  // fallback tenant-list reload; either way the controls are withdrawn. A
  // departure action that refuses leaves the message here rather than
  // dropping the confirmation with it.
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

  const runTransfer = (account: string, label: string): void => {
    void feedback.run(async () => {
      try {
        await transfer.mutateAsync({ tenant, account })
      } catch (error) {
        throw ownershipFailure(ownershipFailureCode(error))
      }
      return leave(
        t('tenantProfile.ownership.transfer.saved', {
          name: displayName,
          account: label,
        }),
      )
    })
  }

  const runDelete = (): void => {
    void feedback.run(async () => {
      try {
        await remove.mutateAsync({ tenant })
      } catch (error) {
        throw ownershipFailure(ownershipFailureCode(error))
      }
      return leave(
        t('tenantProfile.ownership.delete.saved', { name: displayName }),
      )
    })
  }

  return (
    <SectionCard
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
        <Button
          type="button"
          variant="outlined"
          onClick={openTransfer}
          disabled={feedback.pending}
        >
          {t('tenantProfile.ownership.transfer.button')}
        </Button>
        <Button
          type="button"
          variant="outlined"
          color="error"
          onClick={openDelete}
          disabled={feedback.pending}
        >
          {t('tenantProfile.ownership.delete.button')}
        </Button>
      </Box>
      {feedbackRegion}
      {transferOpen && (
        <TransferOwnershipDialog
          tenant={tenant}
          organizationName={displayName}
          ownerAccount={profile.ownerAccount}
          busy={feedback.pending}
          onCancel={() => setTransferOpen(false)}
          onConfirm={(account, label) => {
            setTransferOpen(false)
            runTransfer(account, label)
          }}
        />
      )}
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
 * The transfer dialog: search for one active member, choose one and confirm
 * twice. Mounted only while open, so the member read is issued when the
 * operator asks for it and not on every profile view. The search is a
 * prefix over the members' display names and login identifiers; the list
 * holds the first `MEMBER_SEARCH_LIMIT` matches, which the operator narrows
 * with the search field.
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
        disabled={busy}
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
        disabled={loading || busy}
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
