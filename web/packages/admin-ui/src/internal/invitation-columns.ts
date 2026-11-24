import {
  LONG_TEXT_COLUMN_MAX_WIDTH,
  PRIMARY_COLUMN_MAX_WIDTH,
} from '@speed/ui-kit'

export const INVITATION_IDENTITY_MAX_WIDTH = {
  email: {
    belowMd: 120,
    fromMd: PRIMARY_COLUMN_MAX_WIDTH.fromMd,
  },
  invitedBy: {
    belowMd: 120,
    fromMd: LONG_TEXT_COLUMN_MAX_WIDTH,
  },
} as const
