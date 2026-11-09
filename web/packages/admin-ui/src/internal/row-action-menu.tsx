/**
 * RowActionMenu: a table row's controls gathered behind one trigger.
 *
 * A row with several writes used to lay them out side by side, which widened
 * the actions column with every added control. The trigger carries the row's
 * accessible name and opens a menu; each item keeps its own label, so a
 * keyboard user reaches every write through one stop in the row.
 */

import { useState, type ReactElement } from 'react'
import Button from '@mui/material/Button'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import { useAdminUiTranslation } from './translation.js'

export interface RowAction {
  readonly key: string
  readonly label: string
  readonly onSelect: () => void
  readonly disabled?: boolean
}

export interface RowActionMenuProps {
  /** Accessible name of the trigger, naming the row it belongs to. */
  readonly label: string
  readonly actions: readonly RowAction[]
  /** Whether the whole menu is unavailable, such as while a write is in flight. */
  readonly disabled?: boolean
  /** Marks the trigger when one of its actions was refused. */
  readonly invalid?: boolean
  /** The id of the failure text that describes the refused action. */
  readonly describedBy?: string
}

export function RowActionMenu({
  label,
  actions,
  disabled = false,
  invalid = false,
  describedBy,
}: RowActionMenuProps): ReactElement {
  const { t } = useAdminUiTranslation()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)
  return (
    <>
      <Button
        type="button"
        size="small"
        variant="outlined"
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={anchor !== null}
        aria-invalid={invalid}
        aria-describedby={describedBy}
        disabled={disabled}
        onClick={(event) => setAnchor(event.currentTarget)}
      >
        {t('common.actions')}
      </Button>
      <Menu
        anchorEl={anchor}
        open={anchor !== null}
        onClose={() => setAnchor(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
      >
        {actions.map((action) => (
          <MenuItem
            key={action.key}
            disabled={action.disabled ?? false}
            onClick={() => {
              setAnchor(null)
              action.onSelect()
            }}
          >
            {action.label}
          </MenuItem>
        ))}
      </Menu>
    </>
  )
}
