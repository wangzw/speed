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
 *
 * A tenant-addressed page also opens on a tenant named by the `tenant`
 * query parameter, so a link can carry the tenant across pages (the
 * tenant detail page's related-page links do). The parameter seeds the
 * page's initial state only; the selector remains the way to change it
 * afterwards.
 */

import MenuItem from '@mui/material/MenuItem'
import TextField from '@mui/material/TextField'
import { useConsoleUiTranslation } from './translation.js'
import { useConsoleOperations } from '../operations.js'

/** The query parameter a tenant-addressed page reads its tenant from. */
export const TENANT_QUERY_PARAM = 'tenant'

/** The tenant id a query string names, or '' when the parameter is absent. */
export function tenantFromSearch(search: string): string {
  return new URLSearchParams(search).get(TENANT_QUERY_PARAM)?.trim() ?? ''
}

/** The tenant a page opens on from the current URL; '' outside a browser. */
export function tenantFromLocation(): string {
  return typeof window === 'undefined'
    ? ''
    : tenantFromSearch(window.location.search)
}

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
  // A value seeded from the query string shows before the roster read
  // settles (and stays shown if the roster does not list it), so the
  // selector never blanks a tenant a link carried in; once the roster
  // answers, the id and its alias are the same entry.
  const listed = options.some((tenant) => tenant.id === value)

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
      {value !== '' && !listed && <MenuItem value={value}>{value}</MenuItem>}
      {options.map((tenant) => (
        <MenuItem key={tenant.id} value={tenant.id}>
          {tenant.alias === '' ? tenant.id : tenant.alias}
        </MenuItem>
      ))}
    </TextField>
  )
}
