#!/usr/bin/env bash
# Seed the console's SES SMTP settings from SSM Parameter Store.
#
# Run this ON the console instance (it talks to the Postgres container over the
# compose network, which is not reachable from anywhere else):
#
#   aws ssm start-session --target <instance-id>
#   sudo -i
#   cd /opt/unifyapi && curl -fsSLO https://raw.githubusercontent.com/FelixSphere/unifyapi-console/main/scripts/seed-smtp.sh
#   bash seed-smtp.sh
#
# Credentials are read from SSM at run time using the instance role and are
# never written to disk. They do land in the `options` table, which is how
# new-api stores SMTP config -- there is no environment-variable path.
#
# Provisioned by web/infra/ses-smtp.tf in the unifyapi (marketing) repo.
set -euo pipefail

REGION="${AWS_REGION:-us-east-1}"
COMPOSE_DIR="${COMPOSE_DIR:-/opt/unifyapi}"
SQL_FILE="${SQL_FILE:-$(dirname "$0")/../seed-smtp.sql}"

if [ ! -f "$SQL_FILE" ]; then
  echo "seed-smtp.sql not found at $SQL_FILE (set SQL_FILE=...)" >&2
  exit 1
fi

ssm() {
  aws ssm get-parameter --name "$1" --with-decryption \
    --query Parameter.Value --output text --region "$REGION"
}

echo "--> reading SMTP credentials from SSM (${REGION})"
SMTP_ACCOUNT="$(ssm /unifyapi/console/smtp_username)"
SMTP_TOKEN="$(ssm /unifyapi/console/smtp_password)"
SMTP_SETTINGS="$(ssm /unifyapi/console/smtp_settings)"

# Plain shell rather than jq: jq is not installed on Amazon Linux 2023 by
# default, and adding a package to a boot-critical box for two field reads is a
# worse trade than two greps.
SMTP_HOST="$(printf '%s' "$SMTP_SETTINGS" | sed -n 's/.*"host":"\([^"]*\)".*/\1/p')"
SMTP_FROM="$(printf '%s' "$SMTP_SETTINGS" | sed -n 's/.*"from":"\([^"]*\)".*/\1/p')"

if [ -z "$SMTP_ACCOUNT" ] || [ -z "$SMTP_TOKEN" ] || [ -z "$SMTP_HOST" ] || [ -z "$SMTP_FROM" ]; then
  echo "one or more SMTP parameters were empty -- has web/infra been applied?" >&2
  exit 1
fi

echo "--> seeding options  host=$SMTP_HOST  from=$SMTP_FROM  account=${SMTP_ACCOUNT:0:8}..."
docker compose -f "$COMPOSE_DIR/docker-compose.yml" exec -T postgres \
  psql -U unifyapi -d unifyapi \
    -v smtp_host="$SMTP_HOST" \
    -v smtp_from="$SMTP_FROM" \
    -v smtp_account="$SMTP_ACCOUNT" \
    -v smtp_token="$SMTP_TOKEN" \
    -f - < "$SQL_FILE"

# Options are read into process memory once at boot (InitOptionMap), so without
# this the new values sit in the database and change nothing.
echo "--> restarting console so it re-reads the options table"
docker compose -f "$COMPOSE_DIR/docker-compose.yml" restart console

cat <<'EOF'

OK. Settings are live.

Test before enabling email verification:
  Console -> Settings -> mail section -> send a test email.

While the AWS account is in the SES sandbox, delivery ONLY succeeds to a
verified identity. Sending to any other address returns a 554 "Email address is
not verified" and the console reports a generic send failure. Check with:

  aws sesv2 get-account --region us-east-1 --query ProductionAccessEnabled

If that prints false, request production access before turning on
EmailVerificationEnabled -- otherwise registration breaks for every real user.
EOF
