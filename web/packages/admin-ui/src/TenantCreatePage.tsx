/**
 * TenantCreatePage: the platform console's standalone organization
 * registration.
 *
 * The roster used to carry an inline registration form; this page lets the
 * form hold the organization profile beside the identifier and alias
 * without crowding the roster table. It renders the shared creation form
 * from @speed/org-create-ui in platform mode, so the alias rules, the
 * built-in reserved names and the profile bounds are the same ones the
 * self-service page uses.
 *
 * Registration writes through the console operations bundle. The form
 * requires a display name, which is narrower than the endpoint: the API
 * accepts a registration without a profile. A landed registration hands
 * the new tenant to `onCreated` when the host supplies one, and otherwise
 * routes to the tenant detail through the shell's in-place navigation
 * seam. A refused registration keeps every draft and reports the answer's
 * code through the console's error-text resolver.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import { SectionCard, useWriteFeedback } from '@speed/ui-kit'
import {
  EMPTY_ORGANIZATION_CREATE_VALUES,
  OrganizationCreateForm,
  aliasViolation,
  normalizeOrganizationCreateValues,
  validateOrganizationCreateValues,
} from '@speed/org-create-ui'
import type {
  OrganizationCreateField,
  OrganizationCreateValues,
  OrganizationCreateViolations,
} from '@speed/org-create-ui'
import { AdminPageHeader } from './internal/page-header.js'
import { useAdminNavigate } from './internal/navigation.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useAdminOperations } from './operations.js'
import type { AdminTenant } from './operations.js'

export interface TenantCreatePageProps {
  /**
   * The roster's matched path; the breadcrumb and the post-registration
   * detail link are built from it. Absent, the page still registers but
   * cannot navigate to the new tenant on its own.
   */
  readonly basePath?: string
  /**
   * Host hand-off for a landed registration. Supplying it replaces the
   * page's own detail navigation.
   */
  readonly onCreated?: (tenant: AdminTenant) => void
}

/** The violations to show before the first submit: the alias alone, once
 * the operator has typed something. */
function visibleViolations(
  values: OrganizationCreateValues,
  submitted: boolean,
  all: OrganizationCreateViolations,
): OrganizationCreateViolations {
  if (submitted) {
    return all
  }
  if (values.alias.trim() === '') {
    return {}
  }
  const alias = aliasViolation(values.alias)
  return alias === undefined ? {} : { alias }
}

export function TenantCreatePage({
  basePath,
  onCreated,
}: TenantCreatePageProps) {
  const { t } = useAdminUiTranslation()
  const operations = useAdminOperations()
  const createTenant = operations.useCreateTenant()
  const navigate = useAdminNavigate()
  const feedback = useWriteFeedback()
  const regionRef = useFailureScroll(feedback.outcome)
  const [values, setValues] = useState<OrganizationCreateValues>(
    EMPTY_ORGANIZATION_CREATE_VALUES,
  )
  const [submitted, setSubmitted] = useState(false)

  const change = (field: OrganizationCreateField, value: string) => {
    setValues((current) => ({ ...current, [field]: value }))
  }

  const normalized = normalizeOrganizationCreateValues(values)
  const violations = visibleViolations(
    values,
    submitted,
    validateOrganizationCreateValues(normalized, 'platform'),
  )

  const submit = () => {
    setSubmitted(true)
    if (
      Object.keys(
        validateOrganizationCreateValues(normalized, 'platform'),
      ).length > 0
    ) {
      return
    }
    void feedback.run(async () => {
      const created = await createTenant.mutateAsync(normalized)
      if (onCreated !== undefined) {
        onCreated(created)
      } else if (basePath !== undefined) {
        const href = `${basePath}/${encodeURIComponent(created.id)}`
        if (navigate === undefined) {
          window.location.assign(href)
        } else {
          navigate(href)
        }
      }
      return t('tenants.created', { id: created.id })
    })
  }

  const crumbs = [
    { label: t('shell.title') },
    ...(basePath === undefined
      ? [{ label: t('tenants.title') }]
      : [{ label: t('tenants.title'), href: basePath }]),
    { label: t('tenants.create.title') },
  ]

  return (
    <Box>
      <AdminPageHeader
        title={t('tenants.create.title')}
        breadcrumbs={crumbs}
        sx={{ mb: 2 }}
      />
      <SectionCard title={t('tenants.create.title')}>
        <OrganizationCreateForm
          mode="platform"
          values={values}
          onChange={change}
          violations={violations}
          disabled={feedback.pending}
          pending={feedback.pending}
          submitLabel={t('tenants.create.submit')}
          onSubmit={submit}
        />
        <Box ref={regionRef} sx={{ mt: 2 }}>
          <WriteFeedbackRegion outcome={feedback.outcome} />
        </Box>
      </SectionCard>
    </Box>
  )
}
