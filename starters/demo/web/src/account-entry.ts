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
 * The assembly uses 'both'.
 */
export type AccountEntryPlacement = 'sidebar' | 'header' | 'both'

export const ACCOUNT_ENTRY: AccountEntryPlacement = 'both'
