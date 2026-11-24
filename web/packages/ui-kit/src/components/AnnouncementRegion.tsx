/**
 * One live region whose changes are inserted empty and written after two
 * animation frames, so the text lands in a region the accessibility tree
 * already holds.
 */

import { useCallback, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import type { SxProps, Theme } from '@mui/material/styles'

export interface AnnouncementRegionProps {
  /** Text to announce; null or '' renders the region empty. */
  readonly message: string | null
  /** 'status' renders role="status" aria-live="polite"; 'alert' renders role="alert" alone, without aria-live. Default 'status'. */
  readonly politeness?: 'status' | 'alert'
  /** The id of the region's root element. */
  readonly id?: string
  /** Extra styling for the region's root. */
  readonly sx?: SxProps<Theme>
}

interface WrittenText {
  readonly generation: number
  readonly text: string
}

/** The single live region: inserts each change empty and writes it after two painted frames. */
export function AnnouncementRegion({
  message,
  politeness = 'status',
  id,
  sx,
}: AnnouncementRegionProps) {
  const target = message ?? ''
  // Changing state while rendering advances the generation before commit.
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

  // Cancellation rides on the callback ref's null call: React 18 ignores a
  // callback ref's returned cleanup, and the peer range includes React 18.
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

  // The old node's null call cancels the frames it armed before the new node arms its own.
  // Scheduled from a callback ref, never an effect: a passive effect can flush
  // inside the commit that scheduled it (an MUI Tooltip sets state during
  // commit), and the first frame must paint the empty region before the second
  // writes, or both coalesce and stay silent.
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
