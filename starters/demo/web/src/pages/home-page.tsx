import { useEffect, useRef } from 'react'
import type { ReactElement } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import CardActionArea from '@mui/material/CardActionArea'
import Typography from '@mui/material/Typography'
import { featurePresence, useAuthState, useFeatureState } from '@speed/auth-core'
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
import { useCurrentTenant } from '../chrome.js'
import { useOrganizationAdminAdmission } from '../organization-admin.js'
import { ACCOUNT_PATH, NOTIFICATIONS_PATH, ORGANIZATIONS_NEW_PATH } from '../paths.js'
import { useAppNotice } from '../app-notice.js'
import { DEMO_NAMESPACE } from '../resources.js'

type StartCard =
  | {
      readonly key: 'account' | 'notifications'
      readonly path: string
      readonly gate: 'direct' | 'route'
    }
  | {
      readonly key: 'admin'
      readonly gate: 'admin'
    }
  | {
      readonly key: 'organizationAdmin'
      readonly gate: 'organizationAdmin'
    }
  | {
      readonly key: 'createOrganization'
      readonly gate: 'createOrganization'
    }

const START_CARDS: readonly StartCard[] = [
  { key: 'account', path: ACCOUNT_PATH, gate: 'direct' },
  { key: 'createOrganization', gate: 'createOrganization' },
  { key: 'organizationAdmin', gate: 'organizationAdmin' },
  { key: 'admin', gate: 'admin' },
  { key: 'notifications', path: NOTIFICATIONS_PATH, gate: 'route' },
]

export function HomePage(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const notice = useAppNotice()
  const confirmation = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (notice.visible) {
      confirmation.current?.focus()
    }
  }, [notice.visible])
  return (
    <PageLayout title={t('home.heading')} description={t('home.intro')}>
      {notice.visible ? (
        <Alert
          ref={confirmation}
          severity="success"
          role="none"
          tabIndex={-1}
          onClose={notice.dismiss}
          closeText={t('home.notice.dismiss')}
        >
          <Typography variant="body2">{notice.message}</Typography>
        </Alert>
      ) : null}
      <SectionCard>
        <Box
          sx={{
            display: 'grid',
            gridTemplateColumns: { '@': '1fr', '@sm': 'repeat(2, 1fr)' },
            gap: 2,
          }}
        >
          {START_CARDS.map((card) => (
            <StartCardTile key={card.key} card={card} />
          ))}
        </Box>
      </SectionCard>
    </PageLayout>
  )
}

function StartCardTile({
  card,
}: {
  readonly card: StartCard
}): ReactElement | null {
  if (card.gate === 'admin') {
    return <AdminStartCard />
  }
  if (card.gate === 'organizationAdmin') {
    return <OrganizationAdminStartCard />
  }
  if (card.gate === 'createOrganization') {
    return <CreateOrganizationStartCard />
  }
  if (card.gate === 'route') {
    return <RouteStartCard card={card} />
  }
  return <StartCardView card={card} />
}

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

function AdminStartCard(): ReactElement | null {
  const presence = featurePresence(useFeatureState())
  const tenantName = useCurrentTenant()
  const target = useAdminTarget(
    presentDeclarations(adminShellRoutes, presence),
  )
  if (target === null || tenantName !== 'system') {
    return null
  }
  return <StartCardView card={{ key: 'admin', path: target }} />
}

function OrganizationAdminStartCard(): ReactElement | null {
  const admission = useOrganizationAdminAdmission()
  if (admission.status !== 'allowed') {
    return null
  }
  return (
    <StartCardView card={{ key: 'organizationAdmin', path: admission.target }} />
  )
}

function CreateOrganizationStartCard(): ReactElement | null {
  const offered = useHasRoute(ORGANIZATIONS_NEW_PATH)
  const snapshot = useAuthState()
  if (
    !offered ||
    snapshot.state !== 'authenticated' ||
    snapshot.impersonating
  ) {
    return null
  }
  return (
    <StartCardView
      card={{ key: 'createOrganization', path: ORGANIZATIONS_NEW_PATH }}
    />
  )
}

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
