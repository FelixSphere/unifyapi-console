-- UnifyAPI console SMTP seed — AWS SES over the SMTP interface.
--
-- new-api reads SMTP settings from the `options` table only; there is no
-- environment-variable path (see model/option.go and common/email.go). So they
-- are seeded here, the same way branding is in seed-branding.sql, rather than
-- set in docker-compose.
--
-- NO SECRET IS CHECKED IN. The two credential values are psql variables, bound
-- at run time from SSM by scripts/seed-smtp.sh. Run that script rather than this
-- file directly:
--
--   scripts/seed-smtp.sh
--
-- To run it by hand against a database you already have a DSN for:
--
--   psql "$SQL_DSN" -v smtp_account="$KEY_ID" -v smtp_token="$SMTP_PASSWORD" \
--        -v smtp_host=email-smtp.us-east-1.amazonaws.com \
--        -v smtp_from=noreply@unifyapi.ai -f seed-smtp.sql
--
-- IMPORTANT: options are cached in process memory at boot (InitOptionMap), so
-- restart the container afterwards or nothing changes:
--   docker compose -f /opt/unifyapi/docker-compose.yml restart console

INSERT INTO options (key, value) VALUES
  ('SMTPServer', :'smtp_host')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- 587 + STARTTLS, not 465. new-api picks implicit TLS when SMTPSSLEnabled is
-- true OR (port is 465 AND StartTLS is off) — see newSMTPClient in
-- common/email.go. The two flags below are what actually select STARTTLS; the
-- port number alone does not.
INSERT INTO options (key, value) VALUES
  ('SMTPPort', '587')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

INSERT INTO options (key, value) VALUES
  ('SMTPSSLEnabled', 'false')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

INSERT INTO options (key, value) VALUES
  ('SMTPStartTLSEnabled', 'true')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- The SES SMTP username is an IAM access key ID — it is NOT an email address.
INSERT INTO options (key, value) VALUES
  ('SMTPAccount', :'smtp_account')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- Must be set, and must match the address the IAM send policy is conditioned on
-- (ses:FromAddress in web/infra/ses-smtp.tf). If SMTPFrom is empty, new-api
-- falls back to using SMTPAccount as the From header — an access key ID, which
-- is not a valid address, so every send fails at the MAIL FROM step.
INSERT INTO options (key, value) VALUES
  ('SMTPFrom', :'smtp_from')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- The SES SMTP password. Derived from the IAM secret access key by an
-- HMAC-SHA256 "v4" derivation — it is not the secret access key itself.
-- Terraform computes it via aws_iam_access_key.ses_smtp_password_v4.
INSERT INTO options (key, value) VALUES
  ('SMTPToken', :'smtp_token')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- Deliberately NOT set here: EmailVerificationEnabled.
--
-- Turning it on makes a working mail path a hard dependency of registration —
-- if SES is misconfigured or still sandboxed, nobody can sign up and the failure
-- shows only in the container log. Send a test mail first (scripts/seed-smtp.sh
-- prints how), confirm it arrives, then enable it in Settings, or with:
--   INSERT INTO options (key, value) VALUES ('EmailVerificationEnabled', 'true')
--   ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
