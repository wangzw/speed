/**
 * TenantProfilePage reads one organization's profile as a read-only identity
 * card plus an editable profile card. A cleared display name is valid and falls
 * back to the tenant name.
 */

import { useEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import {
  FORM_MAX_WIDTH,
  ListSkeleton,
  SectionCard,
  useFormatInstant,
  useWriteFeedback,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import { AdminAsyncSection } from './internal/async-section.js'
import { ProfileIcon, SettingsIcon } from './internal/icons.js'
import { AdminPageHeader } from './internal/page-header.js'
import { useAdminUiTranslation } from './internal/translation.js'
import {
  useFailureScroll,
  WriteFeedbackRegion,
} from './internal/write-feedback.js'
import { useOrganizationAdminOperations } from './organization-operations.js'
import type { AdminTenantProfile } from './organization-operations.js'
import { TenantProfileForm } from './TenantProfileForm.js'
import {
  displayNameOf,
  profileFieldsOf,
} from './TenantProfileFields.js'
import type { TenantProfileFields } from './TenantProfileFields.js'
import { TenantOwnership } from './TenantOwnership.js'

/** Props of TenantProfilePage: the tenant, plus the host's update and departure hooks. */
export interface TenantProfilePageProps {
  /** The organization the profile belongs to; every request names it. */
  readonly tenant: string
  /**
   * The host's action after a save lands: reload the caller tenant list the
   * account menu and breadcrumb read. Absent, the page only re-reads its own
   * profile.
   */
  readonly onTenantUpdated?: () => void | Promise<unknown>
  /**
   * The host's action after a deletion ends the caller's membership: leave the
   * organization, reload the caller tenant list and show `message`. Absent, the
   * page falls back to `onTenantUpdated`.
   */
  readonly onOrganizationLeft?: (message: string) => void | Promise<unknown>
}

const KNOWN_STATUSES = new Set<string>(['active', 'disabled', 'deleted'])

// The account identifier is never a fallback for the owner label; it is internal.
function ownerLabelOf(
  ownerDisplayName: string,
  ownerUsername: string,
  unnamed: string,
): string {
  const displayName = ownerDisplayName.trim()
  const username = ownerUsername.trim()
  if (displayName !== '' && username !== '') {
    return `${displayName} (${username})`
  }
  if (username !== '') {
    return username
  }
  if (displayName !== '') {
    return displayName
  }
  return unnamed
}

function statusLabel(status: string, t: (key: string) => string): string {
  return KNOWN_STATUSES.has(status)
    ? t(`tenants.status.${status}`)
    : t('tenants.status.other')
}

function statusColorOf(
  status: string,
): 'success' | 'warning' | 'error' | 'default' {
  switch (status) {
    case 'active':
      return 'success'
    case 'disabled':
      return 'warning'
    case 'deleted':
      return 'error'
    default:
      return 'default'
  }
}

function ProfileField({
  label,
  value,
}: {
  readonly label: string
  readonly value: string
}) {
  return (
    <Box>
      <Typography variant="body1" component="dt" sx={{ fontWeight: 600 }}>
        {label}
      </Typography>
      <Typography
        variant="body2"
        component="dd"
        sx={{ margin: 0, mt: 0.5, wordBreak: 'break-all' }}
      >
        {value}
      </Typography>
    </Box>
  )
}


function profileSignature(profile: AdminTenantProfile): string {
  return JSON.stringify(profileFieldsOf(profile))
}

/**
 * TenantProfilePage reads one organization's profile and renders a read-only
 * identity card and an editable profile card.
 */
export function TenantProfilePage({
  tenant,
  onTenantUpdated,
  onOrganizationLeft,
}: TenantProfilePageProps) {
  const { t } = useAdminUiTranslation()
  const operations = useOrganizationAdminOperations()
  const formatTime = useFormatInstant()
  const profile = operations.useTenantProfile(tenant)
  const write = operations.useTenantProfileWrite()
  const feedback = useWriteFeedback()
  const regionRef = useFailureScroll(feedback.outcome)

  const settled = profile.data
  const [draft, setDraft] = useState<TenantProfileFields | null>(null)
  // A settled read replaces the draft only when its values changed; a same-value
  // background refetch leaves an edit in place.
  const appliedSignature = useRef('')
  useEffect(() => {
    if (settled === undefined) {
      return
    }
    const signature = profileSignature(settled)
    if (appliedSignature.current === signature) {
      return
    }
    appliedSignature.current = signature
    setDraft(profileFieldsOf(settled))
  }, [settled])

  const fields: TenantProfileFields =
    draft ??
    (settled === undefined
      ? {
          displayName: '',
          description: '',
          contactEmail: '',
          website: '',
        }
      : profileFieldsOf(settled))

  const save = (next: TenantProfileFields): void => {
    if (settled === undefined) {
      return
    }
    const name = displayNameOf(next.displayName, settled.name)
    void feedback.run(async () => {
      await write.mutateAsync({ tenant, fields: next })
      // A refusal from the host hook does not overturn the landed write.
      await onTenantUpdated?.()
      return t('tenantProfile.form.savedNamed', { name })
    })
  }

  const pending = profile.isPending && settled === undefined

  return (
    <Box>
      <AdminPageHeader
        title={t('organization.nav.profile')}
        breadcrumbs={[
          { label: t('shell.title') },
          { label: t('organization.nav.profile') },
        ]}
        sx={{ mb: 2 }}
      />

      <AdminAsyncSection
        pending={pending}
        payload={settled}
        error={profile.error}
        refreshing={profile.isFetching}
        empty={false}
        headingLevel="h2"
        loading={
          <ListSkeleton
            label={t('tenantProfile.loading')}
            rows={[
              <Skeleton key="a" variant="text" width="48%" />,
              <Skeleton key="b" variant="text" width="62%" />,
              <Skeleton key="c" variant="text" width="38%" />,
            ]}
          />
        }
        errorState={{
          title: t('tenantProfile.error.title'),
          description: t('tenantProfile.error.description'),
          retryLabel: t('tenantProfile.retry'),
          onRetry: () => void profile.refetch(),
        }}
        notFoundState={{
          title: t('detail.notFound.title'),
          description: t('detail.notFound.description'),
          retryLabel: t('tenantProfile.retry'),
          onRetry: () => void profile.refetch(),
        }}
      >
        {(settledProfile) => {
          const deleted = settledProfile.status === 'deleted'
          return (
            <>
              <SectionCard
                title={t('tenantProfile.identity.title')}
                icon={<ProfileIcon size={24} />}
                description={t('tenantProfile.identity.description')}
                sx={{ mb: 3 }}
              >
                <Box
                  component="dl"
                  sx={{
                    margin: 0,
                    display: 'flex',
                    flexDirection: 'column',
                    gap: 2,
                    maxWidth: FORM_MAX_WIDTH,
                  }}
                >
                  <Box>
                    <Typography
                      variant="body1"
                      component="dt"
                      sx={{ fontWeight: 600 }}
                    >
                      {t('tenantProfile.field.name')}
                    </Typography>
                    <Box
                      component="dd"
                      sx={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: 1,
                        margin: 0,
                        mt: 0.5,
                      }}
                    >
                      <Typography
                        variant="body2"
                        sx={{ wordBreak: 'break-all' }}
                      >
                        {settledProfile.name}
                      </Typography>
                      {/* The chip root accepts no aria-label and gets no
                          accessible name from its contents, so a hidden field
                          name supplies it. */}
                      <Box component="span" sx={visuallyHiddenSx}>
                        {t('tenantProfile.field.statusAria', {
                          label: t('tenantProfile.field.status'),
                          value: '',
                        }).trimEnd()}
                      </Box>
                      <Chip
                        size="small"
                        variant="outlined"
                        color={statusColorOf(settledProfile.status)}
                        label={statusLabel(settledProfile.status, t)}
                      />
                    </Box>
                  </Box>
                  <ProfileField
                    label={t('tenantProfile.field.owner')}
                    value={ownerLabelOf(
                      settledProfile.owner_display_name,
                      settledProfile.owner_username,
                      t('tenantProfile.owner.unnamed'),
                    )}
                  />
                  <ProfileField
                    label={t('tenantProfile.field.createdAt')}
                    value={formatTime(settledProfile.created_at)}
                  />
                </Box>
                <Typography variant="body2" color="text.secondary" sx={{ mt: 2 }}>
                  {t('tenantProfile.identity.nameHint')}
                </Typography>
              </SectionCard>

              <SectionCard
                title={t('tenantProfile.form.title')}
                icon={<SettingsIcon size={24} />}
                description={t('tenantProfile.form.description')}
              >
                {deleted ? (
                  <Box
                    component="dl"
                    sx={{
                      margin: 0,
                      display: 'flex',
                      flexDirection: 'column',
                      gap: 2,
                      maxWidth: FORM_MAX_WIDTH,
                    }}
                  >
                    <ProfileField
                      label={t('tenantProfile.field.displayName')}
                      value={displayNameOf(
                        settledProfile.display_name,
                        settledProfile.name,
                      )}
                    />
                    <ProfileField
                      label={t('tenantProfile.field.description')}
                      value={settledProfile.description || t('common.emptyValue')}
                    />
                    <ProfileField
                      label={t('tenantProfile.field.contactEmail')}
                      value={settledProfile.contact_email || t('common.emptyValue')}
                    />
                    <ProfileField
                      label={t('tenantProfile.field.website')}
                      value={settledProfile.website || t('common.emptyValue')}
                    />
                  </Box>
                ) : (
                  <>
                    <TenantProfileForm
                      value={fields}
                      onChange={setDraft}
                      onSubmit={save}
                      saving={feedback.pending}
                      name={settledProfile.name}
                    />
                    <Box ref={regionRef} sx={{ mt: 2 }}>
                      <WriteFeedbackRegion outcome={feedback.outcome} />
                    </Box>
                  </>
                )}
              </SectionCard>

              <TenantOwnership
                tenant={tenant}
                profile={settledProfile}
                displayName={displayNameOf(
                  settledProfile.display_name,
                  settledProfile.name,
                )}
                onTenantUpdated={onTenantUpdated}
                onOrganizationLeft={onOrganizationLeft}
              />
            </>
          )
        }}
      </AdminAsyncSection>
    </Box>
  )
}
