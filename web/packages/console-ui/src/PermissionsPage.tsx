/**
 * PermissionsPage: the authorization-administration page of the console.
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
 * The page renders the surface in five sections:
 *
 *  - the role catalog: list a tenant's roles, create or rewrite one, and
 *    remove one (removal revokes its grants and bindings with it);
 *  - the grants of the selected role: list them by the same permission code
 *    the catalog spells, grant a permission from the catalog and revoke
 *    one;
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
 * A role row carries the host's description key, never text this package
 * ships: the shell's `describeRole` resolver renders it, and without one the
 * raw key stands so the row is never blank. The permission catalog keeps the
 * full permission code as its primary text and shows the permission's
 * server-rendered description as a secondary line beneath it; the grant
 * picker's options carry the same pair, while the closed control shows the
 * chosen code alone. An empty description leaves the code as the only text,
 * and the authorization table keeps the same permission code the catalog
 * spells as its primary text.
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
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  DataTable,
  EmptyState,
  ListSkeleton,
  LONG_TEXT_COLUMN_MAX_WIDTH,
  PageHeader,
  PRIMARY_COLUMN_MAX_WIDTH,
  SectionCard,
  useWriteFeedback,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import {
  AccountIdentity,
  useAccountIdentity,
} from './internal/account-identity.js'
import { AccountSelector } from './internal/account-selector.js'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { useConsoleDescribeRole } from './internal/describe-role.js'
import { useConsoleUiErrorText } from './internal/error-text.js'
import { RefreshButton } from '@speed/ui-kit'
import { TenantScope, tenantFromLocation } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import {
  pageRefreshRead,
  usePageRefresh,
} from '@speed/ui-kit'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useConsoleOperations } from './operations.js'
import type {
  ConsoleAccount,
  ConsoleBinding,
  ConsoleGrant,
  ConsolePermission,
  ConsoleRole,
} from './operations.js'

/**
 * The id of the page's own "choose a tenant first" text. The disabled
 * refresh is described by it, so the explanation has one copy on the page.
 */
const TENANT_HINT_ID = 'permissions-tenant-hint'

/**
 * One permission's code with its server-rendered description beneath it.
 * The code stays the primary text: it is the vocabulary the grant routes
 * name a permission by, and the one the catalog and the authorization
 * table share. The description arrives already rendered in the request's
 * language and is shown as received; an empty one leaves the code alone.
 */
function PermissionLabel({ entry }: { readonly entry: ConsolePermission }) {
  return (
    <Box>
      <Typography variant="body2" noWrap>
        {entry.permission}
      </Typography>
      {entry.description !== '' && (
        <Typography
          variant="caption"
          color="text.secondary"
          sx={{ display: 'block', whiteSpace: 'normal' }}
        >
          {entry.description}
        </Typography>
      )}
    </Box>
  )
}

/**
 * The unbind control of one binding row. Its accessible name names the
 * account by its readable identity when the account read answers one, and
 * by the recorded identifier otherwise, so a reader hears the same account
 * the row's cell shows.
 */
function UnbindButton({
  binding,
  disabled,
  invalid,
  describedBy,
  onClick,
}: {
  readonly binding: ConsoleBinding
  readonly disabled: boolean
  /** Whether this row is the one the scope's failure names. */
  readonly invalid: boolean
  /** The id of the resolved failure text rendered in this row. */
  readonly describedBy?: string
  readonly onClick: () => void
}) {
  const { t } = useConsoleUiTranslation()
  const { name } = useAccountIdentity(binding.account)
  return (
    <Button
      type="button"
      size="small"
      variant="outlined"
      color="error"
      aria-label={t('permissions.bindings.unbindAria', {
        account: name ?? binding.account,
        role: binding.role,
      })}
      aria-invalid={invalid}
      aria-describedby={invalid ? describedBy : undefined}
      disabled={disabled}
      loading={disabled}
      onClick={onClick}
    >
      {t('permissions.bindings.unbind')}
    </Button>
  )
}

/** The key a binding row carries through the page's failure markers. */
function bindingKey(binding: ConsoleBinding): string {
  return `${binding.account}:${binding.role}`
}

/** The id of one binding row's inline failure text within a scope. */
function bindingErrorId(scope: string, binding: ConsoleBinding): string {
  return `permissions-${scope}-error-${encodeURIComponent(bindingKey(binding))}`
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

export function PermissionsPage({
  tenant: controlledTenant,
  defaultTenant,
  onTenantChange,
}: PermissionsPageProps = {}) {
  const { t } = useConsoleUiTranslation()
  const describeRole = useConsoleDescribeRole()
  const operations = useConsoleOperations()

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
  const [bindAccount, setBindAccount] = useState<ConsoleAccount | null>(null)
  const [bindRoleName, setBindRoleName] = useState('')

  // The account whose held bindings are looked up.
  const [lookupAccount, setLookupAccount] = useState<ConsoleAccount | null>(null)
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

  // The row each scope's failure names. The marker belongs to the row the
  // region reports, and clearing it before an attempt starts keeps it from
  // outliving the result it describes.
  const [failedRole, setFailedRole] = useState<string | null>(null)
  const [failedGrant, setFailedGrant] = useState<string | null>(null)
  const [failedBinding, setFailedBinding] = useState<string | null>(null)
  const [failedLookupBinding, setFailedLookupBinding] = useState<string | null>(
    null,
  )
  // Whether the last refresh's roles read dropped the selected role. The
  // page announces it in one standing status region; choosing a role,
  // changing the tenant or refreshing again clears it.
  const [roleGone, setRoleGone] = useState(false)

  // The one resolver the regions bind: a row's inline marker resolves the
  // same failure code through it, so the two can never carry different
  // copy.
  const resolveErrorText = useConsoleUiErrorText()

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
  // clears the slots and the row markers.
  useEffect(() => {
    resetPageFeedback()
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

  const changeBindAccount = (account: ConsoleAccount | null) => {
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
    void roleFeedback.run(async () => {
      await writeRole.mutateAsync({
        tenant,
        role,
        descriptionKey: roleDescription,
      })
      setRoleName('')
      setRoleDescription('')
      return t('permissions.roles.saved', { role })
    })
  }

  const requestRemoveRole = (role: ConsoleRole) => {
    confirm.ask({
      title: t('permissions.roles.removeTitle'),
      message: t('permissions.roles.removeMessage', { role: role.role }),
      confirmLabel: t('permissions.roles.removeConfirm'),
      // Deleting a role revokes its grants and bindings with it and cannot
      // be undone from the interface: the heavier two-step arm.
      doubleConfirm: true,
      run: () => {
        setFailedRole(null)
        void roleFeedback.run(async () => {
          try {
            await removeRole.mutateAsync({ tenant, role: role.role })
            if (selectedRoleRef.current === role.role) {
              setSelectedRole('')
              grantsFeedback.reset()
              setFailedGrant(null)
            }
            return t('permissions.roles.removed', { role: role.role })
          } catch (error) {
            setFailedRole(role.role)
            throw error
          }
        })
      },
    })
  }

  const grant = () => {
    const permission = grantPermission
    if (permission === '') {
      return
    }
    void grantsFeedback.run(async () => {
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
  }

  const revoke = (grant: ConsoleGrant, code: string) => {
    const key = `${grant.resource}:${grant.action}`
    setFailedGrant(null)
    void grantsFeedback.run(async () => {
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
        setFailedGrant(key)
        throw error
      }
    })
  }

  const bind = () => {
    if (bindAccount === null) {
      return
    }
    const account = bindAccount
    const role = bindRoleName
    setFailedBinding(null)
    void bindingsFeedback.run(async () => {
      await bindRole.mutateAsync({ tenant, account: account.id, role })
      setBindAccount(null)
      setBindRoleName('')
      return t('permissions.bindings.bound', {
        account: account.display_name === '' ? account.id : account.display_name,
        role,
      })
    })
  }

  // The bindings table and the lookup table keep separate scopes and row
  // markers: an unbind in the lookup answers the operator's lookup, not the
  // tenant-wide binding list, so its result lives under the lookup table.
  const unbind = (binding: ConsoleBinding, lookup: boolean) => {
    const feedback = lookup ? lookupFeedback : bindingsFeedback
    const key = bindingKey(binding)
    const mark = (value: string | null) => {
      if (lookup) {
        setFailedLookupBinding(value)
      } else {
        setFailedBinding(value)
      }
    }
    mark(null)
    void feedback.run(async () => {
      try {
        await unbindRole.mutateAsync({
          tenant,
          account: binding.account,
          role: binding.role,
        })
        return t('permissions.bindings.unbound', {
          account: binding.account,
          role: binding.role,
        })
      } catch (error) {
        mark(key)
        throw error
      }
    })
  }

  const permissions = catalog.data ?? []
  // The (resource, action) -> permission-code lookup both the row label and
  // the revoke use: a grant row carries the pair the engine matches on,
  // while the grant route names a permission by its code, so the catalog
  // maps the pair back to the code that names it. A pair the catalog does
  // not name renders as `resource/action` and offers no revoke (see
  // buildGrantCodeIndex).
  const codeByPair = useMemo(() => buildGrantCodeIndex(permissions), [permissions])

  const roleRows = roles.data
  // A builtin role refuses every write -- role rewrite and delete, and
  // grant/revoke -- but not the reads. The selected role therefore opens
  // read-only: the grants it holds are visible, and every write control
  // that would name it is withheld.
  const selectedRoleBuiltin =
    roleRows?.some((role) => role.role === selectedRole && role.builtin) ?? false

  const roleColumns: DataTableColumn<ConsoleRole>[] = [
    {
      id: 'role',
      header: t('permissions.roles.column.role'),
      cellWrap: 'nowrap',
      cellFullValue: (role) => role.role,
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
      priority: 'low',
      cellWrap: 'nowrap',
      cellFullValue: (role) =>
        role.description_key === '' ? undefined : role.description_key,
      // The key names an entry in the host's own bundle, so only the host
      // can render it; without a resolver the raw key stands rather than an
      // empty cell.
      cell: (role) =>
        role.description_key === ''
          ? ''
          : (describeRole?.(role.description_key, role) ?? role.description_key),
    },
    {
      id: 'actions',
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
        return (
          <Box>
            {role.builtin ? (
              <Box
                sx={{
                  display: 'flex',
                  flexWrap: 'wrap',
                  alignItems: 'center',
                  columnGap: 1,
                  rowGap: 1,
                }}
              >
                <Button
                  type="button"
                  size="small"
                  variant="outlined"
                  aria-label={t('permissions.roles.viewGrantsAria', {
                    role: role.role,
                  })}
                  disabled={grantsFeedback.pending}
                  onClick={() => selectRole(role.role)}
                >
                  {t('permissions.roles.viewGrants')}
                </Button>
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
                <Button
                  type="button"
                  size="small"
                  variant="outlined"
                  aria-label={t('permissions.roles.manageGrantsAria', {
                    role: role.role,
                  })}
                  disabled={grantsFeedback.pending}
                  onClick={() => selectRole(role.role)}
                >
                  {t('permissions.roles.manageGrants')}
                </Button>
                <Button
                  type="button"
                  size="small"
                  variant="outlined"
                  color="error"
                  aria-label={t('permissions.roles.removeAria', {
                    role: role.role,
                  })}
                  aria-invalid={failed}
                  aria-describedby={failed ? errorId : undefined}
                  disabled={roleFeedback.pending}
                  loading={roleFeedback.pending}
                  onClick={() => requestRemoveRole(role)}
                >
                  {t('permissions.roles.remove')}
                </Button>
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

  // A grant row shows the same permission code the catalog lists, so "what
  // is granted" and "what the catalog declares" read as one vocabulary. A
  // pair the catalog does not name falls back to the pair's own spelling,
  // and its revoke is withheld (the route names a permission by its code).
  const grantLabel = (grant: ConsoleGrant): string =>
    codeByPair(grant.resource, grant.action) ??
    `${grant.resource}/${grant.action}`

  const grantBaseColumns: DataTableColumn<ConsoleGrant>[] = [
    {
      id: 'permission',
      header: t('permissions.grants.column.permission'),
      cellWrap: 'nowrap',
      cellFullValue: grantLabel,
      cell: (grant) => grantLabel(grant),
    },
  ]
  // A builtin role's grants are read-only: the revoke column is not part of
  // its table, and the grant form above it is withheld as well.
  const grantRevokeColumn: DataTableColumn<ConsoleGrant> = {
    id: 'actions',
    header: t('permissions.grants.column.actions'),
    cellWrap: 'nowrap',
    cell: (grant) => {
      const code = codeByPair(grant.resource, grant.action)
      const key = `${grant.resource}:${grant.action}`
      const failed = failedGrant === key
      const errorId = `permissions-grant-error-${encodeURIComponent(key)}`
      return (
        <Box>
          <Button
            type="button"
            size="small"
            variant="outlined"
            color="error"
            aria-label={t('permissions.grants.revokeAria', {
              permission: grantLabel(grant),
            })}
            aria-invalid={failed}
            aria-describedby={failed ? errorId : undefined}
            disabled={grantsFeedback.pending || code === null}
            loading={grantsFeedback.pending}
            onClick={() => {
              if (code !== null) {
                revoke(grant, code)
              }
            }}
          >
            {t('permissions.grants.revoke')}
          </Button>
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
  const grantColumns = selectedRoleBuiltin
    ? grantBaseColumns
    : [...grantBaseColumns, grantRevokeColumn]

  const catalogColumns: DataTableColumn<ConsolePermission>[] = [
    {
      id: 'permission',
      header: t('permissions.catalog.column.permission'),
      // The code is the column's unbroken primary text; the description
      // beneath it wraps, so the cell's nowrap policy stops at the code.
      cellWrap: 'nowrap',
      cellFullValue: (entry) => entry.permission,
      cell: (entry) => <PermissionLabel entry={entry} />,
    },
  ]

  // Both binding tables render the same columns, but each row's result
  // belongs to that table's own scope and marker.
  const bindingColumns = (
    lookup: boolean,
  ): DataTableColumn<ConsoleBinding>[] => {
    const failedKey = lookup ? failedLookupBinding : failedBinding
    const failureText = lookup ? lookupFailureText : bindingsFailureText
    const feedback = lookup ? lookupFeedback : bindingsFeedback
    return [
      {
        id: 'account',
        header: t('permissions.bindings.column.account'),
        cellWrap: 'nowrap',
        cellFullValue: (binding) => binding.account,
        // The account is the row's identity: the shared primary cap keeps
        // the recorded identifier -- which the refused lookup renders in
        // full -- inside the sm floor alongside the row actions.
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
        header: t('permissions.bindings.column.actions'),
        cellWrap: 'nowrap',
        cell: (binding) => {
          const failed = failedKey === bindingKey(binding)
          const errorId = bindingErrorId(
            lookup ? 'lookup' : 'bindings',
            binding,
          )
          return (
            <Box>
              <UnbindButton
                binding={binding}
                disabled={feedback.pending}
                invalid={failed}
                describedBy={errorId}
                onClick={() => unbind(binding, lookup)}
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
      <PageHeader
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
          <SectionCard title={t('permissions.roles.title')} sx={{ mb: 3 }}>
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
                value={roleDescription}
                onChange={(event) => setRoleDescription(event.target.value)}
                label={t('permissions.roles.create.descriptionLabel')}
              />
              <Button
                type="button"
                variant="contained"
                disabled={
                  roleFeedback.pending || roleName.trim() === ''
                }
                loading={roleFeedback.pending}
                onClick={saveRole}
              >
                {t('permissions.roles.create.submit')}
              </Button>
            </Box>

            <ConsoleAsyncSection
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
            </ConsoleAsyncSection>
            <Box ref={roleRegionRef}>
              <WriteFeedbackRegion outcome={roleFeedback.outcome} />
            </Box>
          </SectionCard>

          <SectionCard title={t('permissions.grants.title')} sx={{ mb: 3 }}>
            {selectedRole === '' ? (
              <Typography variant="body2" color="text.secondary">
                {t('permissions.grants.pickRole')}
              </Typography>
            ) : (
              <>
                <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
                  {t('permissions.grants.forRole', { role: selectedRole })}
                </Typography>
                {selectedRoleBuiltin ? (
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
                          // The options carry the description beneath the
                          // code; the closed control shows the chosen code
                          // alone, so the field keeps its single-line height
                          // and the code stays the primary text.
                          renderValue: (value) => value as string,
                        },
                      }}
                    >
                      {permissions.map((entry) => (
                        <MenuItem key={entry.permission} value={entry.permission}>
                          <PermissionLabel entry={entry} />
                        </MenuItem>
                      ))}
                    </TextField>
                    <Button
                      type="button"
                      variant="contained"
                      disabled={
                        grantsFeedback.pending || grantPermission === ''
                      }
                      loading={grantsFeedback.pending}
                      onClick={grant}
                    >
                      {t('permissions.grants.add')}
                    </Button>
                  </Box>
                )}
                <ConsoleAsyncSection
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
                </ConsoleAsyncSection>
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
            <ConsoleAsyncSection
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
              {(settled) => (
                <DataTable
                  rows={settled}
                  columns={catalogColumns}
                  rowKey={(entry) => entry.permission}
                />
              )}
            </ConsoleAsyncSection>
          </SectionCard>

          <SectionCard title={t('permissions.bindings.title')} sx={{ mb: 3 }}>
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
                disabled={bindingsFeedback.pending}
              />
              <Button
                type="button"
                variant="contained"
                disabled={
                  bindingsFeedback.pending ||
                  bindRoleName.trim() === '' ||
                  bindAccount === null
                }
                loading={bindingsFeedback.pending}
                onClick={bind}
              >
                {t('permissions.bindings.bind')}
              </Button>
            </Box>
            <ConsoleAsyncSection
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
            </ConsoleAsyncSection>
            <Box ref={bindingsRegionRef}>
              <WriteFeedbackRegion outcome={bindingsFeedback.outcome} />
            </Box>
          </SectionCard>

          <SectionCard title={t('permissions.lookup.title')} sx={{ mb: 3 }}>
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
              <Button
                type="button"
                variant="outlined"
                disabled={
                  lookupAccount === null ||
                  accountBindings.isFetching ||
                  lookupFeedback.pending
                }
                loading={accountBindings.isFetching}
                onClick={applyLookup}
              >
                {t('permissions.lookup.apply')}
              </Button>
            </Box>
            {lookupApplied !== '' && (
              <ConsoleAsyncSection
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
              </ConsoleAsyncSection>
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

/**
 * Builds the (resource, action) -> permission-code lookup the revoke uses,
 * from the loaded permission catalog.
 *
 * A grant route names a permission by its CODE (`PUT`/`DELETE .../grants/
 * :permission`, e.g. authn.account.create): the handler translates the code
 * to the (resource, action) pair the engine matches on (pkg/authz/admin's
 * grant handlers). A grant row carries that pair; the page renders the row
 * as the pair's code and the revoke names the permission by that code --
 * and the page's own source of the codes is the permission catalog it
 * already loads.
 *
 * A code's trailing two dot-separated segments spell its pair
 * (authn.account.create -> account/create; a bare module code like
 * audit.read -> audit/read), so those build the primary lookup. Where the
 * pair's resource is not the code's second-to-last segment (pkg/authz's
 * translation table is the authority and is not always a suffix of the
 * code, e.g. authn.mfa.reset -> factor/reset), the pair is matched by its
 * action alone, and only when the catalog declares that action once; an
 * action two pairs share (read, manage, ...) stays ambiguous and resolves
 * to null, so the revoke is not offered a wrong code.
 *
 * Returns a function; a pair the catalog does not name resolves to null.
 */
function buildGrantCodeIndex(
  permissions: readonly ConsolePermission[],
): (resource: string, action: string) => string | null {
  const byPair = new Map<string, string>()
  // One code per action, or null once a second code shares the action.
  const byAction = new Map<string, string | null>()
  for (const entry of permissions) {
    const segments = entry.permission.split('.')
    const action = segments[segments.length - 1] ?? ''
    if (action === '') {
      continue
    }
    if (segments.length >= 2) {
      const resource = segments[segments.length - 2] ?? ''
      if (resource !== '') {
        byPair.set(`${resource}:${action}`, entry.permission)
      }
    }
    byAction.set(action, byAction.has(action) ? null : entry.permission)
  }
  return (resource, action) => {
    const pair = byPair.get(`${resource}:${action}`)
    if (pair !== undefined) {
      return pair
    }
    return byAction.get(action) ?? null
  }
}
