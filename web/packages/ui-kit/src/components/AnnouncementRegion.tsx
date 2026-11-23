/**
 * AnnouncementRegion: one live region whose text is written a frame after
 * the change that asks for it.
 *
 * A live region announces content changes that follow its own existence,
 * never text that mounts with it. A region inserted empty and filled later
 * is announced; a region born holding its text is silent. This component
 * keeps its region in the tree for as long as the host mounts it, inserts
 * every change empty, and writes the text after the next painted frame.
 *
 * The write is scheduled from the commit that inserted the empty text
 * node, through a callback ref that returns its cleanup. It deliberately
 * does not use an effect: a passive effect can be flushed inside the same
 * commit that scheduled it when a descendant updates state during commit
 * (an MUI Tooltip's ref callback does, and the kit's ActionButton wraps
 * every state in one), and a layout effect runs inside the commit by
 * definition. An animation frame cannot be pulled into the commit that
 * scheduled it. Two frames are used: the first runs before its frame
 * paints, so filling there would let the empty insertion and the text
 * change coalesce into one painted frame and stay silent; the second runs
 * after the first frame painted the empty region, so the change reaches an
 * accessibility tree that already holds the region.
 *
 * The text node is keyed by a generation that advances whenever the message
 * changes, so each change starts from a freshly inserted, empty node rather
 * than rewriting the previous text in place, and a message that becomes
 * empty clears the region in that commit, with no frame in between. The
 * armed frames live in a ref and are cancelled whenever the keyed node goes
 * away -- on a generation change and on unmount React calls the callback
 * ref with null -- so cancellation does not depend on the cleanup function a
 * React 19 callback ref may return, which React 18 ignores; the component
 * therefore works on either version its peer range declares.
 *
 * The root carries `data-announcement-written` while it holds the written
 * text. A host whose visible skin (an alert, a banner) stands around this
 * region uses the marker through `:has()` to keep that skin clipped until
 * the text is actually there: showing the skin in the commit the message is
 * requested would paint an empty box for the two frames before the write.
 * The marker exists for that styling contract and is absent while the
 * region is empty.
 *
 * `politeness` selects the region's role and how it is announced: `status`
 * (the default) renders `role="status"` with `aria-live="polite"`; `alert`
 * renders `role="alert"` alone, whose implicit live setting is assertive.
 * An alert does not also carry `aria-live="assertive"`: the role already
 * supplies that setting, and a region that states both is announced twice
 * by some screen readers (reported for VoiceOver and some JAWS
 * configurations). Both carry `aria-atomic="true"`, so the whole region is
 * read rather than the changed node. Styling is the caller's: the host
 * passes the `sx` its former region used, and the component adds no visual
 * rule of its own.
 */

import { useCallback, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import type { SxProps, Theme } from '@mui/material/styles'

export interface AnnouncementRegionProps {
  /** Text to announce; null or '' renders the region empty. */
  readonly message: string | null
  /** 'status' renders role="status" aria-live="polite"; 'alert' renders role="alert" alone, without aria-live. Default 'status'. */
  readonly politeness?: 'status' | 'alert'
  readonly id?: string
  readonly sx?: SxProps<Theme>
}

/** The text a generation has written, and the generation it belongs to. */
interface WrittenText {
  readonly generation: number
  readonly text: string
}

export function AnnouncementRegion({
  message,
  politeness = 'status',
  id,
  sx,
}: AnnouncementRegionProps) {
  const target = message ?? ''
  // Adjusting state while rendering is React's documented way to react to a
  // prop change before commit: the extra render happens before the commit,
  // so the generation the commit carries is already the new one. Two rapid
  // changes in one batch advance the generation once, because only the
  // final target differs from the last rendered one.
  const [previousTarget, setPreviousTarget] = useState(target)
  const [generation, setGeneration] = useState(0)
  if (target !== previousTarget) {
    setPreviousTarget(target)
    setGeneration(generation + 1)
  }
  const [written, setWritten] = useState<WrittenText>({
    generation: -1,
    text: '',
  })

  // The armed frames. Held in a ref so they can be cancelled from the
  // callback ref's null call: React calls it when the keyed node is
  // replaced (a generation change) and when the component unmounts. The
  // returned cleanup of a callback ref is a React 19 feature React 18
  // ignores, and the package declares support for both, so cancellation
  // cannot ride on it.
  const frames = useRef<{ paint: number | null; fill: number | null }>({
    paint: null,
    fill: null,
  })
  const cancelFrames = useCallback(() => {
    if (frames.current.paint !== null) {
      cancelAnimationFrame(frames.current.paint)
      frames.current.paint = null
    }
    if (frames.current.fill !== null) {
      cancelAnimationFrame(frames.current.fill)
      frames.current.fill = null
    }
  }, [])

  // The ref callback belongs to one generation: its identity changes with
  // the generation and the target it captures, and the keyed text node it
  // attaches to remounts on the same change. It is the commit-phase hook
  // that schedules the fill; the old node's null call is what cancels the
  // frames it armed before the new node arms its own.
  const attach = useCallback(
    (node: HTMLSpanElement | null) => {
      cancelFrames()
      if (node === null || target === '') {
        return
      }
      frames.current.paint = requestAnimationFrame(() => {
        frames.current.paint = null
        frames.current.fill = requestAnimationFrame(() => {
          frames.current.fill = null
          setWritten({ generation, text: target })
        })
      })
    },
    [cancelFrames, generation, target],
  )

  const alert = politeness === 'alert'
  const writtenNow = written.generation === generation
  return (
    <Box
      id={id}
      role={alert ? 'alert' : 'status'}
      aria-live={alert ? undefined : 'polite'}
      aria-atomic="true"
      data-announcement-written={writtenNow ? '' : undefined}
      sx={sx}
    >
      <span key={generation} ref={attach}>
        {writtenNow ? written.text : ''}
      </span>
    </Box>
  )
}
