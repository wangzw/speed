/**
 * PreferencesSection: the language-and-timezone surface of the account
 * page.
 *
 * Two selects, both editing the account's own stored preferences through
 * the self-service profile route -- GET /api/v1/auth/me for the stored values
 * and PATCH /api/v1/auth/me for a write (pkg/authn/profile):
 *
 *  - Language: the options are the i18n instance's OWN supported set
 *    (readSupportedLanguages), so a host whose instance speaks a wider or
 *    narrower set gets exactly the choices its catalogs can render;
 *    option labels are the language names in the CURRENT UI language
 *    (Intl.DisplayNames, falling back to the raw tag when the engine
 *    cannot name it). Choosing one PATCHes the account first and, only on
 *    success, runs the persisting switchLanguage -- the settings switch
 *    lands the decision on the device (the manual-choice slot) and the
 *    account together, which is the one place both are meant to move.
 *    Clearing the preference (the "not chosen" choice, the empty string
 *    the server stores as no choice) PATCHes the account alone: the
 *    empty string is not a language the instance can switch to, so the
 *    device keeps the language it is showing.
 *  - Timezone: the options are the engine's IANA list
 *    (Intl.supportedValuesOf('timeZone'), guarded) plus an explicit
 *    "not chosen" choice whose value is the empty string -- the stored
 *    clearing value. The stored value is guaranteed present in the option
 *    list: a value the engine's list omits (a zone removed from newer
 *    tzdata, a value written by another engine) is added as its own
 *    option, because a select whose value matches no option would silently
 *    display the wrong one.
 *
 * Both writes are immediate (no submit button) and PATCH-first: the
 * server is asked before anything moves locally, so a refused write
 * leaves the controls reading the stored values -- the natural revert --
 * and renders the two-line failure alert: the save line plus the
 * resolved code text (authn.bad_request when the server refuses a
 * value, the transport codes when the request never landed). Both selects are disabled while a write is in flight, so a
 * change can never race its own predecessor.
 *
 * The write asks for a fresh authentication like the other self-service
 * writes. The section accepts an OPTIONAL session: when the host passes
 * one, a 401 `authn.insufficient_user_authentication` arms the shared
 * @speed/auth-core step-up gate, the dialog opens, and a won challenge
 * replays exactly that refused write once. Without a session the same
 * refusal renders the failure banner (the host can gate another section
 * on the same page -- elevation applies to the whole session -- or have
 * the viewer sign in again); the section never opens a dialog it has no
 * session to drive.
 *
 * Whose preferences these are comes from the caller's bound client and
 * its access token, not from the session prop, which exists only for the
 * step-up dialog. The section follows the account surfaces'
 * settled-state paradigm: an unresolved load keeps a loading placeholder
 * (one announceable status row), a settled load with no data renders the
 * ui-kit EmptyState error variant with a retry, and the settled-with-data
 * state always renders both controls (a profile answer with no stored
 * locale or time zone renders both as "not chosen").
 */

import { useMemo, useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  useAuthnProfilePatch,
} from '@speed/api-sdk'
import type { AuthSession } from '@speed/auth-core'
import { useStepUpGate } from '@speed/auth-core'
import {
  languageDisplayName,
  readSupportedLanguages,
  switchLanguage,
} from '@speed/i18n'
import {
  AsyncSection,
  errorCodeOf,
  InlineSuccess,
  SectionCard,
} from '@speed/ui-kit'
import { useAccountUiErrorText } from './internal/error-text.js'
import { useProfileQuery } from './internal/queries.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'

export interface PreferencesSectionProps {
  /**
   * Optional: when given, a write refused for a missing step-up is
   * remembered and replayed after the shared step-up dialog's flow
   * succeeds. Without it the refusal renders the failure banner.
   */
  readonly session?: AuthSession
}

/** A refused write, kept so a won step-up can replay exactly it. */
type PreferenceAction =
  | { readonly kind: 'locale'; readonly locale: string }
  | { readonly kind: 'timezone'; readonly timezone: string }

const STEP_UP_REQUIRED = 'authn.insufficient_user_authentication'

/** The engine's IANA timezone list, sorted, or [] when the environment
 * cannot produce one (Intl.supportedValuesOf is newer than the rest of
 * Intl, so its absence is a real deployment shape): the select then
 * offers the "not chosen" choice plus -- via the stored-value guard in
 * the renderer -- whatever value the account already holds, never a
 * fabricated list. */
function supportedTimeZones(): readonly string[] {
  try {
    const values = Intl.supportedValuesOf('timeZone')
    return [...values].sort()
  } catch {
    return []
  }
}

export function PreferencesSection({ session }: PreferencesSectionProps = {}) {
  const { t, i18n } = useAccountUiTranslation()
  const errorText = useAccountUiErrorText()
  const queryClient = useQueryClient()
  const { data, isPending, refetch } = useProfileQuery()
  const patchProfile = useAuthnProfilePatch()
  const [failureCode, setFailureCode] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  const gate = useStepUpGate<PreferenceAction>((action) => {
    if (action.kind === 'locale') {
      void saveLocale(action.locale)
    } else {
      void saveTimezone(action.timezone)
    }
  })

  const languageOptions = useMemo(
    () => readSupportedLanguages(i18n),
    [i18n],
  )
  const timeZoneOptions = useMemo(() => supportedTimeZones(), [])

  const pending = isPending && data === undefined
  const saving = patchProfile.isPending

  const storedLocale = data?.locale ?? ''
  const storedTimezone = data?.time_zone ?? ''

  // The stored timezone is always selectable, even when the engine's list
  // omits it (see the header): the option list is the engine's, plus the
  // stored value when absent from it.
  const timeZoneChoices = useMemo(() => {
    if (storedTimezone !== '' && !timeZoneOptions.includes(storedTimezone)) {
      return [storedTimezone, ...timeZoneOptions]
    }
    return timeZoneOptions
  }, [storedTimezone, timeZoneOptions])

  /** PATCH-first write: the server answers before anything local moves,
   * so a refusal needs no revert (the controls read the stored values)
   * and only the failure alert renders. */
  async function saveLocale(locale: string): Promise<void> {
    setFailureCode(null)
    setSaved(false)
    try {
      await patchProfile.mutateAsync({ data: { locale } })
      // The empty string IS the clearing value, not a language: a
      // successful clear leaves the device on its current language, so
      // only a real choice is switched to (switchLanguage refuses the
      // empty string, which must not read as a failed save).
      if (locale !== '') {
        // The account accepted the choice; land it on the device too (the
        // persisting form -- this is the manual language-switch UI). A
        // storage failure inside switchLanguage is a console warning, never
        // a rejection: the switch itself still happens.
        await switchLanguage(i18n, locale)
      }
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
      setSaved(true)
    } catch (error) {
      const code = errorCodeOf(error)
      if (code === STEP_UP_REQUIRED && session !== undefined) {
        gate.arm({ kind: 'locale', locale })
        return
      }
      setFailureCode(code)
    }
  }

  async function saveTimezone(timezone: string): Promise<void> {
    setFailureCode(null)
    setSaved(false)
    try {
      await patchProfile.mutateAsync({ data: { time_zone: timezone } })
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
      setSaved(true)
    } catch (error) {
      const code = errorCodeOf(error)
      if (code === STEP_UP_REQUIRED && session !== undefined) {
        gate.arm({ kind: 'timezone', timezone })
        return
      }
      setFailureCode(code)
    }
  }

  return (
    <>
      <SectionCard title={t('preferences.title')}>
        <AsyncSection
        pending={pending}
        payload={data}
        empty={false}
        loading={
          <Box
            role="status"
            aria-label={t('preferences.loading')}
            aria-busy="true"
          >
            <Skeleton variant="text" width="42%" />
            <Skeleton variant="text" width="58%" />
          </Box>
        }
        errorState={{
          title: t('preferences.error.title'),
          description: t('preferences.error.description'),
          retryLabel: t('preferences.retry'),
          onRetry: () => void refetch(),
        }}
      >
        {() => (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2.5 }}>
            {failureCode !== null && (
              <Alert severity="error">
                {/* The two-line failure alert: what happened, then the
                    server's own code text (the whitelist resolves
                    authn.bad_request and the transport codes; anything else
                    reads the generic line). */}
                <Typography variant="body2">
                  {t('preferences.saveFailed')}
                </Typography>
                <Typography variant="body2">
                  {errorText(failureCode)}
                </Typography>
              </Alert>
            )}

            {/* The landed write's confirmation, in a polite status region (a
                write here is immediate, so a silent success would leave the
                operator unsure the choice took). */}
            <InlineSuccess message={saved ? t('preferences.saved') : null} />

            <TextField
              select
              fullWidth
              // Both preference controls are short: a language tag or an
              // IANA zone name does not need to span the whole content
              // width, so each is capped to a comfortable select width and
              // fills less than the row on a wide viewport.
              sx={{ maxWidth: '24rem' }}
              slotProps={{
                // displayEmpty rides the Select slot (MUI 9's slotProps
                // architecture): the "not chosen" choice carries the empty
                // string, and without this the control renders blank instead
                // of the option's own label. The empty value is why the
                // InputLabel is shrunk explicitly -- MUI floats the label on
                // its own only when the select is filled, so in the "not
                // chosen" state the label would otherwise sit on the
                // placeholder and overlap it.
                select: { displayEmpty: true },
                inputLabel: { shrink: true },
              }}
              label={t('preferences.language.label')}
              value={storedLocale}
              disabled={saving}
              onChange={(event) => {
                void saveLocale(event.target.value)
              }}
              helperText={t('preferences.language.helper')}
            >
              {/* The "not chosen" choice first: an empty stored locale is
               * the account's real initial state, and the chain's device
               * and default tiers are what actually apply until one is
               * chosen. */}
              <MenuItem value="">
                {t('preferences.language.notChosen')}
              </MenuItem>
              {languageOptions.map((option) => (
                <MenuItem key={option} value={option}>
                  {languageDisplayName(option, i18n.language)}
                </MenuItem>
              ))}
            </TextField>

            <TextField
              select
              fullWidth
              // Capped like the language select above: a zone name is short
              // and the control need not span the content width.
              sx={{ maxWidth: '24rem' }}
              // Same two slots as the language select above: displayEmpty
              // for the "not chosen" placeholder and an explicit shrink so
              // the label floats above it instead of overlapping.
              slotProps={{
                select: { displayEmpty: true },
                inputLabel: { shrink: true },
              }}
              label={t('preferences.timezone.label')}
              value={storedTimezone}
              disabled={saving}
              onChange={(event) => {
                void saveTimezone(event.target.value)
              }}
              helperText={t('preferences.timezone.helper')}
            >
              <MenuItem value="">
                {t('preferences.timezone.notChosen')}
              </MenuItem>
              {timeZoneChoices.map((option) => (
                <MenuItem key={option} value={option}>
                  {option}
                </MenuItem>
              ))}
            </TextField>
          </Box>
        )}
      </AsyncSection>
      </SectionCard>

      {session !== undefined && (
        <StepUpChallenge
          open={gate.open}
          session={session}
          onSuccess={gate.succeed}
          onCancel={gate.cancel}
        />
      )}
    </>
  )
}
