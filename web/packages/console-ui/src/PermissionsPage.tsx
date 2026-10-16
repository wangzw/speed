/**
 * PermissionsPage: the authorization-administration page of the console.
 *
 * The authorization surface is tenant-scoped: the roles, their grants and
 * the account bindings all live under one tenant
 * (/authz/admin/tenants/:tenant/...). The page therefore opens with a
 * tenant selector (fed by the platform roster) and keeps every read
 * disabled until a tenant is chosen; the tenant is an argument of the
 * request, not the session's current tenant.
 *
 * The page renders the surface in five sections:
 *
 *  - the role catalog: list a tenant's roles, create or rewrite one, and
 *    remove one (removal revokes its grants and bindings with it);
 *  - the grants of the selected role: list them, grant a permission from
 *    the catalog and revoke one;
 *  - the permission catalog: every permission the platform declares, for
 *    reference when granting;
 *  - the tenant's bindings: list them, bind a role to an account and
 *    unbind one;
 *  - one account's held bindings, looked up by account id.
 *
 * Every read and write is an operation on the console operations bundle,
 * so the page builds no client and writes no query key. A failed list
 * settles into a retryable placeholder; a failed write renders the
 * answer's code text in one alert, resolved through the console-ui
 * namespace's whitelist. The page opens with ui-kit's PageHeader.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Checkbox from '@mui/material/Checkbox'
import FormControlLabel from '@mui/material/FormControlLabel'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import {
  ConfirmDialog,
  DataTable,
  EmptyState,
  ListSkeleton,
  PageHeader,
  errorCodeOf,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { ConsoleAsyncSection } from './internal/async-section.js'
import { InlineError } from './internal/inline-error.js'
import { TenantScope } from './internal/tenant-scope.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type {
  ConsoleBinding,
  ConsoleGrant,
  ConsolePermission,
  ConsoleRole,
} from './operations.js'

/** A section heading; the page's sections sit one level under the h1. */
function SectionHeading({ children }: { readonly children: string }) {
  return (
    <Typography component="h2" variant="h2" sx={{ fontSize: '1.25rem', mb: 1 }}>
      {children}
    </Typography>
  )
}

export function PermissionsPage() {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()

  const [tenant, setTenant] = useState('')
  const [selectedRole, setSelectedRole] = useState('')

  // Role create/rewrite draft.
  const [roleName, setRoleName] = useState('')
  const [roleDescription, setRoleDescription] = useState('')
  const [roleBuiltin, setRoleBuiltin] = useState(false)

  // Grant draft (the permission to grant to the selected role).
  const [grantPermission, setGrantPermission] = useState('')

  // Binding draft.
  const [bindAccount, setBindAccount] = useState('')
  const [bindRoleName, setBindRoleName] = useState('')

  // The account whose held bindings are looked up.
  const [lookupAccount, setLookupAccount] = useState('')
  const [lookupApplied, setLookupApplied] = useState('')

  // The destructive action a confirm dialog guards.
  const [pendingRoleDelete, setPendingRoleDelete] = useState<string | null>(null)

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

  const permissions = catalog.data ?? []

  const roleColumns: DataTableColumn<ConsoleRole>[] = [
    {
      id: 'role',
      header: t('permissions.roles.column.role'),
      cell: (role) => <Typography variant="body2">{role.role}</Typography>,
    },
    {
      id: 'builtin',
      header: t('permissions.roles.column.builtin'),
      priority: 'medium',
      cell: (role) =>
        role.builtin ? t('permissions.roles.builtinYes') : t('permissions.roles.builtinNo'),
    },
    {
      id: 'description',
      header: t('permissions.roles.column.description'),
      priority: 'low',
      cell: (role) => role.description_key,
    },
    {
      id: 'actions',
      header: t('permissions.roles.column.actions'),
      cell: (role) => (
        <Box sx={{ display: 'flex', flexWrap: 'wrap', columnGap: 1, rowGap: 1 }}>
          <Button
            type="button"
            size="small"
            variant="outlined"
            onClick={() => setSelectedRole(role.role)}
          >
            {t('permissions.roles.manageGrants')}
          </Button>
          <Button
            type="button"
            size="small"
            variant="outlined"
            color="error"
            onClick={() => setPendingRoleDelete(role.role)}
          >
            {t('permissions.roles.remove')}
          </Button>
        </Box>
      ),
    },
  ]

  const grantColumns: DataTableColumn<ConsoleGrant>[] = [
    {
      id: 'resource',
      header: t('permissions.grants.column.resource'),
      cell: (grant) => grant.resource,
    },
    {
      id: 'action',
      header: t('permissions.grants.column.action'),
      cell: (grant) => grant.action,
    },
    {
      id: 'actions',
      header: t('permissions.grants.column.actions'),
      cell: (grant) => (
        <Button
          type="button"
          size="small"
          variant="outlined"
          color="error"
          disabled={revokePermission.isPending}
          onClick={() =>
            revokePermission.mutate({
              tenant,
              role: selectedRole,
              permission: grantPermissionString(grant),
            })
          }
        >
          {t('permissions.grants.revoke')}
        </Button>
      ),
    },
  ]

  const catalogColumns: DataTableColumn<ConsolePermission>[] = [
    {
      id: 'permission',
      header: t('permissions.catalog.column.permission'),
      cell: (entry) => entry.permission,
    },
  ]

  const bindingColumns: DataTableColumn<ConsoleBinding>[] = [
    {
      id: 'account',
      header: t('permissions.bindings.column.account'),
      cell: (binding) => binding.account,
    },
    {
      id: 'role',
      header: t('permissions.bindings.column.role'),
      cell: (binding) => binding.role,
    },
    {
      id: 'actions',
      header: t('permissions.bindings.column.actions'),
      cell: (binding) => (
        <Button
          type="button"
          size="small"
          variant="outlined"
          color="error"
          disabled={unbindRole.isPending}
          onClick={() =>
            unbindRole.mutate({
              tenant,
              account: binding.account,
              role: binding.role,
            })
          }
        >
          {t('permissions.bindings.unbind')}
        </Button>
      ),
    },
  ]

  const roleRows = roles.data
  const rolesPending = roles.isPending && roleRows === undefined

  const writeErrorCode = firstErrorCode([
    writeRole.error,
    grantPermissionMutation.error,
    revokePermission.error,
    bindRole.error,
    unbindRole.error,
    removeRole.error,
  ])

  return (
    <Box>
      <PageHeader
        title={t('permissions.title')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('permissions.title') },
        ]}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            onClick={() => void roles.refetch()}
          >
            {t('common.refresh')}
          </Button>
        }
        sx={{ mb: 2 }}
      />

      <Box component="section" sx={{ mb: 3 }}>
        <TenantScope value={tenant} onSelect={setTenant} />
      </Box>

      {tenant === '' ? (
        <EmptyState
          headingLevel="h2"
          title={t('permissions.needTenant.title')}
          description={t('permissions.needTenant.description')}
        />
      ) : (
        <>
          <Box component="section" sx={{ mb: 3 }}>
            <SectionHeading>{t('permissions.roles.title')}</SectionHeading>
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
              <FormControlLabel
                control={
                  <Checkbox
                    checked={roleBuiltin}
                    onChange={(event) => setRoleBuiltin(event.target.checked)}
                  />
                }
                label={t('permissions.roles.create.builtinLabel')}
              />
              <Button
                type="button"
                variant="contained"
                disabled={writeRole.isPending}
                onClick={() => {
                  writeRole.mutate(
                    {
                      tenant,
                      role: roleName,
                      builtin: roleBuiltin,
                      descriptionKey: roleDescription,
                    },
                    { onSuccess: () => setRoleName('') },
                  )
                }}
              >
                {t('permissions.roles.create.submit')}
              </Button>
            </Box>

            <ConsoleAsyncSection
              pending={rolesPending}
              payload={roleRows}
              error={roles.error}
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
          </Box>

          <Box component="section" sx={{ mb: 3 }}>
            <SectionHeading>{t('permissions.grants.title')}</SectionHeading>
            {selectedRole === '' ? (
              <Typography variant="body2" color="text.secondary">
                {t('permissions.grants.pickRole')}
              </Typography>
            ) : (
              <>
                <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
                  {t('permissions.grants.forRole', { role: selectedRole })}
                </Typography>
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
                  >
                    {permissions.map((entry) => (
                      <MenuItem key={entry.permission} value={entry.permission}>
                        {entry.permission}
                      </MenuItem>
                    ))}
                  </TextField>
                  <Button
                    type="button"
                    variant="contained"
                    disabled={grantPermissionMutation.isPending || grantPermission === ''}
                    onClick={() => {
                      grantPermissionMutation.mutate(
                        { tenant, role: selectedRole, permission: grantPermission },
                        { onSuccess: () => setGrantPermission('') },
                      )
                    }}
                  >
                    {t('permissions.grants.add')}
                  </Button>
                </Box>
                <ConsoleAsyncSection
                  pending={grants.isPending && grants.data === undefined}
                  payload={grants.data}
                  error={grants.error}
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
              </>
            )}
          </Box>

          <Box component="section" sx={{ mb: 3 }}>
            <SectionHeading>{t('permissions.catalog.title')}</SectionHeading>
            <ConsoleAsyncSection
              pending={catalog.isPending && catalog.data === undefined}
              payload={catalog.data}
              error={catalog.error}
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
          </Box>

          <Box component="section" sx={{ mb: 3 }}>
            <SectionHeading>{t('permissions.bindings.title')}</SectionHeading>
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
                size="small"
                value={bindAccount}
                onChange={(event) => setBindAccount(event.target.value)}
                label={t('permissions.bindings.accountLabel')}
              />
              <TextField
                size="small"
                value={bindRoleName}
                onChange={(event) => setBindRoleName(event.target.value)}
                label={t('permissions.bindings.roleLabel')}
              />
              <Button
                type="button"
                variant="contained"
                disabled={bindDisabled(bindRoleName, bindAccount)}
                onClick={() => {
                  bindRole.mutate(
                    { tenant, account: bindAccount, role: bindRoleName },
                    { onSuccess: () => setBindRoleName('') },
                  )
                }}
              >
                {t('permissions.bindings.bind')}
              </Button>
            </Box>
            <ConsoleAsyncSection
              pending={bindings.isPending && bindings.data === undefined}
              payload={bindings.data}
              error={bindings.error}
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
                  columns={bindingColumns}
                  rowKey={(binding) => `${binding.account}:${binding.role}`}
                />
              )}
            </ConsoleAsyncSection>
          </Box>

          <Box component="section" sx={{ mb: 3 }}>
            <SectionHeading>{t('permissions.lookup.title')}</SectionHeading>
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
                size="small"
                value={lookupAccount}
                onChange={(event) => setLookupAccount(event.target.value)}
                label={t('permissions.lookup.accountLabel')}
              />
              <Button
                type="button"
                variant="outlined"
                onClick={() => setLookupApplied(lookupAccount.trim())}
              >
                {t('permissions.lookup.apply')}
              </Button>
            </Box>
            {lookupApplied !== '' && (
              <DataTable
                rows={accountBindings.data ?? []}
                columns={bindingColumns}
                rowKey={(binding) => `${binding.account}:${binding.role}`}
              />
            )}
          </Box>

          <InlineError code={writeErrorCode} />

          <ConfirmDialog
            open={pendingRoleDelete !== null}
            variant="danger"
            title={t('permissions.roles.removeTitle')}
            message={t('permissions.roles.removeMessage', {
              role: pendingRoleDelete ?? '',
            })}
            confirmLabel={t('permissions.roles.removeConfirm')}
            cancelLabel={t('common.cancel')}
            confirmLoading={removeRole.isPending}
            onConfirm={() => {
              if (pendingRoleDelete !== null) {
                removeRole.mutate(
                  { tenant, role: pendingRoleDelete },
                  {
                    onSuccess: () => {
                      if (selectedRole === pendingRoleDelete) {
                        setSelectedRole('')
                      }
                      setPendingRoleDelete(null)
                    },
                  },
                )
              }
            }}
            onCancel={() => setPendingRoleDelete(null)}
          />
        </>
      )}
    </Box>
  )
}

/** The permission string a grant names: resource and action joined. */
function grantPermissionString(grant: ConsoleGrant): string {
  return `${grant.resource}:${grant.action}`
}

/** The bind button stays disabled until both the account and the role are named. */
function bindDisabled(role: string, account: string): boolean {
  return role.trim() === '' || account.trim() === ''
}

/** The first defined error's code, or null when every error is absent. */
function firstErrorCode(errors: readonly unknown[]): string | null {
  for (const error of errors) {
    if (error !== null && error !== undefined) {
      return errorCodeOf(error)
    }
  }
  return null
}
