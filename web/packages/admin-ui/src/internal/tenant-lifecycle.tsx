/**
 * The organization lifecycle controls: request an ownership transfer or delete the
 * organization, both judged with `tenant.manage`, with local 403/404/409 text.
 */

import { useEffect, useId, useMemo, useRef, useState } from 'react'
import Alert from '@mui/material/Alert'
import Autocomplete from '@mui/material/Autocomplete'
import Box from '@mui/material/Box'
import CircularProgress from '@mui/material/CircularProgress'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogContentText from '@mui/material/DialogContentText'
import DialogTitle from '@mui/material/DialogTitle'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { usePermission } from '@speed/auth-core'
import {
  ActionButton,
  ConfirmDialog,
  errorCodeOf,
  httpStatusOf,
  useFormatInstant,
  useInertBackground,
} from '@speed/ui-kit'
import { AccountSelector } from './account-selector.js'
import { useAdminUiErrorText } from './error-text.js'
import { accountUsername, identityLabel } from './account-identity.js'
import { useAdminUiTranslation } from './translation.js'
import { WriteFeedbackRegion } from './write-feedback.js'
import { removedControlHeldFocus } from './section-heading-focus.js'
import { useAdminOperations } from '../operations.js'
import { displayNameOf } from '../TenantProfileFields.js'
import type {
  AdminAccount,
  AdminMember,
  AdminTenant,
} from '../operations.js'

type Translate = ReturnType<typeof useAdminUiTranslation>['t']

const MEMBER_SEARCH_LIMIT = 20

const MEMBER_SEARCH_DEBOUNCE_MS = 250

/** The platform permission that admits both lifecycle writes. */
const TENANT_MANAGE = 'tenant.manage'

const MEMBERSHIP_MANAGE = 'authn.membership.manage'

/** Whether the tenant's kind names an account's own personal organization. */
export function isPersonalTenant(kind: string): boolean {
  return kind === 'personal'
}

function memberOptionLabel(
  t: Translate,
  member: AdminMember,
): string {
  return (
    identityLabel(member.display_name, member.username) ??
    t('common.accountUnavailable')
  )
}

function accountOptionLabel(
  t: Translate,
  account: AdminAccount,
): string {
  return (
    identityLabel(
      account.display_name,
      accountUsername(account.identifiers),
    ) ?? t('common.accountUnavailable')
  )
}

/**
 * The transfer's failure text: a 404 is a gone organization and a 409 the
 * membership/state refusal; every other answer resolves through the whitelist.
 */
function transferFailureText(
  error: unknown,
  t: Translate,
  resolve: (code: string) => string,
): string {
  const status = httpStatusOf(error)
  if (status === 404) {
    return t('tenantLifecycle.transfer.failedNotFound')
  }
  if (status === 409) {
    return t('tenantLifecycle.transfer.failedConflict')
  }
  return resolve(errorCodeOf(error))
}

/**
 * The delete's failure text: a 409 is already a tombstone and a 404 means gone;
 * every other answer resolves through the whitelist.
 */
function deleteFailureText(
  error: unknown,
  t: Translate,
  resolve: (code: string) => string,
): string {
  const status = httpStatusOf(error)
  if (status === 404) {
    return t('tenantLifecycle.deleteConfirm.failedNotFound')
  }
  if (status === 409) {
    return t('tenantLifecycle.deleteConfirm.failedConflict')
  }
  return resolve(errorCodeOf(error))
}

/**
 * The member selector over the tenant's active members; it takes `readOnly`, not
 * disabled, while the dialog's write runs, so a focused field keeps its focus.
 */
function TenantMemberSelector({
  tenant,
  value,
  onChange,
  readOnly,
}: {
  readonly tenant: string
  readonly value: AdminMember | null
  readonly onChange: (member: AdminMember | null) => void
  readonly readOnly: boolean
}) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const [input, setInput] = useState('')
  const [query, setQuery] = useState('')

  useEffect(() => {
    const timer = setTimeout(() => {
      setQuery(input.trim())
    }, MEMBER_SEARCH_DEBOUNCE_MS)
    return () => clearTimeout(timer)
  }, [input])

  const params =
    query === ''
      ? { limit: MEMBER_SEARCH_LIMIT }
      : { query, limit: MEMBER_SEARCH_LIMIT }
  const members = operations.useMembers(tenant, params)
  const rows = useMemo(
    () => (members.data?.members ?? []).filter((member) => member.active),
    [members.data],
  )
  const truncated =
    members.data !== undefined && members.data.next_cursor !== ''
  // A retry begins as a fresh fetch with no data, so the failure stands until
  // a read succeeds; otherwise the retry button would unmount with it.
  const [readFailed, setReadFailed] = useState(false)
  useEffect(() => {
    if (members.isError) {
      setReadFailed(true)
    } else if (members.data !== undefined) {
      setReadFailed(false)
    }
  }, [members.isError, members.data])
  const failed = readFailed || members.isError

  return (
    <Box>
      {/* The member read never disables this field (a native disable would drop
          the typing focus); only the dialog's write locks it, via readOnly. */}
      <Autocomplete<AdminMember>
        size="small"
        value={value}
        readOnly={readOnly}
        onChange={(_event, next) => onChange(next)}
        inputValue={input}
        onInputChange={(_event, nextValue) => setInput(nextValue)}
        options={rows}
        loading={members.isLoading || members.isFetching}
        filterOptions={(options) => options}
        getOptionLabel={(member) => memberOptionLabel(t, member)}
        isOptionEqualToValue={(option, current) =>
          option.account === current.account
        }
        noOptionsText={
          failed
            ? t('tenantLifecycle.transfer.membersFailed')
            : t('tenantLifecycle.transfer.noMembers')
        }
        loadingText={t('tenantLifecycle.transfer.searching')}
        clearText={t('tenantLifecycle.transfer.clear')}
        openText={t('tenantLifecycle.transfer.open')}
        closeText={t('tenantLifecycle.transfer.close')}
        renderInput={(params) => (
          <TextField
            {...params}
            size="small"
            label={t('tenantLifecycle.transfer.memberLabel')}
            slotProps={{
              ...params.slotProps,
              input: {
                ...params.slotProps.input,
                readOnly,
                endAdornment: (
                  <>
                    {members.isFetching ? (
                      <CircularProgress
                        aria-label={t('tenantLifecycle.transfer.searching')}
                        color="inherit"
                        size={16}
                      />
                    ) : null}
                    {params.slotProps.input.endAdornment}
                  </>
                ),
              },
            }}
          />
        )}
        sx={{ minWidth: 0, width: '100%' }}
      />
      {truncated && !failed && (
        <Typography
          component="p"
          variant="caption"
          color="text.secondary"
          sx={{ display: 'block', mt: 0.5 }}
        >
          {t('tenantLifecycle.transfer.membersNarrow')}
        </Typography>
      )}
      {failed && (
        <Box sx={{ mt: 1 }}>
          <Typography
            component="p"
            variant="body2"
            color="error"
            sx={{ mb: 0.5 }}
          >
            {t('tenantLifecycle.transfer.membersFailed')}
          </Typography>
          <ActionButton
            type="button"
            size="small"
            busy={members.isFetching}
            unavailable={readOnly}
            onClick={() => void members.refetch()}
          >
            {t('tenantLifecycle.transfer.membersRetry')}
          </ActionButton>
        </Box>
      )}
    </Box>
  )
}

type LifecycleOutcome =
  | { readonly kind: 'success'; readonly message: string }
  | { readonly kind: 'failure'; readonly message: string }

export interface TenantLifecycleActionsProps {
  /** The organization the actions address. */
  readonly tenant: AdminTenant
  /**
   * Reports a landed write's confirmation to the page; omitted, the component
   * renders its own.
   */
  readonly onSuccess?: (message: string) => void
  /**
   * Reports a landed deletion to the host, which owns the focus landing since
   * the deletion withdraws this control group.
   */
  readonly onDeleted?: (name: string) => void
}

/**
 * The transfer-request dialog: target selection followed by the shared danger
 * confirmation; it stays open on a refusal and closes once the request lands.
 */
function TransferOwnerDialog({
  open,
  tenant,
  canReadMembers,
  onClose,
  onTransferred,
}: {
  readonly open: boolean
  readonly tenant: AdminTenant
  readonly canReadMembers: boolean
  readonly onClose: () => void
  readonly onTransferred: (message: string) => void
}) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const request = operations.useRequestTenantOwner()
  const resolveError = useAdminUiErrorText()
  const titleId = useId()
  const descriptionId = useId()
  const [member, setMember] = useState<AdminMember | null>(null)
  const [account, setAccount] = useState<AdminAccount | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [pending, setPending] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const dialogRef = useRef<HTMLDivElement>(null)
  useInertBackground(open, dialogRef)
  // The heading names the organization as its profile does; the request uses the name.
  const name = displayNameOf(tenant.display_name, tenant.name)

  // A close, tenant change or path change withdraws the draft and its result.
  useEffect(() => {
    setMember(null)
    setAccount(null)
    setConfirming(false)
    setFailure(null)
  }, [open, tenant.name, canReadMembers])

  const targetAccount =
    canReadMembers ? (member?.account ?? null) : (account?.id ?? null)
  const targetLabel =
    canReadMembers
      ? member === null
        ? ''
        : memberOptionLabel(t, member)
      : account === null
        ? ''
        : accountOptionLabel(t, account)

  const write = () => {
    if (targetAccount === null) {
      return
    }
    setConfirming(false)
    setFailure(null)
    setPending(true)
    void (async () => {
      try {
        await request.mutateAsync({
          name: tenant.name,
          account: targetAccount,
        })
        onTransferred(
          t('tenantLifecycle.transfer.success', {
            name: tenant.name,
            target: targetLabel,
          }),
        )
      } catch (error) {
        setFailure(transferFailureText(error, t, resolveError))
      } finally {
        setPending(false)
      }
    })()
  }

  return (
    <>
      <Dialog
        ref={dialogRef}
        open={open}
        onClose={() => {
          if (!pending) {
            onClose()
          }
        }}
        fullWidth
        maxWidth="sm"
        aria-labelledby={titleId}
        aria-describedby={descriptionId}
      >
        <DialogTitle id={titleId}>
          {t('tenantLifecycle.transfer.title', { name })}
        </DialogTitle>
        <DialogContent>
          <DialogContentText id={descriptionId}>
            {t('tenantLifecycle.transfer.description')}
          </DialogContentText>
          <Box sx={{ mt: 2 }}>
            {canReadMembers ? (
              <TenantMemberSelector
                tenant={tenant.name}
                value={member}
                onChange={setMember}
                readOnly={pending}
              />
            ) : (
              <>
                <AccountSelector
                  label={t('tenantLifecycle.transfer.accountLabel')}
                  value={account}
                  onChange={setAccount}
                  disabled={pending}
                  fullWidth
                />
                <Typography
                  component="p"
                  variant="caption"
                  color="text.secondary"
                  sx={{ display: 'block', mt: 1 }}
                >
                  {t('tenantLifecycle.transfer.accountHint')}
                </Typography>
              </>
            )}
          </Box>
          {failure !== null && (
            <Alert severity="error" sx={{ mt: 2 }}>
              {failure}
            </Alert>
          )}
        </DialogContent>
        <DialogActions>
          <ActionButton type="button" unavailable={pending} onClick={onClose}>
            {t('common.cancel')}
          </ActionButton>
          <ActionButton
            type="button"
            emphasis="primary"
            busy={pending}
            unavailable={targetAccount === null}
            unavailableReason={t('tenantLifecycle.transfer.targetRequired')}
            onClick={() => {
              setFailure(null)
              setConfirming(true)
            }}
          >
            {t('tenantLifecycle.transfer.submit')}
          </ActionButton>
        </DialogActions>
      </Dialog>

      <ConfirmDialog
        open={confirming}
        variant="danger"
        doubleConfirm
        title={t('tenantLifecycle.transfer.confirmTitle', { name })}
        message={t('tenantLifecycle.transfer.confirmMessage', {
          target: targetLabel,
        })}
        confirmLabel={t('tenantLifecycle.transfer.confirmLabel')}
        cancelLabel={t('common.cancel')}
        onConfirm={write}
        onCancel={() => setConfirming(false)}
      />
    </>
  )
}

/**
 * The delete dialog: type the organization's name, then confirm twice. The
 * typed value is local, so every opening starts empty.
 */
function DeleteTenantDialog({
  name,
  organizationName,
  busy,
  onCancel,
  onConfirm,
}: {
  readonly name: string
  readonly organizationName: string
  readonly busy: boolean
  readonly onCancel: () => void
  readonly onConfirm: () => void
}) {
  const { t } = useAdminUiTranslation()
  const [typed, setTyped] = useState('')
  return (
    <ConfirmDialog
      open
      variant="danger"
      doubleConfirm
      title={t('tenantLifecycle.deleteConfirm.title', { name: organizationName })}
      message={t('tenantLifecycle.deleteConfirm.message')}
      confirmLabel={t('tenantLifecycle.deleteConfirm.confirmLabel')}
      cancelLabel={t('common.cancel')}
      confirmLoading={busy}
      confirmDisabled={typed !== name}
      confirmDisabledReason={t('tenantLifecycle.deleteConfirm.nameMismatch')}
      onCancel={onCancel}
      onConfirm={onConfirm}
    >
      <TextField
        size="small"
        fullWidth
        label={t('tenantLifecycle.deleteConfirm.nameLabel')}
        value={typed}
        onChange={(event) => setTyped(event.target.value)}
        helperText={t('tenantLifecycle.deleteConfirm.nameHint', { name })}
        autoComplete="off"
        sx={{ mt: 2 }}
      />
    </ConfirmDialog>
  )
}

/**
 * The lifecycle control group: the two entries and their dialogs, each with its
 * own result area; the entries render only for a `tenant.manage` caller.
 */
export function TenantLifecycleActions({
  tenant,
  onSuccess,
  onDeleted,
}: TenantLifecycleActionsProps) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const formatTime = useFormatInstant()
  const canManage = usePermission('system', TENANT_MANAGE)
  const canReadMembers = usePermission('tenant', MEMBERSHIP_MANAGE)
  const remove = operations.useDeleteTenant()
  const cancel = operations.useCancelTenantOwner()
  const resolveError = useAdminUiErrorText()
  const [transferOpen, setTransferOpen] = useState(false)
  const [cancelOpen, setCancelOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [outcome, setOutcome] = useState<LifecycleOutcome | null>(null)
  const pendingTitleId = useId()
  // The group's container: the landing target when a cancelled request takes
  // its panel -- and the cancel control inside it -- out of the document.
  const groupRef = useRef<HTMLDivElement | null>(null)
  // Whether the last landed write was a cancel, so focus hands on exactly once.
  const cancelLanded = useRef(false)

  const deleted = tenant.status === 'deleted'
  const personal = isPersonalTenant(tenant.kind)
  const organizationName = displayNameOf(tenant.display_name, tenant.name)
  // The pending read exists only where the controls render.
  const pendingRead = operations.usePendingTenantOwner(
    tenant.name,
    canManage && !deleted && !personal,
  )
  const pending = pendingRead.data ?? null
  // A result describes one organization; a different tenant drops it.
  useEffect(() => {
    setOutcome(null)
  }, [tenant.name])

  // A settled cancel withdraws the pending panel and its cancel control; focus
  // lands on the group container that stays, unless the reader moved elsewhere.
  useEffect(() => {
    if (!cancelLanded.current) {
      return
    }
    if (pending !== null) {
      return
    }
    cancelLanded.current = false
    if (removedControlHeldFocus()) {
      groupRef.current?.focus()
    }
  }, [pending])

  if (!canManage || deleted || personal) {
    return null
  }

  const settleSuccess = (message: string) => {
    if (onSuccess === undefined) {
      setOutcome({ kind: 'success', message })
    } else {
      onSuccess(message)
    }
  }

  const requestDelete = () => {
    setOutcome(null)
    setDeleteOpen(true)
  }

  const runDelete = () => {
    setOutcome(null)
    void (async () => {
      try {
        await remove.mutateAsync({ name: tenant.name })
        onDeleted?.(tenant.name)
        settleSuccess(
          t('tenantLifecycle.deleteConfirm.success', {
            name: tenant.name,
          }),
        )
      } catch (error) {
        setOutcome({
          kind: 'failure',
          message: deleteFailureText(error, t, resolveError),
        })
      }
    })()
  }

  const runCancel = () => {
    if (pending === null) {
      return
    }
    setOutcome(null)
    void (async () => {
      try {
        await cancel.mutateAsync({ name: tenant.name, request: pending.id })
        cancelLanded.current = true
        settleSuccess(t('tenantLifecycle.transfer.pendingCancelled'))
      } catch (error) {
        setOutcome({
          kind: 'failure',
          message: transferFailureText(error, t, resolveError),
        })
      }
    })()
  }

  const success =
    outcome?.kind === 'success'
      ? { kind: 'success' as const, message: outcome.message }
      : null

  const pendingTarget =
    pending === null
      ? ''
      : (identityLabel(pending.target_display_name, pending.target_username) ??
        t('common.accountUnavailable'))

  return (
    <Box
      ref={groupRef}
      role="group"
      aria-label={t('tenantLifecycle.sectionTitle')}
      tabIndex={-1}
    >
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'flex-start',
          columnGap: 1,
          rowGap: 1,
        }}
      >
        <ActionButton
          type="button"
          size="small"
          aria-label={t('tenantLifecycle.transferAria', { name: tenant.name })}
          unavailable={remove.isPending || cancel.isPending || pending !== null}
          unavailableDescribedBy={
            pending !== null ? pendingTitleId : undefined
          }
          onClick={() => {
            setOutcome(null)
            setTransferOpen(true)
          }}
        >
          {t('tenantLifecycle.transferLabel')}
        </ActionButton>
        <ActionButton
          type="button"
          size="small"
          tone="danger"
          aria-label={t('tenantLifecycle.deleteAria', { name: tenant.name })}
          data-tenant-delete={tenant.name}
          busy={remove.isPending}
          unavailable={cancel.isPending}
          onClick={requestDelete}
        >
          {t('tenantLifecycle.deleteLabel')}
        </ActionButton>
      </Box>
      {pending !== null ? (
        <Box
          component="section"
          aria-label={t('tenantLifecycle.transfer.pendingTitle')}
          sx={{
            mt: 1.5,
            p: 2,
            border: 1,
            borderColor: 'divider',
            borderRadius: 1,
          }}
        >
          <Typography
            id={pendingTitleId}
            variant="subtitle1"
            component="h3"
          >
            {t('tenantLifecycle.transfer.pendingTitle')}
          </Typography>
          <Box sx={{ display: 'flex', flexWrap: 'wrap', columnGap: 3, rowGap: 0.5, mt: 0.5 }}>
            <Typography variant="body2">
              {t('tenantLifecycle.transfer.pendingTarget', {
                target: pendingTarget,
              })}
            </Typography>
            <Typography variant="body2">
              {t('tenantLifecycle.transfer.pendingExpires', {
                expires: formatTime(pending.expires_at),
              })}
            </Typography>
          </Box>
          <ActionButton
            type="button"
            size="small"
            sx={{ mt: 1 }}
            busy={cancel.isPending}
            unavailable={remove.isPending}
            onClick={() => {
              setOutcome(null)
              setCancelOpen(true)
            }}
          >
            {t('tenantLifecycle.transfer.pendingCancel')}
          </ActionButton>
        </Box>
      ) : null}

      {/* The component's own result area, used only when the page did not
          take the success over; a refusal always stands here. */}
      {onSuccess === undefined && <WriteFeedbackRegion outcome={success} />}
      {outcome?.kind === 'failure' && (
        <Alert severity="error" sx={{ mt: 1 }}>
          {outcome.message}
        </Alert>
      )}

      <TransferOwnerDialog
        open={transferOpen}
        tenant={tenant}
        canReadMembers={canReadMembers}
        onClose={() => setTransferOpen(false)}
        onTransferred={(message) => {
          setTransferOpen(false)
          settleSuccess(message)
        }}
      />
      <ConfirmDialog
        open={cancelOpen}
        title={t('tenantLifecycle.transfer.pendingCancelTitle')}
        message={t('tenantLifecycle.transfer.pendingCancelMessage')}
        confirmLabel={t('tenantLifecycle.transfer.pendingCancel')}
        cancelLabel={t('common.cancel')}
        confirmLoading={cancel.isPending}
        onCancel={() => setCancelOpen(false)}
        onConfirm={() => {
          setCancelOpen(false)
          runCancel()
        }}
      />
      {deleteOpen && (
        <DeleteTenantDialog
          name={tenant.name}
          organizationName={organizationName}
          busy={remove.isPending}
          onCancel={() => setDeleteOpen(false)}
          onConfirm={() => {
            setDeleteOpen(false)
            runDelete()
          }}
        />
      )}
    </Box>
  )
}
