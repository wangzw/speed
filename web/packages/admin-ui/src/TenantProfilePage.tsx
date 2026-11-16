/**
 * TenantProfilePage: the organization's own profile page.
 *
 * The page reads one tenant's organization profile (GET
 * /api/v1/tenants/{tenant}/profile) and renders it as two cards. The
 * identity card is read-only: the name is fixed after creation, the state
 * is the platform's to change, and the creation time is a fact. The profile
 * card edits the four descriptive fields the organization owns -- display
 * name, description, contact email and website.
 *
 * The display name may be cleared. An empty display name is not an error:
 * every surface falls back to the name, and the field's placeholder shows
 * that name. A landed save calls the host's `onTenantUpdated`, so the
 * account menu and the breadcrumb -- which read the caller tenant list --
 * show the new name immediately instead of after their next read.
 *
 * The read renders through AdminAsyncSection, so a refusal is its own
 * placeholder (403, no retry), a 404 is the not-found placeholder with a
 * retry, and any other failure is the retryable one. The save reports
 * through one feedback scope whose region sits directly under the form; a
 * refused save resolves through the console's error whitelist and the draft
 * stays exactly as the operator typed it.
 */

import { useEffect, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { formatInstant } from '@speed/i18n'
import {
  FORM_MAX_WIDTH,
  ListSkeleton,
  SectionCard,
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

export interface TenantProfilePageProps {
  /** The organization the profile belongs to; every request names it. */
  readonly tenant: string
  /**
   * The host's action after a profile save lands: it reloads the caller
   * tenant list the account menu and the breadcrumb read, so the new
   * display name is shown without waiting for their next read. Absent, the
   * page only re-reads its own profile.
   */
  readonly onTenantUpdated?: () => void | Promise<unknown>
  /**
   * The host's action after a deletion ends the caller's membership: leave
   * the organization, reload the caller tenant list the account menu and the
   * breadcrumb read, and show `message` on the destination. Absent, the page
   * falls back to `onTenantUpdated` and keeps the message in the ownership
   * card. A transfer changes ownership only and never departs.
   */
  readonly onOrganizationLeft?: (message: string) => void | Promise<unknown>
}

/** The roster's closed state set; a value outside it renders the generic
 * label, never a raw value. */
const KNOWN_STATUSES = new Set<string>(['active', 'disabled', 'deleted'])

/**
 * How the identity card names the owner: the display name and the username
 * together, because a display name alone is ambiguous when two members share
 * one; either alone when the other is absent; and the neutral label when the
 * owner could not be resolved. The account identifier is never a fallback --
 * it is internal and the ownership decision is the only thing that reads it.
 */
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

/** The chip colour a roster state carries: served, paused, or gone. */
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

/** One identity field: its label above its value, the way the account pages
 * stack theirs, rather than a two-column definition row. */
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


/** The profile's four values as a stable signature, so a refetch that
 * answered the same values does not overwrite an operator's draft. */
function profileSignature(profile: AdminTenantProfile): string {
  return JSON.stringify(profileFieldsOf(profile))
}

export function TenantProfilePage({
  tenant,
  onTenantUpdated,
  onOrganizationLeft,
}: TenantProfilePageProps) {
  const { t, i18n } = useAdminUiTranslation()
  const operations = useOrganizationAdminOperations()
  const profile = operations.useTenantProfile(tenant)
  const write = operations.useTenantProfileWrite()
  // The profile card's one control group reports through one scope.
  const feedback = useWriteFeedback()
  const regionRef = useFailureScroll(feedback.outcome)

  const settled = profile.data
  const [draft, setDraft] = useState<TenantProfileFields | null>(null)
  // The values the draft was last built from. A settled read whose values
  // actually changed (a landed save, a refresh) replaces the draft; a
  // background refetch with the same values leaves an edit in place.
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
      // The host re-reads the caller tenant list the menu and the breadcrumb
      // read; a refusal there does not overturn the landed write.
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
                      {/* The chip's root is a generic element: an aria-label
                          is prohibited there, and a generic element gets no
                          accessible name from its contents. A visually hidden
                          field name before it supplies the "Status:" the
                          visible value alone cannot, so a screen reader hears
                          which fact the value states. */}
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
                    value={formatInstant(settledProfile.created_at, i18n.language, {
                      timeZone: 'UTC',
                    })}
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
                  // A tombstone is read-only: the profile can no longer be
                  // written, so the page shows the stored values and no form.
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
