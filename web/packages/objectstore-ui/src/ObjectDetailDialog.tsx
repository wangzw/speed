/**
 * ObjectDetailDialog: one object's metadata, read from GET /objects/{id}.
 *
 * The list rows already carry the object's view, but the details surface
 * reads the object back through its own operation on open -- the
 * canonical "read one object" path, exercised where a row's cached view
 * might be stale. The dialog is controlled by the `id` prop: null keeps it
 * closed (and the read disabled), an id opens it and fetches that object.
 * Every field renders from the objectstore-ui namespace; instants render
 * through Intl in the active language and in UTC, and an absent expiry
 * renders the "never" copy rather than an empty cell.
 *
 * The async state is the shared four-state branch (loading, error,
 * empty/content), so the dialog never re-invents a spinner or an error
 * block. The failure branch splits a permission refusal like the list's.
 */

import { useId } from 'react'
import Button from '@mui/material/Button'
import Dialog from '@mui/material/Dialog'
import DialogActions from '@mui/material/DialogActions'
import DialogContent from '@mui/material/DialogContent'
import DialogTitle from '@mui/material/DialogTitle'
import Skeleton from '@mui/material/Skeleton'
import Stack from '@mui/material/Stack'
import Typography from '@mui/material/Typography'
import { ListSkeleton } from '@speed/ui-kit'
import { useObjectDetailQuery } from './internal/queries.js'
import { ObjectAsyncSection } from './internal/async-section.js'
import { formatByteNumber, formatUtc } from './internal/format.js'
import { stateLabelKey } from './internal/status.js'
import { useObjectstoreUiTranslation } from './internal/translation.js'
import type { StoredObject } from './internal/models.js'

export interface ObjectDetailDialogProps {
  /** The object to show, or null to keep the dialog closed. */
  readonly id: string | null
  /** Fired on close (the backdrop, Escape or the close button). */
  readonly onClose: () => void
}

/** The metadata fields of one object, in the dialog's definition list. */
function DetailRow({
  label,
  value,
}: {
  readonly label: string
  readonly value: string
}) {
  return (
    <Stack direction="row" spacing={2} sx={{ alignItems: 'baseline' }}>
      <Typography variant="body2" color="text.secondary" sx={{ minWidth: 128 }}>
        {label}
      </Typography>
      <Typography variant="body2" sx={{ wordBreak: 'break-all' }}>
        {value}
      </Typography>
    </Stack>
  )
}

/** One object's detail dialog over the generated GET /objects/{id} read. */
export function ObjectDetailDialog({ id, onClose }: ObjectDetailDialogProps) {
  const { t, i18n } = useObjectstoreUiTranslation()
  const titleId = useId()
  const { query } = useObjectDetailQuery(id)
  const object = query.data?.object
  const pending = query.isPending && query.data === undefined

  return (
    <Dialog
      open={id !== null}
      onClose={onClose}
      aria-labelledby={titleId}
      maxWidth="sm"
      fullWidth
    >
      <DialogTitle id={titleId}>{t('detail.title')}</DialogTitle>
      <DialogContent>
        <ObjectAsyncSection<StoredObject>
          pending={pending}
          payload={object}
          error={query.error}
          empty={false}
          loading={
            <ListSkeleton
              label={t('detail.loading')}
              rows={[
                <Skeleton key="a" variant="text" width="70%" />,
                <Skeleton key="b" variant="text" width="55%" />,
                <Skeleton key="c" variant="text" width="60%" />,
              ]}
            />
          }
          errorState={{
            title: t('detail.error.title'),
            description: t('detail.error.description'),
            retryLabel: t('detail.retry'),
            onRetry: () => void query.refetch(),
          }}
        >
          {(settled) => (
            <Stack spacing={1.5} sx={{ mt: 1 }}>
              <DetailRow label={t('detail.field.id')} value={settled.id} />
              <DetailRow
                label={t('detail.field.state')}
                value={t(stateLabelKey(settled.state))}
              />
              <DetailRow
                label={t('detail.field.size')}
                value={t('size.bytes', {
                  size: formatByteNumber(settled.size, i18n.language),
                })}
              />
              <DetailRow label={t('detail.field.mime')} value={settled.mime} />
              <DetailRow
                label={t('detail.field.checksum')}
                value={settled.checksum_sha256}
              />
              <DetailRow
                label={t('detail.field.expires')}
                value={formatUtc(settled.expires_at, i18n.language) ?? t('detail.never')}
              />
              <DetailRow
                label={t('detail.field.created')}
                value={formatUtc(settled.created_at, i18n.language) ?? ''}
              />
              <DetailRow
                label={t('detail.field.updated')}
                value={formatUtc(settled.updated_at, i18n.language) ?? ''}
              />
            </Stack>
          )}
        </ObjectAsyncSection>
      </DialogContent>
      <DialogActions>
        <Button onClick={onClose}>{t('detail.close')}</Button>
      </DialogActions>
    </Dialog>
  )
}
