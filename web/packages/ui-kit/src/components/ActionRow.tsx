/**
 * ActionRow and ActionButton: the one action row for page, dialog and row
 * actions.
 *
 * `ActionRow` owns the row's layout and nothing else. Its children are the
 * host's actions in display order; the row lays them out as one group
 * against the right edge, keeps adjacent controls one spacing unit apart
 * (`theme.spacing(1)`) and wraps rather than overflowing when the
 * available width runs out. The primary action is the last child, so it
 * closes the group at the right end -- callers order their actions
 * accordingly instead of pushing one button to the far side with an
 * automatic margin: an action row is one group, never two ends.
 *
 * `ActionButton` is the row's button. It takes the two independent
 * dimensions the row's hierarchy needs and nothing that could contradict
 * them:
 *
 *  - `emphasis` is the visual weight. `'primary'` renders a contained
 *    button; `'secondary'` (the default) renders a text button.
 *  - `tone` is the semantic colour. `'default'` keeps the theme's primary
 *    role; `'danger'` takes the error role -- filled (contained) when the
 *    action carries the row's primary emphasis, outlined when it is
 *    secondary.
 *
 * `variant` and `color` are deliberately not accepted: the row's hierarchy
 * is expressed by the two slots above, so a call site cannot introduce a
 * third button face.
 *
 * `size` is a decision about the surface the row sits on, not about the
 * row's contents: page forms and dialogs are `'medium'` (the default),
 * table rows and menus are `'small'`. `ActionRow` passes its size down to
 * every `ActionButton` below it; an `ActionButton` used outside a row (a
 * standalone small control) takes the same prop directly.
 *
 * `EmptyState.action` (a centred retry) and per-row inline action clusters
 * are not action rows and keep their own layout.
 */

import { createContext, forwardRef, useContext } from 'react'
import type { ReactNode, Ref } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import type { ButtonProps } from '@mui/material/Button'
import { layerSx } from '../internal/root-style-props.js'
import type { RootStyleProps } from '../internal/root-style-props.js'

/** The two sizes an action row takes: pages/dialogs and table rows/menus. */
export type ActionRowSize = 'medium' | 'small'

/** The visual weight of an action in an action row. */
export type ActionEmphasis = 'primary' | 'secondary'

/** The semantic colour of an action in an action row. */
export type ActionTone = 'default' | 'danger'

/** The enclosing row's size; a standalone button defaults to medium. */
const ActionRowSizeContext = createContext<ActionRowSize>('medium')

export type ActionRowProps = RootStyleProps & {
  /** The actions in display order; the primary action goes last (rightmost). */
  readonly children: ReactNode
  /** The row's size; defaults to 'medium'. See this file's header. */
  readonly size?: ActionRowSize
}

/**
 * The row's layout: one right-aligned group, adjacent controls exactly one
 * spacing unit apart.
 */
const ROW_SX = {
  display: 'flex',
  flexWrap: 'wrap',
  justifyContent: 'flex-end',
  alignItems: 'center',
  gap: 1,
} as const

function ActionRowInner(
  { children, size = 'medium', sx, className }: ActionRowProps,
  ref: Ref<HTMLDivElement>,
) {
  return (
    <ActionRowSizeContext.Provider value={size}>
      <Box ref={ref} className={className} sx={layerSx(ROW_SX, sx)}>
        {children}
      </Box>
    </ActionRowSizeContext.Provider>
  )
}

/**
 * The action row: a right-aligned group of actions one spacing unit apart,
 * with the primary action last. `sx`, `className` and the ref reach the
 * row's root element.
 */
export const ActionRow = forwardRef(ActionRowInner)

export type ActionButtonProps = Omit<
  ButtonProps,
  'variant' | 'color' | 'size'
> & {
  /** Visual weight; defaults to 'secondary'. */
  readonly emphasis?: ActionEmphasis
  /** Semantic colour; defaults to 'default'. */
  readonly tone?: ActionTone
  /** Size; defaults to the enclosing action row's size, otherwise 'medium'. */
  readonly size?: ActionRowSize
}

function ActionButtonInner(
  {
    emphasis = 'secondary',
    tone = 'default',
    size,
    ...buttonProps
  }: ActionButtonProps,
  ref: Ref<HTMLButtonElement>,
) {
  const rowSize = useContext(ActionRowSizeContext)
  const variant =
    emphasis === 'primary'
      ? 'contained'
      : tone === 'danger'
        ? 'outlined'
        : 'text'
  const color = tone === 'danger' ? 'error' : 'primary'
  return (
    <Button
      {...buttonProps}
      ref={ref}
      size={size ?? rowSize}
      variant={variant}
      color={color}
    />
  )
}

/**
 * The action row's button: weight and colour come from `emphasis` and
 * `tone`, size from the enclosing row unless given here.
 */
export const ActionButton = forwardRef(ActionButtonInner)
