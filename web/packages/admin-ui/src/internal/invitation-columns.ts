/**
 * The content caps the invitation table's two nowrap identity columns take.
 *
 * The email column shares the primary identity cap and the inviter the
 * long-text cap; both stay on one line, so each contributes its whole cap to
 * the row's unbreakable width. At the 600px `sm` floor those two shared caps
 * (160px each) together with the status, the two timestamps and the actions
 * column exceed the floor. Two facts widen the worst case: the status column
 * has no cap of its own and "Accepted" is the longest label, and a pending row
 * that was just issued or resent carries a third action, the copy-link
 * control, which widens the actions column.
 *
 * With both identity caps at 120px below `md` each table holds the widest
 * pending row inside the floor with headroom, and the complete values remain
 * reachable through the title and the copy control. From `md` up each column
 * returns to its shared cap: 200px for the email, 160px for the inviter.
 * Both management invitation tables use this pair, so their narrow-tier
 * layout cannot diverge.
 */
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
