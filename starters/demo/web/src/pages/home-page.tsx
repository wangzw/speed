/**
 * The app's starting page: the tenant area's landing (route '/').
 *
 * It is the skeleton's front door, not a business surface. The skeleton
 * carries no business domain, so there is no data to render -- what the page
 * shows instead is the platform already assembled around it: one card per
 * capability that works out of the box (the account settings, the platform
 * console, the object store, the notification centre), each a real
 * destination into the app.
 *
 * A card carries an optional permission. A card without one is offered to
 * every signed-in viewer; a card with one is rendered only when the principal
 * holds it, so a capability that is not the member's to see is not announced
 * by its title either. The console is such a card: it is gated on the same
 * platform code the header entry is (src/app.tsx's ConsoleEntry), so the two
 * never disagree about who may reach the console. The gate fails closed: a
 * permission set the host has not attached yet reads as "not held", so a card
 * stays hidden until the principal is confirmed to hold its code.
 *
 * Each card is an anchor carrying its target path, so a middle click or a
 * copy opens the destination too; a plain click is caught and routed, so the
 * page never reloads (the same shape the shell's navigation entries take).
 */

import type { MouseEvent, ReactElement } from 'react'
import Box from '@mui/material/Box'
import Card from '@mui/material/Card'
import CardActionArea from '@mui/material/CardActionArea'
import Typography from '@mui/material/Typography'
import { usePermission } from '@speed/auth-core'
import type { AuthDomain } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import { useNavigate } from '@speed/route-kit'
import { PageHeader } from '@speed/ui-kit'
import {
  ACCOUNT_PATH,
  CONSOLE_PREFIX,
  NOTIFICATIONS_PATH,
  OBJECTS_PATH,
} from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'

/**
 * The domain and code a card is gated on. It is the same pair a route
 * declaration carries, checked through auth-core's usePermission -- a UX
 * affordance over the set the host attached, never a security boundary.
 */
interface CardPermission {
  /** The permission domain the code resolves in ('system' is the
   * tenant-independent platform set). */
  readonly domain: AuthDomain
  /** The code the principal must hold to be offered the card. */
  readonly permission: string
}

/**
 * One capability the start page offers: the demo-namespace key pair that
 * names it, the path it opens, and the platform code -- if any -- the
 * principal must hold to be offered it. The list is the skeleton's assembled
 * capabilities, each a route the app already serves.
 */
interface StartCard {
  /** The `home.cards.<key>` prefix its title and description resolve from. */
  readonly key: 'account' | 'console' | 'objects' | 'notifications'
  /** The route the card opens. */
  readonly path: string
  /**
   * The platform code the principal must hold to be offered this card. A card
   * with one is hidden until the principal is confirmed to hold it (the gate
   * fails closed); a card without one is offered to every signed-in viewer.
   */
  readonly permission?: CardPermission
}

const START_CARDS: readonly StartCard[] = [
  { key: 'account', path: ACCOUNT_PATH },
  {
    key: 'console',
    path: CONSOLE_PREFIX,
    // The platform-staff code the header's console entry gates on
    // (src/app.tsx's ConsoleEntry): the console's pages resolve their codes in
    // the platform domain, and any one of them tells platform staff apart from
    // an ordinary member. Held to the same code the entry uses, so a member is
    // offered neither the entry nor this card.
    permission: { domain: 'system', permission: 'tenant.read' },
  },
  { key: 'objects', path: OBJECTS_PATH },
  { key: 'notifications', path: NOTIFICATIONS_PATH },
]

/** The app's starting page: the platform's capabilities as a card grid. */
export function HomePage(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Box
      component="section"
      // The page's own root is the containment context (the layout design's
      // component-level response: the component declares it on its root, the
      // grid's tracks query it). The grid cannot declare it itself -- a
      // container query resolves against an ancestor, never the element that
      // owns the rule.
      sx={{
        display: 'flex',
        flexDirection: 'column',
        gap: 4,
        containerType: 'inline-size',
      }}
    >
      <PageHeader title={t('home.heading')} description={t('home.intro')} />
      <Box
        sx={{
          display: 'grid',
          // Container-keyed tracks, the FormLayout/DataTable recipe: one
          // column at the container's narrow end (the `@` shorthand's 0px
          // baseline), two equal tracks from its `sm` (600px) up. The page
          // collapses to one column in a narrow panel even on a wide viewport.
          gridTemplateColumns: { '@': '1fr', '@sm': 'repeat(2, 1fr)' },
          gap: 2,
        }}
      >
        {START_CARDS.map((card) => (
          <StartCardTile key={card.key} card={card} />
        ))}
      </Box>
    </Box>
  )
}

/**
 * One card in the grid. A card whose declaration names a permission is routed
 * through GatedStartCard, which runs the check; a card without one renders
 * directly. The split keeps usePermission out of this dispatcher and out of
 * the map above, where the rules of hooks forbid a conditional hook call.
 */
function StartCardTile({
  card,
}: {
  readonly card: StartCard
}): ReactElement | null {
  if (card.permission === undefined) {
    return <StartCardView card={card} />
  }
  return <GatedStartCard card={card} permission={card.permission} />
}

/**
 * A card whose declaration names a permission: it renders its card only once
 * usePermission confirms the principal holds the code. The check fails closed
 * -- false while the platform set is unknown (the host attaches it once the
 * caller's platform permissions have been read) and false when the code is not
 * held -- so the card stays hidden until the principal is confirmed to hold
 * it, the same direction the header's console entry takes: a member never sees
 * the card for the moment before the set arrives.
 */
function GatedStartCard({
  card,
  permission,
}: {
  readonly card: StartCard
  readonly permission: CardPermission
}): ReactElement | null {
  const allowed = usePermission(permission.domain, permission.permission)
  if (!allowed) {
    return null
  }
  return <StartCardView card={card} />
}

/** One card as an anchor carrying its target path. */
function StartCardView({ card }: { readonly card: StartCard }): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const navigate = useNavigate()
  const open = (path: string) => (event: MouseEvent<HTMLElement>): void => {
    event.preventDefault()
    navigate(path)
  }
  return (
    <Card variant="outlined">
      <CardActionArea
        component="a"
        href={card.path}
        onClick={open(card.path)}
        sx={{
          display: 'flex',
          flexDirection: 'column',
          alignItems: 'flex-start',
          gap: 1,
          p: 2.5,
          height: '100%',
        }}
      >
        <Typography component="h2" variant="h6">
          {t(`home.cards.${card.key}.title`)}
        </Typography>
        <Typography variant="body2" color="text.secondary">
          {t(`home.cards.${card.key}.description`)}
        </Typography>
      </CardActionArea>
    </Card>
  )
}
