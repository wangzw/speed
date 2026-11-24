/**
 * RowActionMenu: a table row's controls gathered behind one trigger. The
 * trigger carries the row's accessible name; each menu item keeps its own
 * label.
 */

import { useRef, useState, type MouseEvent, type ReactElement } from 'react'
import Button from '@mui/material/Button'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import { useActivationGuard, useInertBackground } from '@speed/ui-kit'
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
  /** Whether the whole menu is unavailable, such as while a write is in flight; the trigger stays focusable and opens nothing. */
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
  const menuRef = useRef<HTMLDivElement>(null)
  useInertBackground(anchor !== null, menuRef)
  const openMenu = useActivationGuard(
    (event: MouseEvent<HTMLButtonElement>) => setAnchor(event.currentTarget),
    { busy: false, unavailable: disabled },
  )
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
        aria-disabled={disabled ? true : undefined}
        onClick={openMenu}
      >
        {t('common.actions')}
      </Button>
      <Menu
        ref={menuRef}
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
