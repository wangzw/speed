/**
 * ColorModeSection: the color-mode select over the account's own `color_mode`.
 */

import MenuItem from '@mui/material/MenuItem'
import TextField from '@mui/material/TextField'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  useAuthnProfilePatch,
} from '@speed/api-sdk'
import { SectionCard, useWriteFeedback } from '@speed/ui-kit'
import type { AppThemeModePreference } from '@speed/ui-kit'
import { useProfileQuery } from './internal/queries.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

/**
 * The host supplies the preference shown before the account has saved one
 * and receives the accepted mode.
 */
export interface ColorModeSectionProps {
  /** The preference currently in effect, shown while the account has
   * never saved one. */
  readonly effective: AppThemeModePreference
  /** Fired with the preference the account accepted. */
  readonly onApplied: (value: AppThemeModePreference) => void
  /** Drop the control's own visible label; the accessible name stays. */
  readonly hideLabel?: boolean
}

const COLOR_MODES = ['dark', 'light', 'system'] as const

const OPTION_LABEL_KEYS: Readonly<Record<AppThemeModePreference, string>> = {
  dark: 'appearance.colorMode.options.dark',
  light: 'appearance.colorMode.options.light',
  system: 'appearance.colorMode.options.system',
}

/** The color-mode select over the account's own `color_mode`. */
export function ColorModeSection({
  effective,
  onApplied,
  hideLabel = false,
}: ColorModeSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data } = useProfileQuery()
  const patchProfile = useAuthnProfilePatch()
  const feedback = useWriteFeedback()

  const storedMode = data?.color_mode ?? ''
  const value = storedMode !== '' ? storedMode : effective
  // readOnly, not disabled: the select that started the write keeps focus.
  const saving = patchProfile.isPending || feedback.pending

  // Nothing local moves before the server answers, so a refusal needs no revert.
  function save(mode: AppThemeModePreference): Promise<void> {
    return feedback.run(async () => {
      await patchProfile.mutateAsync({ data: { color_mode: mode } })
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
      onApplied(mode)
      if (mode === 'system') {
        return t('appearance.colorMode.savedSystem')
      }
      return t('appearance.colorMode.saved', {
        value: t(OPTION_LABEL_KEYS[mode]),
        interpolation: { escapeValue: false },
      })
    })
  }

  return (
    <SectionCard title={t('appearance.colorMode.title')}>
      <TextField
        select
        fullWidth
        sx={{ maxWidth: '24rem' }}
        slotProps={{
          input: { readOnly: saving },
          select: {
            displayEmpty: true,
            slotProps: {
              input: { 'aria-label': t('appearance.colorMode.label') },
            },
          },
          inputLabel: hideLabel ? undefined : { shrink: true },
        }}
        label={hideLabel ? undefined : t('appearance.colorMode.label')}
        helperText={t('appearance.colorMode.description')}
        value={value}
        onChange={(event) => {
          void save(event.target.value as AppThemeModePreference)
        }}
      >
        {COLOR_MODES.map((mode) => (
          <MenuItem key={mode} value={mode}>
            {t(OPTION_LABEL_KEYS[mode])}
          </MenuItem>
        ))}
      </TextField>
      <WriteFeedbackRegion outcome={feedback.outcome} />
    </SectionCard>
  )
}
