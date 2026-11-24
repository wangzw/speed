/**
 * TenantScope: the tenant selector for tenant-addressed pages; its value is a
 * request argument, seeded by the `tenant` query parameter.
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
import { displayNameOf } from '../TenantProfileFields.js'

/** The query parameter a tenant-addressed page reads its tenant from. */
export const TENANT_QUERY_PARAM = 'tenant'

/** The tenant name a query string names, or '' when the parameter is absent. */
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
  /** The chosen tenant name, or '' when none is chosen. */
  readonly value: string
  /** Reports the chosen tenant name ('' clears the selection). */
  readonly onSelect: (tenant: string) => void
  /**
   * Whether the selector is disabled. It stays natively disabled while a
   * write into any scope the chosen tenant addresses is in flight; the
   * write starts from another control, so focus is not on the selector.
   */
  readonly disabled?: boolean
  /** A control rendered in the selector's own row, to its right (the refresh). */
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
  // settles (and stays shown if the roster does not list it).
  const listed = options.some((tenant) => tenant.name === value)

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
        <MenuItem key={tenant.name} value={tenant.name}>
          {displayNameOf(tenant.display_name, tenant.name)}
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
