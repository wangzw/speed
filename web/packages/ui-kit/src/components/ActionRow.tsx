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
 *
 * The row's root carries `data-action-row`, an empty-valued marker a test
 * uses to locate the row itself instead of inferring it from a child's
 * parent element. It is a test contract only: it has no styling and no
 * runtime consumers.
 *
 * `ActionButton` never renders a natively disabled control. A native
 * `disabled` attribute makes the element unfocusable; the HTML spec
 * requires an element that loses focusability to stop holding focus, and
 * browser engines move it at different moments. A keyboard user who
 * started the action can lose their place. The two states that replace
 * native disabling keep the button in the tab order:
 *
 *  - `busy` means this button's own action is in flight. It carries
 *    `aria-busy`, shows a progress indicator in place of its text (the
 *    text stays in the layout and the accessibility tree, transparent so
 *    the button's width and name do not change), and swallows activation
 *    until the action settles.
 *  - `unavailable` means the action cannot run now: its precondition is
 *    unmet, or a write is in flight in a scope whose context this button
 *    changes. It carries only `aria-disabled` and swallows activation.
 *
 * Both render `aria-disabled="true"`, keep the button focusable, call
 * `preventDefault()` on the activation they swallow (so a submit button
 * does not submit and an input's implicit submission is stopped) and
 * leave propagation alone. An unavailable button's reason is associated
 * in one of two ways: `unavailableReason` renders the translated text
 * visually hidden and links it through `aria-describedby`, showing the
 * same text in a tooltip on hover and focus; `unavailableDescribedBy`
 * points at explanatory text the page already renders and adds nothing of
 * its own. A caller's own `aria-describedby` stays first.
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

/**
 * The action row: a right-aligned group of actions one spacing unit apart,
 * with the primary action last. `sx`, `className` and the ref reach the
 * row's root element.
 */
export const ActionRow = forwardRef(ActionRowInner)

/** How an unavailable ActionButton explains itself; at most one of the two. */
export type ActionButtonUnavailableExplanation =
  | {
      /** Translated reason text; rendered visually hidden and linked by aria-describedby, shown in a tooltip on hover and focus. */
      readonly unavailableReason?: string
      readonly unavailableDescribedBy?: never
    }
  | {
      readonly unavailableReason?: never
      /** The id of explanatory text already on the page; aria-describedby points at it, nothing extra is rendered. */
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
    /** The action this button started is in flight. Focusable, aria-disabled and aria-busy, progress indicator shown, activation swallowed. */
    readonly busy?: boolean
    /** The action cannot run now: its precondition is unmet, or a write is in flight in a scope whose context this button changes. Focusable, aria-disabled, activation swallowed. */
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
  // The progress indicator is named by the button's own text, which means
  // the button must carry the id while it is busy. A caller's id wins.
  const buttonId = id ?? (isBusy ? generatedButtonId : undefined)
  // The reason is rendered only for `unavailable`, never while the
  // button's own action is in flight; a busy button has no reason to give.
  const activeReason =
    !isBusy && unavailable === true ? unavailableReason : undefined
  const activeDescribedBy =
    !isBusy && unavailable === true ? unavailableDescribedBy : undefined
  // The caller's own description stays first; the component appends its
  // own after it.
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
      // Swallow the activation without stopping propagation: a submit
      // button must not submit, and an input's Enter-driven implicit
      // submission is the browser's synthetic click on this button.
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

  // Every state returns the same two nodes: the tooltip wrapper and the
  // reason node are always there, with the reason's text empty when it
  // does not apply. The button therefore stays a child of the same wrapper
  // in every state and React reconciles it in place; returning the bare
  // button in some states and a wrapped one in others would change the
  // root element's type, unmount the button, and drop a focused button's
  // focus to the document body exactly when its availability changes.
  //
  // The hidden text sits outside the button: inside it, its text would
  // join the button's accessible name. `describeChild` keeps MUI's tooltip
  // from rewriting the name, and the button's own aria-describedby wins
  // over the one the tooltip would add, so the reason is announced once.
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

/**
 * The action row's button: weight and colour come from `emphasis` and
 * `tone`, size from the enclosing row unless given here.
 */
export const ActionButton = forwardRef(ActionButtonInner)
