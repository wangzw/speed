/**
 * TenantCreatePage renders the platform console's standalone organization
 * registration form from the shared @speed/org-create-ui form in platform mode.
 * A landed registration is handed to `onCreated`, or navigates to its detail.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import { SectionCard, useWriteFeedback } from '@speed/ui-kit'
import {
  EMPTY_ORGANIZATION_CREATE_VALUES,
  OrganizationCreateForm,
  nameViolation,
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

/** Props of TenantCreatePage: the roster path and the host's created-tenant hook. */
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

function visibleViolations(
  values: OrganizationCreateValues,
  submitted: boolean,
  all: OrganizationCreateViolations,
): OrganizationCreateViolations {
  if (submitted) {
    return all
  }
  if (values.name.trim() === '') {
    return {}
  }
  const name = nameViolation(values.name)
  return name === undefined ? {} : { name }
}

/**
 * TenantCreatePage registers an organization from the platform console and
 * hands the result to `onCreated` or navigates to its detail page.
 */
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
        const href = `${basePath}/${encodeURIComponent(created.name)}`
        if (navigate === undefined) {
          window.location.assign(href)
        } else {
          navigate(href)
        }
      }
      return t('tenants.created', { name: created.name })
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
