/**
 * The file queue rendered from host-owned rows: every interaction reports up
 * through a callback, and the component never fetches, uploads or holds a
 * File past the handler that received it. Each settle is announced once.
 */

import { forwardRef, useEffect, useRef, useState } from 'react'
import type { ChangeEvent, DragEvent as ReactDragEvent, Ref } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import LinearProgress from '@mui/material/LinearProgress'
import Typography from '@mui/material/Typography'
import { styled } from '@mui/material/styles'
import { useUiKitTranslation } from '../internal/translation.js'
import { layerSx } from '../internal/root-style-props.js'
import type { RootStyleProps } from '../internal/root-style-props.js'
import { visuallyHiddenSx } from '../visually-hidden.js'

/** One row's transfer state, owned by the host. */
export type FileUploaderRowStatus = 'uploading' | 'succeeded' | 'failed'

/** One queue row the host wants rendered, exactly as it should appear. */
export interface FileUploaderRow {
  /** Stable row identity; the key of the rendered card and the id in callbacks. */
  readonly id: string
  /** The file's display name, rendered verbatim. */
  readonly name: string
  /** The row's transfer state. */
  readonly status: FileUploaderRowStatus
  /**
   * Upload progress within [0, 1]: absent renders an indeterminate bar, and
   * out-of-range fractions are clamped. Ignored once the row settles.
   */
  readonly progress?: number
  /** The failed row's error text, host-written and host-translated; rendered verbatim when present. */
  readonly error?: string
}

export interface FileUploaderProps extends RootStyleProps {
  /** The queue to render, owned by the host. Uploads run in host code. */
  readonly rows: readonly FileUploaderRow[]
  /**
   * Reports one pick or drop's files in order. Its presence renders the
   * selection zone; without it the component is a pure queue view.
   */
  readonly onSelectFiles?: (files: readonly File[]) => void
  /** Cancels the named row's transfer (an abort is the host's to perform). */
  readonly onCancel?: (rowId: string) => void
  /** Retries the named row (rendered on failed rows only). */
  readonly onRetry?: (rowId: string) => void
  /** Removes the named row from the queue (rendered on settled rows only). */
  readonly onRemove?: (rowId: string) => void
  /** Pick several files per selection; false by default (native input). */
  readonly multiple?: boolean
  /** Forwarded to the picker as an advisory filter; real validation is the host's. */
  readonly accept?: string
  /** Also accept drag-and-drop; the keyboard path never depends on it. */
  readonly allowDrop?: boolean
  /** Renders the affordances inert and ignores picks and drops. */
  readonly disabled?: boolean
  /** Picker affordance label; defaults to the built-in bilingual text (which names dragging too when `allowDrop` is on). */
  readonly chooseFilesLabel?: string
}

/**
 * Clipped, never `display: none`, so the input stays focusable and is the
 * zone's single tab stop.
 */
const HiddenFileInput = styled('input')(visuallyHiddenSx)

/** The last settle. `rowId` lets two rows with identical announcement text still re-announce. */
type Announcement = {
  readonly kind: 'uploaded' | 'failed'
  readonly name: string
  readonly rowId: string
} | null

type AnnouncementChange =
  | { readonly type: 'clear' }
  | {
      readonly type: 'announce'
      readonly kind: 'uploaded' | 'failed'
      readonly name: string
      readonly rowId: string
    }
  | null

function toPercent(fraction: number): number {
  if (Number.isNaN(fraction)) {
    return 0
  }
  return Math.round(Math.min(1, Math.max(0, fraction)) * 100)
}

function FileUploaderInner(
  {
    accept,
    allowDrop,
    chooseFilesLabel,
    disabled = false,
    multiple,
    onCancel,
    onRemove,
    onRetry,
    onSelectFiles,
    rows,
    sx,
    className,
  }: FileUploaderProps,
  ref: Ref<HTMLDivElement>,
) {
  const { t } = useUiKitTranslation()
  const [announcement, setAnnouncement] = useState<Announcement>(null)
  const previousRowsRef = useRef<readonly FileUploaderRow[] | null>(null)
  const [dragDepth, setDragDepth] = useState(0)

  // A render-time mirror: the diff effect's deps stay `[rows]`, so it must not re-run per announcement.
  const announcementRef = useRef<Announcement>(null)
  announcementRef.current = announcement
  // The deferred re-announcement reads this at fire time, after later commits.
  const rowsRef = useRef<readonly FileUploaderRow[] | null>(null)
  rowsRef.current = rows
  // Owned across effect instances; cleared only when superseded, emptied or unmounted.
  const reannounceTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // A pending re-announcement must never setState after unmount.
  useEffect(() => {
    return () => {
      if (reannounceTimerRef.current !== null) {
        clearTimeout(reannounceTimerRef.current)
        reannounceTimerRef.current = null
      }
    }
  }, [])

  // Diff the rows against the previous commit: a row leaving `uploading`
  // announces, as does one appended already settled. Seeded rows, progress
  // changes and a failed-to-succeeded heal stay quiet. A settle whose text
  // repeats the standing one clears first and re-announces a tick later,
  // because a live region only speaks about text changes.
  useEffect(() => {
    const previous = previousRowsRef.current
    previousRowsRef.current = rows
    if (previous === null) {
      return
    }
    if (rows.length === 0) {
      setAnnouncement(null)
      if (reannounceTimerRef.current !== null) {
        clearTimeout(reannounceTimerRef.current)
        reannounceTimerRef.current = null
      }
      return
    }
    let change: AnnouncementChange = null
    for (const row of rows) {
      const before = previous.find((candidate) => candidate.id === row.id)
      if (before === undefined) {
        if (row.status !== 'uploading') {
          change = {
            type: 'announce',
            kind: row.status === 'succeeded' ? 'uploaded' : 'failed',
            name: row.name,
            rowId: row.id,
          }
        }
        continue
      }
      if (before.status === row.status) {
        continue
      }
      if (row.status === 'uploading') {
        change = { type: 'clear' }
      } else if (before.status === 'uploading') {
        change = {
          type: 'announce',
          kind: row.status === 'succeeded' ? 'uploaded' : 'failed',
          name: row.name,
          rowId: row.id,
        }
      }
    }
    if (change === null) {
      // A departed row's announcement retires: the region may only speak of rows still in `rows`.
      const standing = announcementRef.current
      if (standing !== null && !rows.some((row) => row.id === standing.rowId)) {
        setAnnouncement(null)
      }
      return
    }
    const nextChange = change
    if (nextChange.type === 'clear') {
      setAnnouncement((current) => (current === null ? current : null))
      return
    }
    const standing = announcementRef.current
    const repeatsStandingText =
      standing !== null &&
      standing.kind === nextChange.kind &&
      standing.name === nextChange.name
    if (repeatsStandingText) {
      setAnnouncement(null)
      if (reannounceTimerRef.current !== null) {
        clearTimeout(reannounceTimerRef.current)
      }
      reannounceTimerRef.current = setTimeout(() => {
        reannounceTimerRef.current = null
        // A later rows commit can overtake the timer, so re-check the row is
        // still present and still in the announced state before refilling.
        const target = rowsRef.current?.find(
          (row) => row.id === nextChange.rowId,
        )
        const targetStillSettled =
          target !== undefined &&
          (nextChange.kind === 'uploaded'
            ? target.status === 'succeeded'
            : target.status === 'failed')
        if (!targetStillSettled) {
          return
        }
        setAnnouncement((current) =>
          current === null
            ? {
                kind: nextChange.kind,
                name: nextChange.name,
                rowId: nextChange.rowId,
              }
            : current,
        )
      }, 0)
      return
    }
    setAnnouncement({
      kind: nextChange.kind,
      name: nextChange.name,
      rowId: nextChange.rowId,
    })
  }, [rows])

  const handlePick = (event: ChangeEvent<HTMLInputElement>): void => {
    if (disabled) {
      return
    }
    // Snapshot first: a browser's FileList is live, so clearing `value` would empty it.
    const input = event.currentTarget
    const picked = input.files === null ? [] : Array.from(input.files)
    input.value = ''
    if (picked.length === 0) {
      return
    }
    onSelectFiles?.(picked)
  }

  const handleDragEnter = (event: ReactDragEvent<HTMLElement>): void => {
    event.preventDefault()
    setDragDepth((depth) => depth + 1)
  }

  const handleDragOver = (event: ReactDragEvent<HTMLElement>): void => {
    event.preventDefault()
  }

  const handleDragLeave = (event: ReactDragEvent<HTMLElement>): void => {
    event.preventDefault()
    // Enter/leave pairs fire per child crossing, so a depth counter tracks the drag.
    setDragDepth((depth) => Math.max(0, depth - 1))
  }

  const handleDrop = (event: ReactDragEvent<HTMLElement>): void => {
    event.preventDefault()
    setDragDepth(0)
    if (disabled) {
      return
    }
    const dataTransfer = event.dataTransfer
    const dropped = dataTransfer === null ? [] : Array.from(dataTransfer.files)
    if (dropped.length === 0) {
      return
    }
    onSelectFiles?.(dropped)
  }

  // The render-time half of the same rule: never speak of a row `rows` no longer carries.
  const liveAnnouncement =
    announcement !== null &&
    rows.some((row) => row.id === announcement.rowId)
      ? announcement
      : null
  const pickerRendered = onSelectFiles !== undefined
  const dropDeclared = pickerRendered && allowDrop === true
  const dragAccepting = dropDeclared && !disabled
  const dragActive = dragAccepting && dragDepth > 0
  const triggerLabel =
    chooseFilesLabel ??
    t(dropDeclared ? 'fileUploader.dropOrChoose' : 'fileUploader.chooseFiles')

  return (
    <Box
      ref={ref}
      className={className}
      sx={layerSx(
        {
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'flex-start',
          gap: 1,
        },
        sx,
      )}
    >
      {pickerRendered ? (
        /* One labelled surface is both the click and the drop target. */
        <Box
          component="label"
          tabIndex={-1}
          aria-disabled={disabled || undefined}
          data-drop-active={dragActive || undefined}
          onDragEnter={dragAccepting ? handleDragEnter : undefined}
          onDragOver={dragAccepting ? handleDragOver : undefined}
          onDragLeave={dragAccepting ? handleDragLeave : undefined}
          onDrop={dragAccepting ? handleDrop : undefined}
          sx={{
            width: '100%',
            py: 2,
            px: 2,
            borderRadius: 1,
            border: '1px dashed',
            borderColor: dragActive ? 'primary.main' : 'divider',
            bgcolor: dragActive ? 'action.hover' : 'transparent',
            cursor: disabled ? 'default' : 'pointer',
            opacity: disabled
              ? (theme) => theme.palette.action.disabledOpacity
              : undefined,
            textAlign: 'center',
            '&:focus-within': {
              outline: '2px solid',
              outlineOffset: 2,
              outlineColor: 'primary.main',
            },
          }}
        >
          <Typography variant="body2" color="text.secondary" component="span">
            {triggerLabel}
          </Typography>
          <HiddenFileInput
            type="file"
            multiple={multiple}
            accept={accept}
            disabled={disabled}
            onChange={handlePick}
          />
        </Box>
      ) : null}
      {rows.length > 0 ? (
        /* role="list" keeps list semantics under WebKit, which strips them from a list-style-none ul. */
        <Box
          component="ul"
          role="list"
          sx={{
            width: '100%',
            listStyle: 'none',
            m: 0,
            p: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: 1,
          }}
        >
          {rows.map((row) => (
            <Box
              component="li"
              key={row.id}
              sx={{
                width: '100%',
                border: '1px solid',
                borderColor: 'divider',
                borderRadius: 1,
                px: 1.5,
                py: 1,
              }}
            >
              <Typography variant="body2" sx={{ wordBreak: 'break-all' }}>
                {row.name}
              </Typography>
              {row.status === 'uploading' ? (
                <>
                  <Box
                    sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 1, mt: 0.5 }}
                  >
                    <LinearProgress
                      aria-label={t('fileUploader.statusUploading')}
                      variant={row.progress === undefined ? 'indeterminate' : 'determinate'}
                      value={row.progress === undefined ? undefined : toPercent(row.progress)}
                      sx={{ flexGrow: 1 }}
                    />
                    {onCancel !== undefined ? (
                      <Button
                        size="small"
                        onClick={() => onCancel(row.id)}
                      >
                        {t('fileUploader.actionCancel')}
                      </Button>
                    ) : null}
                  </Box>
                  <Typography
                    variant="caption"
                    color="text.secondary"
                    component="p"
                    sx={{ mt: 0.5 }}
                  >
                    {t('fileUploader.statusUploading')}
                  </Typography>
                </>
              ) : (
                <Box
                  sx={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 1, mt: 0.5 }}
                >
                  <Typography
                    variant="caption"
                    sx={{
                      color:
                        row.status === 'succeeded' ? 'success.main' : 'error.main',
                      flexGrow: 1,
                    }}
                    component="p"
                  >
                    {t(
                      row.status === 'succeeded'
                        ? 'fileUploader.statusSucceeded'
                        : 'fileUploader.statusFailed',
                    )}
                  </Typography>
                  {row.status === 'failed' && onRetry !== undefined ? (
                    <Button
                      size="small"
                      onClick={() => onRetry(row.id)}
                    >
                      {t('fileUploader.actionRetry')}
                    </Button>
                  ) : null}
                  {onRemove !== undefined ? (
                    <Button
                      size="small"
                      onClick={() => onRemove(row.id)}
                    >
                      {t('fileUploader.actionRemove')}
                    </Button>
                  ) : null}
                </Box>
              )}
              {row.status === 'failed' && row.error ? (
                <Typography
                  variant="caption"
                  color="error.main"
                  component="p"
                  sx={{ mt: 0.5 }}
                >
                  {row.error}
                </Typography>
              ) : null}
            </Box>
          ))}
        </Box>
      ) : null}
      {rows.length > 0 ? (
        <Box component="p" role="status" sx={{ m: 0 }}>
          <Typography variant="caption" color="text.secondary" component="span">
            {liveAnnouncement === null
              ? ''
              : t(
                  liveAnnouncement.kind === 'uploaded'
                    ? 'fileUploader.announceUploaded'
                    : 'fileUploader.announceFailed',
                  { name: liveAnnouncement.name },
                )}
          </Typography>
        </Box>
      ) : null}
    </Box>
  )
}

/** The queue renderer: host-owned rows, pick/drop reporting, per-row actions and the live region. */
export const FileUploader = forwardRef(FileUploaderInner)
