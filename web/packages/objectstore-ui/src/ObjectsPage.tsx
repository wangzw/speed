/**
 * ObjectsPage: the tenant object store's page.
 *
 * One page over the objectstore capability's routes: the tenant's stored
 * objects as a cursor-paginated table (name, identifier, state, size, content
 * type, creation time and the row's download / details / delete controls),
 * an upload surface driven by ui-kit's FileUploader, a per-object details
 * dialog and a confirmed delete. The name column shows the label the module
 * normalized once at declaration, truncated to one line; the full value is in
 * the details dialog, and an empty name renders the "unnamed" copy. Every read
 * and write goes through the generated objectstore operations of
 * @speed/api-sdk on the host's QueryClient, under a tenant-namespaced key (see
 * internal/queries.ts); the page builds no client and writes no query key.
 *
 * Pagination is the module's keyset cursor: the list answers a page and the
 * cursor of its last row, so the page keeps a stack of cursors -- the
 * current cursor is the stack's top, Next pushes the answered cursor,
 * Previous pops. An empty next cursor means there is no further page.
 *
 * The upload surface owns its queue (internal/uploads.ts): a picked file
 * runs the module's three-step transfer (declare -> upload bytes ->
 * complete) and the row settles succeeded or failed. The delete control
 * asks the page to hold a description of what it will do, rendered through
 * ui-kit's danger ConfirmDialog; the mutation fires only on the confirming
 * click. A download is saved under the view's stored name, falling back to
 * the identifier plus a MIME-derived extension when the name is empty.
 *
 * Download and delete share one write-feedback scope whose region sits
 * under the list and above the pager: the slot holds at most one outcome,
 * a landed download confirms the object it saved, and a failure renders
 * the answer's code text. Paging and refreshing change the list's context,
 * so both reset the scope, and both are natively disabled while an attempt
 * is in flight. A delete in flight disables and marks busy every delete
 * control on the page, so deletes never overlap, and a failed delete
 * re-reads the tenant subtree so a row the server no longer holds does not
 * stay on screen.
 *
 * The refresh control re-reads the list. It reports the read's busy state
 * as `aria-busy` and stays focusable rather than becoming natively
 * disabled, and a second activation issues no second request. It is
 * natively disabled while a page write is in flight, and with no tenant
 * in play -- a re-read ignores the read's enablement gate, so an enabled
 * control would issue a tenant-less request.
 *
 * The page is tenant-scoped: it renders under the host's tenant context
 * (auth-core's TenantProvider) and reads the current tenant for its query
 * keys and its invalidation. The host mounts it at a route of its choosing
 * -- the page owns no path and adds no navigation entry of its own.
 */

import { useId, useRef, useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import {
  ConfirmDialog,
  DataTable,
  FileUploader,
  ListSkeleton,
  PageHeader,
  PRIMARY_COLUMN_MAX_WIDTH,
  WriteFeedbackRegion,
  useWriteFeedback,
} from '@speed/ui-kit'
import type { DataTableColumn } from '@speed/ui-kit'
import { useObjectstoreUiTranslation } from './internal/translation.js'
import { useObjectUploads } from './internal/uploads.js'
import {
  ObjectAsyncSection,
  isForbidden,
  isTenantRefusal,
} from './internal/async-section.js'
import { ObjectDetailDialog } from './ObjectDetailDialog.js'
import { useObjectstoreUiErrorText } from './internal/error-text.js'
import { downloadFilename, formatByteNumber, formatUtc } from './internal/format.js'
import { stateLabelKey } from './internal/status.js'
import {
  getObjectContent,
  useDeleteObject,
  useInvalidateObjects,
  useObjectsListQuery,
} from './internal/queries.js'
import { saveBlob } from './internal/download.js'
import type { StoredObject } from './internal/models.js'

/** The rows the list requests per page (within the module's accepted range). */
const DEFAULT_PAGE_SIZE = 20

/**
 * How much of an object identifier the list shows. The column is one of
 * the low-priority ones, so its cell stays narrow; the complete
 * identifier is the cell's `title`/copy value and the detail dialog's
 * field, both reachable without widening the table.
 */
const ID_PREFIX_LENGTH = 8

/**
 * The content type's cap. It is narrower than the shared long-text cap:
 * with the identifier, size and created columns and the row controls on
 * screen, the shared value puts the list past the lg floor. The complete
 * type stays on the cell's title and in the detail dialog.
 */
const MIME_COLUMN_MAX_WIDTH = 128

export interface ObjectsPageProps {
  /**
   * The page size the list requests; the module applies a default and an
   * upper bound of its own, so a value here is a hint, never a guarantee.
   */
  readonly pageSize?: number
}

/** The tenant object store's page. */
export function ObjectsPage({ pageSize = DEFAULT_PAGE_SIZE }: ObjectsPageProps) {
  const { t, i18n } = useObjectstoreUiTranslation()
  const resolveErrorText = useObjectstoreUiErrorText()
  const invalidate = useInvalidateObjects()
  const uploads = useObjectUploads()
  const removeObject = useDeleteObject()
  // The one slot every page write settles into: a download or a delete.
  const feedback = useWriteFeedback()

  // The cursor stack: index 0 is the first page (''), each further entry
  // is a page the user advanced to. The top is the page on screen.
  const [cursorStack, setCursorStack] = useState<readonly string[]>([''])
  const cursor = cursorStack[cursorStack.length - 1] ?? ''
  const params =
    cursor === '' ? { limit: pageSize } : { cursor, limit: pageSize }
  const { tenantId, query } = useObjectsListQuery(params)

  const objects = query.data?.objects ?? []
  const nextCursor = query.data?.next_cursor ?? ''
  const hasPrevious = cursorStack.length > 1
  const hasNext = nextCursor !== ''
  const pending = query.isPending && query.data === undefined
  const deletePending = removeObject.isPending
  // The refresh control is natively disabled by the two states that make a
  // re-read wrong, never by the read's own busy state (that one is
  // reported as aria-busy so focus is not dropped).
  const refreshBlocked = tenantId === null || feedback.pending
  const refreshBusy = query.isFetching
  const refreshLoadingId = useId()

  const [detailId, setDetailId] = useState<string | null>(null)
  const [pendingDelete, setPendingDelete] = useState<StoredObject | null>(null)

  // A second activation that lands before the busy state reaches a render
  // issues no second read.
  const refreshInFlight = useRef(false)
  const refresh = (): void => {
    if (refreshInFlight.current || query.isFetching) {
      return
    }
    feedback.reset()
    refreshInFlight.current = true
    void query.refetch().finally(() => {
      refreshInFlight.current = false
    })
  }

  const download = (object: StoredObject): void => {
    void feedback.run(async () => {
      const blob = await getObjectContent(object.id)
      saveBlob(blob, downloadFilename(object.name, object.id, object.mime))
      return t('download.saved', {
        name: object.name === '' ? object.id : object.name,
      })
    })
  }

  const confirmDelete = (): void => {
    const object = pendingDelete
    setPendingDelete(null)
    if (object === null) {
      return
    }
    void feedback.run(async () => {
      try {
        await removeObject.mutateAsync({ id: object.id })
        invalidate()
        return t('delete.deleted')
      } catch (error) {
        // The server refused the delete, but the row it named may already
        // be gone (a not-found or conflict answer): re-read the tenant
        // subtree so the screen converges on what the server holds.
        invalidate()
        throw error
      }
    })
  }

  const columns: DataTableColumn<StoredObject>[] = [
    {
      id: 'name',
      header: t('list.column.name'),
      cellWrap: 'nowrap',
      // The module normalized the name once, so it is safe as a text
      // node; the full value lives in the details dialog, and the cell
      // stays one line with an ellipsis rather than widening the table.
      // The cap is the shared primary-identity one: the sm tier has only
      // the name, state and actions columns to spend its width on, and a
      // 240px name there pushes the row actions past the edge.
      contentMaxWidth: PRIMARY_COLUMN_MAX_WIDTH,
      cell: (object) => (
        <Typography
          variant="body2"
          noWrap
          color={object.name === '' ? 'text.secondary' : undefined}
        >
          {object.name === '' ? t('list.unnamed') : object.name}
        </Typography>
      ),
    },
    {
      id: 'id',
      header: t('list.column.id'),
      priority: 'low',
      cellWrap: 'nowrap',
      cellFullValue: (object) => object.id,
      cell: (object) => (
        <Typography variant="body2">
          {object.id.slice(0, ID_PREFIX_LENGTH)}
        </Typography>
      ),
    },
    {
      id: 'state',
      header: t('list.column.state'),
      priority: 'high',
      cellWrap: 'nowrap',
      cell: (object) => (
        <Typography variant="body2" color="text.secondary">
          {t(stateLabelKey(object.state))}
        </Typography>
      ),
    },
    {
      id: 'size',
      header: t('list.column.size'),
      priority: 'low',
      cellWrap: 'nowrap',
      cell: (object) => (
        <Typography variant="body2">
          {t('size.bytes', { size: formatByteNumber(object.size, i18n.language) })}
        </Typography>
      ),
    },
    {
      id: 'mime',
      header: t('list.column.mime'),
      priority: 'low',
      cellWrap: 'nowrap',
      contentMaxWidth: MIME_COLUMN_MAX_WIDTH,
      cell: (object) => (
        // A registered MIME type can run past 60 characters; the cell
        // truncates instead of widening the table, and the hover title
        // carries the complete value (the details dialog shows it too).
        <Typography variant="body2" noWrap title={object.mime}>
          {object.mime}
        </Typography>
      ),
    },
    {
      id: 'created',
      header: t('list.column.created'),
      priority: 'medium',
      cellWrap: 'nowrap',
      cell: (object) => (
        <Typography variant="body2" color="text.secondary">
          {formatUtc(object.created_at, i18n.language) ?? ''}
        </Typography>
      ),
    },
    {
      id: 'actions',
      header: t('list.column.actions'),
      cellWrap: 'nowrap',
      cell: (object) => (
        <Box sx={{ display: 'flex', gap: 1 }}>
          <Button
            type="button"
            size="small"
            variant="outlined"
            aria-label={t('actions.downloadAria', { id: object.id })}
            onClick={() => download(object)}
          >
            {t('actions.download')}
          </Button>
          <Button
            type="button"
            size="small"
            variant="outlined"
            onClick={() => setDetailId(object.id)}
          >
            {t('actions.details')}
          </Button>
          <Button
            type="button"
            size="small"
            variant="outlined"
            color="error"
            aria-label={t('actions.deleteAria', { id: object.id })}
            // A delete in flight disables and marks busy every delete
            // control on the page, so two deletes never overlap and the
            // one slot cannot lose a result.
            disabled={deletePending}
            loading={deletePending}
            onClick={() => setPendingDelete(object)}
          >
            {t('actions.delete')}
          </Button>
        </Box>
      ),
    },
  ]

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
      <PageHeader
        title={t('page.title')}
        description={t('page.description')}
        actions={
          <Button
            type="button"
            variant="outlined"
            size="small"
            // The busy indicator is named by the button's own text: the
            // button carries the id while it is busy, and the progressbar
            // is labelled by it.
            id={refreshBusy ? refreshLoadingId : undefined}
            // A focused control moved to native disabled drops focus to
            // the document, so the read's busy state is aria-busy only;
            // the two states that make a re-read wrong keep it disabled.
            disabled={refreshBlocked}
            aria-busy={refreshBusy || undefined}
            startIcon={
              refreshBusy ? (
                <CircularProgress
                  aria-labelledby={refreshLoadingId}
                  color="inherit"
                  size={16}
                />
              ) : undefined
            }
            onClick={refresh}
          >
            {t('page.refresh')}
          </Button>
        }
      />

      <Box component="section">
        <Typography variant="h2" sx={{ fontSize: '1.25rem', mb: 1 }}>
          {t('upload.heading')}
        </Typography>
        <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
          {t('upload.description')}
        </Typography>
        <FileUploader
          rows={uploads.rows}
          onSelectFiles={uploads.selectFiles}
          onRetry={uploads.retry}
          onRemove={uploads.remove}
          allowDrop
          multiple
        />
      </Box>

      <Box component="section">
        <Typography variant="h2" sx={{ fontSize: '1.25rem', mb: 1 }}>
          {t('list.heading')}
        </Typography>
        <ObjectAsyncSection<StoredObject[]>
          pending={pending}
          payload={query.data === undefined ? undefined : objects}
          error={query.error}
          empty={objects.length === 0}
          refreshing={query.isFetching}
          headingLevel="h3"
          loading={
            <ListSkeleton
              label={t('list.loading')}
              rows={[
                <Skeleton key="a" variant="text" width="60%" />,
                <Skeleton key="b" variant="text" width="45%" />,
              ]}
            />
          }
          errorState={{
            title: t('list.error.title'),
            description: t('list.error.description'),
            retryLabel: t('list.retry'),
            onRetry: () => void query.refetch(),
          }}
          emptyState={{
            title: t('list.empty.title'),
            description: t('list.empty.description'),
          }}
        >
          {(settled) => (
            <DataTable
              rows={settled}
              columns={columns}
              rowKey={(object) => object.id}
            />
          )}
        </ObjectAsyncSection>
        {/* The page's one write-feedback region: under the list it
            describes, above the pager whose context it belongs to. */}
        <WriteFeedbackRegion
          outcome={feedback.outcome}
          resolve={resolveErrorText}
        />
        {query.data !== undefined &&
        !isTenantRefusal(query.error) &&
        !isForbidden(query.error) &&
        (hasPrevious || hasNext) ? (
          <Box sx={{ display: 'flex', gap: 2, mt: 2 }}>
            <Button
              type="button"
              variant="outlined"
              size="small"
              disabled={!hasPrevious || feedback.pending}
              onClick={() => {
                feedback.reset()
                setCursorStack((stack) => stack.slice(0, stack.length - 1))
              }}
            >
              {t('list.previous')}
            </Button>
            <Button
              type="button"
              variant="outlined"
              size="small"
              disabled={!hasNext || feedback.pending}
              onClick={() => {
                feedback.reset()
                setCursorStack((stack) => [...stack, nextCursor])
              }}
            >
              {t('list.next')}
            </Button>
          </Box>
        ) : null}
      </Box>

      <ObjectDetailDialog id={detailId} onClose={() => setDetailId(null)} />

      <ConfirmDialog
        open={pendingDelete !== null}
        variant="danger"
        // Deleting an object removes its stored bytes for good and cannot be
        // undone from the interface: the heavier two-step arm.
        doubleConfirm
        title={t('delete.title')}
        message={
          pendingDelete === null
            ? undefined
            : t('delete.message', { id: pendingDelete.id })
        }
        confirmLabel={t('delete.confirm')}
        cancelLabel={t('delete.cancel')}
        onConfirm={confirmDelete}
        onCancel={() => setPendingDelete(null)}
      />
    </Box>
  )
}
