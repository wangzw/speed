/**
 * The surface for one titled section: an outlined card rendered as a
 * `<section>`, or content alone under `SectionSurfaceProvider value="plain"`.
 * A plain surface still keeps the section's name and trailing actions.
 */

import { createContext, forwardRef, useContext, useId } from 'react'
import type { ReactNode, Ref } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import Box from '@mui/material/Box'
import Card from '@mui/material/Card'
import Typography from '@mui/material/Typography'
import { ActionRow } from './ActionRow.js'

/** What a `SectionCard` contributes: its own card and heading, or content alone. */
export type SectionSurface = 'card' | 'plain'

/** The surface a `SectionCard` renders in; `'card'` when no provider says otherwise. */
export const SectionSurfaceContext = createContext<SectionSurface>('card')

export type SectionSurfaceProviderProps = {
  /** The surface the sections below it render in; defaults to `'card'`. */
  readonly value?: SectionSurface
  /** The subtree whose sections render in the declared surface. */
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
   * The section title, rendered as a real heading that names the section; a
   * plain surface renders no heading but a string title still names it.
   */
  readonly title?: ReactNode
  /** Optional icon in the title's leading column; renders with the title, on the card surface. */
  readonly icon?: ReactNode
  /** Optional paragraph under the title; a plain surface renders nothing for it. */
  readonly description?: ReactNode
  /** Optional trailing actions; a plain surface keeps them above the content. */
  readonly actions?: ReactNode
  /**
   * The semantic level of the title; defaults to 'h2'. It has no effect on
   * a plain surface, which renders no heading.
   */
  readonly headingLevel?: SectionCardHeadingLevel
  /** Removes the content padding so a table sits edge to edge. */
  readonly flushContent?: boolean
  /**
   * Aligns the content with the title's rail when the card has an icon;
   * defaults to true. A table-bearing card passes false for the full width.
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
    // A plain surface drops the card and heading, not the section's name.
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
            // A flush child starts at the card's edge, so the separation is explicit padding:
            // line-box slack shrinks to nothing under a denser preset.
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

/** The titled section surface. */
export const SectionCard = forwardRef(SectionCardInner)
