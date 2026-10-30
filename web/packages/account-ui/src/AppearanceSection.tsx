/**
 * AppearanceSection: the visual-theme surface of the account page.
 *
 * The section is fully controlled. Which themes exist and what each is
 * called are the HOST's facts, not this package's: it takes the preset
 * choices as `presets` (an id plus an already-translated label), the
 * current selection as `value`, and reports a pick through `onChange`.
 * That is why the package neither imports @speed/theme-presets nor reads
 * the theme provider -- a host can offer its own presets, or none, and
 * this surface lays out and drives the choice without knowing where the
 * themes come from or where the selection is stored.
 *
 * One select over the supplied choices, capped to a comfortable width
 * like the preferences selects. The current value renders as the
 * select's own text; opening it exposes every preset, the current one
 * marked selected. An empty or single-item list is a valid input and
 * never collapses the section: the heading and the labelled control
 * still render (an empty list simply offers no option).
 *
 * The selected preset is applied by the host, so the section carries no
 * state of its own and no confirmation notice: choosing a theme is an
 * immediate, visible change, and the host decides whether it is also
 * remembered.
 */

import MenuItem from '@mui/material/MenuItem'
import TextField from '@mui/material/TextField'
import { SectionCard } from '@speed/ui-kit'
import { useAccountUiTranslation } from './internal/translation.js'

/** One preset the host offers: its id and its host-translated label. */
export interface ThemePresetChoice {
  readonly id: string
  /** The choice's visible name, translated by the host. */
  readonly label: string
}

export interface AppearanceSectionProps {
  /** The preset choices the host offers, in display order. */
  readonly presets: readonly ThemePresetChoice[]
  /** The id of the currently selected preset. */
  readonly value: string
  /** Fired with the chosen preset's id. */
  readonly onChange: (id: string) => void
  /** Drop the control's own visible label: the host names it, e.g. with
   * an item title above the control. The accessible name stays. */
  readonly hideLabel?: boolean
}

export function AppearanceSection({
  presets,
  value,
  onChange,
  hideLabel = false,
}: AppearanceSectionProps) {
  const { t } = useAccountUiTranslation()

  return (
    <SectionCard
      title={t('appearance.title')}
      description={t('appearance.description')}
    >
      <TextField
        select
        fullWidth
        // The preset select is short like the preference selects: an id
        // or a theme name does not need to span the content width.
        sx={{ maxWidth: '24rem' }}
        slotProps={{
          // displayEmpty keeps the control rendering when the effective
          // value matches no option (an empty preset list); the explicit
          // shrink keeps the label above the empty value instead of
          // overlapping it, the same pairing the preferences selects use.
          select: {
            displayEmpty: true,
            // A hidden label leaves the control unnamed unless the name is
            // carried on the input itself.
            slotProps: {
              input: { 'aria-label': t('appearance.theme.label') },
            },
          },
          inputLabel: hideLabel ? undefined : { shrink: true },
        }}
        label={hideLabel ? undefined : t('appearance.theme.label')}
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
