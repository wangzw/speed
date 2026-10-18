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
 * A card is offered only while its destination exists: the account card
 * always, the object and notification cards when the route layer leaves their
 * declarations in force (useHasRoute), and the console card only to platform
 * staff while at least one console page is served -- pointing at the first such
 * page the principal may open, the same derivation the header entry uses
 * (src/console-routes.ts's firstConsoleTarget). The gates fail closed: a
 * permission set or feature document the host has not settled reads as "not
 * offered", so a card stays hidden until the fact it depends on is known.
 *
 * Each card is an anchor carrying its target path, so a middle click or a
 * copy opens the destination too; a plain click is caught and routed, so the
 * page never reloads (the same shape the shell's navigation entries take).
 */

import { useEffect, useState } from 'react'
import type { MouseEvent, ReactElement } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Card from '@mui/material/Card'
import CardActionArea from '@mui/material/CardActionArea'
import Link from '@mui/material/Link'
import Typography from '@mui/material/Typography'
import { featurePresence, useFeatureState } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import {
  presentDeclarations,
  useHasRoute,
} from '@speed/route-kit'
import { PageHeader } from '@speed/ui-kit'
import { useLocation, useNavigate } from 'react-router-dom'
import {
  consoleShellRoutes,
  useConsoleTarget,
} from '../console-routes.js'
import {
  ACCOUNT_PATH,
  ACCOUNT_SETUP_PATH,
  NOTIFICATIONS_PATH,
  OBJECTS_PATH,
} from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'

/**
 * One capability the start page offers: the demo-namespace key pair that
 * names it, the path it opens, and how its offer is decided -- always, by the
 * route layer's presence answer for that path, or by the console's own
 * permission-and-feature derivation.
 */
type StartCard =
  | {
      /** The `home.cards.<key>` prefix its title and description resolve from. */
      readonly key: 'account' | 'objects' | 'notifications'
      /** The route the card opens. */
      readonly path: string
      /**
       * `route` cards follow their destination: they are offered exactly when
       * the route layer leaves the declaration in force.
       */
      readonly gate: 'direct' | 'route'
    }
  | {
      readonly key: 'console'
      readonly gate: 'console'
    }

const START_CARDS: readonly StartCard[] = [
  { key: 'account', path: ACCOUNT_PATH, gate: 'direct' },
  { key: 'console', gate: 'console' },
  { key: 'objects', path: OBJECTS_PATH, gate: 'route' },
  { key: 'notifications', path: NOTIFICATIONS_PATH, gate: 'route' },
]

/** The app's starting page: the platform's capabilities as a card grid. */
export function HomePage(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const location = useLocation()
  const navigate = useNavigate()
  // A registration completed through the SMS sign-in lands here with a
  // one-shot router-state flag (src/pages/sign-in-view.tsx). The notice is
  // local to this visit: clearing the history entry's state means a reload or
  // a later return does not show it again, and dismissing it drops it from
  // the page.
  const [setupNotice, setSetupNotice] = useState(false)
  useEffect(() => {
    const state = location.state as { readonly registration?: unknown } | null
    if (state?.registration !== 'sms') {
      return
    }
    setSetupNotice(true)
    navigate(location.pathname, { replace: true, state: null })
  }, [location.state, location.pathname, navigate])
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
      {setupNotice ? (
        <Alert
          severity="info"
          onClose={() => setSetupNotice(false)}
          closeText={t('home.setupNotice.dismiss')}
        >
          <Typography variant="body2">
            {t('home.setupNotice.title')}
          </Typography>
          <Link
            href={ACCOUNT_SETUP_PATH}
            underline="hover"
            onClick={(event) => {
              event.preventDefault()
              navigate(ACCOUNT_SETUP_PATH)
            }}
          >
            {t('home.setupNotice.action')}
          </Link>
        </Alert>
      ) : null}
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
 * One card in the grid. The split keeps each gate's hooks out of this
 * dispatcher and out of the map above, where the rules of hooks forbid a
 * conditional hook call.
 */
function StartCardTile({
  card,
}: {
  readonly card: StartCard
}): ReactElement | null {
  if (card.gate === 'console') {
    return <ConsoleStartCard />
  }
  if (card.gate === 'route') {
    return <RouteStartCard card={card} />
  }
  return <StartCardView card={card} />
}

/**
 * A card that follows its destination: it renders only while the route
 * layer leaves the declaration in force, so a deployment that does not serve
 * the page's feature does not announce it by its title either.
 */
function RouteStartCard({
  card,
}: {
  readonly card: { readonly key: StartCard['key']; readonly path: string }
}): ReactElement | null {
  const offered = useHasRoute(card.path)
  if (!offered) {
    return null
  }
  return <StartCardView card={card} />
}

/**
 * The console card: offered only to a principal holding a platform permission,
 * while at least one console declaration is in force, and pointing at the
 * first console page the principal may open. It calls the same
 * useConsoleTarget the header's console entry does (src/app.tsx), so card and
 * entry never disagree about where the console is or whether it is offered at
 * all -- including during an impersonation, when neither offers it. While the
 * feature document or the permission set is unsettled the card is hidden (both
 * answers fail closed).
 */
function ConsoleStartCard(): ReactElement | null {
  const presence = featurePresence(useFeatureState())
  const target = useConsoleTarget(
    presentDeclarations(consoleShellRoutes, presence),
  )
  if (target === null) {
    return null
  }
  return <StartCardView card={{ key: 'console', path: target }} />
}

/** One card as an anchor carrying its target path. */
function StartCardView({
  card,
}: {
  readonly card: {
    readonly key: StartCard['key']
    readonly path: string
  }
}): ReactElement {
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
