/**
 * The one action row for page, dialog and row actions. A call site must not
 * push a button to the far side with an automatic margin: the row is one
 * group.
 */

import { createContext, forwardRef, useContext, useId } from 'react'
import type { MouseEvent as ReactMouseEvent, ReactNode, Ref } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import type { ButtonProps } from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Tooltip from '@mui/material/Tooltip'
import { layerSx } from '../internal/root-style-props.js'
import type { RootStyleProps } from '../internal/root-style-props.js'
import { visuallyHiddenSx } from '../visually-hidden.js'

/** The two sizes an action row takes. */
export type ActionRowSize = 'medium' | 'small'

/** The visual weight of an action in an action row. */
export type ActionEmphasis = 'primary' | 'secondary'

/** The semantic colour of an action in an action row. */
export type ActionTone = 'default' | 'danger'

const ActionRowSizeContext = createContext<ActionRowSize>('medium')

export type ActionRowProps = RootStyleProps & {
  /** The actions in display order; the primary action goes last (rightmost). */
  readonly children: ReactNode
  /** The row's size; defaults to 'medium'. */
  readonly size?: ActionRowSize
}

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
      <Box
        ref={ref}
        className={className}
        data-action-row=""
        sx={layerSx(ROW_SX, sx)}
      >
        {children}
      </Box>
    </ActionRowSizeContext.Provider>
  )
}

/** The action row's root: one right-aligned group, the primary action last. */
export const ActionRow = forwardRef(ActionRowInner)

/** How an unavailable ActionButton explains itself; at most one of the two. */
export type ActionButtonUnavailableExplanation =
  | {
      /** Translated reason text; rendered visually hidden, linked by aria-describedby and shown in a tooltip. */
      readonly unavailableReason?: string
      readonly unavailableDescribedBy?: never
    }
  | {
      readonly unavailableReason?: never
      /** The id of explanatory text already on the page; nothing extra is rendered. */
      readonly unavailableDescribedBy?: string
    }

export type ActionButtonProps = Omit<
  ButtonProps,
  | 'variant'
  | 'color'
  | 'size'
  | 'disabled'
  | 'loading'
  | 'loadingIndicator'
  | 'loadingPosition'
> &
  ActionButtonUnavailableExplanation & {
    /** Visual weight; defaults to 'secondary'. */
    readonly emphasis?: ActionEmphasis
    /** Semantic colour; defaults to 'default'. */
    readonly tone?: ActionTone
    /** Size; defaults to the enclosing action row's size, otherwise 'medium'. */
    readonly size?: ActionRowSize
    /** This button's own action is in flight: stays focusable, shows progress, swallows activation. */
    readonly busy?: boolean
    /** The action cannot run now; stays focusable and swallows activation. */
    readonly unavailable?: boolean
  }

function ActionButtonInner(
  {
    emphasis = 'secondary',
    tone = 'default',
    size,
    busy = false,
    unavailable = false,
    unavailableReason,
    unavailableDescribedBy,
    id,
    onClick,
    children,
    sx,
    style,
    'aria-describedby': ariaDescribedBy,
    'aria-disabled': ariaDisabled,
    'aria-busy': ariaBusy,
    ...buttonProps
  }: ActionButtonProps,
  ref: Ref<HTMLButtonElement>,
) {
  const rowSize = useContext(ActionRowSizeContext)
  const generatedButtonId = useId()
  const generatedReasonId = useId()
  const variant =
    emphasis === 'primary'
      ? 'contained'
      : tone === 'danger'
        ? 'outlined'
        : 'text'
  const color = tone === 'danger' ? 'error' : 'primary'

  const isBusy = busy === true
  const isUnavailable = isBusy || unavailable === true
  // The progress indicator is named by the button's text, so the button needs an id while busy.
  const buttonId = id ?? (isBusy ? generatedButtonId : undefined)
  const activeReason =
    !isBusy && unavailable === true ? unavailableReason : undefined
  const activeDescribedBy =
    !isBusy && unavailable === true ? unavailableDescribedBy : undefined
  // The caller's own description stays first.
  const describedBy =
    [
      ariaDescribedBy,
      activeReason !== undefined ? generatedReasonId : undefined,
      activeDescribedBy,
    ]
      .filter((value): value is string => value !== undefined && value !== '')
      .join(' ') || undefined

  const handleClick = (event: ReactMouseEvent<HTMLButtonElement>) => {
    if (isUnavailable) {
      // Swallow the activation but not propagation: Enter still reaches ancestors.
      event.preventDefault()
      return
    }
    onClick?.(event)
  }

  const button = (
    <Button
      {...buttonProps}
      ref={ref}
      id={buttonId}
      size={size ?? rowSize}
      variant={variant}
      color={color}
      aria-disabled={isUnavailable ? true : ariaDisabled}
      aria-busy={isBusy ? true : ariaBusy}
      aria-describedby={describedBy}
      sx={sx}
      style={isBusy ? { ...style, color: 'transparent' } : style}
      onClick={handleClick}
    >
      {children}
      {isBusy ? (
        <Box
          component="span"
          sx={{
            display: 'flex',
            position: 'absolute',
            left: '50%',
            transform: 'translateX(-50%)',
            color: 'action.disabled',
          }}
        >
          <CircularProgress
            aria-labelledby={buttonId}
            color="inherit"
            size={16}
          />
        </Box>
      ) : null}
    </Button>
  )

  // Every state returns the same two nodes, so a focused button reconciles in
  // place; a bare button in some states would change the root's type and drop
  // focus. The hidden text sits outside the button so it stays out of its name.
  return (
    <>
      <Tooltip title={activeReason} describeChild>
        {button}
      </Tooltip>
      <Box component="span" id={generatedReasonId} sx={visuallyHiddenSx}>
        {activeReason ?? ''}
      </Box>
    </>
  )
}

/** The action row's button: weight and colour from `emphasis` and `tone`, size from the row. */
export const ActionButton = forwardRef(ActionButtonInner)
