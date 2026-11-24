#!/bin/sh

set -eu

template=/app/deploy/fly.yaml.tmpl
config=/data/config.yaml

die() {
    printf 'deploy: %s\n' "$*" >&2
    exit 1
}

require() {
    if [ -z "$2" ]; then
        die "$1 is not set. Set it as a Fly secret before deploying:
    flyctl secrets set $1=... --app \$FLY_APP_NAME"
    fi
}

check_scalar() {
    case $2 in
        *'"'* | *\\*)
            die "$1 contains a double quote or a backslash, which the deployment configuration cannot carry"
            ;;
    esac
    case $2 in
        *'
'*)
            die "$1 contains a line break"
            ;;
    esac
    if printf '%s' "$2" | LC_ALL=C grep -q '[[:cntrl:]]'; then
        die "$1 contains a control character"
    fi
}

if [ -n "${DEMO_PUBLIC_URL:-}" ]; then
    public_origin=$DEMO_PUBLIC_URL
else
    [ -n "${FLY_APP_NAME:-}" ] || die "neither DEMO_PUBLIC_URL nor FLY_APP_NAME is set, so the deployment has no origin to serve from"
    public_origin="https://${FLY_APP_NAME}.fly.dev"
fi

case $public_origin in
    https://?*) ;;
    http://?*)
        die "DEMO_PUBLIC_URL must be an https origin: the authentication gateway refuses an insecure public URL"
        ;;
    *)
        die "DEMO_PUBLIC_URL must be an origin such as https://app.fly.dev"
        ;;
esac

public_host=${public_origin#*://}
case $public_host in
    '' | */* | *\?* | *\#* | *' '*)
        die "DEMO_PUBLIC_URL must carry a host and no path, query or space"
        ;;
esac
public_domain=${public_host%%:*}
[ -n "$public_domain" ] || die "DEMO_PUBLIC_URL carries no host"

require DEMO_OPERATOR_USERNAME "${DEMO_OPERATOR_USERNAME:-}"
require DEMO_OPERATOR_EMAIL "${DEMO_OPERATOR_EMAIL:-}"
require DEMO_OPERATOR_PASSWORD "${DEMO_OPERATOR_PASSWORD:-}"
require DEMO_DB_ENCRYPTION_KEY "${DEMO_DB_ENCRYPTION_KEY:-}"

check_scalar DEMO_OPERATOR_USERNAME "$DEMO_OPERATOR_USERNAME"
check_scalar DEMO_OPERATOR_EMAIL "$DEMO_OPERATOR_EMAIL"
check_scalar DEMO_OPERATOR_PASSWORD "$DEMO_OPERATOR_PASSWORD"
check_scalar DEMO_DB_ENCRYPTION_KEY "$DEMO_DB_ENCRYPTION_KEY"

require DEMO_SMTP_HOST "${DEMO_SMTP_HOST:-}"
require DEMO_SMTP_FROM "${DEMO_SMTP_FROM:-}"

check_scalar DEMO_SMTP_HOST "$DEMO_SMTP_HOST"
check_scalar DEMO_SMTP_FROM "$DEMO_SMTP_FROM"
check_scalar DEMO_SMTP_USERNAME "${DEMO_SMTP_USERNAME:-}"
check_scalar DEMO_SMTP_PASSWORD "${DEMO_SMTP_PASSWORD:-}"

smtp_port=${DEMO_SMTP_PORT:-587}
smtp_tls=${DEMO_SMTP_TLS:-starttls}
case $smtp_port in
    '' | *[!0-9]*) die "DEMO_SMTP_PORT must be a whole number" ;;
esac
case $smtp_tls in
    starttls | implicit | none) ;;
    *) die "DEMO_SMTP_TLS must be starttls, implicit or none" ;;
esac

require DEMO_GOOGLE_CLIENT_ID "${DEMO_GOOGLE_CLIENT_ID:-}"
require DEMO_GOOGLE_CLIENT_SECRET "${DEMO_GOOGLE_CLIENT_SECRET:-}"
require DEMO_GITHUB_CLIENT_ID "${DEMO_GITHUB_CLIENT_ID:-}"
require DEMO_GITHUB_CLIENT_SECRET "${DEMO_GITHUB_CLIENT_SECRET:-}"

check_scalar DEMO_GOOGLE_CLIENT_ID "$DEMO_GOOGLE_CLIENT_ID"
check_scalar DEMO_GOOGLE_CLIENT_SECRET "$DEMO_GOOGLE_CLIENT_SECRET"
check_scalar DEMO_GITHUB_CLIENT_ID "$DEMO_GITHUB_CLIENT_ID"
check_scalar DEMO_GITHUB_CLIENT_SECRET "$DEMO_GITHUB_CLIENT_SECRET"

humanverify_mode=${DEMO_HUMANVERIFY_MODE:-pow}
humanverify_phone_mode=${DEMO_HUMANVERIFY_PHONE_MODE:-captcha}
turnstile_action=${DEMO_TURNSTILE_ACTION:-login}
case $humanverify_mode in
    off | pow | captcha | turnstile) ;;
    *) die "DEMO_HUMANVERIFY_MODE must be off, pow, captcha or turnstile" ;;
esac
case $humanverify_phone_mode in
    off | pow | captcha | turnstile) ;;
    *) die "DEMO_HUMANVERIFY_PHONE_MODE must be off, pow, captcha or turnstile" ;;
esac
turnstile_site_key=
turnstile_secret_key=
if [ "$humanverify_mode" = turnstile ] || [ "$humanverify_phone_mode" = turnstile ]; then
    require DEMO_TURNSTILE_SITE_KEY "${DEMO_TURNSTILE_SITE_KEY:-}"
    require DEMO_TURNSTILE_SECRET_KEY "${DEMO_TURNSTILE_SECRET_KEY:-}"
    check_scalar DEMO_TURNSTILE_SITE_KEY "$DEMO_TURNSTILE_SITE_KEY"
    check_scalar DEMO_TURNSTILE_SECRET_KEY "$DEMO_TURNSTILE_SECRET_KEY"
    turnstile_site_key=$DEMO_TURNSTILE_SITE_KEY
    turnstile_secret_key=$DEMO_TURNSTILE_SECRET_KEY
fi
check_scalar DEMO_TURNSTILE_ACTION "$turnstile_action"

mkdir -p /data/var /data/objects

awk -v origin="$public_origin" \
    -v domain="$public_domain" \
    -v username="$DEMO_OPERATOR_USERNAME" \
    -v email="$DEMO_OPERATOR_EMAIL" \
    -v password="$DEMO_OPERATOR_PASSWORD" \
    -v key="$DEMO_DB_ENCRYPTION_KEY" \
    -v smtp_host="$DEMO_SMTP_HOST" \
    -v smtp_port="$smtp_port" \
    -v smtp_tls="$smtp_tls" \
    -v smtp_username="${DEMO_SMTP_USERNAME:-}" \
    -v smtp_password="${DEMO_SMTP_PASSWORD:-}" \
    -v smtp_from="$DEMO_SMTP_FROM" \
    -v google_id="$DEMO_GOOGLE_CLIENT_ID" \
    -v google_secret="$DEMO_GOOGLE_CLIENT_SECRET" \
    -v github_id="$DEMO_GITHUB_CLIENT_ID" \
    -v github_secret="$DEMO_GITHUB_CLIENT_SECRET" \
    -v humanverify_mode="$humanverify_mode" \
    -v humanverify_phone_mode="$humanverify_phone_mode" \
    -v turnstile_site_key="$turnstile_site_key" \
    -v turnstile_secret_key="$turnstile_secret_key" \
    -v turnstile_action="$turnstile_action" '
    function replace(line, needle, repl,   out, i) {
        out = ""
        while ((i = index(line, needle)) > 0) {
            out = out substr(line, 1, i - 1) repl
            line = substr(line, i + length(needle))
        }
        return out line
    }
    {
        line = $0
        line = replace(line, "__PUBLIC_ORIGIN__", origin)
        line = replace(line, "__PUBLIC_DOMAIN__", domain)
        line = replace(line, "__OPERATOR_USERNAME__", username)
        line = replace(line, "__OPERATOR_EMAIL__", email)
        line = replace(line, "__OPERATOR_PASSWORD__", password)
        line = replace(line, "__ENCRYPTION_KEY__", key)
        line = replace(line, "__SMTP_HOST__", smtp_host)
        line = replace(line, "__SMTP_PORT__", smtp_port)
        line = replace(line, "__SMTP_TLS__", smtp_tls)
        line = replace(line, "__SMTP_USERNAME__", smtp_username)
        line = replace(line, "__SMTP_PASSWORD__", smtp_password)
        line = replace(line, "__SMTP_FROM__", smtp_from)
        line = replace(line, "__GOOGLE_CLIENT_ID__", google_id)
        line = replace(line, "__GOOGLE_CLIENT_SECRET__", google_secret)
        line = replace(line, "__GITHUB_CLIENT_ID__", github_id)
        line = replace(line, "__GITHUB_CLIENT_SECRET__", github_secret)
        line = replace(line, "__HUMANVERIFY_MODE__", humanverify_mode)
        line = replace(line, "__HUMANVERIFY_PHONE_MODE__", humanverify_phone_mode)
        line = replace(line, "__TURNSTILE_SITE_KEY__", turnstile_site_key)
        line = replace(line, "__TURNSTILE_SECRET_KEY__", turnstile_secret_key)
        line = replace(line, "__TURNSTILE_ACTION__", turnstile_action)
        print line
    }
' "$template" >"$config.new"

mv "$config.new" "$config"
chown demo:demo /data /data/var /data/objects "$config"
chmod 0600 "$config"

unset DEMO_OPERATOR_USERNAME DEMO_OPERATOR_EMAIL DEMO_OPERATOR_PASSWORD DEMO_DB_ENCRYPTION_KEY DEMO_PUBLIC_URL \
    DEMO_SMTP_HOST DEMO_SMTP_PORT DEMO_SMTP_TLS DEMO_SMTP_USERNAME DEMO_SMTP_PASSWORD DEMO_SMTP_FROM \
    DEMO_GOOGLE_CLIENT_ID DEMO_GOOGLE_CLIENT_SECRET DEMO_GITHUB_CLIENT_ID DEMO_GITHUB_CLIENT_SECRET \
    DEMO_HUMANVERIFY_MODE DEMO_HUMANVERIFY_PHONE_MODE DEMO_TURNSTILE_SITE_KEY DEMO_TURNSTILE_SECRET_KEY \
    DEMO_TURNSTILE_ACTION

su-exec demo /usr/local/bin/demo --config file:///data/config.yaml sqlite seed
exec su-exec demo /usr/local/bin/demo --config file:///data/config.yaml sqlite serve
