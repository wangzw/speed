/**
 * The app's starting page: the tenant area's landing (route '/').
 *
 * It is the skeleton's front door, not a business surface. The skeleton
 * carries no business domain, so there is no data to render -- what the page
 * shows instead is the platform already assembled around it: one card per
 * capability that works out of the box (the account settings, the platform
 * console, the object store, the notification centre), each a real
 * destination into the app, over a short note on where a product's own pages
 * belong.
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
 * One capability the start page offers: the demo-namespace key pair that
 * names it and the path it opens. The list is the skeleton's assembled
 * capabilities, each a route the app already serves.
 */
interface StartCard {
  /** The `home.cards.<key>` prefix its title and description resolve from. */
  readonly key: 'account' | 'console' | 'objects' | 'notifications'
  /** The route the card opens. */
  readonly path: string
}

const START_CARDS: readonly StartCard[] = [
  { key: 'account', path: ACCOUNT_PATH },
  { key: 'console', path: CONSOLE_PREFIX },
  { key: 'objects', path: OBJECTS_PATH },
  { key: 'notifications', path: NOTIFICATIONS_PATH },
]

/** The app's starting page: the platform's capabilities as a card grid. */
export function HomePage(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const navigate = useNavigate()
  const open = (path: string) => (event: MouseEvent<HTMLElement>): void => {
    event.preventDefault()
    navigate(path)
  }
  return (
    <Box
      component="section"
      sx={{ display: 'flex', flexDirection: 'column', gap: 4 }}
    >
      <PageHeader title={t('home.heading')} description={t('home.intro')} />
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: { xs: '1fr', sm: 'repeat(2, 1fr)' },
          gap: 2,
        }}
      >
        {START_CARDS.map((card) => (
          <Card key={card.key} variant="outlined">
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
        ))}
      </Box>
      <Typography variant="body2" color="text.secondary">
        {t('home.grow')}
      </Typography>
    </Box>
  )
}
