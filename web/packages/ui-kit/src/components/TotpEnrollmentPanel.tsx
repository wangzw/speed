/**
 * The authenticator-app enrolment block: symbol, manual secret, code field
 * and action row. Every sentence and accessible name is a prop; the caller
 * owns the `otpauth://` parse and both actions.
 */

import { useId, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { ActionRow } from './ActionRow.js'
import { CopyButton } from './CopyButton.js'
import { TotpQrCode } from './TotpQrCode.js'

/** One authenticator parameter shown beside the manual secret. */
export type TotpEnrollmentParameter = {
  /** The parameter's label, already translated by the caller. */
  readonly label: string
  /** The parameter's value as text. */
  readonly value: string
}

/** The parsed base32 secret the manual entry hands over. */
export type TotpEnrollmentSecret = {
  /** The secret exactly as the parser returned it; copied verbatim. */
  readonly value: string
  /** The display form (for example four-character groups); defaults to `value`. */
  readonly display?: string
}

export type TotpEnrollmentPanelProps = {
  /** The `otpauth://` URI to draw. Omit when the step carries none. */
  readonly otpauthUri?: string
  /** The symbol's accessible name, already translated by the caller. */
  readonly qrLabel: string
  /** Guidance shown above the symbol, already translated. */
  readonly guidance?: string
  /** The manual-entry toggle's label. */
  readonly manualToggleLabel: string
  /** The parsed secret. Omit when the URI did not parse. */
  readonly secret?: TotpEnrollmentSecret
  /** The manual block's heading. */
  readonly secretLabel: string
  /** The copy control's accessible name. */
  readonly copyLabel: string
  /** The post-copy announcement. */
  readonly copiedLabel: string
  /** The copy control's visible label; omit for the icon control. */
  readonly copyVisibleLabel?: string
  /** The authenticator parameters shown beside the secret. */
  readonly parameters?: readonly TotpEnrollmentParameter[]
  /** The code field's label. */
  readonly codeLabel: string
  /** The code typed so far. */
  readonly code: string
  /** Reports every change of the code field. */
  readonly onCodeChange: (code: string) => void
  /** True while an answer is in flight; the code field is read-only. */
  readonly busy?: boolean
  /** A secondary control rendered ahead of the primary action in the panel's action row. */
  readonly secondaryAction?: ReactNode
  /** The primary action; it closes the action row at the right edge, alone or not. */
  readonly primaryAction: ReactNode
}

/**
 * The manual entry opens on request; the primary action closes the action row
 * at the right edge, with a secondary action ahead of it.
 */
export function TotpEnrollmentPanel({
  otpauthUri,
  qrLabel,
  guidance,
  manualToggleLabel,
  secret,
  secretLabel,
  copyLabel,
  copiedLabel,
  copyVisibleLabel,
  parameters,
  codeLabel,
  code,
  onCodeChange,
  busy = false,
  secondaryAction,
  primaryAction,
}: TotpEnrollmentPanelProps) {
  const [manualOpen, setManualOpen] = useState(false)
  const manualId = useId()
  const codeId = useId()

  return (
    <Box sx={{ mt: 2 }}>
      {guidance === undefined ? null : (
        <Typography variant="body2" color="text.secondary">
          {guidance}
        </Typography>
      )}
      {otpauthUri === undefined || otpauthUri.trim() === '' ? null : (
        <Box sx={{ mt: 1.5 }}>
          <TotpQrCode uri={otpauthUri} label={qrLabel} />
        </Box>
      )}
      {secret === undefined ? null : (
        <Box sx={{ mt: 1 }}>
          <Button
            type="button"
            size="small"
            aria-expanded={manualOpen}
            aria-controls={manualOpen ? manualId : undefined}
            onClick={() => setManualOpen((open) => !open)}
          >
            {manualToggleLabel}
          </Button>
          {manualOpen ? (
            <Box
              id={manualId}
              sx={{
                mt: 1,
                p: 1.5,
                border: 1,
                borderColor: 'divider',
                borderRadius: 1,
              }}
            >
              <Typography
                component="p"
                variant="body2"
                color="text.secondary"
                sx={{ m: 0 }}
              >
                {secretLabel}
              </Typography>
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 1,
                  flexWrap: 'wrap',
                  mt: 0.5,
                }}
              >
                <Typography
                  component="code"
                  sx={{
                    fontFamily: (theme) => theme.typography.fontFamilyMono,
                    wordBreak: 'break-all',
                    userSelect: 'all',
                  }}
                >
                  {secret.display ?? secret.value}
                </Typography>
                <CopyButton
                  value={secret.value}
                  label={copyLabel}
                  copiedLabel={copiedLabel}
                  visibleLabel={copyVisibleLabel}
                />
              </Box>
              {parameters === undefined || parameters.length === 0 ? null : (
                <Box
                  component="dl"
                  sx={{
                    display: 'flex',
                    flexWrap: 'wrap',
                    columnGap: 2,
                    rowGap: 0.5,
                    m: 0,
                    mt: 1,
                  }}
                >
                  {parameters.map((parameter) => (
                    <Box
                      key={parameter.label}
                      sx={{ display: 'flex', gap: 0.5 }}
                    >
                      <Typography
                        component="dt"
                        variant="body2"
                        color="text.secondary"
                        sx={{ m: 0 }}
                      >
                        {parameter.label}
                      </Typography>
                      <Typography component="dd" variant="body2" sx={{ m: 0 }}>
                        {parameter.value}
                      </Typography>
                    </Box>
                  ))}
                </Box>
              )}
            </Box>
          ) : null}
        </Box>
      )}
      <Box sx={{ mt: 2.5 }}>
        <Typography
          component="label"
          htmlFor={codeId}
          variant="body2"
          color="text.secondary"
          sx={{ display: 'block', mb: 0.5 }}
        >
          {codeLabel}
        </Typography>
        <TextField
          id={codeId}
          value={code}
          onChange={(event) => onCodeChange(event.target.value)}
          slotProps={{
            htmlInput: {
              // Read-only, not disabled: a disabled field would stop holding focus.
              readOnly: busy,
              inputMode: 'numeric',
              autoComplete: 'one-time-code',
              maxLength: 6,
              size: 6,
            },
          }}
        />
      </Box>
      <ActionRow sx={{ mt: 2 }}>
        {secondaryAction}
        {primaryAction}
      </ActionRow>
    </Box>
  )
}
