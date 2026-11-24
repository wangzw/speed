import type { AppShellShellLayout } from '@speed/layout-kit'

export type AccountEntryPlacement = 'sidebar' | 'header' | 'both'

export const ACCOUNT_ENTRY: AccountEntryPlacement = 'both'

export function accountEntryInUserSlot(
  shellLayout: AppShellShellLayout | undefined,
): boolean {
  return ACCOUNT_ENTRY !== 'sidebar' && shellLayout !== 'sidebar'
}

export function accountEntryInNavFooter(
  shellLayout: AppShellShellLayout | undefined,
): boolean {
  return shellLayout === 'sidebar' || ACCOUNT_ENTRY !== 'header'
}
