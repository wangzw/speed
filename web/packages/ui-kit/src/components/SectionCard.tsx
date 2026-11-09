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
 * section content breathes. With an icon the content also shares the
 * title's rail -- it starts where the title starts, after the icon column --
 * so a form or an info block lines up under its heading. A table-bearing
 * card passes `contentAligned={false}` instead: the heading keeps the icon
 * rail, but the content starts at the card's own padding, because a table
 * cannot spare the width the rail would take. `flushContent` removes that
 * padding for a table that must reach the card's edges; the surface itself
 * stays.
 *
 * A section nested inside another surface (a row's expanded body inside
 * the pane that already holds a card) would otherwise stack a second card
 * and a second heading inside the first. A host marks that region with
 * `SectionSurfaceProvider value="plain"`: under it `SectionCard`
 * contributes no card, no heading element, no description and no content
 * padding -- it keeps the section's trailing actions above its children.
 * The wrapper stays a `<section>` element: a plain string title still names
 * it (`aria-label`), so a page assembled from block cards keeps every
 * section's landmark, and `sx` and `className` still reach it. Without a
 * provider, or with `value="card"`, the component renders exactly the card
 * surface described above.
 */

import { createContext, forwardRef, useContext, useId } from 'react'
import type { ReactNode, Ref } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import Box from '@mui/material/Box'
import Card from '@mui/material/Card'
import Typography from '@mui/material/Typography'
import { ActionRow } from './ActionRow.js'

/**
 * What a `SectionCard` contributes to the surface it renders in: its own
 * card and heading (`'card'`), or content alone (`'plain'`).
 */
export type SectionSurface = 'card' | 'plain'

/** The surface a `SectionCard` renders in; `'card'` when no provider says otherwise. */
export const SectionSurfaceContext = createContext<SectionSurface>('card')

export type SectionSurfaceProviderProps = {
  /** The surface the sections below it render in; defaults to `'card'`. */
  readonly value?: SectionSurface
  readonly children?: ReactNode
}

/** Marks the region its children render in as contributing its own surface. */
export function SectionSurfaceProvider({
  value = 'card',
  children,
}: SectionSurfaceProviderProps) {
  return (
    <SectionSurfaceContext.Provider value={value}>
      {children}
    </SectionSurfaceContext.Provider>
  )
}

/** The surface the enclosing provider declares. */
function useSectionSurface(): SectionSurface {
  return useContext(SectionSurfaceContext)
}

/** The semantic heading element a section title can render as. */
export type SectionCardHeadingLevel = 'h2' | 'h3' | 'h4' | 'h5' | 'h6'

export type SectionCardProps = {
  /**
   * The section title. Given, it renders as a real heading element (the
   * level `headingLevel` names) and becomes the section's accessible name;
   * omitted or `null`, no heading block renders at all. A plain surface
   * renders no heading element; a string title still names the section.
   */
  readonly title?: ReactNode
  /**
   * Optional icon beside the title, in a fixed leading column. It renders
   * only with a title, on the card surface, like the description and
   * actions; the heading lands on that rail, and the content follows it
   * unless `contentAligned` is false.
   */
  readonly icon?: ReactNode
  /**
   * Optional supporting paragraph under the title; a plain surface renders
   * nothing for it.
   */
  readonly description?: ReactNode
  /**
   * Optional trailing actions (buttons, menus) aligned with the title; a
   * plain surface keeps them above the content.
   */
  readonly actions?: ReactNode
  /**
   * The semantic level of the title; defaults to 'h2'. It has no effect on
   * a plain surface, which renders no heading.
   */
  readonly headingLevel?: SectionCardHeadingLevel
  /**
   * Removes the content area's padding so a table can sit edge to edge
   * inside the surface; defaults to false. A plain surface adds no content
   * padding of its own, so the flag has no effect there.
   */
  readonly flushContent?: boolean
  /**
   * Aligns the content with the title's rail when the card carries an icon,
   * skipping the icon column; defaults to true. A table-bearing card passes
   * false so its table keeps the card's full content width.
   */
  readonly contentAligned?: boolean
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
    icon,
    description,
    actions,
    headingLevel = 'h2',
    flushContent = false,
    contentAligned = true,
    children,
    sx,
    className,
  }: SectionCardProps,
  ref: Ref<HTMLElement>,
) {
  const surface = useSectionSurface()
  const titleId = useId()
  const hasTitle = title !== undefined && title !== null

  if (surface === 'plain') {
    // A plain surface drops the card and its heading, not the section's name:
    // a string title still names the section for a screen reader, so a page
    // built from block cards keeps each section's landmark.
    const plainName =
      typeof title === 'string' && title !== '' ? title : undefined
    return (
      <Box
        ref={ref}
        component="section"
        className={className}
        aria-label={plainName}
        sx={sx}
      >
        {/* A section's trailing actions stay reachable on a plain surface:
            the host drops the card, not the section's own controls. */}
        {actions !== undefined && actions !== null && (
          <ActionRow sx={{ mb: 2 }}>{actions}</ActionRow>
        )}
        {children}
      </Box>
    )
  }

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
            display: 'grid',
            gridTemplateColumns: {
              xs: (icon !== undefined && icon !== null
                ? '2rem '
                : '') + 'minmax(0, 1fr)',
              sm:
                (icon !== undefined && icon !== null ? '2rem ' : '') +
                'minmax(0, 1fr)' +
                (actions !== undefined && actions !== null ? ' auto' : ''),
            },
            alignItems: 'center',
            columnGap: 2,
            rowGap: 2,
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
          {icon !== undefined && icon !== null && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                color: 'text.secondary',
              }}
            >
              {icon}
            </Box>
          )}
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
            <ActionRow
              sx={{ flexShrink: 0, gridColumn: { xs: '1 / -1', sm: 'auto' } }}
            >
              {actions}
            </ActionRow>
          )}
        </Box>
      )}
      <Box
        sx={
          flushContent
            ? undefined
            : {
                p: 3,
                // The content shares the title's rail: the icon column and
                // the gap before the title are skipped, so every row starts
                // where the section's own name starts. A table-bearing card
                // opts out, keeping the full content width instead.
                ...(icon !== undefined && icon !== null && contentAligned
                  ? {
                      pl: (theme: { spacing: (n: number) => string }) =>
                        `calc(2rem + ${theme.spacing(2)} + ${theme.spacing(3)})`,
                    }
                  : {}),
              }
        }
      >
        {children}
      </Box>
    </Card>
  )
}

/**
 * The titled section surface: an outlined card rendered as a `<section>`
 * whose heading, description and actions sit above an optionally padded
 * content area, or -- under a plain surface -- its trailing actions and
 * content, with no card, no heading and no description.
 */
export const SectionCard = forwardRef(SectionCardInner)
