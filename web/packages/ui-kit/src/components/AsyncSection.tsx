/**
 * The four-state branch of a section backed by an asynchronous read. Both
 * settled branches keep the same fragment shape, so the stale notice coming
 * and going never remounts the settled node.
 */

import { forwardRef, useLayoutEffect, useRef } from 'react'
import type { ReactElement, ReactNode, Ref } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import type { RootStyleProps } from '../internal/root-style-props.js'
import {
  EmptyState,
  type EmptyStateHeadingLevel,
  type EmptyStateNoHeading,
} from './EmptyState.js'

/** The error placeholder's copy and its retry. */
export interface AsyncSectionErrorState {
  /** The retryable failure's title. */
  readonly title: string
  /** The retryable failure's description. */
  readonly description: string
  /** The retry button's label. */
  readonly retryLabel: string
  /** Fired by the retry button. */
  readonly onRetry: () => void
}

/** The empty placeholder's copy. */
export interface AsyncSectionEmptyState {
  /** The empty state's title. */
  readonly title: string
  /** The empty state's description. */
  readonly description: string
}

/** The refusal placeholder's copy; it replaces the content and offers no retry. */
export interface AsyncSectionRefusedState {
  /** The no-permission placeholder's title. */
  readonly title: string
  /** The no-permission placeholder's description. */
  readonly description: string
}

/** Marks a failed refresh that kept its data; the caller withdraws it when the retry starts. */
export interface AsyncSectionStaleState {
  /** The caller's notice; states that the shown content may be outdated. */
  readonly description: string
  /** The retry button's label. */
  readonly retryLabel: string
  /** Fired by the retry button. */
  readonly onRetry: () => void
}

export interface AsyncSectionProps<Payload> extends RootStyleProps {
  /** No answer yet: renders `loading`. */
  readonly pending: boolean
  /** The settled payload; `undefined` renders the error placeholder
   * (a failed load, or an answer whose body omitted the payload). */
  readonly payload: Payload | undefined
  /** Whether the settled payload renders as empty: takes effect only
   * with `emptyState` provided. */
  readonly empty: boolean
  /** The loading announcement -- a `ListSkeleton` shaped like the rows,
   * or any caller-built node. */
  readonly loading: ReactNode
  /** The retryable error placeholder. */
  readonly errorState: AsyncSectionErrorState
  /** The empty placeholder; omitted by a surface whose payload cannot be
   * empty (a settings document always arrives). */
  readonly emptyState?: AsyncSectionEmptyState
  /** The heading element the placeholders' titles render as; defaults to
   * 'h2', the section-title level the placeholders stand in for. */
  readonly headingLevel?: EmptyStateHeadingLevel
  /**
   * The empty placeholder's own heading element, or `'none'` for a
   * headerless `role="status"` region; defaults to `headingLevel`.
   */
  readonly emptyHeadingLevel?: EmptyStateHeadingLevel | EmptyStateNoHeading
  /**
   * Marks a failed refresh whose earlier data is still on screen: the settled
   * rendering keeps rendering with this alert above it.
   */
  readonly staleState?: AsyncSectionStaleState
  /**
   * The no-permission placeholder. While the section is not pending it
   * replaces every other branch and offers no retry; ui-kit makes no status
   * or permission check of its own.
   */
  readonly refusedState?: AsyncSectionRefusedState
  /** The settled content, built from the narrowed payload. */
  readonly children: (payload: Payload) => ReactNode
}

/** Every branch renders inside one root box only when the host passes `sx`, `className` or a ref. */
function AsyncSectionInner<Payload>(
  {
    pending,
    payload,
    empty,
    loading,
    errorState,
    emptyState,
    headingLevel = 'h2',
    emptyHeadingLevel,
    staleState,
    refusedState,
    children,
    sx,
    className,
  }: AsyncSectionProps<Payload>,
  ref: Ref<HTMLDivElement>,
): ReactElement | null {
  // The content that stays on screen is the focus target when the notice withdraws.
  const settledRef = useRef<HTMLDivElement>(null)
  const noticeHeldFocus = useRef(false)
  useLayoutEffect(() => {
    if (staleState !== undefined) {
      return
    }
    if (!noticeHeldFocus.current) {
      return
    }
    noticeHeldFocus.current = false
    // Only recover focus the removed notice left on the body.
    if (document.activeElement === document.body) {
      settledRef.current?.focus()
    }
  }, [staleState])

  // An omitted prop must not add an element: a wrapper would change the layout context.
  const withRoot = (node: ReactNode): ReactElement | null =>
    sx === undefined && className === undefined && ref == null ? (
      (node as ReactElement | null)
    ) : (
      <Box ref={ref} className={className} sx={sx}>
        {node}
      </Box>
    )

  if (pending) {
    return withRoot(loading)
  }
  if (refusedState !== undefined) {
    return withRoot(
      <EmptyState
        variant="noPermission"
        title={refusedState.title}
        description={refusedState.description}
        headingLevel={headingLevel}
      />,
    )
  }
  if (payload === undefined) {
    return withRoot(
      <EmptyState
        variant="error"
        title={errorState.title}
        description={errorState.description}
        action={
          <Button variant="contained" onClick={errorState.onRetry}>
            {errorState.retryLabel}
          </Button>
        }
        headingLevel={headingLevel}
      />,
    )
  }
  const settled =
    empty && emptyState !== undefined ? (
      <EmptyState
        variant="empty"
        title={emptyState.title}
        description={emptyState.description}
        headingLevel={emptyHeadingLevel ?? headingLevel}
      />
    ) : (
      children(payload)
    )
  return withRoot(
    <>
      {staleState === undefined ? null : (
        <Alert
          severity="warning"
          role="alert"
          onFocus={() => {
            noticeHeldFocus.current = true
          }}
          action={
            <Button color="inherit" onClick={staleState.onRetry}>
              {staleState.retryLabel}
            </Button>
          }
          sx={{ marginBottom: 2 }}
        >
          {staleState.description}
        </Alert>
      )}
      <Box ref={settledRef} tabIndex={-1}>
        {settled}
      </Box>
    </>,
  )
}

/**
 * The four-state branch. `forwardRef` erases the component's type
 * parameters, so the cast below restores the generic signature.
 */
export const AsyncSection = forwardRef(AsyncSectionInner) as <Payload>(
  props: AsyncSectionProps<Payload> & {
    readonly ref?: Ref<HTMLDivElement>
  },
) => ReactElement | null
