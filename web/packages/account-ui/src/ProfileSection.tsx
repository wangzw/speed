/**
 * ProfileSection: the display-name surface of the account page.
 */

import { useState } from 'react'
import type { FormEvent } from 'react'
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
  useActivationGuard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { useProfileQuery } from './internal/queries.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'
import { useAccountUiTranslation } from './internal/translation.js'

interface ProfileSectionProps {
  /** Renders the name as a rule-less field that saves itself on blur and
   * Enter, with no button and an accessible-only label. */
  readonly inline?: boolean
  /**
   * Called after a save settles successfully and the profile query is
   * invalidated, with the display name the server stored (read from the PATCH
   * response). Omitting it changes nothing.
   */
  readonly onDisplayNameSaved?: (name: string) => void
}

function ProfileSkeleton({ label }: { readonly label: string }) {
  return (
    <Box role="status" aria-label={label} aria-busy="true">
      <Skeleton variant="text" width="30%" />
      <Skeleton variant="text" width="52%" />
    </Box>
  )
}

/** The display-name field over the profile's own PATCH. */
export function ProfileSection({
  inline = false,
  onDisplayNameSaved,
}: ProfileSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const patchProfile = useAuthnProfilePatch()

  // Null tracks the loaded value (lazy seed): the field falls back to the
  // account's answer until the user types. An empty string is a real edit.
  const [editedName, setEditedName] = useState<string | null>(null)
  const feedback = useWriteFeedback()

  const pending = isPending && data === undefined
  // In flight through the whole attempt, including the refetch that follows.
  const busy = patchProfile.isPending || feedback.pending
  const name = editedName ?? data?.display_name ?? ''
  // A save with nothing changed would fire a no-op PATCH.
  const unchanged = name === (data?.display_name ?? '')

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
      // Report only the name the server settled on, after the refetch.
      onDisplayNameSaved?.(saved.display_name)
      return t('profile.saved')
    })
  }

  /** A save with nothing changed would fire a no-op PATCH; the inline field
   * and the submit button share this rule. */
  function commit(nextName: string): void {
    if (busy || nextName === (data?.display_name ?? '')) {
      return
    }
    void save(nextName)
  }

  // Guard the form's own submission too, so a programmatic submit or a
  // synthetic Enter cannot fire a second PATCH.
  const guardedSubmit = useActivationGuard(
    (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault()
      commit(name)
    },
    { busy, unavailable: unchanged },
  )

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
            onSubmit={guardedSubmit}
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
              slotProps={{
                htmlInput: {
                  // Locked, not disabled: the field keeps focus while busy.
                  readOnly: busy,
                  ...(inline
                    ? { 'aria-label': t('profile.displayName.label') }
                    : {}),
                },
              }}
              value={name}
              onChange={(event) => setEditedName(event.target.value)}
              onBlur={
                inline
                  ? () => {
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
                  // Neither is native disabled: the button keeps focus and
                  // states why it cannot run.
                  busy={busy}
                  unavailable={!busy && unchanged}
                  unavailableReason={
                    !busy && unchanged
                      ? t('profile.saveUnavailable')
                      : undefined
                  }
                >
                  {t('profile.save')}
                </ActionButton>
              </ActionRow>
            )}

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
