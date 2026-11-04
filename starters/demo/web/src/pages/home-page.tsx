/**
 * The app's starting page: the tenant area's landing (route '/').
 *
 * It is the skeleton's front door, not a business surface. The skeleton
 * carries no business domain, so there is no data to render -- what the page
 * shows instead is the platform already assembled around it: one card per
 * capability that works out of the box (the account settings, the platform
 * admin, the notification centre), each a real destination into the app.
 *
 * A card is offered only while its destination exists: the account card
 * always, the notification card when the route layer leaves its
 * declaration in force (useHasRoute), and the admin card only to platform
 * staff while at least one admin page is served -- pointing at the first such
 * page the principal may open, the same derivation the header entry uses
 * (src/admin-routes.ts's firstAdminTarget). The gates fail closed: a
 * permission set or feature document the host has not settled reads as "not
 * offered", so a card stays hidden until the fact it depends on is known.
 *
 * Each card is an anchor carrying its target path, so a middle click, a
 * modified click or a copy opens the destination too; a plain primary click
 * is caught and routed, so the page never reloads (route-kit's
 * useRouteLinkProps decides which click is which).
 */

import { useEffect, useRef } from 'react'
import type { ReactElement } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import CardActionArea from '@mui/material/CardActionArea'
import Typography from '@mui/material/Typography'
import { featurePresence, useFeature, useFeatureState } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import {
  presentDeclarations,
  useHasRoute,
  useRouteLinkProps,
} from '@speed/route-kit'
import { PageLayout, SectionCard } from '@speed/ui-kit'
import {
  adminShellRoutes,
  useAdminTarget,
} from '../admin-routes.js'
import { useWorkspaceAdminAdmission } from '../workspace-admin.js'
import { ACCOUNT_PATH, NOTIFICATIONS_PATH } from '../paths.js'
import { useTenantMembershipNotice } from '../tenant-membership-notice.js'
import { ReviewNoticeControl } from '../review-notice-control.js'
import { DEMO_NAMESPACE } from '../resources.js'

/**
 * One capability the start page offers: the demo-namespace key pair that
 * names it, the path it opens, and how its offer is decided -- always, by the
 * route layer's presence answer for that path, or by the admin's own
 * permission-and-feature derivation.
 */
type StartCard =
  | {
      /** The `home.cards.<key>` prefix its title and description resolve from. */
      readonly key: 'account' | 'notifications'
      /** The route the card opens. */
      readonly path: string
      /**
       * `route` cards follow their destination: they are offered exactly when
       * the route layer leaves the declaration in force.
       */
      readonly gate: 'direct' | 'route'
    }
  | {
      readonly key: 'admin'
      readonly gate: 'admin'
    }
  | {
      readonly key: 'workspaceAdmin'
      readonly gate: 'workspaceAdmin'
    }

const START_CARDS: readonly StartCard[] = [
  { key: 'account', path: ACCOUNT_PATH, gate: 'direct' },
  { key: 'workspaceAdmin', gate: 'workspaceAdmin' },
  { key: 'admin', gate: 'admin' },
  { key: 'notifications', path: NOTIFICATIONS_PATH, gate: 'route' },
]

/** The app's starting page: the platform's capabilities as a card grid. */
export function HomePage(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  // The acceptance confirmation the invitation landing announced on its way
  // here (src/tenant-membership-notice.tsx). It is a one-shot notice: the
  // viewer dismisses it, and the dismissal lasts for the identity's session.
  const membershipNotice = useTenantMembershipNotice()
  // The acceptance navigated here from the landing, which unmounts on the
  // way: focus would otherwise fall to the document body with no signal that
  // the page changed. The confirmation is the journey's result, so it takes
  // the focus when it is the reason this page mounted. The announcement
  // itself is the provider's standing live region; this block is its visible
  // twin and carries no live semantics of its own.
  const confirmation = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (membershipNotice.visible) {
      confirmation.current?.focus()
    }
  }, [membershipNotice.visible])
  // The review build's notice control is offered only while the process
  // declares its review-only feature. A baseline deployment never does, so the
  // control is absent there; the gate fails closed while the feature document
  // is unsettled.
  const reviewNoticeServed = useFeature('review.notification.control')
  return (
    <PageLayout title={t('home.heading')} description={t('home.intro')}>
      {membershipNotice.visible ? (
        <Alert
          ref={confirmation}
          severity="success"
          // Not a live region: announcing belongs to the provider's standing
          // status region, which existed before this text did. Rendering the
          // block as one too would announce the confirmation twice (and a
          // region born with its text is unreliable anyway).
          role="none"
          tabIndex={-1}
          onClose={membershipNotice.dismiss}
          closeText={t('home.membershipNotice.dismiss')}
        >
          <Typography variant="body2">
            {t('home.membershipNotice.title')}
          </Typography>
        </Alert>
      ) : null}
      <SectionCard>
        <Box
          sx={{
            display: 'grid',
            // Container-keyed tracks, the FormLayout/DataTable recipe: one
            // column at the container's narrow end (the `@` shorthand's 0px
            // baseline), two equal tracks from its `sm` (600px) up. The
            // container is the page frame's (PageLayout), so the grid
            // collapses to one column in a narrow panel even on a wide
            // viewport.
            gridTemplateColumns: { '@': '1fr', '@sm': 'repeat(2, 1fr)' },
            gap: 2,
          }}
        >
          {START_CARDS.map((card) => (
            <StartCardTile key={card.key} card={card} />
          ))}
        </Box>
      </SectionCard>
      {reviewNoticeServed ? <ReviewNoticeControl /> : null}
    </PageLayout>
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
  if (card.gate === 'admin') {
    return <AdminStartCard />
  }
  if (card.gate === 'workspaceAdmin') {
    return <WorkspaceAdminStartCard />
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
 * The admin card: offered only to a principal holding a platform permission,
 * while at least one admin declaration is in force, and pointing at the
 * first admin page the principal may open. It calls the same
 * useAdminTarget the header's admin entry does (src/app.tsx), so card and
 * entry never disagree about where the admin is or whether it is offered at
 * all -- including during an impersonation, when neither offers it. While the
 * feature document or the permission set is unsettled the card is hidden (both
 * answers fail closed).
 */
function AdminStartCard(): ReactElement | null {
  const presence = featurePresence(useFeatureState())
  const target = useAdminTarget(
    presentDeclarations(adminShellRoutes, presence),
  )
  if (target === null) {
    return null
  }
  return <StartCardView card={{ key: 'admin', path: target }} />
}

/**
 * The workspace admin card: offered exactly when the workspace surface's
 * admission allows it -- a business tenant other than `system`, no
 * impersonation, at least one served workspace declaration, and the
 * tenant-domain permission set for THIS tenant holding one of its codes.
 * It calls the same useWorkspaceAdminAdmission the account menu's workspace
 * group does (src/workspace-admin.ts), so the card and the group never
 * disagree; a pending or foreign-tenant permission read fails closed and the
 * card stays hidden.
 */
function WorkspaceAdminStartCard(): ReactElement | null {
  const admission = useWorkspaceAdminAdmission()
  if (admission.status !== 'allowed') {
    return null
  }
  return (
    <StartCardView card={{ key: 'workspaceAdmin', path: admission.target }} />
  )
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
  const link = useRouteLinkProps(card.path)
  return (
    // A bordered inset, not a second surface: the grid already sits on a
    // SectionCard, and a card here would stack a second translucent layer
    // inside it (the dashboard KPI tiles follow the same rule). The rim is
    // the card's own border color, so a theme that gives containers a
    // visible edge gives the tiles the same one.
    <Box
      sx={{
        border: 1,
        borderColor: (theme) => theme.surface.container.borderColor,
        borderRadius: 1,
        overflow: 'hidden',
      }}
    >
      <CardActionArea
        component="a"
        href={link.href}
        onClick={link.onClick}
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
    </Box>
  )
}
