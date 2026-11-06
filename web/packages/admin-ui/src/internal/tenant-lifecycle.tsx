/**
 * The organization lifecycle controls the platform surface adds to the
 * tenant roster and the tenant detail: hand the organization to another
 * account, or write its tombstone.
 *
 * Both entries are platform operations over the roster
 * (`PUT /api/v1/system/tenants/{tenant}/owner`,
 * `DELETE /api/v1/system/tenants/{tenant}`), judged in the platform domain with
 * `tenant.manage`. The server is the authority; the entries are a UX
 * affordance for a control group that can only ever fail otherwise, so
 * they render only for a caller whose attached platform permission set
 * holds `tenant.manage`. An unknown or absent set fails closed. A personal
 * organization (`kind === 'personal'`) is the account's own domain and
 * carries no transfer or delete entry, and a tombstone is terminal, so
 * neither does a deleted row.
 *
 * **Transfer target: two paths, chosen by what the caller may read.** The
 * endpoint itself validates only that the target account is an active
 * member of the organization and that its account is active; it answers
 * 409 for anything else. The dialog therefore offers:
 *
 *  - the organization's member selector when the caller's tenant
 *    permission set holds `authn.membership.manage`, because that is the
 *    permission the member read (`GET /api/v1/tenants/:tenant/members`)
 *    requires; it lists only active members, so a suspended or unknown
 *    account is not offered; and
 *  - the platform account selector otherwise. That path names any account,
 *    and the server still refuses a non-member with 409. The dialog never
 *    claims an account is a member and never filters the server's check
 *    out of the request.
 *
 * Either path is followed by the shared danger confirmation with the
 * two-step guard: the transfer dialog gathers the target, the confirmation
 * names it, and only the confirming click writes.
 *
 * **The visible title names the organization; the controls and the write
 * name the alias.** A dialog heading is read by a person, so it says the
 * display name (falling back to the alias). A control's accessible name and
 * every request use the alias, the tenant's name on the request surface.
 *
 * **Delete is terminal.** It asks through the shared danger confirmation
 * with the two-step guard and a typed-alias guard: the confirming button
 * stays disabled until the operator types the organization's alias, and its
 * copy states the impact: memberships, bindings, roles and grants are
 * removed, and the alias can never be reused.
 *
 * **Failure text is the dialog's own.** Both the transfer endpoint and the
 * delete endpoint answer 409 for several distinct refusals under the same
 * `urn:speed:tenant:*` problem type, so the generic resolver's
 * registration-oriented conflict leaf would misdescribe them. A 404, 403
 * and 409 are rendered as the lifecycle copy the operator can act on; any
 * other code goes through the admin-ui resolver. The transfer's 409 copy
 * names the membership requirement inline; the delete's 409 copy says the
 * organization is already a tombstone.
 */

import { useEffect, useId, useMemo, useState } from 'react'
import Alert from '@mui/material/Alert'
import Autocomplete from '@mui/material/Autocomplete'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogContentText from '@mui/material/DialogContentText'
import DialogTitle from '@mui/material/DialogTitle'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { usePermission } from '@speed/auth-core'
import { ConfirmDialog, errorCodeOf, httpStatusOf } from '@speed/ui-kit'
import { AccountSelector } from './account-selector.js'
import { useAdminUiErrorText } from './error-text.js'
import { useAdminUiTranslation } from './translation.js'
import { WriteFeedbackRegion } from './write-feedback.js'
import { useAdminOperations } from '../operations.js'
import { displayNameOf } from '../TenantProfileFields.js'
import type {
  AdminAccount,
  AdminMember,
  AdminTenant,
} from '../operations.js'

/** The admin-ui namespace's translate function, as the helpers below take it. */
type Translate = ReturnType<typeof useAdminUiTranslation>['t']

/** How many rows one member search asks the page for. */
const MEMBER_SEARCH_LIMIT = 20

/** The pause between the last keystroke and the member read it triggers. */
const MEMBER_SEARCH_DEBOUNCE_MS = 250

/** The platform permission that admits both lifecycle writes. */
const TENANT_MANAGE = 'tenant.manage'

/** The tenant permission the member list read requires. */
const MEMBERSHIP_MANAGE = 'authn.membership.manage'

/** Whether the tenant's kind names an account's own personal organization. */
export function isPersonalTenant(kind: string): boolean {
  return kind === 'personal'
}

/** The option text: the display name beside the account, or the account alone. */
function memberOptionLabel(member: AdminMember): string {
  return member.display_name === ''
    ? member.account
    : `${member.display_name} (${member.account})`
}

/** The option text of a platform account. */
function accountOptionLabel(account: AdminAccount): string {
  return account.display_name === ''
    ? account.id
    : `${account.display_name} (${account.id})`
}

/**
 * The transfer's failure text. A 404 means the organization is gone, a 409
 * is the endpoint's membership/state refusal (the account is not an active
 * member, the account itself is inactive, or the organization is a
 * tombstone). Every other answer -- including a bare 403 and the
 * registration-verification gate's refusal -- resolves through the
 * console's whitelist.
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
 * The delete's failure text. A 409 is terminal (the organization is
 * already a tombstone); a 404 means it no longer exists; every
 * other answer resolves through the console's whitelist, where
 * `tenant.protected` and a bare 403 both read as actionable refusals.
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
 * The organization's member selector: an autocomplete over the tenant's
 * active members, searched by display name or login identifier prefix.
 *
 * The read is the same tenant-scoped membership read the invitations and
 * members page uses; a suspended membership is not an eligible owner, so
 * only active rows become options. A failed read is stated with a retry
 * rather than shown as "no members", and a read that left a next page
 * unread asks the operator to narrow the text instead of presenting the
 * first page as the whole membership.
 */
function TenantMemberSelector({
  tenant,
  value,
  onChange,
  disabled,
}: {
  readonly tenant: string
  readonly value: AdminMember | null
  readonly onChange: (member: AdminMember | null) => void
  readonly disabled: boolean
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
  const failed = members.isError

  return (
    <Box>
      <Autocomplete<AdminMember>
        size="small"
        value={value}
        disabled={disabled}
        onChange={(_event, next) => onChange(next)}
        inputValue={input}
        onInputChange={(_event, nextValue) => setInput(nextValue)}
        options={rows}
        loading={members.isLoading || members.isFetching}
        filterOptions={(options) => options}
        getOptionLabel={memberOptionLabel}
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
          <Button
            type="button"
            size="small"
            variant="outlined"
            disabled={disabled}
            onClick={() => void members.refetch()}
          >
            {t('tenantLifecycle.transfer.membersRetry')}
          </Button>
        </Box>
      )}
    </Box>
  )
}

/** One lifecycle result the control group reports under its own buttons. */
type LifecycleOutcome =
  | { readonly kind: 'success'; readonly message: string }
  | { readonly kind: 'failure'; readonly message: string }

export interface TenantLifecycleActionsProps {
  /** The organization the actions address. */
  readonly tenant: AdminTenant
  /**
   * Reports a landed write's confirmation to the page. Passed, the page
   * owns the success region (its row may leave the table after a delete);
   * omitted, the component renders its own.
   */
  readonly onSuccess?: (message: string) => void
}

/**
 * The transfer dialog: target selection followed by the shared danger
 * confirmation. It stays open on a refusal so the failure stands beside
 * the target that was chosen, and closes on a landed write.
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
  const transfer = operations.useTransferTenantOwner()
  const resolveError = useAdminUiErrorText()
  const titleId = useId()
  const descriptionId = useId()
  const [member, setMember] = useState<AdminMember | null>(null)
  const [account, setAccount] = useState<AdminAccount | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [pending, setPending] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  // The dialog's heading names the organization the way its profile does;
  // the request and its controls name the alias.
  const name = displayNameOf(tenant.displayName, tenant.alias)

  // The dialog belongs to one tenant and one target path. A close, a
  // tenant change or a path change withdraws the draft and its result.
  useEffect(() => {
    setMember(null)
    setAccount(null)
    setConfirming(false)
    setFailure(null)
  }, [open, tenant.alias, canReadMembers])

  const targetAccount =
    canReadMembers ? (member?.account ?? null) : (account?.id ?? null)
  const targetLabel =
    canReadMembers
      ? member === null
        ? ''
        : memberOptionLabel(member)
      : account === null
        ? ''
        : accountOptionLabel(account)

  const write = () => {
    if (targetAccount === null) {
      return
    }
    setConfirming(false)
    setFailure(null)
    setPending(true)
    void (async () => {
      try {
        await transfer.mutateAsync({
          alias: tenant.alias,
          account: targetAccount,
        })
        onTransferred(
          t('tenantLifecycle.transfer.success', {
            alias: tenant.alias,
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
                tenant={tenant.alias}
                value={member}
                onChange={setMember}
                disabled={pending}
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
          <Button type="button" onClick={onClose} disabled={pending}>
            {t('common.cancel')}
          </Button>
          <Button
            type="button"
            variant="contained"
            disabled={targetAccount === null || pending}
            loading={pending}
            onClick={() => {
              setFailure(null)
              setConfirming(true)
            }}
          >
            {t('tenantLifecycle.transfer.submit')}
          </Button>
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
 * The delete dialog: type the organization's alias, then confirm twice.
 * Deletion is terminal and has no restore route, so the exact alias and the
 * double confirmation are both required. The typed value is local to the
 * dialog, which the page mounts only while it is open, so every opening
 * starts from an empty field. The visible title names the organization by
 * its display name; the hint and the request name the alias.
 */
function DeleteTenantDialog({
  alias,
  name,
  busy,
  onCancel,
  onConfirm,
}: {
  readonly alias: string
  readonly name: string
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
      title={t('tenantLifecycle.deleteConfirm.title', { name })}
      message={t('tenantLifecycle.deleteConfirm.message')}
      confirmLabel={t('tenantLifecycle.deleteConfirm.confirmLabel')}
      cancelLabel={t('common.cancel')}
      confirmLoading={busy}
      confirmDisabled={typed !== alias}
      onCancel={onCancel}
      onConfirm={onConfirm}
    >
      <TextField
        size="small"
        fullWidth
        label={t('tenantLifecycle.deleteConfirm.aliasLabel')}
        value={typed}
        onChange={(event) => setTyped(event.target.value)}
        helperText={t('tenantLifecycle.deleteConfirm.aliasHint', { alias })}
        autoComplete="off"
        sx={{ mt: 2 }}
      />
    </ConfirmDialog>
  )
}

/**
 * The lifecycle control group: two entries, the transfer dialog and the
 * typed delete confirmation, each with its own result area.
 *
 * The entries render only for a caller whose platform permission set holds
 * `tenant.manage`, on a live (non-tombstone) organization that is not a
 * personal domain. That is a visibility affordance, not the authorization:
 * both writes are still judged by the server.
 */
export function TenantLifecycleActions({
  tenant,
  onSuccess,
}: TenantLifecycleActionsProps) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const canManage = usePermission('system', TENANT_MANAGE)
  const canReadMembers = usePermission('tenant', MEMBERSHIP_MANAGE)
  const remove = operations.useDeleteTenant()
  const resolveError = useAdminUiErrorText()
  const [transferOpen, setTransferOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [outcome, setOutcome] = useState<LifecycleOutcome | null>(null)

  const deleted = tenant.status === 'deleted'
  const personal = isPersonalTenant(tenant.kind)
  const name = displayNameOf(tenant.displayName, tenant.alias)
  // A result describes the organization as it stood when the write landed;
  // a different tenant in the same slot drops it.
  useEffect(() => {
    setOutcome(null)
  }, [tenant.alias])

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
        await remove.mutateAsync({ alias: tenant.alias })
        settleSuccess(
          t('tenantLifecycle.deleteConfirm.success', {
            alias: tenant.alias,
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

  const success =
    outcome?.kind === 'success'
      ? { kind: 'success' as const, message: outcome.message }
      : null

  return (
    <Box>
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'flex-start',
          columnGap: 1,
          rowGap: 1,
        }}
      >
        <Button
          type="button"
          size="small"
          variant="outlined"
          aria-label={t('tenantLifecycle.transferAria', { alias: tenant.alias })}
          disabled={remove.isPending}
          onClick={() => {
            setOutcome(null)
            setTransferOpen(true)
          }}
        >
          {t('tenantLifecycle.transferLabel')}
        </Button>
        <Button
          type="button"
          size="small"
          variant="outlined"
          color="error"
          aria-label={t('tenantLifecycle.deleteAria', { alias: tenant.alias })}
          disabled={remove.isPending}
          loading={remove.isPending}
          onClick={requestDelete}
        >
          {t('tenantLifecycle.deleteLabel')}
        </Button>
      </Box>

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
      {deleteOpen && (
        <DeleteTenantDialog
          alias={tenant.alias}
          name={name}
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
