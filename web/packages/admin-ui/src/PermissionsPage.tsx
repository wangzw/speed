/**
 * PermissionsPage: the platform console's authorization-administration page.
 *
 * The authorization surface is tenant-scoped: the roles, their grants and
 * the account bindings all live under one tenant
 * (/api/v1/system/tenants/:tenant/...). The page therefore opens with a
 * tenant selector (fed by the platform roster) and keeps every read
 * disabled until a tenant is chosen; the tenant is an argument of the
 * request, not the session's current tenant.
 *
 * The tenant scope is optionally controlled: `tenant` locks the page to the
 * given value and hides the selector, `defaultTenant` seeds the page's own
 * selection ahead of the URL's `tenant` parameter, and `onTenantChange`
 * reports each re-selection made through the page's own selector.
 *
 * The page is the platform-staff surface only. It reads the platform
 * permission catalog, searches the platform account roster for the account
 * pickers, and resolves an account's identity through the platform account
 * read. The organization surface's roles page is a separate component: it
 * reads the tenant's own governed catalog and operations and never reaches
 * `/api/v1/system`.
 *
 * The page renders in five sections:
 *
 *  - the role catalog: list a tenant's roles, create or rewrite one, and
 *    remove one (removal revokes its grants and bindings with it);
 *  - the grants of the selected role: list them by the permission code the
 *    server reports, grant a permission from the catalog and revoke one;
 *  - the permission catalog: every permission the platform declares, for
 *    reference when granting;
 *  - the tenant's bindings: list them, bind a role to an account chosen
 *    through the account search picker and unbind one;
 *  - one account's held bindings, looked up by the account chosen through
 *    the same picker.
 *
 * Both binding tables render an account through the shared account identity
 * unit, keeping the recorded identifier as the column's full value; the
 * unbind control names the account by that same readable identity when the
 * account read answers one, and by the recorded identifier otherwise.
 *
 * A role row shows the role's description as the server reports it: the
 * operator's free text for a role this surface wrote, or a builtin role's
 * description rendered in the request's language. The role name is the key
 * a binding holds and is never renamed; the create form names a role (or
 * rewrites the description of the role that already carries the name). The
 * permission catalog and a grant row show the server-rendered description
 * as the primary text with the permission code beneath it, and a permission
 * the server marks dangerous carries a visible high-risk badge. The catalog is
 * grouped by the `group` the declaring modules report, folded into the shared
 * authorization, membership, identity, settings and "other" headings; an entry
 * the caller cannot grant is still listed and marked, while the grant picker
 * offers only the entries the caller may grant. An empty description leaves
 * the code as the only text. A grant the server cannot translate to a
 * permission code keeps its recorded resource/action spelling and cannot be
 * revoked. Granting a dangerous permission is confirmed through the same
 * two-step dialog the organization page uses.
 *
 * Every read and write is an operation on the console operations bundle,
 * so the page builds no client and writes no query key. A failed list
 * settles into a retryable placeholder, and a refused read into the
 * no-permission placeholder.
 *
 * Each control group reports its writes through its own feedback scope:
 * the role catalog (create and delete), the selected role's grants (grant
 * and revoke), the tenant's bindings (bind and unbind), and the
 * account-binding lookup (the unbind actions of its own table, which do
 * not borrow the bindings scope). A failure marks the affected row inline
 * with the same resolved text its region shows and brings that region into
 * view; a success names the changed role, permission or account and never
 * moves the page. The tenant seeds every scope, so changing it resets them
 * all and disables the tenant selector while any is in flight; the
 * selected role resets the grants scope, and the chosen account resets the
 * binding scope. The role deletion is confirmed through the shared
 * pending-confirmation dialog: it closes on the confirming click, and the
 * write's outcome lands in the role scope once the modal no longer hides
 * it. The page's refresh action re-reads every enabled read describing the
 * body, resets every scope and row marker first, and -- when a successful
 * roles read no longer lists the selected role -- clears the selection and
 * announces it in a standing status region. The page opens with ui-kit's
 * PageHeader.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Ref } from 'react'
import Box from '@mui/material/Box'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  ActionButton,
  DataTable,
  EmptyState,
  ListSkeleton,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  primaryColumnMaxWidthSx,
  useWriteFeedback,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import {
  AccountIdentity,
  accountIdentityValue,
  accountLabel,
  accountNotice,
  useAccountIdentity,
  type AccountIdentityValue,
} from './internal/account-identity.js'
import { AccountSelector } from './internal/account-selector.js'
import { AdminAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { useAdminUiErrorText } from './internal/error-text.js'
import { RefreshButton } from '@speed/ui-kit'
import {
  PERMISSION_GROUPS,
  permissionGroupKey,
  permissionGroupOf,
} from './internal/roles/PermissionName.js'
import { TenantScope, tenantFromLocation } from './internal/tenant-scope.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  pageRefreshRead,
  usePageRefresh,
} from '@speed/ui-kit'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useAdminOperations } from './operations.js'
import { AdminPageHeader } from './internal/page-header.js'
import {
  focusAfterRowRemoval,
  useRowRemovalFocus,
} from './internal/focus-after-removal.js'
import { useSectionHeadingFocus } from './internal/section-heading-focus.js'
import type {
  AdminAccount,
  AdminBinding,
  AdminGrant,
  AdminOperations,
  AdminPermission,
  AdminRole,
} from './operations.js'

/**
 * The id of the page's own "choose a tenant first" text. The disabled
 * refresh is described by it, so the explanation has one copy on the page.
 */
const TENANT_HINT_ID = 'permissions-tenant-hint'

/**
 * One permission's server-rendered description as its primary text with the
 * permission code beneath it, plus the high-risk badge when the server marks
 * the permission dangerous. The description arrives already rendered in the
 * request's language and is shown as received; an empty one leaves the code
 * as the only line. A grant the server cannot translate passes no catalog
 * entry, so its recorded resource/action spelling stands alone.
 *
 * The primary line alone is capped when the caller is a table cell: prose
 * stacks beneath it, and the cap must not squeeze that second line. The
 * grant picker's menu is not a table cell and keeps the full width.
 */
function PermissionLabel({
  entry,
  code,
  capIdentity = false,
}: {
  /** The catalog entry naming this permission, when the catalog has one. */
  readonly entry: AdminPermission | undefined
  /** The spelling this surface shows when no description resolves. */
  readonly code: string
  readonly capIdentity?: boolean
}) {
  const { t } = useAdminUiTranslation()
  const description = entry?.description ?? ''
  const primary = description === '' ? code : description
  const dangerous = entry?.dangerous === true
  return (
    <Box>
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          flexWrap: 'wrap',
          columnGap: 1,
          rowGap: 0.5,
        }}
      >
        <Typography
          variant="body2"
          noWrap={description === ''}
          sx={{
            ...(capIdentity ? primaryColumnMaxWidthSx : undefined),
            ...(description === ''
              ? { fontFamily: (theme) => theme.typography.fontFamilyMono }
              : undefined),
          }}
        >
          {primary}
        </Typography>
        {dangerous && (
          <Typography
            component="span"
            variant="caption"
            sx={(theme) => ({
              border: `1px solid ${theme.palette.warning.main}`,
              borderRadius: 0.5,
              color: theme.palette.warning.main,
              px: 0.5,
              whiteSpace: 'nowrap',
            })}
          >
            {t('permissions.catalog.dangerous')}
          </Typography>
        )}
      </Box>
      {description !== '' && (
        <Typography
          variant="caption"
          color="text.secondary"
          sx={(theme) => ({
            display: 'block',
            whiteSpace: 'normal',
            fontFamily: theme.typography.fontFamilyMono,
          })}
        >
          {code}
        </Typography>
      )}
    </Box>
  )
}

/**
 * The catalog's entries folded into the fixed permission-group order the
 * shared vocabulary defines. A permission the declaring modules left ungrouped
 * falls into "other" rather than disappearing from the catalog.
 */
function groupCatalog(entries: readonly AdminPermission[]) {
  return PERMISSION_GROUPS.flatMap((group) => {
    const grouped = entries.filter(
      (entry) => permissionGroupOf(entry.group) === group,
    )
    return grouped.length === 0 ? [] : [{ group, entries: grouped }]
  })
}

/**
 * The unbind control of one binding row. Its accessible name names the
 * account by the readable identity the caller resolved, and by the recorded
 * identifier otherwise, so a reader hears the same account the row's cell
 * shows.
 */
function UnbindButton({
  binding,
  identity,
  busy,
  unavailable,
  invalid,
  describedBy,
  rowAttribute,
  controlRef,
  onClick,
}: {
  readonly binding: AdminBinding
  /** The account's resolved identity, or the neutral copy when none. */
  readonly identity: AccountIdentityValue
  /** Whether this row's unbind write is in flight. */
  readonly busy: boolean
  /** Whether another write in the scope keeps this control out of reach. */
  readonly unavailable: boolean
  /** Whether this row is the one the scope's failure names. */
  readonly invalid: boolean
  /** The id of the resolved failure text rendered in this row. */
  readonly describedBy?: string
  /** The attribute naming this row's control for the unload focus hand-off. */
  readonly rowAttribute: string
  /** This row's control ref, for the removal focus record. */
  readonly controlRef?: Ref<HTMLButtonElement>
  readonly onClick: (identity: AccountIdentityValue) => void
}) {
  const { t } = useAdminUiTranslation()
  return (
    <ActionButton
      ref={controlRef}
      type="button"
      size="small"
      tone="danger"
      aria-label={t('permissions.bindings.unbindAria', {
        account: accountLabel(t, identity),
        role: binding.role,
      })}
      aria-invalid={invalid}
      aria-describedby={invalid ? describedBy : undefined}
      busy={busy}
      unavailable={unavailable}
      {...{ [rowAttribute]: bindingKey(binding) }}
      onClick={() => onClick(identity)}
    >
      {t('permissions.bindings.unbind')}
    </ActionButton>
  )
}

/** The platform surface's unbind control: the account identity comes from
 * the platform account read. */
function PlatformUnbindButton(
  props: Omit<Parameters<typeof UnbindButton>[0], 'identity'>,
) {
  const { name, username } = useAccountIdentity(props.binding.account)
  return <UnbindButton {...props} identity={{ name, username }} />
}

/** The key a binding row carries through the page's failure markers. */
function bindingKey(binding: AdminBinding): string {
  return `${binding.account}:${binding.role}`
}

/** The id of one binding row's inline failure text within a scope. */
function bindingErrorId(scope: string, binding: AdminBinding): string {
  return `permissions-${scope}-error-${encodeURIComponent(bindingKey(binding))}`
}

/** The role-scope control a write belongs to: the create form or one row. */
type PendingRoleWrite =
  | { readonly kind: 'create' }
  | { readonly kind: 'remove'; readonly role: string }

/** The grants-scope control a write belongs to. */
type PendingGrantWrite =
  | { readonly kind: 'grant' }
  | { readonly kind: 'revoke'; readonly key: string }

/** The bindings-scope control a write belongs to. */
type PendingBindingWrite =
  | { readonly kind: 'bind' }
  | { readonly kind: 'unbind'; readonly key: string }

/** A row a landed write will remove, with the position that row held. */
interface RowRemoval {
  readonly value: string
  readonly index: number
}

export interface PermissionsPageProps {
  /**
   * A controlled tenant scope: when given, the page's reads and writes name
   * this tenant and the selector is not rendered. An empty string is a
   * controlled "no tenant".
   */
  readonly tenant?: string
  /**
   * The page's initial tenant when it is not controlled. Wins over the
   * tenant query parameter.
   */
  readonly defaultTenant?: string
  /** Reports each re-selection made through the page's own selector. */
  readonly onTenantChange?: (tenant: string) => void
}

/** The platform console's authorization-administration page. */
export function PermissionsPage({
  tenant,
  defaultTenant,
  onTenantChange,
}: PermissionsPageProps = {}) {
  const operations = useAdminOperations()
  return (
    <PermissionsPageBody
      operations={operations}
      tenant={tenant}
      defaultTenant={defaultTenant}
      onTenantChange={onTenantChange}
    />
  )
}

function PermissionsPageBody({
  operations,
  tenant: controlledTenant,
  defaultTenant,
  onTenantChange,
}: PermissionsPageProps & {
  readonly operations: AdminOperations
}) {
  const { t } = useAdminUiTranslation()

  const [innerTenant, setInnerTenant] = useState(
    () => defaultTenant ?? tenantFromLocation(),
  )
  const tenant = controlledTenant ?? innerTenant
  const tenantControlled = controlledTenant !== undefined
  const [selectedRole, setSelectedRole] = useState('')
  // A removal settles after the confirmation dialog has closed, and the
  // operator may have moved to another role in between. The settlement
  // judges the selection as it stands then, not the one the click captured
  // in its closure.
  const selectedRoleRef = useRef(selectedRole)
  useEffect(() => {
    selectedRoleRef.current = selectedRole
  }, [selectedRole])

  // Role create/rewrite draft. There is no builtin toggle: the mark is the
  // host's to set when it seeds the role, and a role write carrying it is
  // refused — see the create call below.
  const [roleName, setRoleName] = useState('')
  const [roleDescription, setRoleDescription] = useState('')

  // Grant draft (the permission to grant to the selected role).
  const [grantPermission, setGrantPermission] = useState('')

  // Binding draft.
  const [bindAccount, setBindAccount] = useState<AdminAccount | null>(null)
  const [bindRoleName, setBindRoleName] = useState('')

  // The account whose held bindings are looked up.
  const [lookupAccount, setLookupAccount] = useState<AdminAccount | null>(null)
  const [lookupApplied, setLookupApplied] = useState('')

  // The destructive action the shared confirmation dialog guards. The
  // dialog closes on the confirming click; the write's outcome lands in
  // the role scope after it closes.
  const confirm = usePendingConfirm()

  // One scope per control group: the role catalog, the selected role's
  // grants, the tenant's bindings, and the account-binding lookup.
  const roleFeedback = useWriteFeedback()
  const grantsFeedback = useWriteFeedback()
  const bindingsFeedback = useWriteFeedback()
  const lookupFeedback = useWriteFeedback()
  const roleRegionRef = useFailureScroll(roleFeedback.outcome)
  const grantsRegionRef = useFailureScroll(grantsFeedback.outcome)
  const bindingsRegionRef = useFailureScroll(bindingsFeedback.outcome)
  const lookupRegionRef = useFailureScroll(lookupFeedback.outcome)
  const rolesSection = useSectionHeadingFocus()
  const grantsSection = useSectionHeadingFocus()
  const bindingsSection = useSectionHeadingFocus()
  const lookupSection = useSectionHeadingFocus()

  // The row each scope's failure names. The marker belongs to the row the
  // region reports, and clearing it before an attempt starts keeps it from
  // outliving the result it describes.
  const [failedRole, setFailedRole] = useState<string | null>(null)
  const [failedGrant, setFailedGrant] = useState<string | null>(null)
  const [failedBinding, setFailedBinding] = useState<string | null>(null)
  const [failedLookupBinding, setFailedLookupBinding] = useState<string | null>(
    null,
  )
  // The control each scope's in-flight write belongs to: only that control
  // reports busy, while its siblings in the same scope are unavailable.
  const [pendingRoleWrite, setPendingRoleWrite] =
    useState<PendingRoleWrite | null>(null)
  const [pendingGrantWrite, setPendingGrantWrite] =
    useState<PendingGrantWrite | null>(null)
  const [pendingBindingWrite, setPendingBindingWrite] =
    useState<PendingBindingWrite | null>(null)
  const [pendingLookupWrite, setPendingLookupWrite] = useState<string | null>(
    null,
  )
  // The row a landed write removed, recorded when the attempt starts so the
  // focus hand-off can name the row that moved up.
  const roleRemoval = useRef<RowRemoval | null>(null)
  const grantRemoval = useRef<RowRemoval | null>(null)
  const bindingRemoval = useRef<RowRemoval | null>(null)
  const lookupRemoval = useRef<RowRemoval | null>(null)
  // Whether each scope's control held focus when its row left; only then
  // does that scope's landing move it.
  const roleFocus = useRowRemovalFocus()
  const grantFocus = useRowRemovalFocus()
  const bindingFocus = useRowRemovalFocus()
  const lookupFocus = useRowRemovalFocus()
  // Whether the last refresh's roles read dropped the selected role. The
  // page announces it in one standing status region; choosing a role,
  // changing the tenant or refreshing again clears it.
  const [roleGone, setRoleGone] = useState(false)

  // The one resolver the regions bind: a row's inline marker resolves the
  // same failure code through it, so the two can never carry different
  // copy.
  const resolveErrorText = useAdminUiErrorText()

  const roles = operations.useRoles(tenant)
  const catalog = operations.usePermissions()
  const grants = operations.useGrants(tenant, selectedRole || undefined)
  const bindings = operations.useBindings(tenant)
  const accountBindings = operations.useAccountBindings({
    tenant,
    account: lookupApplied === '' ? undefined : lookupApplied,
  })

  const writeRole = operations.useWriteRole()
  const removeRole = operations.useRemoveRole()
  const grantPermissionMutation = operations.useGrantPermission()
  const revokePermission = operations.useRevokePermission()
  const bindRole = operations.useBindRole()
  const unbindRole = operations.useUnbindRole()

  const roleFailureText = failureTextOf(roleFeedback.outcome, resolveErrorText)
  const grantsFailureText = failureTextOf(
    grantsFeedback.outcome,
    resolveErrorText,
  )
  const bindingsFailureText = failureTextOf(
    bindingsFeedback.outcome,
    resolveErrorText,
  )
  const lookupFailureText = failureTextOf(
    lookupFeedback.outcome,
    resolveErrorText,
  )
  const anyPending =
    roleFeedback.pending ||
    grantsFeedback.pending ||
    bindingsFeedback.pending ||
    lookupFeedback.pending

  const permissions = catalog.data ?? []
  // The catalog entry that names a permission, for the readable primary text
  // of a grant row and the dangerous mark. A grant the server cannot
  // translate has no code and no entry, so the row keeps its recorded pair.
  const permissionByCode = useMemo(
    () => new Map(permissions.map((entry) => [entry.permission, entry])),
    [permissions],
  )
  // The catalog groups the declaring modules name, folded into the fixed
  // order the shared permission vocabulary defines; a permission the modules
  // left ungrouped is folded into "other" rather than dropped.
  const catalogGroups = useMemo(
    () => groupCatalog(permissions),
    [permissions],
  )
  // The grant picker offers only permissions the caller may grant; the
  // catalog itself still lists every declared permission.
  const grantablePermissions = useMemo(
    () => permissions.filter((entry) => entry.grantable),
    [permissions],
  )

  // The one function the page-level context changes share -- the tenant
  // change and the refresh action: it clears every slot and every row
  // marker, so a cleared slot never leaves a control carrying
  // `aria-invalid` and a description that no longer renders.
  const resetPageFeedback = useCallback(() => {
    roleFeedback.reset()
    grantsFeedback.reset()
    bindingsFeedback.reset()
    lookupFeedback.reset()
    setFailedRole(null)
    setFailedGrant(null)
    setFailedBinding(null)
    setFailedLookupBinding(null)
    setRoleGone(false)
  }, [
    roleFeedback.reset,
    grantsFeedback.reset,
    bindingsFeedback.reset,
    lookupFeedback.reset,
  ])
  // A controlled tenant arrives as a prop, so the reset cannot wait for the
  // page's own selector: any change of the argument every write names
  // clears the slots and the row markers. The selection and the drafts name
  // accounts and a role from the tenant being left, so they are cleared
  // with them; otherwise a selection made under one tenant would stand
  // against the next one's reads.
  useEffect(() => {
    resetPageFeedback()
    setSelectedRole('')
    setBindAccount(null)
    setLookupAccount(null)
    setLookupApplied('')
  }, [tenant, resetPageFeedback])

  // The refresh action re-reads every read describing the page body that is
  // enabled at that moment: the roles, the permission catalog, the bindings,
  // the selected role's grants (only with a role chosen) and the applied
  // account's bindings (only with an account applied). The roster the
  // selector is addressed by is not a body read and stays out.
  const pageRefresh = usePageRefresh(
    [
      pageRefreshRead('roles', roles, {
        // A successful roles read that no longer lists the selected role
        // clears the selection and announces it; a failed read keeps the
        // selection, its stale marker tells the story.
        onSettled: (settled) => {
          if (settled.status !== 'success' || settled.data === undefined) {
            return
          }
          const selected = selectedRoleRef.current
          if (
            selected === '' ||
            settled.data.some((role) => role.role === selected)
          ) {
            return
          }
          setSelectedRole('')
          grantsFeedback.reset()
          setFailedGrant(null)
          setRoleGone(true)
        },
      }),
      pageRefreshRead('permissions', catalog, {
        enabled: tenant !== '' && catalog.isEnabled,
      }),
      pageRefreshRead('grants', grants),
      pageRefreshRead('bindings', bindings),
      pageRefreshRead('account-bindings', accountBindings),
    ],
    resetPageFeedback,
  )

  // The tenant is an argument of every write on the page, so a change
  // clears every slot and every row marker: the old result would read as
  // describing the new tenant.
  const changeTenant = (next: string) => {
    setInnerTenant(next)
    onTenantChange?.(next)
    resetPageFeedback()
  }

  // The selected role drives the grants scope: another role's result would
  // be read as this role's, so choosing one clears the slot.
  const selectRole = (role: string) => {
    setSelectedRole(role)
    setRoleGone(false)
    grantsFeedback.reset()
    setFailedGrant(null)
  }

  const changeBindAccount = (account: AdminAccount | null) => {
    setBindAccount(account)
    bindingsFeedback.reset()
    setFailedBinding(null)
  }

  const changeBindRole = (role: string) => {
    setBindRoleName(role)
    bindingsFeedback.reset()
    setFailedBinding(null)
  }

  // The lookup scope follows the applied account, not the draft: applying
  // another account resets it.
  const applyLookup = () => {
    lookupFeedback.reset()
    setFailedLookupBinding(null)
    setLookupApplied(lookupAccount?.id ?? '')
  }

  const saveRole = () => {
    const role = roleName
    setFailedRole(null)
    setPendingRoleWrite({ kind: 'create' })
    void roleFeedback
      .run(async () => {
        await writeRole.mutateAsync({
          tenant,
          role,
          description: roleDescription,
        })
        setRoleName('')
        setRoleDescription('')
        return t('permissions.roles.saved', { role })
      })
      .finally(() => setPendingRoleWrite(null))
  }

  const requestRemoveRole = (role: AdminRole) => {
    confirm.ask({
      title: t('permissions.roles.removeTitle'),
      message: t('permissions.roles.removeMessage', { role: role.role }),
      confirmLabel: t('permissions.roles.removeConfirm'),
      // Deleting a role revokes its grants and bindings with it and cannot
      // be undone from the interface: the heavier two-step arm.
      doubleConfirm: true,
      run: () => {
        setFailedRole(null)
        roleRemoval.current = {
          value: role.role,
          index: roleRows?.findIndex((row) => row.role === role.role) ?? -1,
        }
        setPendingRoleWrite({ kind: 'remove', role: role.role })
        void roleFeedback
          .run(async () => {
            try {
              await removeRole.mutateAsync({ tenant, role: role.role })
              if (selectedRoleRef.current === role.role) {
                setSelectedRole('')
                grantsFeedback.reset()
                setFailedGrant(null)
              }
              return t('permissions.roles.removed', { role: role.role })
            } catch (error) {
              roleRemoval.current = null
              setFailedRole(role.role)
              throw error
            }
          })
          .finally(() => setPendingRoleWrite(null))
      },
    })
  }

  const grant = () => {
    const permission = grantPermission
    if (permission === '') {
      return
    }
    const run = () => {
      // The grant and the revoke share the grants scope: a new attempt starts
      // from a clean result, so the refused row's marker and its inline reason
      // do not outlive the result they described.
      setFailedGrant(null)
      setPendingGrantWrite({ kind: 'grant' })
      void grantsFeedback
        .run(async () => {
          await grantPermissionMutation.mutateAsync({
            tenant,
            role: selectedRole,
            permission,
          })
          setGrantPermission('')
          return t('permissions.grants.granted', {
            permission,
            role: selectedRole,
          })
        })
        .finally(() => setPendingGrantWrite(null))
    }
    // A permission the catalog marks dangerous widens the grantee's power over
    // other accounts or the tenant's policy: the deliberate two-step
    // confirmation stands in front of it, as it does on the organization page.
    if (permissionByCode.get(permission)?.dangerous === true) {
      confirm.ask({
        title: t('permissions.grants.dangerousTitle'),
        message: t('permissions.grants.dangerousMessage', {
          permission,
          role: selectedRole,
        }),
        confirmLabel: t('permissions.grants.dangerousConfirm'),
        doubleConfirm: true,
        run,
      })
      return
    }
    run()
  }

  const revoke = (grant: AdminGrant, code: string) => {
    const key = `${grant.resource}:${grant.action}`
    setFailedGrant(null)
    grantRemoval.current = {
      value: key,
      index: grants.data?.findIndex(
        (entry) => `${entry.resource}:${entry.action}` === key,
      ) ?? -1,
    }
    setPendingGrantWrite({ kind: 'revoke', key })
    void grantsFeedback
      .run(async () => {
        try {
          await revokePermission.mutateAsync({
            tenant,
            role: selectedRole,
            permission: code,
          })
          return t('permissions.grants.revoked', {
            permission: code,
            role: selectedRole,
          })
        } catch (error) {
          grantRemoval.current = null
          setFailedGrant(key)
          throw error
        }
      })
      .finally(() => setPendingGrantWrite(null))
  }

  const bind = () => {
    if (bindAccount === null) {
      return
    }
    const account = bindAccount
    const role = bindRoleName
    setFailedBinding(null)
    setPendingBindingWrite({ kind: 'bind' })
    void bindingsFeedback
      .run(async () => {
        await bindRole.mutateAsync({ tenant, account: account.id, role })
        setBindAccount(null)
        setBindRoleName('')
        return accountNotice(
          t,
          {
            named: 'permissions.bindings.bound',
            unnamed: 'permissions.bindings.boundUnnamed',
          },
          accountIdentityValue(account),
          { role },
        )
      })
      .finally(() => setPendingBindingWrite(null))
  }

  // The bindings table and the lookup table keep separate scopes and row
  // markers: an unbind in the lookup answers the operator's lookup, not the
  // tenant-wide binding list, so its result lives under the lookup table.
  const unbind = (
    binding: AdminBinding,
    lookup: boolean,
    identity: AccountIdentityValue,
  ) => {
    const feedback = lookup ? lookupFeedback : bindingsFeedback
    const key = bindingKey(binding)
    const rows = lookup ? accountBindings.data : bindings.data
    const mark = (value: string | null) => {
      if (lookup) {
        setFailedLookupBinding(value)
      } else {
        setFailedBinding(value)
      }
    }
    mark(null)
    const removal = {
      value: key,
      index: rows?.findIndex((row) => bindingKey(row) === key) ?? -1,
    }
    if (lookup) {
      lookupRemoval.current = removal
      setPendingLookupWrite(key)
    } else {
      bindingRemoval.current = removal
      setPendingBindingWrite({ kind: 'unbind', key })
    }
    void feedback
      .run(async () => {
        try {
          await unbindRole.mutateAsync({
            tenant,
            account: binding.account,
            role: binding.role,
          })
          return accountNotice(
            t,
            {
              named: 'permissions.bindings.unbound',
              unnamed: 'permissions.bindings.unboundUnnamed',
            },
            identity,
            { role: binding.role },
          )
        } catch (error) {
          if (lookup) {
            lookupRemoval.current = null
          } else {
            bindingRemoval.current = null
          }
          mark(key)
          throw error
        }
      })
      .finally(() => {
        if (lookup) {
          setPendingLookupWrite(null)
        } else {
          setPendingBindingWrite(null)
        }
      })
  }

  const roleRows = roles.data

  // A landed write removes the row whose control started it; the same
  // control on the next row takes focus, else the previous row's, else the
  // section heading. A failed re-read never confirms the row left, so it
  // drops the record instead of letting a later read move focus.
  useEffect(() => {
    const removed = roleRemoval.current
    if (removed === null) {
      return
    }
    if (roles.error !== null) {
      roleRemoval.current = null
      return
    }
    if (roleRows === undefined) {
      return
    }
    if (roleRows.some((row) => row.role === removed.value)) {
      return
    }
    roleRemoval.current = null
    focusAfterRowRemoval(
      'data-role-remove',
      removed.value,
      removed.index,
      roleFocus.takeHeldFocus(removed.value),
      rolesSection.focusHeading,
    )
  }, [roleRows, roles.error, rolesSection.focusHeading, roleFocus.takeHeldFocus])

  useEffect(() => {
    const removed = grantRemoval.current
    if (removed === null) {
      return
    }
    if (grants.error !== null) {
      grantRemoval.current = null
      return
    }
    if (grants.data === undefined) {
      return
    }
    if (
      grants.data.some(
        (entry) => `${entry.resource}:${entry.action}` === removed.value,
      )
    ) {
      return
    }
    grantRemoval.current = null
    focusAfterRowRemoval(
      'data-grant-revoke',
      removed.value,
      removed.index,
      grantFocus.takeHeldFocus(removed.value),
      grantsSection.focusHeading,
    )
  }, [grants.data, grants.error, grantsSection.focusHeading, grantFocus.takeHeldFocus])

  useEffect(() => {
    const removed = bindingRemoval.current
    if (removed === null) {
      return
    }
    if (bindings.error !== null) {
      bindingRemoval.current = null
      return
    }
    if (bindings.data === undefined) {
      return
    }
    if (
      bindings.data.some((binding) => bindingKey(binding) === removed.value)
    ) {
      return
    }
    bindingRemoval.current = null
    focusAfterRowRemoval(
      'data-binding-unbind',
      removed.value,
      removed.index,
      bindingFocus.takeHeldFocus(removed.value),
      bindingsSection.focusHeading,
    )
  }, [bindings.data, bindings.error, bindingsSection.focusHeading, bindingFocus.takeHeldFocus])

  useEffect(() => {
    const removed = lookupRemoval.current
    if (removed === null) {
      return
    }
    if (accountBindings.error !== null) {
      lookupRemoval.current = null
      return
    }
    if (accountBindings.data === undefined) {
      return
    }
    if (
      accountBindings.data.some(
        (binding) => bindingKey(binding) === removed.value,
      )
    ) {
      return
    }
    lookupRemoval.current = null
    focusAfterRowRemoval(
      'data-lookup-unbind',
      removed.value,
      removed.index,
      lookupFocus.takeHeldFocus(removed.value),
      lookupSection.focusHeading,
    )
  }, [
    accountBindings.data,
    accountBindings.error,
    lookupSection.focusHeading,
    lookupFocus.takeHeldFocus,
  ])

  // A locked role refuses every write -- role rewrite and delete, and
  // grant/revoke -- but not the reads. Only the server's builtin mark locks
  // a role here: the platform surface has no host-protected role names. The
  // selected role therefore opens read-only: the grants it holds are
  // visible, and every write control that would name it is withheld.
  const roleLocked = (role: AdminRole): boolean => role.builtin
  const selectedRoleRow = roleRows?.find((role) => role.role === selectedRole)
  const selectedRoleLocked =
    selectedRoleRow === undefined ? false : roleLocked(selectedRoleRow)

  const roleColumns: DataTableColumn<AdminRole>[] = [
    {
      id: 'role',
      header: t('permissions.roles.column.role'),
      cellWrap: 'nowrap',
      cellFullValue: (role) => role.role,
      // The role name is the table's primary identity and has no length
      // bound in the console's contract (a tenant names its own roles).
      // The shared whole-cell cap -- which counts the copy control --
      // keeps the row actions on screen at the sm floor, where this
      // column and the actions are all the roles table shows; the complete
      // name stays on the cell title and copy control.
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (role) => <Typography variant="body2">{role.role}</Typography>,
    },
    {
      id: 'builtin',
      header: t('permissions.roles.column.builtin'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (role) =>
        role.builtin ? t('permissions.roles.builtinYes') : t('permissions.roles.builtinNo'),
    },
    {
      id: 'description',
      header: t('permissions.roles.column.description'),
      priority: 'medium',
      // A free-text description wraps rather than truncating: the server
      // renders a builtin role's text in the request's language and returns
      // an operator's text as written, so there is no key to resolve here.
      cellWrap: 'wrap',
      // The cap keeps the wrapped prose from widening the row past its
      // tier's floor; the text itself stays complete on the cell.
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (role) => role.description,
    },
    {
      id: 'actions',
      align: 'right',
      header: t('permissions.roles.column.actions'),
      cellWrap: 'nowrap',
      // A builtin role's grants and existence are fixed: the server refuses
      // every write with authz.ErrBuiltinRole, but it refuses no read. The
      // row therefore keeps a read-only entry that opens the grant list and
      // drops the two write controls. A disabled button cannot be focused and
      // drops out of the Tab order, so a tooltip or aria-describedby that
      // depends on focus or hover is unreachable to keyboard-only users; it is
      // still in the accessibility tree, though, and a control that can never
      // succeed is not put in front of anyone. Visible text sits in every
      // reader's order and carries the reason.
      cell: (role) => {
        const failed = failedRole === role.role
        const errorId = `permissions-role-error-${encodeURIComponent(role.role)}`
        const removeBusy =
          pendingRoleWrite?.kind === 'remove' &&
          pendingRoleWrite.role === role.role
        const scopePending = pendingRoleWrite !== null
        return (
          <Box>
            {roleLocked(role) ? (
              <Box
                sx={{
                  display: 'flex',
                  flexWrap: 'wrap',
                  alignItems: 'center',
                  columnGap: 1,
                  rowGap: 1,
                }}
              >
                <ActionButton
                  type="button"
                  size="small"
                  aria-label={t('permissions.roles.viewGrantsAria', {
                    role: role.role,
                  })}
                  unavailable={grantsFeedback.pending}
                  onClick={() => selectRole(role.role)}
                >
                  {t('permissions.roles.viewGrants')}
                </ActionButton>
                <Typography variant="body2" color="text.secondary">
                  {t('permissions.roles.builtinLocked')}
                </Typography>
              </Box>
            ) : (
              <Box
                sx={{
                  display: 'flex',
                  flexWrap: 'wrap',
                  columnGap: 1,
                  rowGap: 1,
                }}
              >
                <ActionButton
                  type="button"
                  size="small"
                  aria-label={t('permissions.roles.manageGrantsAria', {
                    role: role.role,
                  })}
                  unavailable={grantsFeedback.pending}
                  onClick={() => selectRole(role.role)}
                >
                  {t('permissions.roles.manageGrants')}
                </ActionButton>
                <ActionButton
                  ref={roleFocus.controlRef(role.role)}
                  type="button"
                  size="small"
                  tone="danger"
                  aria-label={t('permissions.roles.removeAria', {
                    role: role.role,
                  })}
                  aria-invalid={failed}
                  aria-describedby={failed ? errorId : undefined}
                  busy={removeBusy}
                  unavailable={scopePending && !removeBusy}
                  data-role-remove={role.role}
                  onClick={() => requestRemoveRole(role)}
                >
                  {t('permissions.roles.remove')}
                </ActionButton>
              </Box>
            )}
            {failed && roleFailureText !== null && (
              <Typography
                id={errorId}
                variant="caption"
                color="error"
                sx={{ display: 'block', mt: 0.5 }}
              >
                {roleFailureText}
              </Typography>
            )}
          </Box>
        )
      },
    },
  ]

  // A grant row takes the permission code the server reports for it as the
  // key into the catalog, so "what is granted" and "what the catalog
  // declares" read as one vocabulary: the catalog's description is the
  // primary text and the code sits beneath it. A grant whose pair the server
  // cannot translate carries an empty permission; the row falls back to the
  // pair's own spelling, and its revoke is withheld -- the route names a
  // permission by its code, and there is no code to send.
  const grantLabel = (grant: AdminGrant): string =>
    grant.permission !== ''
      ? grant.permission
      : `${grant.resource}/${grant.action}`

  const grantBaseColumns: DataTableColumn<AdminGrant>[] = [
    {
      id: 'permission',
      header: t('permissions.grants.column.permission'),
      // The readable description wraps; the code beneath it is unbroken but
      // may break at its dots on a narrow tier.
      cellWrap: 'wrap',
      cellFullValue: grantLabel,
      // The column declares no whole-cell cap -- that would squeeze the
      // wrapped description -- so the identity line alone carries the
      // shared cap (see PermissionLabel).
      cell: (grant) => (
        <PermissionLabel
          entry={permissionByCode.get(grant.permission)}
          code={grantLabel(grant)}
          capIdentity
        />
      ),
    },
  ]
  // A builtin role's grants are read-only: the revoke column is not part of
  // its table, and the grant form above it is withheld as well.
  const grantRevokeColumn: DataTableColumn<AdminGrant> = {
    id: 'actions',
    align: 'right',
    header: t('permissions.grants.column.actions'),
    cellWrap: 'nowrap',
    cell: (grant) => {
      const code = grant.permission === '' ? null : grant.permission
      const key = `${grant.resource}:${grant.action}`
      const failed = failedGrant === key
      const errorId = `permissions-grant-error-${encodeURIComponent(key)}`
      // A grant with no permission code cannot name the permission the
      // revoke route takes. The control stays visible but unavailable, and
      // the reason sits beneath it as text, so a reader who never focuses
      // the control still gets the explanation.
      const unmappedId = `permissions-grant-unmapped-${encodeURIComponent(key)}`
      const revokeBusy =
        pendingGrantWrite?.kind === 'revoke' && pendingGrantWrite.key === key
      const scopePending = pendingGrantWrite !== null
      return (
        <Box>
          <ActionButton
            ref={grantFocus.controlRef(key)}
            type="button"
            size="small"
            tone="danger"
            aria-label={t('permissions.grants.revokeAria', {
              permission: grantLabel(grant),
            })}
            aria-invalid={failed}
            aria-describedby={failed ? errorId : undefined}
            busy={revokeBusy}
            unavailable={code === null || (scopePending && !revokeBusy)}
            unavailableDescribedBy={code === null ? unmappedId : undefined}
            data-grant-revoke={key}
            onClick={() => {
              if (code !== null) {
                revoke(grant, code)
              }
            }}
          >
            {t('permissions.grants.revoke')}
          </ActionButton>
          {code === null && (
            <Typography
              id={unmappedId}
              variant="caption"
              color="text.secondary"
              sx={{ display: 'block', mt: 0.5, whiteSpace: 'normal' }}
            >
              {t('permissions.grants.revokeUnavailable')}
            </Typography>
          )}
          {failed && grantsFailureText !== null && (
            <Typography
              id={errorId}
              variant="caption"
              color="error"
              sx={{ display: 'block', mt: 0.5 }}
            >
              {grantsFailureText}
            </Typography>
          )}
        </Box>
      )
    },
  }
  const grantColumns = selectedRoleLocked
    ? grantBaseColumns
    : [...grantBaseColumns, grantRevokeColumn]

  const catalogColumns: DataTableColumn<AdminPermission>[] = [
    {
      id: 'permission',
      header: t('permissions.catalog.column.permission'),
      // The server-rendered description is the column's primary text and
      // wraps; the code beneath it is unbroken but may break at its dots on
      // a narrow tier. The column declares no whole-cell cap -- that would
      // squeeze the wrapped text -- so the identity line alone carries the
      // shared primary cap (see PermissionLabel).
      cellWrap: 'wrap',
      cellFullValue: (entry) => entry.permission,
      cell: (entry) => (
        <Box>
          <PermissionLabel
            entry={entry}
            code={entry.permission}
            capIdentity
          />
          {/* A permission the caller does not hold is still listed; the mark
              says it cannot be granted, so the absence from the picker reads
              as a property of the caller rather than of the catalog. */}
          {!entry.grantable && (
            <Typography
              variant="caption"
              color="text.secondary"
              sx={{ display: 'block' }}
            >
              {t('permissions.catalog.notGrantable')}
            </Typography>
          )}
        </Box>
      ),
    },
  ]

  // Both binding tables render the same columns, but each row's result
  // belongs to that table's own scope and marker.
  const bindingColumns = (
    lookup: boolean,
  ): DataTableColumn<AdminBinding>[] => {
    const failedKey = lookup ? failedLookupBinding : failedBinding
    const failureText = lookup ? lookupFailureText : bindingsFailureText
    const scopePending = lookup
      ? pendingLookupWrite !== null
      : pendingBindingWrite !== null
    const pendingKey = lookup
      ? pendingLookupWrite
      : pendingBindingWrite?.kind === 'unbind'
        ? pendingBindingWrite.key
        : null
    return [
      {
        id: 'account',
        header: t('permissions.bindings.column.account'),
        cellWrap: 'nowrap',
        // The account is the row's identity, so the shared primary cap keeps
        // it inside the sm floor alongside the row actions. The opaque
        // account id is neither shown nor offered as a copy value.
        contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
        cell: (binding) => <AccountIdentity account={binding.account} />,
      },
      {
        id: 'role',
        header: t('permissions.bindings.column.role'),
        cellWrap: 'nowrap',
        cellFullValue: (binding) => binding.role,
        contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
        cell: (binding) => binding.role,
      },
      {
        id: 'actions',
        align: 'right',
        header: t('permissions.bindings.column.actions'),
        cellWrap: 'nowrap',
        cell: (binding) => {
          const key = bindingKey(binding)
          const failed = failedKey === key
          const errorId = bindingErrorId(
            lookup ? 'lookup' : 'bindings',
            binding,
          )
          const busy = pendingKey === key
          return (
            <Box>
              <PlatformUnbindButton
                binding={binding}
                busy={busy}
                unavailable={scopePending && !busy}
                invalid={failed}
                describedBy={errorId}
                rowAttribute={
                  lookup ? 'data-lookup-unbind' : 'data-binding-unbind'
                }
                controlRef={(lookup ? lookupFocus : bindingFocus).controlRef(
                  key,
                )}
                onClick={(identity) => unbind(binding, lookup, identity)}
              />
              {failed && failureText !== null && (
                <Typography
                  id={errorId}
                  variant="caption"
                  color="error"
                  sx={{ display: 'block', mt: 0.5 }}
                >
                  {failureText}
                </Typography>
              )}
            </Box>
          )
        },
      },
    ]
  }

  const rolesPending = roles.isPending && roleRows === undefined

  return (
    <Box>
      <AdminPageHeader
        title={t('permissions.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('permissions.title') },
        ]}
        sx={{ mb: 2 }}
      />

      {/* The standing role-disappeared announcement: mounted with the page,
          filled only when a refresh's roles read drops the selection. */}
      <Box component="p" role="status" sx={visuallyHiddenSx}>
        {roleGone ? t('permissions.roles.selectionCleared') : ''}
      </Box>

      <SectionCard sx={{ mb: 3 }}>
        {tenantControlled ? (
          <RefreshButton
            busy={pageRefresh.busy}
            disabled={tenant === ''}
            writePending={anyPending}
            describedBy={TENANT_HINT_ID}
            disabledReason={t('permissions.needTenant.description')}
            label={t('common.refresh')}
            onClick={pageRefresh.refresh}
          />
        ) : (
          <TenantScope
            value={tenant}
            onSelect={changeTenant}
            disabled={anyPending}
            trailing={
              <RefreshButton
                busy={pageRefresh.busy}
                disabled={tenant === ''}
                writePending={anyPending}
                describedBy={TENANT_HINT_ID}
                disabledReason={t('permissions.needTenant.description')}
                label={t('common.refresh')}
                onClick={pageRefresh.refresh}
              />
            }
          />
        )}
      </SectionCard>

      {tenant === '' ? (
        <EmptyState
          headingLevel="h2"
          title={t('permissions.needTenant.title')}
          description={
            <span id={TENANT_HINT_ID}>
              {t('permissions.needTenant.description')}
            </span>
          }
        />
      ) : (
        <>
          <SectionCard
            ref={rolesSection.sectionRef}
            title={t('permissions.roles.title')}
            sx={{ mb: 3 }}
          >
            <Box
              sx={{
                display: 'flex',
                flexWrap: 'wrap',
                alignItems: 'center',
                columnGap: 2,
                rowGap: 1,
                mb: 2,
              }}
            >
              <TextField
                size="small"
                value={roleName}
                onChange={(event) => setRoleName(event.target.value)}
                label={t('permissions.roles.create.roleLabel')}
              />
              <TextField
                size="small"
                multiline
                minRows={1}
                maxRows={3}
                value={roleDescription}
                onChange={(event) => setRoleDescription(event.target.value)}
                label={t('permissions.roles.create.descriptionLabel')}
                // The server caps a role description at 500 characters and
                // answers an over-long value with 400; the field stops the
                // input at the same bound.
                slotProps={{ htmlInput: { maxLength: 500 } }}
              />
              <ActionButton
                type="button"
                emphasis="primary"
                busy={pendingRoleWrite?.kind === 'create'}
                unavailable={
                  roleName.trim() === '' ||
                  (pendingRoleWrite !== null &&
                    pendingRoleWrite.kind !== 'create')
                }
                unavailableReason={
                  roleName.trim() === ''
                    ? t('permissions.roles.create.roleRequired')
                    : undefined
                }
                onClick={saveRole}
              >
                {t('permissions.roles.create.submit')}
              </ActionButton>
            </Box>

            <AdminAsyncSection
              pending={rolesPending}
              payload={roleRows}
              error={roles.error}
              refreshing={roles.isFetching}
              empty={roleRows !== undefined && roleRows.length === 0}
              headingLevel="h3"
              loading={
                <ListSkeleton
                  label={t('permissions.roles.loading')}
                  rows={[
                    <Skeleton key="a" variant="text" width="55%" />,
                    <Skeleton key="b" variant="text" width="40%" />,
                  ]}
                />
              }
              errorState={{
                title: t('permissions.roles.error.title'),
                description: t('permissions.roles.error.description'),
                retryLabel: t('permissions.roles.retry'),
                onRetry: () => void roles.refetch(),
              }}
              emptyState={{
                title: t('permissions.roles.empty.title'),
                description: t('permissions.roles.empty.description'),
              }}
            >
              {(settled) => (
                <DataTable
                  rows={settled}
                  columns={roleColumns}
                  rowKey={(role) => role.role}
                />
              )}
            </AdminAsyncSection>
            <Box ref={roleRegionRef}>
              <WriteFeedbackRegion outcome={roleFeedback.outcome} />
            </Box>
          </SectionCard>

          <SectionCard
            ref={grantsSection.sectionRef}
            title={t('permissions.grants.title')}
            sx={{ mb: 3 }}
          >
            {selectedRole === '' ? (
              <Typography variant="body2" color="text.secondary">
                {t('permissions.grants.pickRole')}
              </Typography>
            ) : (
              <>
                <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
                  {t('permissions.grants.forRole', { role: selectedRole })}
                </Typography>
                {selectedRoleLocked ? (
                  <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
                    {t('permissions.grants.builtinReadOnly')}
                  </Typography>
                ) : (
                  <Box
                    sx={{
                      display: 'flex',
                      flexWrap: 'wrap',
                      alignItems: 'flex-end',
                      columnGap: 2,
                      rowGap: 1,
                      mb: 2,
                    }}
                  >
                    <TextField
                      select
                      size="small"
                      value={grantPermission}
                      onChange={(event) => setGrantPermission(event.target.value)}
                      label={t('permissions.grants.permissionLabel')}
                      sx={{ minWidth: 240 }}
                      slotProps={{
                        select: {
                          // The options carry the readable description above
                          // the code; the closed control shows the chosen
                          // code alone, so the field keeps its single-line
                          // height and names the permission unambiguously.
                          renderValue: (value) => value as string,
                        },
                      }}
                    >
                      {grantablePermissions.map((entry) => (
                        <MenuItem key={entry.permission} value={entry.permission}>
                          <PermissionLabel
                            entry={entry}
                            code={entry.permission}
                          />
                        </MenuItem>
                      ))}
                    </TextField>
                    <ActionButton
                      type="button"
                      emphasis="primary"
                      busy={pendingGrantWrite?.kind === 'grant'}
                      unavailable={
                        grantPermission === '' ||
                        (pendingGrantWrite !== null &&
                          pendingGrantWrite.kind !== 'grant')
                      }
                      unavailableReason={
                        grantPermission === ''
                          ? t('permissions.grants.permissionRequired')
                          : undefined
                      }
                      onClick={grant}
                    >
                      {t('permissions.grants.add')}
                    </ActionButton>
                  </Box>
                )}
                <AdminAsyncSection
                  pending={grants.isPending && grants.data === undefined}
                  payload={grants.data}
                  error={grants.error}
                  refreshing={grants.isFetching}
                  empty={grants.data !== undefined && grants.data.length === 0}
                  headingLevel="h3"
                  loading={
                    <ListSkeleton
                      label={t('permissions.grants.loading')}
                      rows={[<Skeleton key="a" variant="text" width="50%" />]}
                    />
                  }
                  errorState={{
                    title: t('permissions.grants.error.title'),
                    description: t('permissions.grants.error.description'),
                    retryLabel: t('permissions.grants.retry'),
                    onRetry: () => void grants.refetch(),
                  }}
                  emptyState={{
                    title: t('permissions.grants.empty.title'),
                    description: t('permissions.grants.empty.description'),
                  }}
                >
                  {(settled) => (
                    <DataTable
                      rows={settled}
                      columns={grantColumns}
                      rowKey={(grant) => `${grant.resource}:${grant.action}`}
                    />
                  )}
                </AdminAsyncSection>
                <Box ref={grantsRegionRef}>
                  <WriteFeedbackRegion outcome={grantsFeedback.outcome} />
                </Box>
              </>
            )}
          </SectionCard>

          <SectionCard
            title={t('permissions.catalog.title')}
            flushContent
            sx={{ mb: 3 }}
          >
            <AdminAsyncSection
              pending={catalog.isPending && catalog.data === undefined}
              payload={catalog.data}
              error={catalog.error}
              refreshing={catalog.isFetching}
              empty={catalog.data !== undefined && catalog.data.length === 0}
              headingLevel="h3"
              loading={
                <ListSkeleton
                  label={t('permissions.catalog.loading')}
                  rows={[<Skeleton key="a" variant="text" width="45%" />]}
                />
              }
              errorState={{
                title: t('permissions.catalog.error.title'),
                description: t('permissions.catalog.error.description'),
                retryLabel: t('permissions.catalog.retry'),
                onRetry: () => void catalog.refetch(),
              }}
              emptyState={{
                title: t('permissions.catalog.empty.title'),
                description: t('permissions.catalog.empty.description'),
              }}
            >
              {() =>
                catalogGroups.map((group) => (
                  <Box
                    key={group.group}
                    sx={{ '&:not(:last-of-type)': { mb: 3 } }}
                  >
                    <Typography component="h3" variant="subtitle1" sx={{ mb: 1 }}>
                      {t(permissionGroupKey(group.group))}
                    </Typography>
                    <DataTable
                      rows={group.entries}
                      columns={catalogColumns}
                      rowKey={(entry) => entry.permission}
                    />
                  </Box>
                ))
              }
            </AdminAsyncSection>
          </SectionCard>

          <SectionCard
            ref={bindingsSection.sectionRef}
            title={t('permissions.bindings.title')}
            sx={{ mb: 3 }}
          >
            <Box
              sx={{
                display: 'flex',
                flexWrap: 'wrap',
                alignItems: 'flex-end',
                columnGap: 2,
                rowGap: 1,
                mb: 2,
              }}
            >
              <AccountSelector
                label={t('permissions.bindings.accountLabel')}
                value={bindAccount}
                onChange={changeBindAccount}
                disabled={bindingsFeedback.pending}
              />
              <TextField
                size="small"
                value={bindRoleName}
                onChange={(event) => changeBindRole(event.target.value)}
                label={t('permissions.bindings.roleLabel')}
                slotProps={{ input: { readOnly: bindingsFeedback.pending } }}
              />
              <ActionButton
                type="button"
                emphasis="primary"
                busy={pendingBindingWrite?.kind === 'bind'}
                unavailable={
                  bindAccount === null ||
                  bindRoleName.trim() === '' ||
                  (pendingBindingWrite !== null &&
                    pendingBindingWrite.kind !== 'bind')
                }
                unavailableReason={
                  bindAccount === null
                    ? t('permissions.bindings.accountRequired')
                    : bindRoleName.trim() === ''
                      ? t('permissions.bindings.roleRequired')
                      : undefined
                }
                onClick={bind}
              >
                {t('permissions.bindings.bind')}
              </ActionButton>
            </Box>
            <AdminAsyncSection
              pending={bindings.isPending && bindings.data === undefined}
              payload={bindings.data}
              error={bindings.error}
              refreshing={bindings.isFetching}
              empty={bindings.data !== undefined && bindings.data.length === 0}
              headingLevel="h3"
              loading={
                <ListSkeleton
                  label={t('permissions.bindings.loading')}
                  rows={[<Skeleton key="a" variant="text" width="52%" />]}
                />
              }
              errorState={{
                title: t('permissions.bindings.error.title'),
                description: t('permissions.bindings.error.description'),
                retryLabel: t('permissions.bindings.retry'),
                onRetry: () => void bindings.refetch(),
              }}
              emptyState={{
                title: t('permissions.bindings.empty.title'),
                description: t('permissions.bindings.empty.description'),
              }}
            >
              {(settled) => (
                <DataTable
                  rows={settled}
                  columns={bindingColumns(false)}
                  rowKey={(binding) => `${binding.account}:${binding.role}`}
                />
              )}
            </AdminAsyncSection>
            <Box ref={bindingsRegionRef}>
              <WriteFeedbackRegion outcome={bindingsFeedback.outcome} />
            </Box>
          </SectionCard>

          <SectionCard
            ref={lookupSection.sectionRef}
            title={t('permissions.lookup.title')}
            sx={{ mb: 3 }}
          >
            <Box
              sx={{
                display: 'flex',
                flexWrap: 'wrap',
                alignItems: 'flex-end',
                columnGap: 2,
                rowGap: 1,
                mb: 2,
              }}
            >
              <AccountSelector
                label={t('permissions.lookup.accountLabel')}
                value={lookupAccount}
                onChange={setLookupAccount}
                disabled={lookupFeedback.pending}
              />
              <ActionButton
                type="button"
                busy={accountBindings.isFetching}
                unavailable={lookupAccount === null || lookupFeedback.pending}
                unavailableReason={
                  lookupAccount === null
                    ? t('permissions.lookup.accountRequired')
                    : undefined
                }
                onClick={applyLookup}
              >
                {t('permissions.lookup.apply')}
              </ActionButton>
            </Box>
            {lookupApplied !== '' && (
              <AdminAsyncSection
                pending={
                  accountBindings.isPending &&
                  accountBindings.data === undefined
                }
                payload={accountBindings.data}
                error={accountBindings.error}
                refreshing={accountBindings.isFetching}
                empty={
                  accountBindings.data !== undefined &&
                  accountBindings.data.length === 0
                }
                headingLevel="h3"
                loading={
                  <ListSkeleton
                    label={t('permissions.lookup.loading')}
                    rows={[<Skeleton key="a" variant="text" width="48%" />]}
                  />
                }
                errorState={{
                  title: t('permissions.lookup.error.title'),
                  description: t('permissions.lookup.error.description'),
                  retryLabel: t('permissions.lookup.retry'),
                  onRetry: () => void accountBindings.refetch(),
                }}
                emptyState={{
                  title: t('permissions.lookup.empty.title'),
                  description: t('permissions.lookup.empty.description'),
                }}
              >
                {(settled) => (
                  <DataTable
                    rows={settled}
                    columns={bindingColumns(true)}
                    rowKey={(binding) => `${binding.account}:${binding.role}`}
                  />
                )}
              </AdminAsyncSection>
            )}
            <Box ref={lookupRegionRef}>
              <WriteFeedbackRegion outcome={lookupFeedback.outcome} />
            </Box>
          </SectionCard>

          <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
        </>
      )}
    </Box>
  )
}
