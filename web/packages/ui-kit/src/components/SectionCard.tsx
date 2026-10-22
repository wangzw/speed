/**
 * SectionCard: the surface for one titled section of a page.
 *
 * A section on a console page is a container surface: a bounded,
 * outlined area holding a heading, optional supporting text, optional
 * trailing actions and the section's content. `SectionCard` owns that
 * shape so every page's sections share one surface and one heading
 * hierarchy instead of a hand-written `Paper variant="outlined"` here
 * and a bare `Box component="section"` there.
 *
 * The component renders MUI's `Card` as a `<section>`: `Card` carries the
 * container surface through the theme's `variant="outlined"` override and
 * clips its content to the surface radius (`overflow: hidden`), so a
 * table placed flush against the edges is cut to the card's corners.
 *
 * The title renders as a real heading element, `headingLevel` deciding
 * which one: a section's own title is an `h2` under the page's `h1`,
 * while a section nested inside another section's content is an `h3`.
 * The visible size is always the `h5` typography role (20px from the
 * token scale); `component` keeps the semantic level independent of that
 * look. With a title the section is named by it through `aria-labelledby`,
 * which also exposes the `<section>` as a region landmark; without a
 * title no heading block renders and the section carries no accessible
 * name.
 *
 * The description and actions belong to the heading block: they render
 * only when a title does, because a section with no heading has no block
 * for them to attach to.
 *
 * The content area is padded at the theme's spacing scale so ordinary
 * section content breathes. `flushContent` removes that padding for a
 * table that must reach the card's edges; the surface itself stays.
 */

import { forwardRef, useId } from 'react'
import type { ReactNode, Ref } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import Box from '@mui/material/Box'
import Card from '@mui/material/Card'
import Typography from '@mui/material/Typography'

/** The semantic heading element a section title can render as. */
export type SectionCardHeadingLevel = 'h2' | 'h3' | 'h4' | 'h5' | 'h6'

export type SectionCardProps = {
  /**
   * The section title. Given, it renders as a real heading element (the
   * level `headingLevel` names) and becomes the section's accessible name;
   * omitted or `null`, no heading block renders at all.
   */
  readonly title?: ReactNode
  /** Optional supporting paragraph under the title. */
  readonly description?: ReactNode
  /** Optional trailing actions (buttons, menus) aligned with the title. */
  readonly actions?: ReactNode
  /** The semantic level of the title; defaults to 'h2'. */
  readonly headingLevel?: SectionCardHeadingLevel
  /**
   * Removes the content area's padding so a table can sit edge to edge
   * inside the surface; defaults to false.
   */
  readonly flushContent?: boolean
  /** The section's content. */
  readonly children: ReactNode
  /** Extra styling applied to the card root. */
  readonly sx?: SxProps<Theme>
  /** Extra class applied to the card root. */
  readonly className?: string
}

function SectionCardInner(
  {
    title,
    description,
    actions,
    headingLevel = 'h2',
    flushContent = false,
    children,
    sx,
    className,
  }: SectionCardProps,
  ref: Ref<HTMLElement>,
) {
  const titleId = useId()
  const hasTitle = title !== undefined && title !== null

  return (
    <Card
      ref={ref}
      component="section"
      variant="outlined"
      className={className}
      aria-labelledby={hasTitle ? titleId : undefined}
      sx={sx}
    >
      {hasTitle && (
        <Box
          sx={{
            display: 'flex',
            alignItems: 'flex-start',
            justifyContent: 'space-between',
            gap: 2,
            flexWrap: 'wrap',
            px: 3,
            pt: 3,
            // A flush child starts at the card's edge, so the heading block
            // has to carry the separation itself: otherwise the gap is only
            // the line box's slack, which a denser type scale (a preset with
            // tighter line heights) shrinks to nothing and the child's first
            // row -- a table's header band -- sits on the title's descenders.
            ...(flushContent ? { pb: 3 } : {}),
          }}
        >
          <Box sx={{ minWidth: 0 }}>
            <Typography id={titleId} component={headingLevel} variant="h5">
              {title}
            </Typography>
            {description !== undefined && description !== null && (
              <Typography variant="body2" color="text.secondary" sx={{ mt: 0.5 }}>
                {description}
              </Typography>
            )}
          </Box>
          {actions !== undefined && actions !== null && (
            <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexShrink: 0 }}>
              {actions}
            </Box>
          )}
        </Box>
      )}
      <Box sx={flushContent ? undefined : { p: 3 }}>{children}</Box>
    </Card>
  )
}

/**
 * The titled section surface: an outlined card rendered as a `<section>`
 * whose heading, description and actions sit above an optionally padded
 * content area.
 */
export const SectionCard = forwardRef(SectionCardInner)
