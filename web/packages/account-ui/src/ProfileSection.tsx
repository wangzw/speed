/**
 * ProfileSection: the display-name surface of the account page.
 *
 * The profile write is PATCH /api/v1/auth/me (authn.profile.patch, the self-service
 * profile module pkg/authn/profile), the same route the preferences section
 * writes through for its locale and time zone. This section edits the one
 * remaining self-writable scalar the preferences section does not own --
 * the account's display name -- so the account page offers the whole of the
 * profile's writable set: the display name here, the language and time zone
 * in the preferences section.
 *
 * The section reads GET /api/v1/auth/me for the stored name, seeds the field from
 * it and PATCHes a one-field body on save. The write is not fired on every
 * keystroke: it goes out from the save button, so a half-typed name never
 * lands on the account, and the button is disabled while the field still
 * holds the account's own name (a save with nothing changed would fire a
 * no-op PATCH) -- the same no-change-disabled convention the console's
 * account-detail save controls set.
 *
 * The write does not ask for a fresh authentication: the route admits the
 * account's own display name, language and time zone directly
 * (REQ-account-058), so the section drives no step-up gate and opens no
 * dialog. A refusal resolves through the code whitelist and renders in the
 * section's write-feedback region below the control group; a successful save
 * refetches the profile so the field shows the account's own answer, and the
 * region confirms it with a message naming the display name. The region's
 * slot holds at most one outcome, so a success and a failure never stand
 * beside each other.
 *
 * The section takes no session: its profile read and its PATCH travel on the
 * shared access token through the generated calls.
 *
 * A host that keeps the display name outside the profile query -- the shell's
 * account entry, for one -- passes onDisplayNameSaved to hear the name the
 * write settled on. The callback runs after the PATCH succeeds and the
 * profile query is invalidated, and receives the display name the server
 * stored, read from the PATCH response rather than the typed value. Omitting
 * it leaves the section's behaviour unchanged.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  useAuthnProfilePatch,
} from '@speed/api-sdk'
import {
  ActionButton,
  ActionRow,
  FORM_MAX_WIDTH,
  SectionCard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { useProfileQuery } from './internal/queries.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'
import { useAccountUiTranslation } from './internal/translation.js'

interface ProfileSectionProps {
  /**
   * Renders the name as a rule-less field that saves itself: the value is
   * committed on blur and on Enter, no button is rendered, and the field
   * carries its name as an accessible label instead of a visible one. The
   * host places the label beside it.
   */
  readonly inline?: boolean
  /**
   * Called after a save settles successfully and the profile query is
   * invalidated, with the display name the server stored (read from the PATCH
   * response). Omitting it changes nothing.
   */
  readonly onDisplayNameSaved?: (name: string) => void
}

/** The pending-state placeholder, read aloud as one loading announcement. */
function ProfileSkeleton({ label }: { readonly label: string }) {
  return (
    <Box role="status" aria-label={label} aria-busy="true">
      <Skeleton variant="text" width="30%" />
      <Skeleton variant="text" width="52%" />
    </Box>
  )
}

export function ProfileSection({
  inline = false,
  onDisplayNameSaved,
}: ProfileSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const patchProfile = useAuthnProfilePatch()

  // The field's local edit, or null while it still tracks the loaded value
  // -- the lazy-seed idiom: value falls back to the account's answer until
  // the user types, so there is no effect seeding the field off a value
  // that arrives asynchronously. An empty string is a real edit (a cleared
  // name), which `??` preserves.
  const [editedName, setEditedName] = useState<string | null>(null)
  const feedback = useWriteFeedback()

  const pending = isPending && data === undefined
  // Disabled through the whole attempt, including the refetch that follows
  // the mutation, so the field cannot change context under a pending scope.
  const busy = patchProfile.isPending || feedback.pending
  const name = editedName ?? data?.display_name ?? ''

  function invalidateProfile(): Promise<void> {
    return queryClient.invalidateQueries({
      queryKey: getAuthnProfileGetQueryKey(),
    })
  }

  function save(nextName: string): Promise<void> {
    return feedback.run(async () => {
      const saved = await patchProfile.mutateAsync({
        data: { display_name: nextName },
      })
      setEditedName(null)
      await invalidateProfile()
      // Report only the name the server settled on, after the refetch has
      // been triggered, so a host tracking it never shows an unconfirmed value.
      onDisplayNameSaved?.(saved.display_name)
      // Translated when the attempt settles; it names the changed object.
      return t('profile.saved')
    })
  }

  /** Commit a name only when it differs from the account's own: a save with
   * nothing changed would fire a no-op PATCH. The inline field and the
   * non-inline submit button share this rule. */
  function commit(nextName: string): void {
    if (busy || nextName === (data?.display_name ?? '')) {
      return
    }
    void save(nextName)
  }

  return (
    <SectionCard title={t('profile.title')}>
      <AccountAsyncSection
        pending={pending}
        payload={data}
        empty={false}
        error={error}
        refreshing={isFetching}
        loading={<ProfileSkeleton label={t('profile.loading')} />}
        errorState={{
          title: t('profile.error.title'),
          description: t('profile.error.description'),
          retryLabel: t('profile.retry'),
          onRetry: () => void refetch(),
        }}
      >
        {() => (
          <Box
            component="form"
            noValidate
            onSubmit={(event) => {
              event.preventDefault()
              // Enter commits the inline field, but an unchanged value is no
              // write -- the same rule the non-inline save button follows.
              commit(name)
            }}
            sx={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'flex-start',
              gap: inline ? 0 : 2,
              maxWidth: FORM_MAX_WIDTH,
            }}
          >
            <TextField
              fullWidth
              size="small"
              variant="outlined"
              label={inline ? undefined : t('profile.displayName.label')}
              slotProps={
                inline
                  ? {
                      htmlInput: {
                        'aria-label': t('profile.displayName.label'),
                      },
                    }
                  : undefined
              }
              value={name}
              disabled={busy}
              onChange={(event) => setEditedName(event.target.value)}
              onBlur={
                inline
                  ? () => {
                      // The value commits itself: a changed name is saved
                      // when the field loses focus, and Enter does the same
                      // through the form's submit.
                      commit(name)
                    }
                  : undefined
              }
            />

            {inline ? null : (
              <ActionRow sx={{ width: '100%' }}>
                <ActionButton
                  type="submit"
                  emphasis="primary"
                  // Disabled while the field still holds the account's own
                  // name: a save with nothing changed would fire a no-op
                  // PATCH, and the console account detail's save controls set
                  // the same no-change-disabled convention.
                  disabled={busy || name === (data?.display_name ?? '')}
                >
                  {t('profile.save')}
                </ActionButton>
              </ActionRow>
            )}

            {/* One scope's result, below the control group it confirms. */}
            <WriteFeedbackRegion
              outcome={feedback.outcome}
              sx={{ alignSelf: 'stretch' }}
            />
          </Box>
        )}
      </AccountAsyncSection>
    </SectionCard>
  )
}
