/**
 * PermissionsPage is the platform console's authorization page: one tenant's
 * roles, grants, catalog, bindings and one account's held bindings, all named
 * by the tenant chosen through its selector.
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
  useInertSelectMenu,
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

// One copy of the "choose a tenant first" explanation, shared with the disabled refresh.
const TENANT_HINT_ID = 'permissions-tenant-hint'

// The server-rendered description as primary text with the code beneath it and a
// high-risk badge. In a table cell only the primary line is capped, so the stacked
// code is not squeezed.
function PermissionLabel({
  entry,
  code,
  capIdentity = false,
}: {
  readonly entry: AdminPermission | undefined
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

// An ungrouped permission falls into "other" rather than disappearing.
function groupCatalog(entries: readonly AdminPermission[]) {
  return PERMISSION_GROUPS.flatMap((group) => {
    const grouped = entries.filter(
      (entry) => permissionGroupOf(entry.group) === group,
    )
    return grouped.length === 0 ? [] : [{ group, entries: grouped }]
  })
}

// Its accessible name uses the readable identity, or the recorded identifier
// when none, so a reader hears the account the row's cell shows.
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
  readonly identity: AccountIdentityValue
  readonly busy: boolean
  readonly unavailable: boolean
  readonly invalid: boolean
  readonly describedBy?: string
  readonly rowAttribute: string
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

function PlatformUnbindButton(
  props: Omit<Parameters<typeof UnbindButton>[0], 'identity'>,
) {
  const { name, username } = useAccountIdentity(props.binding.account)
  return <UnbindButton {...props} identity={{ name, username }} />
}

function bindingKey(binding: AdminBinding): string {
  return `${binding.account}:${binding.role}`
}

function bindingErrorId(scope: string, binding: AdminBinding): string {
  return `permissions-${scope}-error-${encodeURIComponent(bindingKey(binding))}`
}

type PendingRoleWrite =
  | { readonly kind: 'create' }
  | { readonly kind: 'remove'; readonly role: string }

type PendingGrantWrite =
  | { readonly kind: 'grant' }
  | { readonly kind: 'revoke'; readonly key: string }

type PendingBindingWrite =
  | { readonly kind: 'bind' }
  | { readonly kind: 'unbind'; readonly key: string }

interface RowRemoval {
  readonly value: string
  readonly index: number
}

/** Props of PermissionsPage: the tenant scope, optionally controlled by the host. */
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

/**
 * PermissionsPage renders the platform console's authorization administration
 * for the tenant chosen through its selector.
 */
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
  const grantMenu = useInertSelectMenu()

  const [innerTenant, setInnerTenant] = useState(
    () => defaultTenant ?? tenantFromLocation(),
  )
  const tenant = controlledTenant ?? innerTenant
  const tenantControlled = controlledTenant !== undefined
  const [selectedRole, setSelectedRole] = useState('')
  // A removal settles after the dialog closed, so it judges the selection as it
  // stands then, not the one the click captured.
  const selectedRoleRef = useRef(selectedRole)
  useEffect(() => {
    selectedRoleRef.current = selectedRole
  }, [selectedRole])

  const [roleName, setRoleName] = useState('')
  const [roleDescription, setRoleDescription] = useState('')

  const [grantPermission, setGrantPermission] = useState('')

  const [bindAccount, setBindAccount] = useState<AdminAccount | null>(null)
  const [bindRoleName, setBindRoleName] = useState('')

  const [lookupAccount, setLookupAccount] = useState<AdminAccount | null>(null)
  const [lookupApplied, setLookupApplied] = useState('')

  const confirm = usePendingConfirm()

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

  const [failedRole, setFailedRole] = useState<string | null>(null)
  const [failedGrant, setFailedGrant] = useState<string | null>(null)
  const [failedBinding, setFailedBinding] = useState<string | null>(null)
  const [failedLookupBinding, setFailedLookupBinding] = useState<string | null>(
    null,
  )
  const [pendingRoleWrite, setPendingRoleWrite] =
    useState<PendingRoleWrite | null>(null)
  const [pendingGrantWrite, setPendingGrantWrite] =
    useState<PendingGrantWrite | null>(null)
  const [pendingBindingWrite, setPendingBindingWrite] =
    useState<PendingBindingWrite | null>(null)
  const [pendingLookupWrite, setPendingLookupWrite] = useState<string | null>(
    null,
  )
  const roleRemoval = useRef<RowRemoval | null>(null)
  const grantRemoval = useRef<RowRemoval | null>(null)
  const bindingRemoval = useRef<RowRemoval | null>(null)
  const lookupRemoval = useRef<RowRemoval | null>(null)
  const roleFocus = useRowRemovalFocus()
  const grantFocus = useRowRemovalFocus()
  const bindingFocus = useRowRemovalFocus()
  const lookupFocus = useRowRemovalFocus()
  // Announced in one standing status region; choosing a role, changing the
  // tenant or refreshing again clears it.
  const [roleGone, setRoleGone] = useState(false)

  // The row's inline marker resolves through the same resolver as the region.
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
  // A grant the server cannot translate has no code and no entry, so the row
  // keeps its recorded pair.
  const permissionByCode = useMemo(
    () => new Map(permissions.map((entry) => [entry.permission, entry])),
    [permissions],
  )
  const catalogGroups = useMemo(
    () => groupCatalog(permissions),
    [permissions],
  )
  const grantablePermissions = useMemo(
    () => permissions.filter((entry) => entry.grantable),
    [permissions],
  )

  // Clears every slot and row marker, so no control keeps aria-invalid for text
  // that no longer renders.
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
  useEffect(() => {
    resetPageFeedback()
    setSelectedRole('')
    setBindAccount(null)
    setLookupAccount(null)
    setLookupApplied('')
  }, [tenant, resetPageFeedback])

  const pageRefresh = usePageRefresh(
    [
      pageRefreshRead('roles', roles, {
        // A successful read that no longer lists the selection clears it; a
        // failed read keeps it, its stale marker telling the story.
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

  const changeTenant = (next: string) => {
    setInnerTenant(next)
    onTenantChange?.(next)
    resetPageFeedback()
  }

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

  // The lookup scope follows the applied account, not the draft.
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
      // Role deletion revokes its grants and bindings and cannot be undone.
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
    // A dangerous permission widens power over other accounts or the policy, so
    // it takes the deliberate two-step confirmation.
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

  // The lookup's unbind result lives under the lookup table, not the
  // tenant-wide binding list.
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

  // A landed write removes its row: focus moves to the next row's control, else
  // the previous row's, else the section heading. A failed re-read drops the record.
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

  // A builtin role refuses every write but no read; no host-protected role names
  // exist on the platform surface.
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
      // The cap counts the copy control, keeping the row actions on screen at
      // the sm floor; the complete name stays on the title and copy control.
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
      cellWrap: 'wrap',
      contentMaxWidth: LONG_TEXT_COLUMN_MAX_WIDTH,
      cell: (role) => role.description,
    },
    {
      id: 'actions',
      align: 'right',
      header: t('permissions.roles.column.actions'),
      cellWrap: 'nowrap',
      // A builtin role keeps a read-only "view grants" entry and drops the write
      // controls. A disabled control's reason would be unreachable to keyboard
      // users, so visible text carries it.
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

  // A grant the server cannot translate carries an empty permission: the row falls
  // back to resource/action, and its revoke is withheld because there is no code.
  const grantLabel = (grant: AdminGrant): string =>
    grant.permission !== ''
      ? grant.permission
      : `${grant.resource}/${grant.action}`

  const grantBaseColumns: DataTableColumn<AdminGrant>[] = [
    {
      id: 'permission',
      header: t('permissions.grants.column.permission'),
      cellWrap: 'wrap',
      cellFullValue: grantLabel,
      cell: (grant) => (
        <PermissionLabel
          entry={permissionByCode.get(grant.permission)}
          code={grantLabel(grant)}
          capIdentity
        />
      ),
    },
  ]
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
      // A grant with no code cannot name a revoke target; the control stays visible
      // but unavailable, with the reason as text beneath it.
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
      cellWrap: 'wrap',
      cellFullValue: (entry) => entry.permission,
      cell: (entry) => (
        <Box>
          <PermissionLabel
            entry={entry}
            code={entry.permission}
            capIdentity
          />
          {/* A permission the caller cannot grant is still listed and marked, so
              its absence from the picker reads as the caller's limit. */}
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
        // The opaque account id is neither shown nor offered as a copy value.
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
                // The server caps a description at 500 characters; the field
                // stops the input at the same bound.
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
                          ...grantMenu,
                          // Without this the closed control renders the selected
                          // PermissionLabel and wraps; render the code alone.
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
