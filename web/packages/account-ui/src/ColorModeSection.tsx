/**
 * ColorModeSection: the color-mode surface of the account page.
 *
 * One select over the fixed three-valued set -- dark, light, follow-device
 * -- editing the account's own `color_mode` through the self-service
 * profile route (GET /api/v1/auth/me for the stored value, PATCH
 * /api/v1/auth/me for the write; pkg/authn/profile). The values and their
 * labels belong to this package: the host supplies only the preference
 * currently in effect (`effective`), shown while the account has never
 * saved one, and receives every successful write through `onApplied`.
 *
 * The write is immediate and PATCH-first like PreferencesSection: the
 * server is asked before anything moves locally, so a refusal leaves the
 * control reading the stored value -- the natural revert -- and renders
 * the resolved code text in this section's own feedback region; an
 * accepted write renders a confirmation naming the chosen mode in the
 * same region. The
 * section never reads the theme provider and never touches storage:
 * handing the applied value to the provider and updating a device mirror
 * are the host's half, done in `onApplied`.
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

export interface ColorModeSectionProps {
  /** The preference currently in effect, shown while the account has
   * never saved one. */
  readonly effective: AppThemeModePreference
  /** Fired with the preference the account accepted. */
  readonly onApplied: (value: AppThemeModePreference) => void
  /** Drop the control's own visible label: the host names it, e.g. with
   * an item title above the control. The accessible name stays. */
  readonly hideLabel?: boolean
}

/** The fixed option set, in display order. */
const COLOR_MODES = ['dark', 'light', 'system'] as const

const OPTION_LABEL_KEYS: Readonly<Record<AppThemeModePreference, string>> = {
  dark: 'appearance.colorMode.options.dark',
  light: 'appearance.colorMode.options.light',
  system: 'appearance.colorMode.options.system',
}

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

  // The account's own choice wins; an account that has never saved one
  // shows the preference the host reports as currently in effect.
  const storedMode = data?.color_mode ?? ''
  const value = storedMode !== '' ? storedMode : effective
  // A write in flight disables the control, so a change can never race
  // its own predecessor.
  const saving = patchProfile.isPending || feedback.pending

  /** PATCH-first write: the server answers before anything local moves,
   * so a refusal needs no revert (the control reads the stored value) and
   * only this scope's region reports it. */
  function save(mode: AppThemeModePreference): Promise<void> {
    return feedback.run(async () => {
      await patchProfile.mutateAsync({ data: { color_mode: mode } })
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
      onApplied(mode)
      // The confirmation names the control and the chosen value, like the
      // language and time-zone writes: an account-setting write without a
      // success line reads as unattempted. The unchosen value is a
      // behaviour, not a mode name, so it confirms with its own sentence.
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
        // The color-mode select is short like the preference and theme
        // selects: a mode name does not need to span the content width.
        sx={{ maxWidth: '24rem' }}
        slotProps={{
          // displayEmpty keeps the control rendering whatever value it
          // is given; the explicit shrink keeps a visible label above the
          // value instead of overlapping it.
          select: {
            displayEmpty: true,
            // A hidden label leaves the control unnamed unless the name is
            // carried on the input itself.
            slotProps: {
              input: { 'aria-label': t('appearance.colorMode.label') },
            },
          },
          inputLabel: hideLabel ? undefined : { shrink: true },
        }}
        label={hideLabel ? undefined : t('appearance.colorMode.label')}
        helperText={t('appearance.colorMode.description')}
        value={value}
        disabled={saving}
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
