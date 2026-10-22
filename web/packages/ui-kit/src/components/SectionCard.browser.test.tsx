/**
 * SectionCard with flush content, in a real browser: the title's
 * separation from what follows it.
 *
 * A `flushContent` section drops the content area's padding, so the
 * distance between the title and the first row of that content is whatever
 * the heading block reserves. Relying on the line box's own slack is not a
 * gap: under a preset with a denser type scale (compact renders the title at
 * 18px with a 1.15 line height) the slack shrinks to well under a pixel, and
 * the following row -- a table's header band, an opaque surface -- paints
 * across the title's descenders. The cases measure the geometry in
 * Chromium, with a title ending in a descender as the worst case, and hold
 * every shipped preset to at least two of its own spacing units.
 */
import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { themePresets } from '@speed/theme-presets'
import { defaultTokens } from '@speed/tokens'
import { DataTable } from './DataTable.js'
import type { DataTableColumn } from './DataTable.js'
import { SectionCard } from './SectionCard.js'
import { renderWithProviders } from '../../test-utils/render.js'

interface Row {
  readonly id: string
}

const ROW: Row = { id: 'acme' }

const COLUMNS: readonly DataTableColumn<Row>[] = [
  { id: 'id', header: 'Identifier', cell: (row) => row.id },
]

/**
 * The fewest spacing units a titled flush section separates its title from
 * its child by. The padding itself is the section's `pb: 3`; the floor is
 * expressed in units so a preset that only changes the spacing base still
 * carries a real gap.
 */
const MINIMUM_SPACING_STEPS = 2

/** One shipped preset and the spacing unit it renders with. */
function presetsUnderTest() {
  return themePresets.map((preset) => ({
    id: preset.id,
    unit: preset.tokens.spacing?.unit ?? defaultTokens.spacing.unit,
  }))
}

describe('SectionCard flush separation in Chromium', () => {
  for (const { id, unit } of presetsUnderTest()) {
    it(`keeps a ${MINIMUM_SPACING_STEPS}-unit gap under the ${id} preset`, async () => {
      await page.viewport(1280, 900)
      renderWithProviders(
        <SectionCard title="Recent activity" flushContent>
          <DataTable rows={[ROW]} columns={COLUMNS} rowKey={(row) => row.id} />
        </SectionCard>,
        { language: 'en-US', presets: themePresets, defaultPreset: id },
      )

      // The title's line box is where its glyphs live; the header band is
      // the child's first opaque row. Their distance is the whole gap.
      const heading = await screen.findByRole('heading', {
        name: 'Recent activity',
      })
      const headerCell = screen.getByRole('columnheader', {
        name: 'Identifier',
      })
      const gap =
        headerCell.getBoundingClientRect().top -
        heading.getBoundingClientRect().bottom
      expect(gap).toBeGreaterThanOrEqual(MINIMUM_SPACING_STEPS * unit - 0.5)
    })
  }
})
