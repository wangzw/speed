/**
 * TenantRosterPage: the roster-administration page of the console.
 *
 * The platform side of the tenant story: it lists the whole roster (the
 * tenants the process serves), registers a tenant not registered yet,
 * renames a tenant's alias and enables or disables a tenant. Switching
 * the current session's tenant is the tenant-side action and lives in
 * @speed/tenancy-ui; managing the roster is this page, and the two share
 * neither a control nor a query key.
 *
 * Every read and write is an operation on the console operations bundle
 * -- the host binds the generated /tenant/admin operations to it -- so
 * the page builds no client and writes no query key. The row's state
 * renders from the roster's own spelling (active / disabled); a value
 * outside that closed set renders the generic label, never a raw value.
 * A failed list render settles into the retryable error placeholder; a
 * failed write renders the answer's code text in one banner beside the
 * offending control, resolved from the console-ui namespace's whitelist
 * (a roster collision reads the conflict text), never a raw key.
 *
 * The page renders a level-one heading (it is the page), and the async
 * section's placeholders render at the next level down, so the heading
 * order never skips.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { AsyncSection, ListSkeleton, errorCodeOf } from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { useConsoleUiTranslation } from './internal/translation.js'
import { useConsoleOperations } from './operations.js'
import type { ConsoleTenant } from './operations.js'

/**
 * The roster's closed status set, one bundle key each under
 * tenants.status. A value outside it renders the generic other label,
 * never a raw value.
 */
const KNOWN_STATUSES = new Set<string>(['active', 'disabled'])

/** The status label for a roster value, guarding the closed set. */
function statusLabel(
  status: string,
  t: (key: string) => string,
): string {
  return KNOWN_STATUSES.has(status)
    ? t(`tenants.status.${status}`)
    : t('tenants.status.other')
}

/** One roster row: its read-only fields and its rename/enable/disable controls. */
function TenantRow({ tenant }: { readonly tenant: ConsoleTenant }) {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const renameTenant = operations.useSetTenantAlias()
  const setStatus = operations.useSetTenantStatus()
  const [alias, setAlias] = useState('')

  const disabled = tenant.status === 'disabled'

  return (
    <Box
      sx={{
        py: 1.5,
        display: 'flex',
        flexWrap: 'wrap',
        alignItems: 'center',
        columnGap: 2,
        rowGap: 1,
      }}
    >
      <Box sx={{ minWidth: 0, flexGrow: 1 }}>
        <Typography variant="body1" sx={{ fontWeight: 500 }}>
          {tenant.alias}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {tenant.id}
        </Typography>
      </Box>
      <Typography variant="body2" color="text.secondary">
        {statusLabel(tenant.status, t)}
      </Typography>
      <TextField
        size="small"
        value={alias}
        onChange={(event) => setAlias(event.target.value)}
        label={t('tenants.rename.label')}
      />
      <Button
        type="button"
        size="small"
        variant="outlined"
        disabled={renameTenant.isPending}
        onClick={() => {
          renameTenant.mutate({ id: tenant.id, alias })
        }}
      >
        {t('tenants.rename.submit')}
      </Button>
      <Button
        type="button"
        size="small"
        variant="outlined"
        disabled={setStatus.isPending}
        onClick={() => {
          setStatus.mutate({
            id: tenant.id,
            status: disabled ? 'active' : 'disabled',
          })
        }}
      >
        {disabled ? t('tenants.enable') : t('tenants.disable')}
      </Button>
      <Box sx={{ flexBasis: '100%' }}>
        <InlineError
          code={
            renameTenant.isError
              ? errorCodeOf(renameTenant.error)
              : setStatus.isError
                ? errorCodeOf(setStatus.error)
                : null
          }
        />
      </Box>
    </Box>
  )
}

export function TenantRosterPage() {
  const { t } = useConsoleUiTranslation()
  const operations = useConsoleOperations()
  const tenants = operations.useTenants()
  const createTenant = operations.useCreateTenant()
  const [newId, setNewId] = useState('')
  const [newAlias, setNewAlias] = useState('')

  const rows = tenants.data
  // A load with no data yet -- the first fetch in flight, or one parked
  // by react-query's default networkMode 'online' while offline -- keeps
  // the loading branch: isPending alone tracks "no answer yet".
  const pending = tenants.isPending && rows === undefined

  return (
    <Box>
      <Typography variant="h1" sx={{ fontSize: '1.75rem', mb: 2 }}>
        {t('tenants.title')}
      </Typography>

      <Box component="section" sx={{ mb: 3 }}>
        <Typography variant="h2" sx={{ fontSize: '1.25rem', mb: 1 }}>
          {t('tenants.create.title')}
        </Typography>
        <Box
          sx={{
            display: 'flex',
            flexWrap: 'wrap',
            alignItems: 'flex-end',
            columnGap: 2,
            rowGap: 1,
          }}
        >
          <TextField
            size="small"
            value={newId}
            onChange={(event) => setNewId(event.target.value)}
            label={t('tenants.create.idLabel')}
          />
          <TextField
            size="small"
            value={newAlias}
            onChange={(event) => setNewAlias(event.target.value)}
            label={t('tenants.create.aliasLabel')}
          />
          <Button
            type="button"
            variant="contained"
            disabled={createTenant.isPending}
            onClick={() => {
              createTenant.mutate({ id: newId, alias: newAlias })
            }}
          >
            {t('tenants.create.submit')}
          </Button>
        </Box>
        <Box sx={{ mt: 1 }}>
          <InlineError
            code={createTenant.isError ? errorCodeOf(createTenant.error) : null}
          />
        </Box>
      </Box>

      <AsyncSection
        pending={pending}
        payload={rows}
        empty={rows !== undefined && rows.length === 0}
        headingLevel="h2"
        loading={
          <ListSkeleton
            label={t('tenants.loading')}
            rows={[
              <Skeleton key="a" variant="text" width="60%" />,
              <Skeleton key="b" variant="text" width="42%" />,
            ]}
          />
        }
        errorState={{
          title: t('tenants.error.title'),
          description: t('tenants.error.description'),
          retryLabel: t('tenants.retry'),
          onRetry: () => void tenants.refetch(),
        }}
        emptyState={{
          title: t('tenants.empty.title'),
          description: t('tenants.empty.description'),
        }}
      >
        {(settled) => (
          <Box>
            {settled.map((tenant, index) => (
              <Box
                key={tenant.id}
                sx={
                  index > 0
                    ? { borderTop: '1px solid', borderColor: 'divider' }
                    : undefined
                }
              >
                <TenantRow tenant={tenant} />
              </Box>
            ))}
          </Box>
        )}
      </AsyncSection>
    </Box>
  )
}
