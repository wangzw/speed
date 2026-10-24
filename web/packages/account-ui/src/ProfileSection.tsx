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
 * The route admits a caller only with the freshness the account's factors
 * call for (a fresh multi-factor authentication when the account has an
 * active factor, a fresh first factor otherwise). An unelevated caller is
 * answered 401 authn.insufficient_user_authentication; the section opens
 * the step-up dialog with the typed name remembered, and a won challenge
 * re-runs that one save exactly once. Every other refusal resolves through
 * the code whitelist and renders in the section's write-feedback region
 * below the control group; a successful save refetches the profile so the
 * field shows the account's own answer, and the region confirms it with a
 * message naming the display name. The region's slot holds at most one
 * outcome, so a success and a failure never stand beside each other.
 *
 * The section takes the session as its only prop -- the step-up dialog
 * drives it -- while the profile read and the PATCH travel on the shared
 * access token through the generated calls.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  useAuthnProfilePatch,
} from '@speed/api-sdk'
import type { AuthSession } from '@speed/auth-core'
import { useStepUpGate } from '@speed/auth-core'
import {
  AsyncSection,
  FORM_MAX_WIDTH,
  SectionCard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { useProfileQuery } from './internal/queries.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'

export interface ProfileSectionProps {
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
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

export function ProfileSection({ session }: ProfileSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, refetch } = useProfileQuery()
  const patchProfile = useAuthnProfilePatch()

  // The field's local edit, or null while it still tracks the loaded value
  // -- the lazy-seed idiom: value falls back to the account's answer until
  // the user types, so there is no effect seeding the field off a value
  // that arrives asynchronously. An empty string is a real edit (a cleared
  // name), which `??` preserves.
  const [editedName, setEditedName] = useState<string | null>(null)
  const feedback = useWriteFeedback()

  const gate = useStepUpGate<{ readonly name: string }>((action) => {
    void save(action.name)
  })

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
      try {
        await patchProfile.mutateAsync({ data: { display_name: nextName } })
      } catch (error) {
        // The write refused for insufficient step-up belongs to the dialog;
        // settling with null leaves the slot untouched.
        if (gate.intercept(error, { name: nextName })) {
          return null
        }
        throw error
      }
      setEditedName(null)
      await invalidateProfile()
      // Translated when the attempt settles; it names the changed object.
      return t('profile.saved')
    })
  }

  return (
    <>
      <SectionCard title={t('profile.title')}>
        <AsyncSection
          pending={pending}
          payload={data}
          empty={false}
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
                void save(name)
              }}
              sx={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'flex-start',
                gap: 2,
                maxWidth: FORM_MAX_WIDTH,
              }}
            >
              <TextField
                fullWidth
                label={t('profile.displayName.label')}
                value={name}
                disabled={busy}
                onChange={(event) => setEditedName(event.target.value)}
              />

              <Button
                type="submit"
                variant="contained"
                // Disabled while the field still holds the account's own name:
                // a save with nothing changed would fire a no-op PATCH, and the
                // console account detail's save controls set the same
                // no-change-disabled convention.
                disabled={busy || name === (data?.display_name ?? '')}
              >
                {t('profile.save')}
              </Button>

              {/* One scope's result, below the control group it confirms. */}
              <WriteFeedbackRegion
                outcome={feedback.outcome}
                sx={{ alignSelf: 'stretch' }}
              />
            </Box>
          )}
        </AsyncSection>
      </SectionCard>

      <StepUpChallenge
        open={gate.open}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
      />
    </>
  )
}
