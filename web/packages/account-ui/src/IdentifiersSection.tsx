/**
 * IdentifiersSection: the identifier surface of the account page, the sign-in
 * ways and the contact addresses.
 */

import { Fragment } from 'react'
import type { FormEvent, ReactElement } from 'react'
import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Chip from '@mui/material/Chip'
import FormControlLabel from '@mui/material/FormControlLabel'
import MenuItem from '@mui/material/MenuItem'
import Radio from '@mui/material/Radio'
import Table from '@mui/material/Table'
import TableBody from '@mui/material/TableBody'
import TableCell from '@mui/material/TableCell'
import TableRow from '@mui/material/TableRow'
import Select from '@mui/material/Select'
import Skeleton from '@mui/material/Skeleton'
import Stack from '@mui/material/Stack'
import Switch from '@mui/material/Switch'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  getAuthnSessionListQueryKey,
  useAuthnLoginAddIdentifier,
  useAuthnLoginRemoveIdentifier,
  useAuthnLoginUpdateIdentifier,
  useAuthnLoginVerifyIdentifier,
} from '@speed/api-sdk'
import type { AuthnProfileIdentifier } from '@speed/api-sdk'
import type { AuthSession, AuthnHumanGate } from '@speed/auth-core'
import { humanGateNeedsProof, stepUpReasonOf, useStepUpGate, useHumanVerification } from '@speed/auth-core'
import {
  ActionButton,
  ActionRow,
  AnnouncementRegion,
  ConfirmDialog,
  errorCodeOf,
  FORM_MAX_WIDTH,
  HumanVerification,
  ListSkeleton,
  parseEmailAddress,
  SectionCard,
  useActivationGuard,
  useInertSelectMenu,
  useWriteFeedback,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { runIdentityTransition } from './internal/identity-transition.js'
import { useProfileQuery } from './internal/queries.js'
import { retryAfterMsOf } from './internal/retry-after.js'
import {
  rememberStepUpResume,
  takeStepUpResume,
} from './internal/step-up-resume.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

/**
 * The host supplies the session, the axis this occurrence manages and which
 * add kinds to offer.
 */
export interface IdentifiersSectionProps {
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
  /** Whether the add surface offers the email kind. Defaults to true. */
  readonly emailAvailable?: boolean
  /** Whether the add surface offers the username kind. Defaults to true; an
   * account that already lists a username never offers it. */
  readonly offerUsername?: boolean
  /** The deployment's human-verification gate, when the host already read the
   * discovery document; omitted, the section reads it itself. */
  readonly humanGate?: AuthnHumanGate
  /** The host's current path, sent as the step-up flow's `return_to` and the
   * page a re-login returns to. */
  readonly returnPath?: string
  /** Which axis this occurrence manages: 'both' (the default), 'signIn'
   * (no contact controls or list) or 'contact' (no sign-in switch or list). */
  readonly axis?: 'both' | 'signIn' | 'contact'
  /** Whether the section renders its own add entry. */
  readonly showAddButton?: boolean
  /** A counter the host increments to open the add form. */
  readonly openAddSignal?: number
}

type RowActionKey = 'verify' | 'defaultContact' | 'remove'

interface ContactActionsProps {
  readonly canVerify: boolean
  readonly verifyOpen?: boolean
  readonly verifyOpenReason?: string
  readonly canDefault?: boolean
  readonly isDefault?: boolean
  readonly canRemove?: boolean
  readonly busyAction?: RowActionKey
  readonly scopeUnavailable?: boolean
  readonly onVerify: () => void
  readonly onSetDefault?: () => void
  readonly onRemove: () => void
  readonly onVerifyRef?: (node: HTMLButtonElement | null) => void
  readonly onDefaultRef?: (node: HTMLButtonElement | null) => void
  readonly onRemoveRef?: (node: HTMLButtonElement | null) => void
}

interface RowAction {
  readonly key: RowActionKey
  readonly label: string
  readonly busy: boolean
  readonly unavailable: boolean
  readonly unavailableReason?: string
  readonly tone?: 'danger'
  readonly onClick: () => void
  readonly refCallback?: (node: HTMLButtonElement | null) => void
}

function ContactActions({
  canVerify,
  verifyOpen = false,
  verifyOpenReason,
  canDefault = false,
  isDefault = false,
  canRemove = true,
  busyAction,
  scopeUnavailable = false,
  onVerify,
  onSetDefault,
  onRemove,
  onVerifyRef,
  onDefaultRef,
  onRemoveRef,
}: ContactActionsProps): ReactElement | null {
  const { t } = useAccountUiTranslation()
  const actions: RowAction[] = []
  if (canVerify) {
    actions.push({
      key: 'verify',
      label: t('identifiers.verify'),
      busy: false,
      unavailable: scopeUnavailable || verifyOpen,
      ...(verifyOpen && verifyOpenReason !== undefined
        ? { unavailableReason: verifyOpenReason }
        : {}),
      onClick: onVerify,
      ...(onVerifyRef === undefined ? {} : { refCallback: onVerifyRef }),
    })
  }
  if (canDefault && onSetDefault !== undefined && !isDefault) {
    actions.push({
      key: 'defaultContact',
      label: t('identifiers.axes.defaultContact'),
      busy: busyAction === 'defaultContact',
      unavailable: scopeUnavailable && busyAction !== 'defaultContact',
      onClick: onSetDefault,
      ...(onDefaultRef === undefined ? {} : { refCallback: onDefaultRef }),
    })
  }
  if (canRemove) {
    actions.push({
      key: 'remove',
      label: t('identifiers.remove'),
      busy: busyAction === 'remove',
      unavailable: scopeUnavailable && busyAction !== 'remove',
      tone: 'danger',
      onClick: onRemove,
      ...(onRemoveRef === undefined ? {} : { refCallback: onRemoveRef }),
    })
  }
  if (actions.length === 0) {
    return null
  }
  const button = (action: RowAction) => (
    <ActionButton
      ref={action.refCallback}
      size="small"
      tone={action.tone}
      busy={action.busy}
      unavailable={action.unavailable}
      unavailableReason={action.unavailableReason}
      onClick={action.onClick}
    >
      {action.label}
    </ActionButton>
  )
  if (actions.length === 1) {
    const action = actions[0]
    if (action === undefined) {
      return null
    }
    return button(action)
  }
  return (
    <ActionRow size="small">
      {actions.map((action) => (
        <Fragment key={action.key}>{button(action)}</Fragment>
      ))}
    </ActionRow>
  )
}

interface SignInSwitchProps {
  readonly checked: boolean
  readonly label: string
  readonly describedBy?: string
  readonly busy: boolean
  readonly unavailable: boolean
  readonly onChange: () => void
}

/** `aria-disabled` rather than a native disable, so a keyboard user keeps their place. */
function SignInSwitch({
  checked,
  label,
  describedBy,
  busy,
  unavailable,
  onChange,
}: SignInSwitchProps): ReactElement {
  const guardedChange = useActivationGuard(onChange, { busy, unavailable })
  return (
    <Switch
      checked={checked}
      slotProps={{
        input: {
          'aria-label': label,
          'aria-describedby': describedBy,
          'aria-disabled': busy || unavailable ? 'true' : undefined,
        },
      }}
      onChange={guardedChange}
    />
  )
}

interface DefaultContactChoiceProps {
  readonly checked: boolean
  readonly label: string
  readonly ariaLabel: string
  readonly unavailable: boolean
  readonly unavailableReason?: string
  readonly onChange: () => void
}

function DefaultContactChoice({
  checked,
  label,
  ariaLabel,
  unavailable,
  unavailableReason,
  onChange,
}: DefaultContactChoiceProps): ReactElement {
  const reasonId = useId()
  const describedBy =
    unavailable && unavailableReason !== undefined ? reasonId : undefined
  const guardedChange = useActivationGuard(onChange, {
    busy: false,
    unavailable,
  })
  return (
    <>
      <FormControlLabel
        control={
          <Radio
            size="small"
            checked={checked}
            slotProps={{
              input: {
                name: 'authn-default-contact',
                'aria-label': ariaLabel,
                'aria-disabled': unavailable ? 'true' : undefined,
                'aria-describedby': describedBy,
              },
            }}
            onChange={guardedChange}
          />
        }
        label={label}
      />
      {describedBy === undefined ? null : (
        <Box component="span" id={reasonId} sx={visuallyHiddenSx}>
          {unavailableReason}
        </Box>
      )}
    </>
  )
}

interface EmailSignInSelectProps {
  readonly value: string
  readonly readOnly: boolean
  readonly label: string
  readonly options: readonly AuthnProfileIdentifier[]
  readonly onSelect: (value: string) => void
}

/** The email group's sign-in select; its own hook instance keeps the page
 * inert while its menu is open. */
function EmailSignInSelect({
  value,
  readOnly,
  label,
  options,
  onSelect,
}: EmailSignInSelectProps): ReactElement {
  const menu = useInertSelectMenu()
  return (
    <Select
      {...menu}
      size="small"
      value={value}
      // Read-only rather than disabled: MUI reports aria-readonly on
      // the combobox and ignores activation while keeping focus.
      readOnly={readOnly}
      slotProps={{
        input: {
          'aria-label': label,
        },
      }}
      onChange={(event) => {
        onSelect(event.target.value)
      }}
      sx={{ mt: 1, minWidth: '18rem' }}
    >
      {options.map((row) => (
        <MenuItem key={row.id ?? row.value} value={row.value ?? ''}>
          {row.value ?? ''}
        </MenuItem>
      ))}
    </Select>
  )
}

const KNOWN_IDENTIFIER_KINDS = new Set<string>([
  'email',
  'phone',
  'username',
])

type AddKind = 'email' | 'username'

type PendingSend =
  | { readonly step: 'add'; readonly value: string }
  | { readonly step: 'row'; readonly address: string }

type AxisChange = {
  readonly signIn: boolean
}

type SignInSelectionChange = {
  readonly id: string
  readonly value: string
  readonly signIn: boolean
}

type IdentifierGroup = 'signIn' | 'contact'

type PendingUpdate =
  | {
      readonly kind: 'flags'
      readonly id: string
      readonly via: 'emailSwitch' | 'phoneSwitch'
    }
  | { readonly kind: 'select' }
  | { readonly kind: 'default'; readonly id: string }

interface LandingRecord {
  readonly id: string
  readonly order: readonly string[]
  readonly control: HTMLElement | null
}

type SectionAction =
  | { readonly step: 'remove'; readonly id: string }
  | { readonly step: 'add'; readonly kind: AddKind; readonly value: string }
  | {
      readonly step: 'flags'
      readonly id: string
      readonly value: string
      readonly change: AxisChange
    }
  | { readonly step: 'defaultContact'; readonly id: string; readonly value: string }
  | { readonly step: 'verifyCode'; readonly code: string }
  | { readonly step: 'rowVerifyCode'; readonly code: string }
  | {
      readonly step: 'selectSignInEmail'
      readonly changes: readonly SignInSelectionChange[]
    }

const ROW_RESEND_NOTE_ID = 'identifiers-row-resend-note'

const RESUME_SCOPE = 'identifiers'

// Resumable only when the step carries no one-time code or typed address.
function resumableAction(value: unknown): SectionAction | null {
  if (typeof value !== 'object' || value === null) {
    return null
  }
  const action = value as {
    readonly step?: unknown
    readonly id?: unknown
    readonly value?: unknown
    readonly change?: unknown
    readonly changes?: unknown
  }
  switch (action.step) {
    case 'remove':
      return typeof action.id === 'string'
        ? { step: 'remove', id: action.id }
        : null
    case 'defaultContact':
      return typeof action.id === 'string' && typeof action.value === 'string'
        ? { step: 'defaultContact', id: action.id, value: action.value }
        : null
    case 'flags': {
      const change = action.change
      if (
        typeof action.id !== 'string' ||
        typeof action.value !== 'string' ||
        typeof change !== 'object' ||
        change === null ||
        typeof (change as { readonly signIn?: unknown }).signIn !== 'boolean'
      ) {
        return null
      }
      return {
        step: 'flags',
        id: action.id,
        value: action.value,
        change: { signIn: (change as { readonly signIn: boolean }).signIn },
      }
    }
    case 'selectSignInEmail': {
      if (!Array.isArray(action.changes)) {
        return null
      }
      const changes: SignInSelectionChange[] = []
      for (const entry of action.changes) {
        if (typeof entry !== 'object' || entry === null) {
          return null
        }
        const row = entry as {
          readonly id?: unknown
          readonly value?: unknown
          readonly signIn?: unknown
        }
        if (
          typeof row.id !== 'string' ||
          typeof row.value !== 'string' ||
          typeof row.signIn !== 'boolean'
        ) {
          return null
        }
        changes.push({ id: row.id, value: row.value, signIn: row.signIn })
      }
      return { step: 'selectSignInEmail', changes }
    }
    default:
      return null
  }
}

function focusFellWith(removed: HTMLElement | null): boolean {
  const active = document.activeElement
  if (active === null || active === document.body) {
    return true
  }
  if (!(active instanceof HTMLElement) || !active.isConnected) {
    return true
  }
  return removed !== null && removed.contains(active)
}

function IdentifierListSkeleton({ label }: { readonly label: string }) {
  const widths = [
    { primary: '40%', secondary: '56%' },
    { primary: '30%', secondary: '48%' },
  ]
  return (
    <ListSkeleton
      label={label}
      rows={widths.map((width) => (
        <>
          <Skeleton variant="text" width={width.primary} />
          <Skeleton variant="text" width={width.secondary} />
        </>
      ))}
    />
  )
}

// `sign_in` can be true on an unproved row, so membership follows the server's flag.
function axesOf(row: {
  readonly kind?: string
  readonly sign_in?: boolean
  readonly default_contact?: boolean
}): {
  readonly username: boolean
  readonly signIn: boolean
  readonly defaultContact: boolean
} {
  const username = row.kind === 'username'
  return {
    username,
    signIn: username || row.sign_in === true,
    defaultContact: row.default_contact === true,
  }
}

function isContact(row: { readonly kind?: string }): boolean {
  return row.kind !== 'username'
}

/**
 * The identifiers surface: adds, verifies and removes the account's login
 * identifiers through its own endpoints, each write settling one result
 * region.
 */
export function IdentifiersSection({
  session,
  emailAvailable = true,
  offerUsername = true,
  humanGate,
  returnPath,
  axis = 'both',
  showAddButton = true,
  openAddSignal,
}: IdentifiersSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const removeIdentifier = useAuthnLoginRemoveIdentifier()
  const lastAddSignal = useRef(openAddSignal)
  useEffect(() => {
    if (
      openAddSignal !== undefined &&
      openAddSignal !== lastAddSignal.current
    ) {
      lastAddSignal.current = openAddSignal
      openAdd()
    }
  }, [openAddSignal])
  const addIdentifier = useAuthnLoginAddIdentifier()
  const verifyIdentifier = useAuthnLoginVerifyIdentifier()
  const updateIdentifier = useAuthnLoginUpdateIdentifier()
  const [pendingSend, setPendingSend] = useState<PendingSend | null>(null)
  const human = useHumanVerification(session, {
    scope: 'other',
    enabled: pendingSend !== null,
    ...(humanGate === undefined ? {} : { gate: humanGate }),
  })
  const gateDocument = humanGate ?? human.gate
  const needsProof = humanGateNeedsProof(gateDocument, 'other')
  const proofBlocked = needsProof && human.proof === null

  const [removeTarget, setRemoveTarget] = useState<string | null>(null)
  const [lastDisabledSignInEmail, setLastDisabledSignInEmail] = useState<{
    readonly id: string
    readonly value: string
  } | null>(null)
  const feedback = useWriteFeedback()
  const [failureText, setFailureText] = useState<string | null>(null)

  const [pendingRemoveId, setPendingRemoveId] = useState<string | null>(null)
  const [pendingUpdate, setPendingUpdate] = useState<PendingUpdate | null>(null)
  const removeAttempt = useRef(0)
  const updateAttempt = useRef(0)
  function beginRemove(id: string): () => void {
    const token = removeAttempt.current + 1
    removeAttempt.current = token
    setPendingRemoveId(id)
    return () => {
      if (removeAttempt.current === token) {
        setPendingRemoveId(null)
      }
    }
  }
  function beginUpdate(value: PendingUpdate): () => void {
    const token = updateAttempt.current + 1
    updateAttempt.current = token
    setPendingUpdate(value)
    return () => {
      if (updateAttempt.current === token) {
        setPendingUpdate(null)
      }
    }
  }

  const removeButtons = useRef(new Map<string, HTMLButtonElement>())
  const defaultButtons = useRef(new Map<string, HTMLButtonElement>())
  const verifyButtons = useRef(new Map<string, HTMLButtonElement>())
  const addEntry = useRef<HTMLButtonElement | null>(null)
  const addKindMenu = useInertSelectMenu()
  const addRegion = useRef<HTMLDivElement | null>(null)

  const removalLanding = useRef<LandingRecord | null>(null)
  const defaultLanding = useRef<LandingRecord | null>(null)
  const verifyLanding = useRef<LandingRecord | null>(null)
  const addLanding = useRef<LandingRecord | null>(null)
  const addForm = useRef<HTMLFormElement | null>(null)
  const rowVerifyForm = useRef<HTMLFormElement | null>(null)
  const addLanded = useRef(false)
  const addWasOpen = useRef(false)
  const verifyLanded = useRef(false)
  const sentToBefore = useRef<string | null>(null)

  const [addOpen, setAddOpen] = useState(false)
  const [addReadSettled, setAddReadSettled] = useState(true)
  const [addKind, setAddKind] = useState<AddKind>(
    emailAvailable ? 'email' : 'username',
  )
  const [addValue, setAddValue] = useState('')
  const [sentTo, setSentTo] = useState<string | null>(null)
  const [code, setCode] = useState('')
  const [fieldErrorKey, setFieldErrorKey] = useState<string | null>(null)

  const [rowVerify, setRowVerify] = useState<{
    readonly key: string
    readonly value: string
  } | null>(null)
  const [rowSentTo, setRowSentTo] = useState<string | null>(null)
  const [rowCode, setRowCode] = useState('')
  const [rowCodeInvalid, setRowCodeInvalid] = useState(false)
  const [rowResendUntil, setRowResendUntil] = useState(0)
  const [rowResendFromSuccess, setRowResendFromSuccess] = useState(false)
  const [rowNow, setRowNow] = useState(() => Date.now())

  const addCodeRef = useRef<HTMLInputElement>(null)
  const valueInput = useRef<HTMLInputElement>(null)
  // Focus moves once the attempt settles: the value field is read-only while
  // a write is in flight.
  const focusValuePending = useRef(false)
  useEffect(() => {
    if (sentTo !== null) {
      addCodeRef.current?.focus()
    }
  }, [sentTo])

  const addBusy =
    addIdentifier.isPending || verifyIdentifier.isPending || feedback.pending
  const writeBusy =
    removeIdentifier.isPending || updateIdentifier.isPending || feedback.pending
  const scopeBlocked = writeBusy || addBusy
  useEffect(() => {
    if (addBusy || !focusValuePending.current) {
      return
    }
    focusValuePending.current = false
    valueInput.current?.focus()
  }, [addBusy])

  useEffect(() => {
    if (rowResendUntil <= Date.now()) {
      return undefined
    }
    const timer = setInterval(() => {
      const current = Date.now()
      setRowNow(current)
      if (current >= rowResendUntil) {
        clearInterval(timer)
      }
    }, 1000)
    return () => {
      clearInterval(timer)
    }
  }, [rowResendUntil])

  const gate = useStepUpGate<SectionAction>((action) => {
    switch (action.step) {
      case 'remove':
        void remove(action.id)
        return
      case 'add':
        void add(action.kind, action.value)
        return
      case 'flags': {
        const via =
          (rows ?? []).find((row) => row.id === action.id)?.kind === 'phone'
            ? 'phoneSwitch'
            : 'emailSwitch'
        void setFlags(action.id, action.value, action.change, via)
        return
      }
      case 'defaultContact':
        void setDefaultContact(action.id, action.value)
        return
      case 'verifyCode':
        void verify(action.code)
        return
      case 'rowVerifyCode':
        void rowVerifyCode(action.code)
        return
      case 'selectSignInEmail':
        void selectSignInEmail(action.changes)
        return
    }
  })

  const rememberRelogin = useCallback(() => {
    if (returnPath === undefined) {
      return
    }
    const action = gate.peek()
    if (action !== null && resumableAction(action) !== null) {
      rememberStepUpResume(
        session.getSnapshot().principal?.account_id ?? '',
        RESUME_SCOPE,
        returnPath,
        action,
      )
    }
  }, [returnPath, gate.peek, session])

  const resumed = useRef(false)
  useEffect(() => {
    if (resumed.current || returnPath === undefined || data === undefined) {
      return
    }
    resumed.current = true
    const account = session.getSnapshot().principal?.account_id ?? ''
    const record = takeStepUpResume(account, RESUME_SCOPE)
    if (record === null) {
      return
    }
    if (record.path !== returnPath) {
      rememberStepUpResume(
        record.account,
        record.scope,
        record.path,
        record.operation,
      )
      return
    }
    const action = resumableAction(record.operation)
    if (action === null) {
      return
    }
    gate.arm(action)
    gate.succeed()
  }, [returnPath, data, gate, session])

  const pending = isPending && data === undefined
  const rows = !pending && data !== undefined ? data.identifiers : undefined
  // The server still judges the last way in on the write itself; this is the
  // visible half of the same rule.
  const passwordSet = data?.password_set === true
  const loginNameCount =
    rows === undefined
      ? 0
      : rows.filter(
          (row) => row.kind === 'username' || (row.kind === 'email' && row.sign_in),
        ).length
  const usernamePresent =
    rows !== undefined && rows.some((row) => row.kind === 'username')
  const verificationPending = data?.verification?.state === 'pending'
  const rowVerifyAvailable = emailAvailable && !verificationPending
  const rowRemainingSeconds = Math.max(
    0,
    Math.ceil((rowResendUntil - rowNow) / 1000),
  )
  const addKinds: AddKind[] = []
  if (emailAvailable && !verificationPending) {
    addKinds.push('email')
  }
  if (offerUsername && !usernamePresent) {
    addKinds.push('username')
  }
  const formKind: AddKind = addKinds.includes(addKind)
    ? addKind
    : (addKinds[0] ?? 'username')
  const addFaceReplacesList =
    axis === 'contact' && addOpen && addKinds.length > 0
  const showHeader = pending || (rows !== undefined && rows.length > 0)

  function visibleIds(all: readonly AuthnProfileIdentifier[]): string[] {
    const visible =
      axis === 'contact'
        ? all.filter((row) => (row.kind ?? '') === 'email')
        : all.filter(isContact)
    return visible
      .filter((row) => row.id != null)
      .map((row) => row.id as string)
  }

  function visibleVerifyKeys(
    all: readonly AuthnProfileIdentifier[],
  ): string[] {
    const visible =
      axis === 'contact'
        ? all.filter((row) => (row.kind ?? '') === 'email')
        : all.filter(isContact)
    return visible.map((row, index) => `contact:${row.id ?? index}`)
  }

  useLayoutEffect(() => {
    const target = removalLanding.current
    if (target === null) {
      return
    }
    const remaining = new Set(visibleIds(rows ?? []))
    if (remaining.has(target.id)) {
      return
    }
    removalLanding.current = null
    if (!focusFellWith(target.control)) {
      return
    }
    const index = target.order.indexOf(target.id)
    const candidates =
      index === -1
        ? [...target.order]
        : [
            ...target.order.slice(index + 1),
            ...target.order.slice(0, index).reverse(),
          ]
    for (const id of candidates) {
      if (!remaining.has(id)) {
        continue
      }
      const button = removeButtons.current.get(id)
      if (button !== undefined) {
        button.focus()
        return
      }
    }
    addRegion.current?.focus()
  }, [rows])

  useLayoutEffect(() => {
    const target = defaultLanding.current
    if (target === null) {
      return
    }
    const settled = (rows ?? []).find((row) => row.id === target.id)
    if (settled === undefined) {
      defaultLanding.current = null
      return
    }
    if (settled.default_contact !== true) {
      return
    }
    defaultLanding.current = null
    if (!focusFellWith(target.control)) {
      return
    }
    const index = target.order.indexOf(target.id)
    const candidates =
      index === -1
        ? [...target.order]
        : [
            ...target.order.slice(index + 1),
            ...target.order.slice(0, index).reverse(),
          ]
    for (const id of candidates) {
      const button = defaultButtons.current.get(id)
      if (button !== undefined && button.isConnected) {
        button.focus()
        return
      }
    }
    addRegion.current?.focus()
  }, [rows])

  useLayoutEffect(() => {
    if (!verifyLanded.current || rowVerify !== null) {
      return
    }
    verifyLanded.current = false
    const target = verifyLanding.current
    verifyLanding.current = null
    if (target === null) {
      return
    }
    if (!focusFellWith(target.control)) {
      return
    }
    const index = target.order.indexOf(target.id)
    const candidates =
      index === -1
        ? [...target.order]
        : [
            ...target.order.slice(index + 1),
            ...target.order.slice(0, index).reverse(),
          ]
    for (const key of candidates) {
      if (key === target.id) {
        continue
      }
      const button = verifyButtons.current.get(key)
      if (button !== undefined && button.isConnected) {
        button.focus()
        return
      }
    }
    addRegion.current?.focus()
  }, [rowVerify])

  useLayoutEffect(() => {
    if (!addLanded.current || addOpen || !addReadSettled) {
      return
    }
    addLanded.current = false
    const target = addLanding.current
    addLanding.current = null
    if (target === null) {
      return
    }
    if (!focusFellWith(target.control)) {
      return
    }
    const entry = addEntry.current
    if (entry !== null && entry.isConnected) {
      entry.focus()
      return
    }
    addRegion.current?.focus()
  }, [addOpen, addReadSettled])

  useLayoutEffect(() => {
    if (!addOpen) {
      addWasOpen.current = false
      return
    }
    if (addWasOpen.current) {
      return
    }
    addWasOpen.current = true
    if (!focusFellWith(addEntry.current)) {
      return
    }
    const first = addForm.current?.querySelector<HTMLElement>(
      '[role="combobox"], input:not([type="hidden"]), textarea',
    )
    first?.focus()
  }, [addOpen])

  useLayoutEffect(() => {
    const previous = sentToBefore.current
    sentToBefore.current = sentTo
    if (previous === null || sentTo !== null) {
      return
    }
    const active = document.activeElement
    if (active !== null && active !== document.body) {
      return
    }
    valueInput.current?.focus()
  }, [sentTo])

  async function invalidateProfile(): Promise<void> {
    await queryClient.invalidateQueries({ queryKey: getAuthnProfileGetQueryKey() })
  }

  async function invalidateAfterWrite(): Promise<void> {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: getAuthnProfileGetQueryKey() }),
      queryClient.invalidateQueries({
        queryKey: getAuthnSessionListQueryKey(),
      }),
    ])
  }

  function clearFailure(): void {
    feedback.reset()
    setFailureText(null)
  }

  function openAdd(): void {
    closeRowVerify()
    addLanded.current = false
    addLanding.current = null
    setAddReadSettled(true)
    setFieldErrorKey(null)
    setAddOpen(true)
  }

  function closeAdd(): void {
    setAddOpen(false)
    setPendingSend(null)
    setSentTo(null)
    setAddValue('')
    setCode('')
    setFieldErrorKey(null)
    clearFailure()
  }

  function remove(id: string): Promise<void> {
    const identifierValue = rows?.find((row) => row.id === id)?.value ?? ''
    const control = removeButtons.current.get(id) ?? null
    if (control !== null) {
      removalLanding.current = {
        id,
        order: visibleIds(rows ?? []),
        control,
      }
    }
    const done = beginRemove(id)
    return feedback.run(async () => {
      setFailureText(null)
      try {
        await removeIdentifier.mutateAsync({ id })
      } catch (error) {
        removalLanding.current = null
        const failure = errorCodeOf(error)
        const reason = stepUpReasonOf(failure)
        if (reason !== null) {
          gate.arm({ step: 'remove', id }, reason)
          return null
        }
        await invalidateProfile()
        throw error
      }
      await invalidateProfile()
      return t('identifiers.removed', {
        value: identifierValue,
        interpolation: { escapeValue: false },
      })
    }).finally(done)
  }

  // Adding a value to sign-in rotates the credential and runs inside the
  // identity-transition window; removing it does not.
  function setFlags(
    id: string,
    value: string,
    change: AxisChange,
    via: 'emailSwitch' | 'phoneSwitch',
  ): Promise<void> {
    const done = beginUpdate({ kind: 'flags', id, via })
    return feedback.run(async () => {
      setFailureText(null)
      const write = () =>
        updateIdentifier.mutateAsync({
          id,
          data: { sign_in: change.signIn },
        })
      try {
        if (change.signIn) {
          await runIdentityTransition(session, write, invalidateAfterWrite)
        } else {
          await write()
          await invalidateProfile()
        }
      } catch (error) {
        const reason = stepUpReasonOf(errorCodeOf(error))
        if (reason !== null) {
          gate.arm({ step: 'flags', id, value, change }, reason)
          return null
        }
        await invalidateProfile()
        throw error
      }
      return t('identifiers.axes.signInChanged', {
        value,
        interpolation: { escapeValue: false },
      })
    }).finally(done)
  }

  // The chosen address is written first, inside the transition window; the
  // ones it replaces are then turned off.
  function selectSignInEmail(
    changes: readonly SignInSelectionChange[],
  ): Promise<void> {
    const chosen = changes.find((change) => change.signIn)
    const done = beginUpdate({ kind: 'select' })
    return feedback.run(async () => {
      setFailureText(null)
      const write = async (): Promise<void> => {
        if (chosen !== undefined) {
          await updateIdentifier.mutateAsync({
            id: chosen.id,
            data: { sign_in: true },
          })
        }
        for (const change of changes) {
          if (change.signIn) {
            continue
          }
          await updateIdentifier.mutateAsync({
            id: change.id,
            data: { sign_in: false },
          })
        }
      }
      try {
        if (chosen !== undefined) {
          await runIdentityTransition(session, write, invalidateAfterWrite)
        } else {
          await write()
          await invalidateProfile()
        }
      } catch (error) {
        const reason = stepUpReasonOf(errorCodeOf(error))
        if (reason !== null) {
          gate.arm({ step: 'selectSignInEmail', changes }, reason)
          return null
        }
        await invalidateProfile()
        throw error
      }
      return t('identifiers.axes.signInChanged', {
        value: chosen?.value ?? changes[0]?.value ?? '',
        interpolation: { escapeValue: false },
      })
    }).finally(done)
  }

  function setDefaultContact(id: string, value: string): Promise<void> {
    const control = defaultButtons.current.get(id) ?? null
    if (control !== null) {
      defaultLanding.current = {
        id,
        order: visibleIds(rows ?? []),
        control,
      }
    }
    const done = beginUpdate({ kind: 'default', id })
    return feedback.run(async () => {
      setFailureText(null)
      try {
        await updateIdentifier.mutateAsync({ id, data: { default_contact: true } })
      } catch (error) {
        defaultLanding.current = null
        const reason = stepUpReasonOf(errorCodeOf(error))
        if (reason !== null) {
          gate.arm({ step: 'defaultContact', id, value }, reason)
          return null
        }
        await invalidateProfile()
        throw error
      }
      await invalidateProfile()
      return t('identifiers.axes.defaultChanged', {
        value,
        interpolation: { escapeValue: false },
      })
    }).finally(done)
  }

  function add(kind: AddKind, value: string): Promise<void> {
    if (kind === 'email' && proofBlocked) {
      return Promise.resolve()
    }
    setFieldErrorKey(null)
    if (addOpen) {
      addLanding.current = { id: '', order: [], control: addForm.current }
    }
    return feedback.run(async () => {
      setFailureText(null)
      const presented = kind === 'email' && needsProof ? human.proof : null
      async function settleAdded(result: {
        readonly state?: string
      }): Promise<string | null> {
        if (result.state === 'pending') {
          setSentTo(value)
          setCode('')
          return null
        }
        addLanded.current = true
        setAddReadSettled(false)
        closeAdd()
        try {
          await (kind === 'username' ? invalidateAfterWrite() : invalidateProfile())
        } catch {
          // The write already committed, so a failed re-read leaves the stale list.
        } finally {
          setAddReadSettled(true)
        }
        return t('identifiers.add.added')
      }
      try {
        const data =
          presented === null
            ? { kind, value }
            : { kind, value, human_proof: presented }
        // The username write rotates the credential and runs inside the window.
        if (kind === 'username') {
          return await runIdentityTransition(
            session,
            () => addIdentifier.mutateAsync({ data }),
            settleAdded,
          )
        }
        const result = await addIdentifier.mutateAsync({ data })
        if (presented !== null) {
          human.consume()
        }
        return await settleAdded(result)
      } catch (error) {
        const failure = errorCodeOf(error)
        // Only the username write proves the account; the email add is never gated.
        const reason = kind === 'username' ? stepUpReasonOf(failure) : null
        if (reason !== null) {
          gate.arm({ step: 'add', kind, value }, reason)
          return null
        }
        // The proof is single-use: obtain a fresh one after a refusal.
        if (presented !== null) {
          human.consume()
        }
        // An email refusal is the address's, so the field carries it.
        if (
          kind === 'email' &&
          (failure === 'authn.bad_request' || failure === 'authn.email_taken')
        ) {
          setFieldErrorKey(
            failure === 'authn.email_taken'
              ? 'identifiers.add.emailTaken'
              : 'identifiers.add.invalidEmail',
          )
          return null
        }
        if (
          kind === 'username' &&
          (failure === 'authn.username_taken' || failure === 'authn.conflict')
        ) {
          setFieldErrorKey('identifiers.add.usernameTaken')
          focusValuePending.current = true
          return null
        }
        if (
          kind === 'username' &&
          (failure === 'authn.username_reserved' ||
            failure === 'authn.username_invalid')
        ) {
          setFieldErrorKey(
            failure === 'authn.username_reserved'
              ? 'identifiers.add.usernameReserved'
              : 'identifiers.add.usernameInvalid',
          )
          focusValuePending.current = true
          return null
        }
        throw error
      }
    })
  }

  function verify(nextCode: string): Promise<void> {
    if (addOpen) {
      addLanding.current = { id: '', order: [], control: addForm.current }
    }
    return feedback.run(async () => {
      setFailureText(null)
      try {
        await verifyIdentifier.mutateAsync({ data: { code: nextCode } })
        addLanded.current = true
        await invalidateProfile()
        closeAdd()
      } catch (error) {
        const failure = errorCodeOf(error)
        if (failure === 'authn.email_taken') {
          setSentTo(null)
          setCode('')
          setFieldErrorKey('identifiers.add.emailTaken')
          return null
        }
        const reason = stepUpReasonOf(failure)
        if (reason !== null) {
          gate.arm({ step: 'verifyCode', code: nextCode }, reason)
          return null
        }
        throw error
      }
      return t('identifiers.add.added')
    })
  }

  function openRowVerify(key: string, value: string): void {
    closeAdd()
    setRowCodeInvalid(false)
    setRowCode('')
    setRowSentTo(null)
    setRowResendUntil(0)
    setRowResendFromSuccess(false)
    setRowVerify({ key, value })
  }

  function closeRowVerify(): void {
    setPendingSend(null)
    setRowVerify(null)
    setRowSentTo(null)
    setRowCode('')
    setRowCodeInvalid(false)
    setRowResendUntil(0)
    setRowResendFromSuccess(false)
    clearFailure()
  }

  // The route accepts the account's own address as a re-add of its proof.
  function rowSend(address: string): Promise<void> {
    if (proofBlocked) {
      return Promise.resolve()
    }
    setRowCodeInvalid(false)
    return feedback.run(async () => {
      setFailureText(null)
      const presented = needsProof ? human.proof : null
      try {
        const result = await addIdentifier.mutateAsync({
          data:
            presented === null
              ? { kind: 'email', value: address }
              : { kind: 'email', value: address, human_proof: presented },
        })
        if (presented !== null) {
          human.consume()
        }
        const interval =
          typeof result.resend_after === 'number' ? result.resend_after : 0
        const held = interval > 0
        setRowResendUntil(held ? Date.now() + interval * 1000 : 0)
        setRowResendFromSuccess(held)
        setRowNow(Date.now())
        setRowSentTo(address)
        setRowCode('')
        return null
      } catch (error) {
        if (presented !== null) {
          human.consume()
        }
        const retryAfter = retryAfterMsOf(error)
        if (retryAfter !== undefined) {
          setRowResendUntil(Date.now() + retryAfter)
          setRowResendFromSuccess(false)
          setRowNow(Date.now())
        }
        throw error
      }
    })
  }

  const addRef = useRef(add)
  addRef.current = add
  const rowSendRef = useRef(rowSend)
  rowSendRef.current = rowSend

  function askAddEmail(value: string): void {
    if (pendingSend !== null) {
      return
    }
    clearFailure()
    setFieldErrorKey(null)
    if (
      !human.pending &&
      gateDocument !== null &&
      (!needsProof || human.proof !== null)
    ) {
      void add('email', value)
      return
    }
    setPendingSend({ step: 'add', value })
  }

  function askRowSend(address: string): void {
    if (pendingSend !== null) {
      return
    }
    clearFailure()
    setRowCodeInvalid(false)
    if (
      !human.pending &&
      gateDocument !== null &&
      (!needsProof || human.proof !== null)
    ) {
      void rowSend(address)
      return
    }
    setPendingSend({ step: 'row', address })
  }

  useEffect(() => {
    if (pendingSend === null || human.pending || gateDocument === null) {
      return
    }
    if (needsProof && human.proof === null) {
      return
    }
    if (writeBusy) {
      return
    }
    const send = pendingSend
    setPendingSend(null)
    if (send.step === 'add') {
      void addRef.current('email', send.value)
      return
    }
    void rowSendRef.current(send.address)
  }, [
    pendingSend,
    human.pending,
    gateDocument,
    needsProof,
    human.proof,
    writeBusy,
  ])

  function rowVerifyCode(nextCode: string): Promise<void> {
    // A late token must not send a second code that invalidates this one.
    setPendingSend(null)
    const address = rowSentTo ?? rowVerify?.value ?? ''
    setRowCodeInvalid(false)
    const openKey = rowVerify?.key
    if (openKey !== undefined) {
      verifyLanding.current = {
        id: openKey,
        order: visibleVerifyKeys(rows ?? []),
        control: rowVerifyForm.current,
      }
    }
    return feedback.run(async () => {
      setFailureText(null)
      try {
        await verifyIdentifier.mutateAsync({ data: { code: nextCode } })
        verifyLanded.current = true
        setRowVerify(null)
        setRowSentTo(null)
        setRowCode('')
        setRowResendUntil(0)
        setRowResendFromSuccess(false)
        await invalidateProfile()
      } catch (error) {
        const failure = errorCodeOf(error)
        if (failure === 'authn.login_failed') {
          setRowCodeInvalid(true)
          setRowCode('')
          return null
        }
        if (failure === 'authn.email_taken') {
          setFailureText('verification.addressTaken')
          return null
        }
        const reason = stepUpReasonOf(failure)
        if (reason !== null) {
          gate.arm({ step: 'rowVerifyCode', code: nextCode }, reason)
          return null
        }
        throw error
      }
      return t('verification.verifiedDescription', {
        email: address,
        interpolation: { escapeValue: false },
      })
    })
  }

  function submitAdd(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault()
    if (sentTo !== null) {
      void verify(code)
      return
    }
    if (axis === 'contact' || formKind === 'email') {
      const address = parseEmailAddress(addValue)
      if (address === null) {
        clearFailure()
        setFieldErrorKey('identifiers.add.invalidEmail')
        return
      }
      askAddEmail(address)
      return
    }
    const username = addValue.trim()
    if (username !== '') {
      void add('username', username)
    }
  }

  function submitRowVerify(event: FormEvent<HTMLFormElement>): void {
    event.preventDefault()
    if (rowSentTo === null) {
      askRowSend(rowVerify?.value ?? '')
      return
    }
    if (rowCode.trim() !== '') {
      void rowVerifyCode(rowCode)
    }
  }

  const guardedAddSubmit = useActivationGuard(submitAdd, {
    busy: addBusy || pendingSend !== null,
    unavailable:
      writeBusy ||
      (sentTo === null ? addValue.trim() === '' : code.trim() === ''),
  })
  const guardedRowVerifySubmit = useActivationGuard(submitRowVerify, {
    busy: addBusy,
    unavailable:
      writeBusy || (rowSentTo !== null && rowCode.trim() === ''),
  })

  const addSendInFlight =
    (pendingSend !== null && pendingSend.step === 'add') ||
    (addIdentifier.isPending && addOpen)
  const addVerifyInFlight = verifyIdentifier.isPending && addOpen
  const addValueMissing = addValue.trim() === ''
  const addValueMissingReason =
    axis === 'contact' || formKind === 'email'
      ? t('identifiers.add.emailEmpty')
      : t('identifiers.add.usernameEmpty')

  const valueLabel =
    formKind === 'email'
      ? t('identifiers.add.value.labelEmail')
      : t('identifiers.add.value.labelUsername')
  const valueHelper =
    formKind === 'email'
      ? t('identifiers.add.value.helperEmail')
      : t('identifiers.add.value.helperUsername')

  const humanBlock = (
    <HumanVerification
      scope="other"
      gate={human.gate ?? humanGate}
      proof={human.proof}
      status={human.status}
      captcha={human.captcha}
      onToken={human.adoptToken}
      onRetry={human.retry}
      onRefreshCaptcha={human.refreshCaptcha}
      onRequestAudio={human.requestAudio}
      audioRetryAfterSeconds={human.audioRetryAfterSeconds}
      disabled={addBusy}
    />
  )

  interface GroupSpec {
    readonly key: IdentifierGroup
    readonly kind?: string
    readonly title: string
    readonly empty: string
  }
  const signInKindSpecs: readonly GroupSpec[] = [
    {
      key: 'signIn',
      kind: 'username',
      title: 'identifiers.kind.username',
      empty: 'identifiers.groups.signInEmpty',
    },
    {
      key: 'signIn',
      kind: 'email',
      title: 'identifiers.kind.email',
      empty: 'identifiers.groups.signInEmpty',
    },
    {
      key: 'signIn',
      kind: 'phone',
      title: 'identifiers.kind.phone',
      empty: 'identifiers.groups.signInEmpty',
    },
  ]
  const groups: readonly GroupSpec[] =
    axis === 'signIn'
      ? signInKindSpecs
      : axis === 'contact'
        ? [
            {
              key: 'contact',
              title: 'identifiers.groups.contact',
              empty: 'identifiers.groups.contactEmpty',
            },
          ]
        : [
            {
              key: 'signIn',
              title: 'identifiers.groups.signIn',
              empty: 'identifiers.groups.signInEmpty',
            },
            {
              key: 'contact',
              title: 'identifiers.groups.contact',
              empty: 'identifiers.groups.contactEmpty',
            },
          ]
  const groupIdBase = useId()

  function renderSignInKind(
    spec: GroupSpec,
    allRows: readonly AuthnProfileIdentifier[],
  ) {
    const kind = spec.kind ?? ''
    const headingId = `${groupIdBase}-${kind}`
    const kindRows = allRows.filter((row) => (row.kind ?? '') === kind)
    const signInRow =
      kindRows.find((row) => row.sign_in === true) ??
      kindRows.find((row) => row.proven === true) ??
      kindRows[0] ??
      null
    const usernameRow = kind === 'username' ? (kindRows[0] ?? null) : null
    const emailSignInRow =
      kind === 'email'
        ? (kindRows.find(
            (row) => row.id != null && row.sign_in === true,
          ) ?? null)
        : null
    const provenEmails =
      kind === 'email'
        ? kindRows.filter((row) => row.proven === true && row.id != null)
        : []
    const emailOptions =
      kind === 'email'
        ? kindRows.filter(
            (row) => row.proven === true || row.sign_in === true,
          )
        : []
    const named = axis === 'signIn'
    return (
      <Box
        key={kind}
        role="group"
        aria-labelledby={named ? headingId : undefined}
        aria-label={named ? undefined : t(spec.title)}
        aria-describedby={
          kind === 'email' && emailSignInRow === null
            ? `${headingId}-off`
            : undefined
        }
      >
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 1.5,
          }}
        >
          {named ? (
            <Typography
              id={headingId}
              component="h3"
              variant="body1"
              sx={{ fontWeight: 600 }}
            >
              {t(spec.title)}
            </Typography>
          ) : null}
          {kind === 'username' ? null : kind === 'email' ? (
            <SignInSwitch
              checked={emailSignInRow !== null}
              label={
                emailSignInRow === null
                  ? t('identifiers.action.enable')
                  : t('identifiers.action.disable')
              }
              describedBy={
                emailSignInRow === null ? `${headingId}-off` : undefined
              }
              busy={
                pendingUpdate?.kind === 'flags' &&
                pendingUpdate.via === 'emailSwitch'
              }
              unavailable={
                scopeBlocked ||
                (emailSignInRow === null && provenEmails.length === 0)
              }
              onChange={() => {
                if (emailSignInRow !== null) {
                  setLastDisabledSignInEmail({
                    id: emailSignInRow.id,
                    value: emailSignInRow.value ?? '',
                  })
                  void setFlags(
                    emailSignInRow.id,
                    emailSignInRow.value ?? '',
                    { signIn: false },
                    'emailSwitch',
                  )
                  return
                }
                const remembered =
                  lastDisabledSignInEmail === null
                    ? undefined
                    : provenEmails.find(
                        (row) =>
                          row.id === lastDisabledSignInEmail.id ||
                          (row.value ?? '') ===
                            lastDisabledSignInEmail.value,
                      )
                const target =
                  remembered ??
                  provenEmails.find(
                    (row) => row.default_contact === true,
                  ) ??
                  provenEmails[0]
                if (target === undefined) {
                  return
                }
                setLastDisabledSignInEmail(null)
                void setFlags(target.id, target.value ?? '', {
                  signIn: true,
                }, 'emailSwitch')
              }}
            />
          ) : signInRow?.id != null ? (
            <SignInSwitch
              checked={signInRow.sign_in === true}
              label={
                signInRow.sign_in === true
                  ? t('identifiers.action.disable')
                  : t('identifiers.action.enable')
              }
              busy={
                pendingUpdate?.kind === 'flags' &&
                pendingUpdate.via === 'phoneSwitch'
              }
              unavailable={scopeBlocked}
              onChange={() =>
                void setFlags(
                  signInRow.id as string,
                  signInRow.value ?? '',
                  { signIn: signInRow.sign_in !== true },
                  'phoneSwitch',
                )
              }
            />
          ) : null}
        </Box>
        {kind === 'email' ? (
          emailSignInRow !== null ? (
            <EmailSignInSelect
              value={emailSignInRow.value ?? ''}
              readOnly={scopeBlocked}
              label={t('identifiers.kind.email')}
              options={emailOptions}
              onSelect={(chosen) => {
                // The whole selection is one action, so a step-up replay lands
                // the same end state.
                const changes = kindRows
                  .filter((row) => row.id != null)
                  .map((row) => ({
                    id: row.id as string,
                    value: row.value ?? '',
                    signIn: (row.value ?? '') === chosen,
                  }))
                  .filter((change) => {
                    const row = kindRows.find(
                      (candidate) => candidate.id === change.id,
                    )
                    return (row?.sign_in === true) !== change.signIn
                  })
                if (changes.length > 0) {
                  void selectSignInEmail(changes)
                }
              }}
            />
          ) : (
            <Typography
              id={`${headingId}-off`}
              variant="body2"
              color="text.secondary"
              sx={{ mt: 0.5 }}
            >
              {t('identifiers.usage.emailNone')}
            </Typography>
          )
        ) : (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: 1.5,
              mt: 0.5,
            }}
          >
            <Typography variant="body2" color="text.secondary">
              {signInRow === null || signInRow.value === undefined ? (
                t(
                  kind === 'username'
                    ? 'identifiers.usage.usernameNone'
                    : 'identifiers.usage.phoneNone',
                )
              ) : kind === 'username' || signInRow.sign_in === true ? (
                (() => {
                  const marker = '__VALUE__'
                  const sentence = t('identifiers.usage.loginAs', {
                    kind: t(spec.title),
                    value: marker,
                  })
                  const [before, after = ''] = sentence.split(marker)
                  return (
                    <>
                      {before}
                      <Box
                        component="span"
                        sx={{ color: 'text.primary', fontWeight: 600 }}
                      >
                        {signInRow.value}
                      </Box>
                      {after}
                    </>
                  )
                })()
              ) : (
                t('identifiers.usage.phoneNotSignIn')
              )}
            </Typography>
          </Box>
        )}
        {usernameRow !== null ? (
          <Typography
            variant="body2"
            color="text.secondary"
            sx={{ mt: 0.5 }}
          >
            {t('identifiers.usernamePermanent')}
          </Typography>
        ) : null}
      </Box>
    )
  }

  function renderRowVerifyForm(address: string) {
    const rowSendInFlight =
      (pendingSend !== null && pendingSend.step === 'row') ||
      (addIdentifier.isPending && rowVerify !== null)
    const rowVerifyInFlight = verifyIdentifier.isPending && rowVerify !== null
    const resendCountingDown = rowRemainingSeconds > 0
    const resendUnavailable =
      !rowSendInFlight && (scopeBlocked || resendCountingDown)
    const resendExplanation =
      resendCountingDown && rowResendFromSuccess
        ? { unavailableDescribedBy: ROW_RESEND_NOTE_ID }
        : resendUnavailable && !scopeBlocked && resendCountingDown
          ? { unavailableReason: t('identifiers.resendWaiting') }
          : {}
    return (
      <Box
        component="form"
        ref={rowVerifyForm}
        noValidate
        onSubmit={guardedRowVerifySubmit}
        sx={{ mt: 1.5, maxWidth: FORM_MAX_WIDTH }}
      >
        <Stack spacing={1.5} sx={{ alignItems: 'flex-start' }}>
          <TextField
            fullWidth
            type="email"
            label={t('identifiers.add.value.labelEmail')}
            value={address}
            helperText={t('identifiers.add.value.helperEmail')}
            slotProps={{ htmlInput: { readOnly: true } }}
          />
          {pendingSend !== null && pendingSend.step === 'row'
            ? humanBlock
            : null}
          {rowSentTo === null ? null : (
            <TextField
              fullWidth
              autoFocus
              inputMode="numeric"
              autoComplete="one-time-code"
              label={t('identifiers.add.code.label')}
              value={rowCode}
              error={rowCodeInvalid}
              helperText={
                rowCodeInvalid ? t('identifiers.verifyCodeInvalid') : undefined
              }
              slotProps={{
                htmlInput: {
                  'aria-invalid': rowCodeInvalid,
                  readOnly: addBusy,
                },
              }}
              onChange={(event) => {
                setRowCode(event.target.value)
                if (rowCodeInvalid) {
                  setRowCodeInvalid(false)
                }
              }}
            />
          )}
          <ActionRow size="small" sx={{ width: '100%' }}>
            <ActionButton
              type="button"
              unavailable={scopeBlocked}
              onClick={closeRowVerify}
            >
              {t('identifiers.add.cancel')}
            </ActionButton>
            {rowSentTo === null ? null : (
              <ActionButton
                type="button"
                busy={rowSendInFlight}
                unavailable={resendUnavailable}
                {...resendExplanation}
                onClick={() => askRowSend(address)}
              >
                {t('verification.resend')}
              </ActionButton>
            )}
            {rowSentTo === null ? (
              <ActionButton
                type="submit"
                emphasis="primary"
                busy={rowSendInFlight}
                unavailable={!rowSendInFlight && scopeBlocked}
              >
                {t('identifiers.add.submitEmail')}
              </ActionButton>
            ) : (
              <ActionButton
                type="submit"
                emphasis="primary"
                busy={rowVerifyInFlight}
                unavailable={
                  !rowVerifyInFlight &&
                  (scopeBlocked || rowCode.trim() === '')
                }
                unavailableReason={
                  !rowVerifyInFlight &&
                  !scopeBlocked &&
                  rowCode.trim() === ''
                    ? t('identifiers.add.codeEmpty')
                    : undefined
                }
              >
                {t('identifiers.add.verify')}
              </ActionButton>
            )}
          </ActionRow>
          {rowSentTo !== null &&
          rowRemainingSeconds > 0 &&
          rowResendFromSuccess ? (
            <Typography
              id={ROW_RESEND_NOTE_ID}
              variant="body2"
              color="text.secondary"
            >
              {t('verification.resendIn', {
                seconds: rowRemainingSeconds,
              })}
            </Typography>
          ) : null}
        </Stack>
      </Box>
    )
  }

  function renderContactRow(
    identifier: AuthnProfileIdentifier,
    index: number,
  ) {
    const id = identifier.id ?? null
    const kind = identifier.kind ?? ''
    const kindLabel = KNOWN_IDENTIFIER_KINDS.has(kind)
      ? t(`identifiers.kind.${kind}`)
      : t('identifiers.kind.other')
    const value = identifier.value ?? ''
    // `verified_at` is present on every identifier, so `proven` alone answers ownership.
    const verified = identifier.proven === true
    const stateLabel = verified
      ? t('identifiers.verified')
      : t('identifiers.unverified')
    const showChip = kind !== 'username'
    const axes = axesOf(identifier)
    const rowKey = `contact:${id ?? index}`
    const canVerifyRow = rowVerifyAvailable && kind === 'email' && !verified
    const rowFlowOpen = canVerifyRow && rowVerify?.key === rowKey
    const lastLoginName =
      passwordSet &&
      loginNameCount === 1 &&
      kind === 'email' &&
      axes.signIn
    const canRemoveRow = id !== null && !lastLoginName
    const showActionsMenu = canVerifyRow || canRemoveRow
    const scopeUnavailable = scopeBlocked
    const busyAction: RowActionKey | undefined =
      id !== null && pendingRemoveId === id ? 'remove' : undefined
    return (
      <Box
        component="li"
        key={rowKey}
        sx={{ py: 1.5, minWidth: 0 }}
      >
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            gap: 1.5,
          }}
        >
          <Box sx={{ minWidth: 0 }}>
            <Typography variant="body1" noWrap sx={{ fontWeight: 600 }}>
              {value}
            </Typography>
            <Typography variant="body2" color="text.secondary">
              {kindLabel}
            </Typography>
            {lastLoginName ? (
              <Typography variant="body2" color="text.secondary">
                {t('identifiers.lastLoginName')}
              </Typography>
            ) : null}
          </Box>
          <Box
            sx={{
              flexShrink: 0,
              display: 'flex',
              alignItems: 'center',
              gap: 1,
            }}
          >
            {showChip ? (
              <Chip
                size="small"
                variant="outlined"
                color={verified ? 'success' : 'default'}
                label={stateLabel}
              />
            ) : null}
            {showActionsMenu ? (
              <ContactActions
                canVerify={canVerifyRow}
                verifyOpen={rowFlowOpen}
                verifyOpenReason={t('identifiers.verifyInProgress')}
                canRemove={canRemoveRow}
                busyAction={busyAction}
                scopeUnavailable={scopeUnavailable}
                onVerify={() => openRowVerify(rowKey, value)}
                onVerifyRef={(node) => {
                  if (node === null) {
                    verifyButtons.current.delete(rowKey)
                  } else {
                    verifyButtons.current.set(rowKey, node)
                  }
                }}
                onRemove={() => {
                  if (id !== null) {
                    setRemoveTarget(id)
                  }
                }}
                onRemoveRef={(node) => {
                  if (id === null) {
                    return
                  }
                  if (node === null) {
                    removeButtons.current.delete(id)
                  } else {
                    removeButtons.current.set(id, node)
                  }
                }}
              />
            ) : null}
          </Box>
        </Box>
        {kind === 'email' && verified ? (
          <DefaultContactChoice
            checked={axes.defaultContact}
            label={t('identifiers.axes.defaultContact')}
            ariaLabel={t('identifiers.axes.defaultAria', {
              value,
              interpolation: { escapeValue: false },
            })}
            unavailable={scopeUnavailable || id === null}
            unavailableReason={
              id === null
                ? t('identifiers.axes.defaultUnavailable')
                : undefined
            }
            onChange={() => {
              if (id !== null && !axes.defaultContact) {
                void setDefaultContact(id, value)
              }
            }}
          />
        ) : null}
        {rowFlowOpen ? renderRowVerifyForm(value) : null}
      </Box>
    )
  }

  return (
    <>
      <SectionCard title={showHeader ? t('identifiers.title') : undefined}>
        {addFaceReplacesList ? null : (
        <AccountAsyncSection
        pending={pending}
        payload={rows}
        empty={rows !== undefined && rows.length === 0}
        error={error}
        refreshing={isFetching}
        loading={<IdentifierListSkeleton label={t('identifiers.loading')} />}
        errorState={{
          title: t('identifiers.error.title'),
          description: t('identifiers.error.description'),
          retryLabel: t('identifiers.retry'),
          onRetry: () => void refetch(),
        }}
        emptyState={{
          title: t('identifiers.empty.title'),
          description: t('identifiers.empty.description'),
        }}
      >
        {(settledRows) => (
          <Box sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            {axis === 'contact' ? (
              <>
              <Table
                size="small"
                sx={{
                  '& td': { border: 0, padding: '8px 0' },
                  '& td + td': { paddingLeft: 16 },
                }}
              >
                <TableBody>
                  {settledRows
                    .filter((row) => (row.kind ?? '') === 'email')
                    .map((row, index) => {
                      const id = row.id ?? null
                      const kind = row.kind ?? ''
                      const verified = row.proven === true
                      const axes = axesOf(row)
                      return (
                        <TableRow key={id ?? index}>
                          <TableCell sx={{ width: '11rem' }}>
                            {row.value ?? ''}
                          </TableCell>
                          <TableCell>
                            <Stack
                              direction="row"
                              spacing={1}
                              sx={{ alignItems: 'center' }}
                            >
                              <Chip
                                size="small"
                                variant="outlined"
                                color={verified ? 'success' : 'default'}
                                label={
                                  verified
                                    ? t('identifiers.verified')
                                    : t('identifiers.unverified')
                                }
                              />
                              {axes.defaultContact ? (
                                <Chip
                                  size="small"
                                  variant="outlined"
                                  label={t('identifiers.axes.defaultContact')}
                                />
                              ) : null}
                            </Stack>
                          </TableCell>
                          <TableCell align="right">
                            {id !== null ? (
                              <ContactActions
                                canVerify={
                                  kind === 'email' &&
                                  !verified &&
                                  rowVerifyAvailable
                                }
                                canDefault={kind === 'email' && verified}
                                isDefault={axes.defaultContact}
                                busyAction={
                                  pendingRemoveId === id
                                    ? 'remove'
                                    : pendingUpdate?.kind === 'default' &&
                                        pendingUpdate.id === id
                                      ? 'defaultContact'
                                      : undefined
                                }
                                scopeUnavailable={scopeBlocked}
                                onVerify={() =>
                                  openRowVerify(
                                    `contact:${id}`,
                                    row.value ?? '',
                                  )
                                }
                                onVerifyRef={(node) => {
                                  const key = `contact:${id}`
                                  if (node === null) {
                                    verifyButtons.current.delete(key)
                                  } else {
                                    verifyButtons.current.set(key, node)
                                  }
                                }}
                                onSetDefault={() =>
                                  void setDefaultContact(id, row.value ?? '')
                                }
                                onDefaultRef={(node) => {
                                  if (node === null) {
                                    defaultButtons.current.delete(id)
                                  } else {
                                    defaultButtons.current.set(id, node)
                                  }
                                }}
                                onRemove={() => setRemoveTarget(id)}
                                onRemoveRef={(node) => {
                                  if (node === null) {
                                    removeButtons.current.delete(id)
                                  } else {
                                    removeButtons.current.set(id, node)
                                  }
                                }}
                              />
                            ) : null}
                          </TableCell>
                        </TableRow>
                      )
                    })}
                </TableBody>
              </Table>
              {rowVerify !== null && rowVerify.key.startsWith('contact:')
                ? renderRowVerifyForm(rowVerify.value)
                : null}
              </>
            ) : (
              groups.map((group) => {
              const groupKey = group.kind ?? group.key
              const headingId = `${groupIdBase}-${groupKey}`
              const showHeading =
                axis !== 'signIn' && (axis === 'both' || groups.length > 1)
              if (group.key === 'signIn') {
                const kindSpecs =
                  axis === 'signIn'
                    ? [group]
                    : signInKindSpecs.filter((spec) =>
                        settledRows.some(
                          (row) => (row.kind ?? '') === (spec.kind ?? ''),
                        ),
                      )
                return (
                  <Box
                    component="section"
                    key={groupKey}
                    aria-labelledby={showHeading ? headingId : undefined}
                  >
                    {axis === 'signIn' ? null : (
                      <Typography
                        id={headingId}
                        component="h3"
                        variant="subtitle1"
                        sx={{ mb: 0.5 }}
                      >
                        {t(group.title)}
                      </Typography>
                    )}
                    <Box
                      sx={{
                        display: 'flex',
                        flexDirection: 'column',
                        gap: 2,
                      }}
                    >
                      {kindSpecs.map((spec) =>
                        renderSignInKind(spec, settledRows),
                      )}
                    </Box>
                  </Box>
                )
              }
              const contactRows = settledRows.filter(isContact)
              return (
                <Box
                  component="section"
                  key={groupKey}
                  aria-labelledby={showHeading ? headingId : undefined}
                >
                  <Typography
                    id={headingId}
                    component="h3"
                    variant="subtitle1"
                    sx={{ mb: 0.5 }}
                  >
                    {t(group.title)}
                  </Typography>
                  {contactRows.length === 0 ? (
                    <Typography variant="body2" color="text.secondary">
                      {t(group.empty)}
                    </Typography>
                  ) : (
                    <Box
                      component="ul"
                      role="list"
                      sx={{ m: 0, p: 0, listStyle: 'none' }}
                    >
                      {contactRows.map((identifier, index) =>
                        renderContactRow(identifier, index),
                      )}
                    </Box>
                  )}
                </Box>
              )
              })
            )}
          </Box>
        )}
      </AccountAsyncSection>
        )}

      <Box
        ref={addRegion}
        tabIndex={-1}
        role="group"
        aria-label={t('identifiers.title')}
        sx={{ mt: 2 }}
      >
        {axis === 'signIn' || addKinds.length === 0 ? null : !addOpen ? (
          showAddButton ? (
          <ActionButton
            ref={addEntry}
            size="small"
            unavailable={scopeBlocked}
            onClick={openAdd}
          >
            {t('identifiers.add.open')}
          </ActionButton>
          ) : null
        ) : (
          <>
            <Typography variant="subtitle1" component="h3" sx={{ mb: 1 }}>
              {axis === 'contact'
                ? t('identifiers.addEmail.title')
                : t('identifiers.add.title')}
            </Typography>
            <Box
              component="form"
              ref={addForm}
              noValidate
              onSubmit={guardedAddSubmit}
              sx={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'flex-start',
                gap: 2,
                maxWidth: FORM_MAX_WIDTH,
                mt: 1,
              }}
            >
              {sentTo === null ? (
                <>
                  {axis === 'contact' ? null : (
                    <TextField
                      select
                      fullWidth
                      label={t('identifiers.add.kind.label')}
                      value={formKind}
                      // It never starts this write, so it stays natively disabled.
                      disabled={addBusy}
                      slotProps={{ select: addKindMenu }}
                      onChange={(event) => {
                        setPendingSend(null)
                        setAddKind(event.target.value as AddKind)
                        setFieldErrorKey(null)
                      }}
                    >
                      {addKinds.map((option) => (
                        <MenuItem key={option} value={option}>
                          {t(`identifiers.add.kind.${option}`)}
                        </MenuItem>
                      ))}
                    </TextField>
                  )}
                  <TextField
                    fullWidth
                    type={
                      axis === 'contact' || formKind === 'email'
                        ? 'email'
                        : 'text'
                    }
                    autoComplete={
                      axis === 'contact' || formKind === 'email'
                        ? 'email'
                        : 'username'
                    }
                    inputRef={valueInput}
                    label={valueLabel}
                    error={fieldErrorKey !== null}
                    helperText={
                      fieldErrorKey === null
                        ? valueHelper
                        : t(fieldErrorKey)
                    }
                    value={addValue}
                    slotProps={{ htmlInput: { readOnly: addBusy } }}
                    onChange={(event) => {
                      setPendingSend(null)
                      setAddValue(event.target.value)
                      if (fieldErrorKey !== null) {
                        setFieldErrorKey(null)
                      }
                    }}
                  />
                  {pendingSend !== null && pendingSend.step === 'add'
                    ? humanBlock
                    : null}
                  <ActionRow sx={{ width: '100%' }}>
                    <ActionButton
                      type="button"
                      unavailable={scopeBlocked}
                      onClick={closeAdd}
                    >
                      {t('identifiers.add.cancel')}
                    </ActionButton>
                    <ActionButton
                      type="submit"
                      emphasis="primary"
                      busy={addSendInFlight}
                      unavailable={
                        !addSendInFlight && (scopeBlocked || addValueMissing)
                      }
                      unavailableReason={
                        !addSendInFlight && !scopeBlocked && addValueMissing
                          ? addValueMissingReason
                          : undefined
                      }
                    >
                      {axis === 'contact' || formKind === 'email'
                        ? t('identifiers.add.submitEmail')
                        : t('identifiers.add.submitUsername')}
                    </ActionButton>
                  </ActionRow>
                </>
              ) : (
                <>
                  <AnnouncementRegion
                    message={t('identifiers.add.sent', {
                      value: sentTo,
                      interpolation: { escapeValue: false },
                    })}
                    sx={{ typography: 'body2', color: 'text.secondary' }}
                  />
                  <TextField
                    fullWidth
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    label={t('identifiers.add.code.label')}
                    value={code}
                    inputRef={addCodeRef}
                    slotProps={{ htmlInput: { readOnly: addBusy } }}
                    onChange={(event) => setCode(event.target.value)}
                  />
                  <ActionRow sx={{ width: '100%' }}>
                    <ActionButton
                      type="button"
                      unavailable={scopeBlocked}
                      onClick={() => {
                        setSentTo(null)
                        setCode('')
                        setFieldErrorKey(null)
                        clearFailure()
                      }}
                    >
                      {t('identifiers.add.change')}
                    </ActionButton>
                    <ActionButton
                      type="submit"
                      emphasis="primary"
                      busy={addVerifyInFlight}
                      unavailable={
                        !addVerifyInFlight &&
                        (scopeBlocked || code.trim() === '')
                      }
                      unavailableReason={
                        !addVerifyInFlight &&
                        !scopeBlocked &&
                        code.trim() === ''
                          ? t('identifiers.add.codeEmpty')
                          : undefined
                      }
                    >
                      {t('identifiers.add.verify')}
                    </ActionButton>
                  </ActionRow>
                </>
              )}
            </Box>
          </>
        )}
      </Box>

      {/* Outside AsyncSection, so an empty list still shows it. */}
      <WriteFeedbackRegion outcome={feedback.outcome} sx={{ mt: 1 }} />
      {failureText !== null && (
        <Alert severity="error" role="alert" sx={{ width: '100%', mt: 1 }}>
          {t(failureText)}
        </Alert>
      )}
      </SectionCard>

      <ConfirmDialog
        open={removeTarget !== null}
        title={t('identifiers.confirmTitle')}
        message={t('identifiers.confirmMessage')}
        variant="danger"
        confirmLabel={t('identifiers.confirmLabel')}
        onCancel={() => setRemoveTarget(null)}
        onConfirm={() => {
          const id = removeTarget
          setRemoveTarget(null)
          if (id !== null) {
            void remove(id)
          }
        }}
      />

      <StepUpChallenge
        open={gate.open}
        reason={gate.reason}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
        returnPath={returnPath}
        onRelogin={rememberRelogin}
      />
    </>
  )
}
