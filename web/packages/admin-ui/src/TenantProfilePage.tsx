/**
 * TenantProfilePage: the organization's own profile page.
 *
 * The page reads one tenant's organization profile (GET
 * /api/v1/tenants/{tenant}/profile) and renders it as two cards. The
 * identity card is read-only: the alias is fixed after creation, the state
 * is the platform's to change, and the creation time is a fact. The profile
 * card edits the four descriptive fields the organization owns -- display
 * name, description, contact email and website.
 *
 * The display name may be cleared. An empty display name is not an error:
 * every surface falls back to the alias, and the field's placeholder shows
 * that alias. A landed save calls the host's `onTenantUpdated`, so the
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
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { formatInstant } from '@speed/i18n'
import {
  ListSkeleton,
  RefreshButton,
  SectionCard,
  pageRefreshRead,
  usePageRefresh,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AdminAsyncSection } from './internal/async-section.js'
import { DefinitionRow } from './internal/layout.js'
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
   * The host's action after a write ends the caller's membership -- a
   * transfer hands the organization on, a deletion ends it: leave the
   * organization, reload the caller tenant list the account menu and the
   * breadcrumb read, and show `message` on the destination. Absent, the page
   * falls back to `onTenantUpdated` and keeps the message in the ownership
   * card.
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
  // A page refresh remounts the ownership card, withdrawing its dialog,
  // draft and feedback together with the re-read.
  const [ownershipEpoch, setOwnershipEpoch] = useState(0)
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

  // The read is the page's one body read; the refresh re-reads it and
  // withdraws the draft and any result that described the previous answer.
  const refresh = usePageRefresh(
    [pageRefreshRead('tenant-profile', profile)],
    () => {
      setDraft(null)
      appliedSignature.current = ''
      feedback.reset()
      setOwnershipEpoch((epoch) => epoch + 1)
    },
  )

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
    const name = displayNameOf(next.displayName, settled.alias)
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
        actions={
          <RefreshButton
            busy={refresh.busy}
            writePending={feedback.pending}
            label={t('common.refresh')}
            onClick={refresh.refresh}
          />
        }
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
                description={t('tenantProfile.identity.description')}
                sx={{ mb: 3 }}
              >
                <Box component="dl" sx={{ margin: 0 }}>
                  <DefinitionRow
                    label={t('tenantProfile.field.alias')}
                    value={settledProfile.alias}
                  />
                  <DefinitionRow
                    label={t('tenantProfile.field.owner')}
                    value={ownerLabelOf(
                      settledProfile.ownerDisplayName,
                      settledProfile.ownerUsername,
                      t('tenantProfile.owner.unnamed'),
                    )}
                  />
                  <DefinitionRow
                    label={t('tenantProfile.field.status')}
                    value={statusLabel(settledProfile.status, t)}
                  />
                  <DefinitionRow
                    label={t('tenantProfile.field.createdAt')}
                    value={formatInstant(settledProfile.createdAt, i18n.language, {
                      timeZone: 'UTC',
                    })}
                  />
                </Box>
                <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>
                  {t('tenantProfile.identity.aliasHint')}
                </Typography>
              </SectionCard>

              <SectionCard
                title={t('tenantProfile.form.title')}
                description={t('tenantProfile.form.description')}
              >
                {deleted ? (
                  // A tombstone is read-only: the profile can no longer be
                  // written, so the page shows the stored values and no form.
                  <Box component="dl" sx={{ margin: 0 }}>
                    <DefinitionRow
                      label={t('tenantProfile.field.displayName')}
                      value={displayNameOf(
                        settledProfile.displayName,
                        settledProfile.alias,
                      )}
                    />
                    <DefinitionRow
                      label={t('tenantProfile.field.description')}
                      value={settledProfile.description || t('common.emptyValue')}
                    />
                    <DefinitionRow
                      label={t('tenantProfile.field.contactEmail')}
                      value={settledProfile.contactEmail || t('common.emptyValue')}
                    />
                    <DefinitionRow
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
                      alias={settledProfile.alias}
                    />
                    <Box ref={regionRef} sx={{ mt: 2 }}>
                      <WriteFeedbackRegion outcome={feedback.outcome} />
                    </Box>
                  </>
                )}
              </SectionCard>

              <TenantOwnership
                key={`ownership:${ownershipEpoch}`}
                tenant={tenant}
                profile={settledProfile}
                displayName={displayNameOf(
                  settledProfile.displayName,
                  settledProfile.alias,
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
