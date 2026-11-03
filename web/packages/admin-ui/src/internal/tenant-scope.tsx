/**
 * TenantScope: the tenant selector the tenant-addressed administration
 * pages carry.
 *
 * Some administration surfaces address a tenant as a route segment
 * (/api/v1/system/tenants/:tenant/..., the invitation and membership
 * routes, the SSO provider routes). Those pages are still platform
 * surfaces -- the operator is platform staff -- but each request names
 * the tenant it acts on, and the tenant is an argument of the request,
 * not the session's current tenant (the console has none; switching the
 * current tenant is tenancy-ui's, a different shell).
 *
 * The selector lists the platform roster (GET /api/v1/system/tenants, the
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
 *
 * The roster read behind the selector has three states beside its content,
 * told apart in the field's helper text: while it loads the selector is
 * disabled and says so; a 403 says the viewer may not list tenants and
 * offers no retry (retrying can only be refused again); any other failure
 * says the roster could not complete this request and offers a retry; an
 * empty roster says no tenant is registered. A tenant seeded from the URL
 * stays shown in every one of those states, so a carried link never
 * blanks its selection.
 */

import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import MenuItem from '@mui/material/MenuItem'
import TextField from '@mui/material/TextField'
import { isForbidden } from './async-section.js'
import { SelectorRow } from './selector-row.js'
import { useAdminUiTranslation } from './translation.js'
import { useAdminOperations } from '../operations.js'

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
  /**
   * Whether the selector is disabled. A page disables it while any scope
   * its tenant feeds has a write in flight: a change would reset those
   * scopes and leave a settling attempt describing the tenant left behind.
   */
  readonly disabled?: boolean
  /**
   * A control rendered in the selector's own row, to its right. The page
   * whose body is addressed by this selector puts its refresh here, next
   * to what the refresh re-reads.
   */
  readonly trailing?: ReactNode
}

/** The tenant selector, fed by the platform roster read. */
export function TenantScope({
  value,
  onSelect,
  disabled = false,
  trailing,
}: TenantScopeProps) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const tenants = operations.useTenants()
  const options = tenants.data ?? []
  // A value seeded from the query string shows before the roster read
  // settles (and stays shown if the roster does not list it), so the
  // selector never blanks a tenant a link carried in; once the roster
  // answers, the id and its alias are the same entry.
  const listed = options.some((tenant) => tenant.id === value)

  const loading = tenants.isPending
  const forbidden = isForbidden(tenants.error)
  const failed =
    !loading && !forbidden && tenants.error !== null && tenants.error !== undefined
  const empty = tenants.data !== undefined && tenants.data.length === 0

  let helperText: ReactNode
  if (loading) {
    helperText = t('common.tenantLoading')
  } else if (forbidden) {
    helperText = t('common.tenantForbidden')
  } else if (failed) {
    helperText = (
      <Box component="span">
        {t('common.tenantLoadFailed')}{' '}
        <Button
          type="button"
          size="small"
          variant="text"
          onClick={() => void tenants.refetch()}
        >
          {t('common.tenantRetry')}
        </Button>
      </Box>
    )
  } else if (empty) {
    helperText = t('common.tenantEmpty')
  }

  const selector = (
    <TextField
      select
      size="small"
      label={t('common.tenant')}
      value={value}
      disabled={disabled || loading}
      helperText={helperText}
      onChange={(event) => onSelect(event.target.value)}
      sx={{ minWidth: trailing === undefined ? 240 : 0 }}
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
  if (trailing === undefined) {
    return selector
  }
  return (
    <SelectorRow selectorMaxWidth={240} trailing={trailing}>
      {selector}
    </SelectorRow>
  )
}
