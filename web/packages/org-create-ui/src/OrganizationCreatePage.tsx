/**
 * The self-service organization-creation page: owns the draft, the create
 * attempt and the host's hand-over to the new organization.
 */

import { useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import { useTranslation } from '@speed/i18n'
import { InlineError, SectionCard } from '@speed/ui-kit'
import { OrganizationCreateForm } from './OrganizationCreateForm.js'
import {
  organizationCreateFailure,
  useOrganizationCreateErrorText,
} from './error-text.js'
import type { OrganizationCreateFailure } from './error-text.js'
import { ORG_CREATE_UI_NAMESPACE } from './resources.js'
import {
  EMPTY_ORGANIZATION_CREATE_VALUES,
  nameViolation,
  normalizeOrganizationCreateValues,
  validateOrganizationCreateValues,
} from './validation.js'
import type {
  CreatedOrganization,
  OrganizationCreateField,
  OrganizationCreateSubmission,
  OrganizationCreateValues,
  OrganizationCreateViolations,
} from './validation.js'

/** The page's props: the create operation, the host's hand-over after
 * creation, and where the page goes on success. */
export interface OrganizationCreatePageProps {
  /** The org.create operation, mapped to this package's submission shape. */
  readonly createOrganization: (
    submission: OrganizationCreateSubmission,
  ) => Promise<CreatedOrganization>
  /**
   * The host's hand-over after a landed creation: record the name, settle
   * the new organization as current and re-read the caller's tenants. A
   * rejection is offered as a retry of this hand-over, not of the write.
   */
  readonly onCreated: (created: CreatedOrganization) => Promise<void>
  /** Where the page goes once the hand-over resolves. */
  readonly successHref: string
  /** In-place navigation; absent falls back to a full-page assignment. */
  readonly onNavigate?: (href: string) => void
}

type Phase = 'idle' | 'creating' | 'switching' | 'switch-failed'

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

/** Renders the self-service creation form and the create-then-enter
 * sequence. */
export function OrganizationCreatePage({
  createOrganization,
  onCreated,
  successHref,
  onNavigate,
}: OrganizationCreatePageProps) {
  const { t } = useTranslation(ORG_CREATE_UI_NAMESPACE)
  const resolveErrorText = useOrganizationCreateErrorText()
  const [values, setValues] = useState<OrganizationCreateValues>(
    EMPTY_ORGANIZATION_CREATE_VALUES,
  )
  const [submitted, setSubmitted] = useState(false)
  const [phase, setPhase] = useState<Phase>('idle')
  const [created, setCreated] = useState<CreatedOrganization | null>(null)
  const [createFailure, setCreateFailure] =
    useState<OrganizationCreateFailure | null>(null)
  const [switchFailure, setSwitchFailure] =
    useState<OrganizationCreateFailure | null>(null)

  const navigate =
    onNavigate ??
    ((href: string) => {
      window.location.assign(href)
    })

  const change = (field: OrganizationCreateField, value: string) => {
    setValues((current) => ({ ...current, [field]: value }))
  }

  const enter = async (organization: CreatedOrganization) => {
    setSwitchFailure(null)
    setPhase('switching')
    try {
      await onCreated(organization)
    } catch (error) {
      setSwitchFailure(organizationCreateFailure(error))
      setPhase('switch-failed')
      return
    }
    setPhase('idle')
    navigate(successHref)
  }

  const submit = async () => {
    if (phase !== 'idle') {
      return
    }
    setSubmitted(true)
    const normalized = normalizeOrganizationCreateValues(values)
    if (
      Object.keys(
        validateOrganizationCreateValues(normalized, 'self-service'),
      ).length > 0
    ) {
      return
    }
    setCreateFailure(null)
    setPhase('creating')
    let organization: CreatedOrganization
    try {
      organization = await createOrganization({
        name: normalized.name,
        displayName: normalized.displayName,
        description: normalized.description,
      })
    } catch (error) {
      setCreateFailure(organizationCreateFailure(error))
      setPhase('idle')
      return
    }
    setCreated(organization)
    await enter(organization)
  }

  const valuesNow = normalizeOrganizationCreateValues(values)
  const violations = visibleViolations(
    values,
    submitted,
    validateOrganizationCreateValues(valuesNow, 'self-service'),
  )

  return (
    <Box component="section">
      <Typography component="h1" variant="h4" sx={{ mb: 1 }}>
        {t('page.title')}
      </Typography>
      <Typography variant="body1" color="text.secondary" sx={{ mb: 3 }}>
        {t('page.intro')}
      </Typography>

      <SectionCard>
        <OrganizationCreateForm
          mode="self-service"
          values={values}
          onChange={change}
          violations={violations}
          disabled={phase !== 'idle'}
          pending={phase === 'creating'}
          submitLabel={
            phase === 'creating' ? t('submitting') : t('submit')
          }
          onSubmit={() => void submit()}
        />

        {phase === 'switch-failed' && created !== null ? (
          <Alert severity="error" role="alert" sx={{ mt: 3 }}>
            <Typography variant="body2" sx={{ fontWeight: 600 }}>
              {t('switching.failedTitle')}
            </Typography>
            <Typography variant="body2">
              {t('switching.failedDescription')}
            </Typography>
            {switchFailure === null ? null : (
              <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>
                {resolveErrorText(
                  switchFailure.code,
                  switchFailure.retryAfterSeconds,
                )}
              </Typography>
            )}
            <Box sx={{ mt: 1.5 }}>
              <Button
                type="button"
                size="small"
                variant="outlined"
                onClick={() => void enter(created)}
              >
                {t('switching.retry')}
              </Button>
            </Box>
          </Alert>
        ) : null}

        {phase === 'switching' ? (
          <Alert severity="info" role="status" sx={{ mt: 3 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}>
              <CircularProgress size={18} />
              <Typography variant="body2">{t('switching.title')}</Typography>
            </Box>
          </Alert>
        ) : null}

        {createFailure === null ? null : (
          <Box sx={{ mt: 3 }}>
            <InlineError
              code={createFailure.code}
              resolve={resolveErrorText}
              retryAfterSeconds={createFailure.retryAfterSeconds}
            />
          </Box>
        )}
      </SectionCard>
    </Box>
  )
}
