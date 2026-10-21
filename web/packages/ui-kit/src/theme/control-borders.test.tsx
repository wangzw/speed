/**
 * The control-boundary contract: an outlined input's resting border reads
 * `palette.text.secondary`.
 *
 * MUI's default resting border is 23% of the foreground, which lands near
 * 2:1 on this theme's surfaces -- below the 3:1 WCAG 1.4.11 asks of a
 * control boundary. The proof reads the stylesheet a real TextField emits
 * (see @speed/test-utils/emitted-css), because the color is carried by a
 * theme callback rather than by the component's own markup.
 */

import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { ThemeProvider } from '@mui/material/styles'
import TextField from '@mui/material/TextField'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { createAppTheme } from './createAppTheme.js'
import type { AppThemeMode } from './createAppTheme.js'

describe('theme control boundaries', () => {
  for (const mode of ['light', 'dark'] as const) {
    it(`paints the outlined input's resting border in text.secondary (${mode})`, () => {
      const { theme } = createAppTheme(undefined, undefined, mode as AppThemeMode)
      render(
        <ThemeProvider theme={theme}>
          <TextField label="field" />
        </ThemeProvider>,
      )
      expect(emittedStyleText()).toContain(theme.palette.text.secondary)
    })
  }
})
