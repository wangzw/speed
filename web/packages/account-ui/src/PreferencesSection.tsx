/**
 * PreferencesSection: the language-and-timezone surface of the account page.
 */

import { useMemo } from 'react'
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
import {
  languageDisplayName,
  readSupportedLanguages,
  switchLanguage,
} from '@speed/i18n'
import {
  SectionCard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { useProfileQuery } from './internal/queries.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'
import { ACCOUNT_UI_NAMESPACE } from './resources.js'

/** Sorted, or [] when the engine cannot produce one: the select then offers
 * "not chosen" plus the stored value, never a fabricated list. */
function supportedTimeZones(): readonly string[] {
  try {
    const values = Intl.supportedValuesOf('timeZone')
    return [...values].sort()
  } catch {
    return []
  }
}

/** The language and time-zone selects over the profile's own PATCH. */
export function PreferencesSection() {
  const { t, i18n } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const patchProfile = useAuthnProfilePatch()
  const languageFeedback = useWriteFeedback()
  const timezoneFeedback = useWriteFeedback()

  const languageOptions = useMemo(
    () => readSupportedLanguages(i18n),
    [i18n],
  )
  const timeZoneOptions = useMemo(() => supportedTimeZones(), [])

  const pending = isPending && data === undefined
  // Read-only rather than disabled: a select keeps focus while the write runs.
  const saving =
    patchProfile.isPending || languageFeedback.pending || timezoneFeedback.pending

  const storedLocale = data?.locale ?? ''
  const storedTimezone = data?.time_zone ?? ''

  // A select whose value matches no option displays the wrong one, so a
  // stored zone the engine's list omits is added as its own option.
  const timeZoneChoices = useMemo(() => {
    if (storedTimezone !== '' && !timeZoneOptions.includes(storedTimezone)) {
      return [storedTimezone, ...timeZoneOptions]
    }
    return timeZoneOptions
  }, [storedTimezone, timeZoneOptions])

  /** The server answers before anything local moves, so a refusal needs no
   * revert. */
  function saveLocale(locale: string): Promise<void> {
    return languageFeedback.run(async () => {
      await patchProfile.mutateAsync({ data: { locale } })
      // The empty string is the clearing value, not a language: only a real
      // choice is switched to, and a clear leaves the device as it is.
      if (locale !== '') {
        await switchLanguage(i18n, locale)
      }
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
      // Translated in the language the write just chose.
      const target = locale === '' ? i18n.language : locale
      const translate = i18n.getFixedT(target, ACCOUNT_UI_NAMESPACE)
      if (locale === '') {
        return translate('preferences.language.followsDevice')
      }
      return translate('preferences.language.saved', {
        value: languageDisplayName(locale, target, languageOptions),
        interpolation: { escapeValue: false },
      })
    })
  }

  function saveTimezone(timezone: string): Promise<void> {
    return timezoneFeedback.run(async () => {
      await patchProfile.mutateAsync({ data: { time_zone: timezone } })
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
      if (timezone === '') {
        return t('preferences.timezone.followsDevice')
      }
      return t('preferences.timezone.saved', {
        value: timezone,
        interpolation: { escapeValue: false },
      })
    })
  }

  return (
    <SectionCard title={t('preferences.title')}>
      <AccountAsyncSection
        pending={pending}
        payload={data}
        empty={false}
        error={error}
        refreshing={isFetching}
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
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            <Box sx={{ display: 'flex', flexDirection: 'column' }}>
              <Typography variant="body1" sx={{ fontWeight: 600 }}>
                {t('preferences.language.label')}
              </Typography>
              <TextField
                select
                fullWidth
                sx={{ mt: 0.5, maxWidth: '24rem' }}
                slotProps={{
                  input: { readOnly: saving },
                  // displayEmpty: without it the empty-string "not chosen"
                  // value renders blank instead of the option's own label.
                  select: {
                    displayEmpty: true,
                    slotProps: {
                      input: {
                        'aria-label': t('preferences.language.label'),
                      },
                    },
                  },
                }}
                value={storedLocale}
                onChange={(event) => {
                  void saveLocale(event.target.value)
                }}
                helperText={t('preferences.language.helper')}
              >
                <MenuItem value="">
                  {t('preferences.language.notChosen')}
                </MenuItem>
                {languageOptions.map((option) => (
                  <MenuItem key={option} value={option}>
                    {languageDisplayName(option, i18n.language, languageOptions)}
                  </MenuItem>
                ))}
              </TextField>
              <WriteFeedbackRegion outcome={languageFeedback.outcome} />
            </Box>

            <Box sx={{ display: 'flex', flexDirection: 'column' }}>
              <Typography variant="body1" sx={{ fontWeight: 600 }}>
                {t('preferences.timezone.label')}
              </Typography>
              <TextField
                select
                fullWidth
                sx={{ mt: 0.5, maxWidth: '24rem' }}
                slotProps={{
                  input: { readOnly: saving },
                  select: {
                    displayEmpty: true,
                    slotProps: {
                      input: {
                        'aria-label': t('preferences.timezone.label'),
                      },
                    },
                  },
                }}
                value={storedTimezone}
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
              <WriteFeedbackRegion outcome={timezoneFeedback.outcome} />
            </Box>
          </Box>
        )}
      </AccountAsyncSection>
    </SectionCard>
  )
}
