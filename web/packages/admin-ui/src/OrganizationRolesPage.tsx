/**
 * OrganizationRolesPage: the organization administrator's roles and
 * permissions page.
 *
 * The page owns the role itself and nothing that changes a member's
 * membership: it creates roles, edits their free-text descriptions,
 * removes them behind the heavier two-step confirmation, manages a
 * selected role's grants, and shows who holds the role read-only. Binding
 * and unbinding a role to an account belongs to the members page.
 *
 * The sections run top to bottom in the order the operator works in:
 *
 *  - the role catalog: roles, their descriptions, permission and active
 *    member counts, the builtin mark, and the row actions;
 *  - the selected role's grants: the readable permission name with the
 *    code beneath it, a visible high-risk badge, grant and revoke;
 *  - the selected role's usage: the active-member count, the holders with
 *    cursor paging, and -- only for a caller who may manage memberships --
 *    an entry into the members page filtered to this role;
 *  - the permission catalog: a collapsed reference area that expands into
 *    a grouped, searchable list.
 *
 * `can_manage` on the role rows is the server's answer to whether the
 * caller may write; a false answer hides every write control rather than
 * rendering a disabled one. The host's protected role names are locked the
 * way a builtin role is. A dangerous permission is granted only after the
 * confirmation dialog's deliberate second click.
 *
 * Every read and write runs through the organization operations bundle,
 * so the page builds no client and writes no query key.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Skeleton from '@mui/material/Skeleton'
import { usePermission } from '@speed/auth-core'
import {
  ListSkeleton,
  SectionCard,
  errorCodeOf,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AdminPageHeader } from './internal/page-header.js'
import { AdminAsyncSection } from './internal/async-section.js'
import { AccountsIcon, PermissionsIcon } from './internal/icons.js'
import { PendingConfirmDialog, usePendingConfirm } from './internal/confirm.js'
import { useAdminUiErrorText } from './internal/error-text.js'
import { adminRouteById, useAdminRouteTable } from './internal/route-table.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  failureTextOf,
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useOrganizationAdminOperations } from './organization-operations.js'
import type {
  AdminTenantGrant,
  AdminTenantPermission,
  AdminTenantRole,
} from './organization-operations.js'
import { RoleCreateForm } from './internal/roles/RoleCreateForm.js'
import type { RoleCreateDraft } from './internal/roles/RoleCreateForm.js'
import { RoleDescriptionPanel } from './internal/roles/RoleDescriptionPanel.js'
import { RoleGrantsSection } from './internal/roles/RoleGrantsSection.js'
import { RoleTable } from './internal/roles/RoleTable.js'
import { RoleUsageSection } from './internal/roles/RoleUsageSection.js'
import { PermissionCatalogReference } from './internal/roles/PermissionCatalogReference.js'

/** The holder page size the usage read asks for. */
const USAGE_PAGE_SIZE = 50
/** The invitation/membership declaration the usage entry links to. */
const INVITATIONS_ROUTE_ID = 'organization-invitations'

export interface OrganizationRolesPageProps {
  /** The current tenant every read and write names. */
  readonly tenant: string
  /** The role names the host protects from writes. */
  readonly protectedRoles?: readonly string[]
}

/** The cursor frame of the usage list, tagged with the role it belongs to. */
interface UsagePageFrame {
  readonly tenant: string
  readonly role: string
  readonly cursors: readonly string[]
  readonly page: number
}

function firstUsagePage(tenant: string, role: string): UsagePageFrame {
  return { tenant, role, cursors: [''], page: 0 }
}

/** Why a role's grants are read-only, or null when they are writable. */
function grantsLockNote(
  role: AdminTenantRole,
  protectedRoles: readonly string[],
  t: (key: string) => string,
): string | null {
  if (role.builtin) {
    return t('permissions.grants.builtinReadOnly')
  }
  if (protectedRoles.includes(role.role)) {
    return t('permissions.grants.protectedReadOnly')
  }
  if (!role.can_manage) {
    return t('permissions.grants.noManageReadOnly')
  }
  return null
}

export function OrganizationRolesPage({
  tenant,
  protectedRoles = [],
}: OrganizationRolesPageProps) {
  const { t } = useAdminUiTranslation()
  const operations = useOrganizationAdminOperations()
  const routes = useAdminRouteTable()
  const resolveErrorText = useAdminUiErrorText()

  const [selectedRole, setSelectedRole] = useState('')
  const selectedRoleRef = useRef(selectedRole)
  useEffect(() => {
    selectedRoleRef.current = selectedRole
  }, [selectedRole])

  const [editingRole, setEditingRole] = useState<AdminTenantRole | undefined>(
    undefined,
  )
  const [descriptionFailed, setDescriptionFailed] = useState<string | null>(
    null,
  )
  const [catalogExpanded, setCatalogExpanded] = useState(false)
  const [failedRole, setFailedRole] = useState<string | null>(null)
  const [failedGrant, setFailedGrant] = useState<string | null>(null)
  const [usageFrame, setUsageFrame] = useState<UsagePageFrame>(() =>
    firstUsagePage(tenant, ''),
  )

  const roleFeedback = useWriteFeedback()
  const grantsFeedback = useWriteFeedback()
  const roleRegionRef = useFailureScroll(roleFeedback.outcome)
  const grantsRegionRef = useFailureScroll(grantsFeedback.outcome)
  const confirm = usePendingConfirm()

  // The caller's session permission sets fail closed: no session, a
  // pending read or an absent set reads as no permission. The server is
  // still the authority; these gates only decide which controls render.
  const sessionCanManageRoles = usePermission('tenant', 'authz.tenant.role.manage')
  const sessionCanManageMembers = usePermission(
    'tenant',
    'authn.membership.manage',
  )

  const roles = operations.useTenantRoles(tenant)
  const catalog = operations.useTenantPermissions(tenant)
  const selectedRoleRow = roles.data?.find(
    (role) => role.role === selectedRole,
  )
  const grants = operations.useTenantGrants(
    tenant,
    selectedRole === '' ? undefined : selectedRole,
  )

  const usageCurrent =
    usageFrame.tenant === tenant && usageFrame.role === selectedRole
      ? usageFrame
      : firstUsagePage(tenant, selectedRole)
  const usageCursor = usageCurrent.cursors[usageCurrent.page] ?? ''
  const roleBindings = operations.useTenantRoleBindings(
    tenant,
    selectedRole === '' ? undefined : selectedRole,
    {
      limit: USAGE_PAGE_SIZE,
      ...(usageCursor === '' ? {} : { after: usageCursor }),
    },
  )

  const writeRole = operations.useTenantWriteRole()
  const removeRole = operations.useTenantRemoveRole()
  const grantPermission = operations.useTenantGrantPermission()
  const revokePermission = operations.useTenantRevokePermission()

  const roleFailureText = failureTextOf(roleFeedback.outcome, resolveErrorText)
  const grantsFailureText = failureTextOf(
    grantsFeedback.outcome,
    resolveErrorText,
  )
  const roleRows = roles.data
  // Creating the first role of a tenant has no row to read `can_manage`
  // from, so the session's tenant set covers that one case; every other
  // decision follows the server's per-row answer.
  const canManage =
    roleRows === undefined
      ? false
      : roleRows.some((role) => role.can_manage) ||
        (roleRows.length === 0 && sessionCanManageRoles)

  const invitationsRoute = adminRouteById(routes, INVITATIONS_ROUTE_ID)
  const memberHref =
    sessionCanManageMembers &&
    invitationsRoute !== undefined &&
    selectedRole !== ''
      ? `${invitationsRoute.path}${
          invitationsRoute.path.includes('?') ? '&' : '?'
        }role=${encodeURIComponent(selectedRole)}`
      : undefined

  const resetPageFeedback = useCallback(() => {
    roleFeedback.reset()
    grantsFeedback.reset()
    setFailedRole(null)
    setFailedGrant(null)
    setDescriptionFailed(null)
  }, [roleFeedback.reset, grantsFeedback.reset])

  // A controlled tenant arrives as a prop: every selection, draft and
  // result names the tenant being left, so they are cleared with it.
  useEffect(() => {
    resetPageFeedback()
    setSelectedRole('')
    setEditingRole(undefined)
    setUsageFrame(firstUsagePage(tenant, ''))
  }, [tenant, resetPageFeedback])

  const selectRole = (role: string) => {
    setSelectedRole(role)
    grantsFeedback.reset()
    setFailedGrant(null)
    setDescriptionFailed(null)
    setUsageFrame(firstUsagePage(tenant, role))
  }

  const createRole = async (draft: RoleCreateDraft): Promise<boolean> => {
    let landed = false
    await roleFeedback.run(async () => {
      await writeRole.mutateAsync({
        tenant,
        role: draft.role,
        description: draft.description,
      })
      landed = true
      return t('permissions.roles.saved', { role: draft.role })
    })
    return landed
  }

  const saveDescription = (description: string) => {
    const role = editingRole
    if (role === undefined) {
      return
    }
    setDescriptionFailed(null)
    void roleFeedback.run(async () => {
      try {
        await writeRole.mutateAsync({
          tenant,
          role: role.role,
          description,
        })
        setEditingRole(undefined)
        return t('permissions.roles.description.saved', { role: role.role })
      } catch (error) {
        setDescriptionFailed(resolveErrorText(errorCodeOf(error)))
        throw error
      }
    })
  }

  const requestRemove = (role: AdminTenantRole) => {
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
              setUsageFrame(firstUsagePage(tenant, ''))
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

  const grant = (entry: AdminTenantPermission) => {
    const run = () => {
      setFailedGrant(null)
      void grantsFeedback.run(async () => {
        await grantPermission.mutateAsync({
          tenant,
          role: selectedRole,
          permission: entry.permission,
        })
        return t('permissions.grants.granted', {
          permission: entry.permission,
          role: selectedRole,
        })
      })
    }
    if (entry.dangerous) {
      confirm.ask({
        title: t('permissions.grants.dangerousTitle'),
        message: t('permissions.grants.dangerousMessage', {
          permission: entry.permission,
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

  const revoke = (grant: AdminTenantGrant) => {
    if (grant.permission === '') {
      return
    }
    const key = `${grant.resource}:${grant.action}`
    setFailedGrant(null)
    void grantsFeedback.run(async () => {
      try {
        await revokePermission.mutateAsync({
          tenant,
          role: selectedRole,
          permission: grant.permission,
        })
        return t('permissions.grants.revoked', {
          permission: grant.permission,
          role: selectedRole,
        })
      } catch (error) {
        setFailedGrant(key)
        throw error
      }
    })
  }

  const moveUsagePage = (delta: number, cursor?: string) => {
    setUsageFrame((previous) => {
      const current =
        previous.tenant === tenant && previous.role === selectedRole
          ? previous
          : firstUsagePage(tenant, selectedRole)
      if (delta < 0) {
        return current.page === 0
          ? current
          : { ...current, page: current.page - 1 }
      }
      const cursors = [...current.cursors]
      cursors[current.page + 1] = cursor ?? ''
      return {
        tenant,
        role: selectedRole,
        cursors,
        page: current.page + 1,
      }
    })
  }

  const rolesPending = roles.isPending && roleRows === undefined
  const roleLockNote =
    selectedRoleRow === undefined
      ? null
      : grantsLockNote(selectedRoleRow, protectedRoles, t)

  return (
    <Box>
      <AdminPageHeader
        title={t('organization.nav.roles')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('organization.nav.roles') },
        ]}
        sx={{ mb: 2 }}
      />

      <SectionCard
        title={t('permissions.roles.title')}
        icon={<PermissionsIcon size={24} />}
        sx={{ mb: 3 }}
      >
        {canManage && (
          <RoleCreateForm
            roles={roleRows ?? []}
            busy={roleFeedback.pending}
            onCreate={createRole}
          />
        )}
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
            <RoleTable
              roles={settled}
              selectedRole={selectedRole}
              protectedRoles={protectedRoles}
              failedRole={failedRole}
              failureText={roleFailureText}
              busy={roleFeedback.pending}
              onSelect={selectRole}
              onEditDescription={setEditingRole}
              onRemove={requestRemove}
            />
          )}
        </AdminAsyncSection>
        <Box ref={roleRegionRef}>
          <WriteFeedbackRegion outcome={roleFeedback.outcome} />
        </Box>
      </SectionCard>

      <SectionCard
        title={t('permissions.grants.title')}
        icon={<PermissionsIcon size={24} />}
        sx={{ mb: 3 }}
      >
        <RoleGrantsSection
          grants={grants}
          catalog={catalog.data ?? []}
          selectedRole={selectedRole}
          lockNote={roleLockNote}
          busy={grantsFeedback.pending}
          failureText={grantsFailureText}
          failedGrant={failedGrant}
          outcome={grantsFeedback.outcome}
          regionRef={grantsRegionRef}
          onGrant={grant}
          onRevoke={revoke}
        />
      </SectionCard>

      <SectionCard
        title={t('permissions.usage.title')}
        icon={<AccountsIcon size={24} />}
        sx={{ mb: 3 }}
      >
        <RoleUsageSection
          role={selectedRoleRow}
          bindings={roleBindings}
          page={usageCurrent.page}
          busy={false}
          memberHref={memberHref}
          onPrevious={() => moveUsagePage(-1)}
          onNext={() =>
            moveUsagePage(1, roleBindings.data?.next_cursor ?? '')
          }
        />
      </SectionCard>

      <PermissionCatalogReference
        catalog={catalog}
        expanded={catalogExpanded}
        onToggle={() => setCatalogExpanded((expanded) => !expanded)}
      />

      <RoleDescriptionPanel
        open={editingRole !== undefined}
        role={editingRole}
        saving={roleFeedback.pending}
        failureText={descriptionFailed}
        onSave={saveDescription}
        onClose={() => setEditingRole(undefined)}
        onExited={() => {
          setEditingRole(undefined)
          setDescriptionFailed(null)
        }}
      />

      <PendingConfirmDialog state={confirm} cancelLabel={t('common.cancel')} />
    </Box>
  )
}
