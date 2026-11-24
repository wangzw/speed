/** The placeholder a host renders once protected content's authenticated
 * snapshot has turned anonymous. */

import Button from '@mui/material/Button'
import { EmptyState } from '@speed/ui-kit'
import type { EmptyStateProps } from '@speed/ui-kit'
import { useAuthUiTranslation } from './internal/translation.js'

/** The host supplies the sign-in action and the heading level; the
 * placeholder holds no state. */
export interface SessionEndedScreenProps {
  /** Fired when the viewer asks to sign in again; the host navigates. */
  readonly onSignIn: () => void
  /**
   * The heading element the title renders as. Defaults to 'h1' because the
   * screen is a whole-page placeholder; a host embedding it under a heading
   * of its own passes the level that continues the page's order.
   */
  readonly headingLevel?: EmptyStateProps['headingLevel']
}

/**
 * The session-ended placeholder: a heading, its description and a sign-in
 * action that hands the viewer back to the host's sign-in surface.
 */
export function SessionEndedScreen({
  onSignIn,
  headingLevel = 'h1',
}: SessionEndedScreenProps) {
  const { t } = useAuthUiTranslation()
  return (
    <EmptyState
      variant="noPermission"
      headingLevel={headingLevel}
      title={t('sessionEnded.title')}
      description={t('sessionEnded.description')}
      action={
        <Button variant="contained" onClick={onSignIn}>
          {t('sessionEnded.signInAction')}
        </Button>
      }
    />
  )
}
