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
 * The transfer dialog lists the organization's active members (the server
 * grants the target tenant-admin when it does not hold it) and asks for the
 * deliberate second confirmation. The delete dialog requires the operator to
 * type the organization's alias and also asks for the second confirmation;
 * deletion is a tombstone with no restore route, so both guards are the heavy
 * ones.
 *
 * A refusal is resolved locally rather than through the console's server-code
 * whitelist: the tenant admin answers every 409 with one conflict type, so
 * the page re-reads the profile to tell an already-deleted organization from
 * an ownership change that beat this attempt. Each case gets its own
 * sentence, and nothing outside that closed set renders a raw code.
 */

import { useState } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import MenuItem from '@mui/material/MenuItem'
import TextField from '@mui/material/TextField'
import { useAuthState } from '@speed/auth-core'
import { RESERVED_ALIAS_PREFIX } from '@speed/org-create-ui'
import {
  ConfirmDialog,
  httpStatusOf,
  SectionCard,
  useWriteFeedback,
  WriteFeedbackRegion,
} from '@speed/ui-kit'
import { useAdminUiTranslation } from './internal/translation.js'
import { useOrganizationAdminOperations } from './organization-operations.js'
import type { AdminTenantProfile } from './organization-operations.js'
import type { AdminMember } from './operations.js'

/**
 * The local failure codes this surface resolves. They are UI facts, not
 * server codes: the tenant admin answers every conflict with one type, and
 * only the profile re-read can say which conflict it was.
 */
const OWNERSHIP_FORBIDDEN = 'ownership.forbidden'
const OWNERSHIP_NOT_FOUND = 'ownership.not_found'
const OWNERSHIP_OWNER_CHANGED = 'ownership.owner_changed'
const OWNERSHIP_DELETED = 'ownership.deleted'
const OWNERSHIP_CONFLICT = 'ownership.conflict'
const OWNERSHIP_UNKNOWN = 'ownership.unknown'

/** The failure code a synthetic error carries into the feedback scope. */
function ownershipFailure(code: string): Error & { readonly code: string } {
  return Object.assign(new Error(code), { code })
}

/**
 * The readable case behind a refusal. A 403, a 404 and a non-conflict status
 * are read from the answer itself; every 409 is one server type, so the
 * profile is re-read: a tombstone is the deleted case, a different owner is
 * the ownership-changed case, and anything else is a general conflict (for
 * example a target that stopped being an active member between the dialog's
 * read and the write).
 */
async function ownershipFailureCode(
  error: unknown,
  ownerAccount: string,
  reload: () => Promise<AdminTenantProfile | undefined>,
): Promise<string> {
  const status = httpStatusOf(error)
  if (status === 403) {
    return OWNERSHIP_FORBIDDEN
  }
  if (status === 404) {
    return OWNERSHIP_NOT_FOUND
  }
  if (status !== 409) {
    return OWNERSHIP_UNKNOWN
  }
  let fresh: AdminTenantProfile | undefined
  try {
    fresh = await reload()
  } catch (reloadError) {
    // A caller who stopped being the owner is removed from the organization,
    // so the re-read of the profile is refused while the organization itself
    // still exists: that is the ownership-changed case. A missing row is the
    // not-found case; any other failure leaves the general conflict.
    const reloadStatus = httpStatusOf(reloadError)
    if (reloadStatus === 403) {
      return OWNERSHIP_OWNER_CHANGED
    }
    if (reloadStatus === 404) {
      return OWNERSHIP_NOT_FOUND
    }
    return OWNERSHIP_CONFLICT
  }
  if (fresh?.status === 'deleted') {
    return OWNERSHIP_DELETED
  }
  if (fresh !== undefined && fresh.ownerAccount !== ownerAccount) {
    return OWNERSHIP_OWNER_CHANGED
  }
  return OWNERSHIP_CONFLICT
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
  /** The organization's display name (or alias) for the dialog and notices. */
  readonly displayName: string
  /** The host's action after a transfer lands: reload the caller tenant list
   * the account menu and breadcrumb read. */
  readonly onTenantUpdated?: () => void | Promise<unknown>
  /** The host's action after a delete lands: leave the organization and
   * reload the caller tenant list. */
  readonly onOrganizationDeleted?: () => void | Promise<unknown>
  /** Re-reads the profile; used to classify a 409 refusal. */
  readonly reloadProfile: () => Promise<AdminTenantProfile | undefined>
}

/** The owner-only controls on the organization profile page. */
export function TenantOwnership({
  tenant,
  profile,
  displayName,
  onTenantUpdated,
  onOrganizationDeleted,
  reloadProfile,
}: TenantOwnershipProps): ReactElement | null {
  const { t } = useAdminUiTranslation()
  const operations = useOrganizationAdminOperations()
  const snapshot = useAuthState()
  const transfer = operations.useTenantOwnerTransfer()
  const remove = operations.useTenantOwnerDelete()
  const feedback = useWriteFeedback()
  const [transferOpen, setTransferOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)

  const currentAccount =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.account_id ?? '')
      : ''
  const personal = tenant.startsWith(RESERVED_ALIAS_PREFIX)

  const resolve = (code: string): string => {
    switch (code) {
      case OWNERSHIP_FORBIDDEN:
        return t('tenantProfile.ownership.error.forbidden')
      case OWNERSHIP_NOT_FOUND:
        return t('tenantProfile.ownership.error.notFound')
      case OWNERSHIP_OWNER_CHANGED:
        return t('tenantProfile.ownership.error.ownerChanged')
      case OWNERSHIP_DELETED:
        return t('tenantProfile.ownership.error.deleted')
      case OWNERSHIP_CONFLICT:
        return t('tenantProfile.ownership.error.conflict')
      default:
        return t('errors.unknown')
    }
  }

  // Only the current owner sees the controls; a personal organization and a
  // tombstone never carry them. The server decides every write regardless.
  // A refusal can leave the profile naming another owner (or a tombstone)
  // underneath this component; the message of the attempt that observed the
  // change stays visible even though the controls are gone.
  const controlsAllowed =
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

  const runTransfer = (account: string, label: string): void => {
    void feedback.run(async () => {
      try {
        await transfer.mutateAsync({ tenant, account })
      } catch (error) {
        throw ownershipFailure(
          await ownershipFailureCode(error, profile.ownerAccount, reloadProfile),
        )
      }
      await settleHost(onTenantUpdated)
      return t('tenantProfile.ownership.transfer.saved', {
        name: displayName,
        account: label,
      })
    })
  }

  const runDelete = (): void => {
    void feedback.run(async () => {
      try {
        await remove.mutateAsync({ tenant })
      } catch (error) {
        throw ownershipFailure(
          await ownershipFailureCode(error, profile.ownerAccount, reloadProfile),
        )
      }
      await settleHost(onOrganizationDeleted ?? onTenantUpdated)
      return t('tenantProfile.ownership.delete.saved', { name: displayName })
    })
  }

  return (
    <SectionCard
      title={t('tenantProfile.ownership.title')}
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
          alias={profile.alias}
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

/** The member the transfer names, as the select and the notice show it. */
function memberLabel(member: AdminMember): string {
  const name = member.display_name.trim()
  return name === '' || name === member.account
    ? member.account
    : `${name} (${member.account})`
}

/**
 * The transfer dialog: choose one active member and confirm twice. Mounted
 * only while open, so the member read is issued when the operator asks for it
 * and not on every profile view.
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
  const members = operations.useMembers(tenant, { limit: 1000 })
  const [target, setTarget] = useState('')

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
        ? t('tenantProfile.ownership.transfer.noMembers')
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
          onConfirm(selected.account, memberLabel(selected))
        }
      }}
    >
      <TextField
        select
        size="small"
        fullWidth
        label={t('tenantProfile.ownership.transfer.targetLabel')}
        value={target}
        onChange={(event) => setTarget(event.target.value)}
        disabled={loading}
        helperText={helperText}
        sx={{ mt: 2 }}
      >
        {options.map((member) => (
          <MenuItem key={member.account} value={member.account}>
            {memberLabel(member)}
          </MenuItem>
        ))}
      </TextField>
    </ConfirmDialog>
  )
}

interface DeleteOrganizationDialogProps {
  readonly alias: string
  readonly organizationName: string
  readonly busy: boolean
  readonly onCancel: () => void
  readonly onConfirm: () => void
}

/**
 * The delete dialog: type the organization's alias, then confirm twice.
 * Deletion is terminal and has no restore route, so the alias typing and the
 * double confirmation are both required.
 */
function DeleteOrganizationDialog({
  alias,
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
      confirmDisabled={typed !== alias}
      onCancel={onCancel}
      onConfirm={onConfirm}
    >
      <TextField
        size="small"
        fullWidth
        label={t('tenantProfile.ownership.delete.aliasLabel')}
        value={typed}
        onChange={(event) => setTyped(event.target.value)}
        helperText={t('tenantProfile.ownership.delete.aliasHint', { alias })}
        autoComplete="off"
        sx={{ mt: 2 }}
      />
    </ConfirmDialog>
  )
}
