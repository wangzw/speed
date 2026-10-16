/**
 * TenantScope: the tenant selector the tenant-addressed administration
 * pages carry.
 *
 * Some administration surfaces address a tenant as a route segment
 * (/authz/admin/tenants/:tenant/..., the invitation and membership
 * routes, the SSO provider routes). Those pages are still platform
 * surfaces -- the operator is platform staff -- but each request names
 * the tenant it acts on, and the tenant is an argument of the request,
 * not the session's current tenant (the console has none; switching the
 * current tenant is tenancy-ui's, a different shell).
 *
 * The selector lists the platform roster (GET /tenant/admin/tenants, the
 * same read the roster page owns) so the operator picks a registered
 * tenant by its alias rather than typing an identifier. The value is the
 * chosen tenant id, or '' for none chosen yet; a page holds it in its own
 * state and disables its tenant-scoped reads while it is empty.
 */

import MenuItem from '@mui/material/MenuItem'
import TextField from '@mui/material/TextField'
import { useConsoleUiTranslation } from './translation.js'
import { useConsoleOperations } from '../operations.js'

export interface TenantScopeProps {
  /** The chosen tenant id, or '' when none is chosen. */
  readonly value: string
  /** Reports the chosen tenant id ('' clears the selection). */
  readonly onSelect: (tenant: string) => void
}

/** The tenant selector, fed by the platform roster read. */
export function TenantScope({ value, onSelect }: TenantScopeProps) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const tenants = operations.useTenants()
  const options = tenants.data ?? []

  return (
    <TextField
      select
      size="small"
      label={t('common.tenant')}
      value={value}
      onChange={(event) => onSelect(event.target.value)}
      sx={{ minWidth: 240 }}
    >
      <MenuItem value="">{t('common.tenantNone')}</MenuItem>
      {options.map((tenant) => (
        <MenuItem key={tenant.id} value={tenant.id}>
          {tenant.alias === '' ? tenant.id : tenant.alias}
        </MenuItem>
      ))}
    </TextField>
  )
}
