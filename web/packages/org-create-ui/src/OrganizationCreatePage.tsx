/**
 * The self-service organization-creation page.
 *
 * The page owns the draft, the creation attempt and the hand-over that
 * follows it. It never talks to the transport itself: the host supplies
 * `createOrganization` (the org.create operation mapped to this package's
 * submission shape) and `onCreated` (the host's switch to the new
 * organization -- recording its name, settling it as current and
 * re-reading the caller's tenants). After `onCreated` resolves the page
 * navigates to `successHref`; a failure there is a retry of the hand-over
 * alone, never a second creation, so a network fault after a landed write
 * cannot leave a duplicate organization behind.
 *
 * A creation refusal renders through the package's resolver in one error
 * region under the form. The form's values survive a refusal; a landed
 * creation disables the form and shows the entering status until the
 * hand-over settles or its retry clears it.
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

/** The page's position in the create-and-enter sequence. */
type Phase = 'idle' | 'creating' | 'switching' | 'switch-failed'

/**
 * The violations to show before the first submit: only the name, and
 * only once the operator has typed something, so an untouched form is not
 * painted with a required-field error.
 */
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
