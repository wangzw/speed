/**
 * AppearanceSection: the controlled theme-preset picker on the account page.
 */

import MenuItem from '@mui/material/MenuItem'
import TextField from '@mui/material/TextField'
import { SectionCard, useInertSelectMenu } from '@speed/ui-kit'
import { useAccountUiTranslation } from './internal/translation.js'

/** One preset the host offers: its id and its host-translated label. */
export interface ThemePresetChoice {
  /** The preset's id, matched against the section's selected value and
   * reported by `onChange`. */
  readonly id: string
  /** The choice's visible name, translated by the host. */
  readonly label: string
}

/**
 * The host owns the preset list and the current selection; the section
 * holds no state.
 */
export interface AppearanceSectionProps {
  /** The preset choices the host offers, in display order. */
  readonly presets: readonly ThemePresetChoice[]
  /** The id of the currently selected preset. */
  readonly value: string
  /** Fired with the chosen preset's id. */
  readonly onChange: (id: string) => void
  /** Drop the control's own visible label; the accessible name stays. */
  readonly hideLabel?: boolean
}

/** The controlled theme-preset picker. */
export function AppearanceSection({
  presets,
  value,
  onChange,
  hideLabel = false,
}: AppearanceSectionProps) {
  const { t } = useAccountUiTranslation()
  const menu = useInertSelectMenu()

  return (
    <SectionCard
      title={t('appearance.title')}
      description={t('appearance.description')}
    >
      <TextField
        select
        fullWidth
        sx={{ maxWidth: '24rem' }}
        slotProps={{
          // displayEmpty keeps the select rendering when no option matches the value.
          select: {
            ...menu,
            displayEmpty: true,
            slotProps: {
              input: { 'aria-label': t('appearance.theme.label') },
            },
          },
          inputLabel: hideLabel ? undefined : { shrink: true },
        }}
        label={hideLabel ? undefined : t('appearance.theme.label')}
        helperText={t('appearance.theme.helper')}
        value={value}
        onChange={(event) => {
          onChange(event.target.value)
        }}
      >
        {presets.map((preset) => (
          <MenuItem key={preset.id} value={preset.id}>
            {preset.label}
          </MenuItem>
        ))}
      </TextField>
    </SectionCard>
  )
}
