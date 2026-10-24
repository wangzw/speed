/**
 * AsyncSection: the four-state branch of a section backed by an
 * asynchronous read.
 *
 * A read that has not answered yet, a settled read that delivered
 * nothing, a settled read that is empty and a settled read with content
 * are the same four states on every list-bearing surface, and each one
 * has a fixed rendering: the loading announcement, a retryable error
 * placeholder, an empty placeholder, or the caller's content. This
 * component owns that mapping, so the surfaces cannot drift into
 * rendering the same state differently.
 *
 * The states are passed in already derived -- `pending` (no answer
 * yet), the settled `payload` (undefined: a failed load, or an answer
 * whose body omitted it) and `empty` (the settled payload counts as
 * empty) -- because only the caller knows how its query's pending flag
 * folds cached data, and which emptiness counts. The content is a
 * render callback receiving the narrowed payload, not a node: a
 * caller's content expression typically maps the payload, which must
 * neither be evaluated nor re-narrowed while the payload is absent.
 * The error and empty placeholders are `EmptyState` with the section's
 * own copy and heading level: a surface whose section header hides
 * while its placeholder shows passes the header's level, so the page's
 * heading order never skips.
 *
 * `staleState` marks a refresh that failed while earlier data is still
 * on screen: the settled rendering -- the content, or the empty
 * placeholder when the payload is empty -- keeps rendering and an alert
 * with a retry action renders above it. The caller supplies it only for
 * that state -- the latest refresh failed, the data is still on screen
 * and no read is in flight -- and withdraws it when the retry starts,
 * which unmounts the alert; a retry that fails again re-supplies it, so
 * the alert mounts anew and its `role="alert"` announces again. When the
 * notice withdraws while one of its controls holds focus -- the retry was
 * activated by keyboard, or an automatic re-read started while the focus
 * sat on the notice -- focus moves to the settled content that stays on
 * screen, instead of falling back to the document body and forcing a
 * keyboard user to tab in from the top again. The settled branches always
 * return the same fragment shape, so the notice appearing or withdrawing
 * never unmounts the node beneath it and never loses the state that node
 * holds. A refusal such as HTTP 403 does not
 * use `staleState`: it makes the older data meaningless, so the caller
 * replaces the content with the no-permission placeholder instead.
 *
 * `refusedState` is that replacement as a prop: the caller decides a
 * read was refused (ui-kit inspects no status and knows no permission
 * semantics), and while the section is not pending the no-permission
 * placeholder takes the place of everything else -- content, empty and
 * error branches alike. It carries no retry, because a refusal can only
 * be answered with a refusal again. Omitting it renders the previous
 * behaviour unchanged.
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

/**
 * The refusal placeholder's copy: the caller decided the read was
 * refused (a permission answer, a policy decision). The placeholder
 * replaces the content and offers no retry.
 */
export interface AsyncSectionRefusedState {
  /** The no-permission placeholder's title. */
  readonly title: string
  /** The no-permission placeholder's description. */
  readonly description: string
}

/**
 * The stale-data marker: a refresh failed while the rendered data stayed
 * on screen. The caller supplies it exactly while that is the state and
 * withdraws it when the retry starts.
 */
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
   * The empty placeholder's own heading element, or `'none'` to render it
   * headerless as one `role="status"` region (the error placeholder keeps
   * `headingLevel`). An empty placeholder under a page heading that
   * already names the space passes `'none'` so the placeholder does not
   * re-enter the heading outline as a redundant chapter; defaults to
   * `headingLevel`.
   */
  readonly emptyHeadingLevel?: EmptyStateHeadingLevel | EmptyStateNoHeading
  /**
   * Marks a failed refresh whose earlier data is still on screen: the
   * settled rendering (the content, or the empty placeholder when the
   * payload is empty) keeps rendering with this alert above it.
   * Omitted in every other state, including a refusal (403), which the
   * caller renders as the no-permission placeholder instead.
   */
  readonly staleState?: AsyncSectionStaleState
  /**
   * The no-permission placeholder, supplied when the caller decided the
   * read was refused. While the section is not pending it replaces the
   * content, the empty placeholder and the error branch alike, and
   * offers no retry: a refusal can only be refused again. ui-kit makes
   * no status or permission check of its own -- the caller decides, and
   * omitting the prop keeps every previous branch unchanged.
   */
  readonly refusedState?: AsyncSectionRefusedState
  /** The settled content, built from the narrowed payload. */
  readonly children: (payload: Payload) => ReactNode
}

/**
 * The four-state branch of a section backed by an asynchronous read.
 * Every branch renders inside one root box when the host passes `sx`,
 * `className` or a ref; with none of them the branch renders exactly the
 * node it always did, so a fragment keeps its children's identities.
 */
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
  // The content that stays on screen is the fallback focus target when the
  // stale notice withdraws: the retry button inside it is about to be
  // removed, and focus would otherwise land on the document body.
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
    // Focus is only recovered when the removed notice left it on the
    // body; a reader who moved focus elsewhere has already chosen a new
    // resting place.
    if (document.activeElement === document.body) {
      settledRef.current?.focus()
    }
  }, [staleState])

  // The host's root props exist only when asked for: an omitted prop must
  // not add an element around the branch (a wrapped fragment would change
  // its children's layout context).
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
    // A refusal replaces whatever was on screen: the older data no longer
    // means anything, and there is no retry that could change the answer.
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
          <Button onClick={errorState.onRetry}>{errorState.retryLabel}</Button>
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
  // Both settled branches keep one wrapper element: the stale notice
  // mounts and unmounts as the fragment's first child, so the settled node
  // keeps its identity and its local state across the notice's lifecycle.
  // The wrapper is the focus target the withdrawing notice hands off to.
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
 * The four-state branch (see `AsyncSectionInner`). The generic signature
 * is preserved across `forwardRef` by the cast below: React 18 forwards a
 * ref only through `forwardRef`, which erases the component's own type
 * parameters.
 */
export const AsyncSection = forwardRef(AsyncSectionInner) as <Payload>(
  props: AsyncSectionProps<Payload> & {
    readonly ref?: Ref<HTMLDivElement>
  },
) => ReactElement | null
