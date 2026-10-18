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
 * the code whitelist and renders in the banner above the field; a
 * successful save refetches the profile so the field shows the account's
 * own answer, and its success notice is a role="status" live region,
 * mounted empty and visually clipped until the notice fills it (a live
 * region announces only content changes that follow its own insertion).
 *
 * The section takes the session as its only prop -- the step-up dialog
 * drives it -- while the profile read and the PATCH travel on the shared
 * access token through the generated calls.
 */

import { useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  useAuthnProfilePatch,
} from '@speed/api-sdk'
import type { AuthSession } from '@speed/auth-core'
import {
  AsyncSection,
  errorCodeOf,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { useProfileQuery } from './internal/queries.js'
import { useStepUpGate } from './internal/step-up-gate.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'

/** The code the profile write answers an unelevated caller with; the one
 * that opens the step-up dialog rather than the plain failure banner. */
const STEP_UP_REQUIRED = 'authn.insufficient_user_authentication'

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
  const [failureCode, setFailureCode] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  const gate = useStepUpGate<{ readonly name: string }>((action) => {
    void save(action.name)
  })

  const pending = isPending && data === undefined
  const name = editedName ?? data?.display_name ?? ''

  function invalidateProfile(): Promise<void> {
    return queryClient.invalidateQueries({
      queryKey: getAuthnProfileGetQueryKey(),
    })
  }

  async function save(nextName: string): Promise<void> {
    setFailureCode(null)
    setSaved(false)
    try {
      await patchProfile.mutateAsync({ data: { display_name: nextName } })
      setEditedName(null)
      setSaved(true)
      await invalidateProfile()
    } catch (error) {
      const code = errorCodeOf(error)
      if (code === STEP_UP_REQUIRED) {
        gate.arm({ name: nextName })
        return
      }
      setFailureCode(code)
    }
  }

  return (
    <Box>
      <Box sx={{ mb: 2 }}>
        <Typography variant="h5" component="h2">
          {t('profile.title')}
        </Typography>
      </Box>

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
              maxWidth: 600,
            }}
          >
            {/* The save notice is a role="status" live region mounted for
                the whole settled phase: clipped and visually silent until
                a save lands, when the same node fills with the notice, so
                a screen reader announces a change to a region it already
                knows rather than a region born with its text. */}
            <Alert
              severity="success"
              role="status"
              sx={saved ? { width: '100%' } : visuallyHiddenSx}
            >
              {saved ? t('profile.saved') : ''}
            </Alert>

            <InlineError code={failureCode} />

            <TextField
              fullWidth
              label={t('profile.displayName.label')}
              value={name}
              disabled={patchProfile.isPending}
              onChange={(event) => setEditedName(event.target.value)}
            />

            <Button
              type="submit"
              variant="contained"
              // Disabled while the field still holds the account's own name:
              // a save with nothing changed would fire a no-op PATCH, and the
              // console account detail's save controls set the same
              // no-change-disabled convention.
              disabled={patchProfile.isPending || name === (data?.display_name ?? '')}
            >
              {t('profile.save')}
            </Button>
          </Box>
        )}
      </AsyncSection>

      <StepUpChallenge
        open={gate.open}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
      />
    </Box>
  )
}
