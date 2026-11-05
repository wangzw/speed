/**
 * Where the skeleton puts the account entry.
 *
 * The capability is the same in both places -- a trigger that opens the
 * account panes and the sign-out action -- so an assembled product picks
 * one by editing this literal, not by a runtime setting:
 *
 *  - 'sidebar': the entry is a row at the bottom of the navigation column,
 *    opening upward. The header carries no avatar menu.
 *  - 'header': the entry is the avatar menu in the header's user slot. The
 *    navigation column carries no account row.
 *  - 'both': both entries render; they open the same menu items.
 *
 * The assembly uses 'both'. The shell's shape narrows it: the sidebar shape
 * moves the header's user slot into the drawer paper's bottom row, directly
 * under the account row, so 'both' there would stack two triggers for one
 * menu. In that shape the row is the account entry, whichever value this
 * constant holds.
 */
import type { AppShellShellLayout } from '@speed/layout-kit'

export type AccountEntryPlacement = 'sidebar' | 'header' | 'both'

export const ACCOUNT_ENTRY: AccountEntryPlacement = 'both'

/**
 * Whether the shell's user slot carries the account entry in the given shape.
 * The sidebar shape does not: its bottom row sits under the account row, and
 * two triggers for one menu in one corner read as a duplicated control.
 */
export function accountEntryInUserSlot(
  shellLayout: AppShellShellLayout | undefined,
): boolean {
  return ACCOUNT_ENTRY !== 'sidebar' && shellLayout !== 'sidebar'
}

/**
 * Whether the navigation column carries the account row in the given shape.
 * The sidebar shape always does -- its row is the only account entry that
 * shape has, whatever the constant says -- and the topbar shape follows the
 * constant.
 */
export function accountEntryInNavFooter(
  shellLayout: AppShellShellLayout | undefined,
): boolean {
  return shellLayout === 'sidebar' || ACCOUNT_ENTRY !== 'header'
}
