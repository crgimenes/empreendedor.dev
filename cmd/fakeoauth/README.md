# fakeoauth (DEV/Test Only)

Minimal OAuth2 server for local development and offline tests. Implements Authorization Code flow with optional PKCE (S256), no consent screen, always issuing a `code` and tokens for a fixed user configured via flags.

> Warning: Do NOT use in production. No HTTPS, no persistence, limited validation. For local / controlled CI environments only.

## Features

- Authorization Code + optional PKCE (S256)
- Opaque `access_token` and dummy `refresh_token`
- Optional `id_token` (JWT HS256) with basic claims
- `/oauth/userinfo` endpoint returning fixed user data
- Periodic in memory cleanup of expired codes/tokens
- Configurable artificial latency for timeout testing
- No external dependencies (standard library only)

## Endpoints

| Method | Path            | Description                                   |
|--------|-----------------|-----------------------------------------------|
| GET    | /oauth/authorize| Issues code and redirects to redirect_uri     |
| POST   | /oauth/token    | Exchanges code for tokens                     |
| GET    | /oauth/userinfo | Returns user JSON (Bearer)                    |
| GET    | /healthz        | Simple health probe                           |

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| --addr | 127.0.0.1:9100 | Listen address |
| --base-url | `http://127.0.0.1:9100` | Public base used as iss in id_token |
| --client-id | fake-client-id | Expected client_id |
| --client-secret | (empty) | Optional client_secret (ignored if empty) |
| --user-id | u-123 | Fixed user ID |
| --username | tester | Username/login |
| --name | Test User | Display name |
| --email | `tester@example.local` | Fixed email |
| --avatar-url | (empty) | Avatar URL |
| --issue-id-token | false | If true issues id_token JWT |
| --jwt-secret | dev-secret | HMAC HS256 secret |
| --token-ttl | 15m | Access token lifetime |
| --latency | 0s | Artificial per request latency |
| --verbose | false | Verbose logging |

## Basic Runs

```sh
# Default run
fakeoauth

# Issue id_token
fakeoauth --issue-id-token --jwt-secret dev-secret

# Add 500ms latency and custom user
fakeoauth --latency 500ms --user-id u-999 --username alice --name "Alice Dev" --email alice@example.local
```

## Manual Flow (cURL)

```sh
BASE=http://127.0.0.1:9100
REDIR=http://127.0.0.1:8080/fake/oauth/callback
CLIENT_ID=fake-client-id
STATE=xyz
CHAL=abc123   # normally S256(challenge) derived; can be test value here

echo "Opening authorize..."
AUTH_URL="$BASE/oauth/authorize?response_type=code&client_id=$CLIENT_ID&redirect_uri=$REDIR&scope=profile+email&state=$STATE"

curl -i "$AUTH_URL" -L | grep -i location || true
# Copy the code returned in the final redirect URL
CODE=PUT_THE_CODE_HERE

curl -s -X POST "$BASE/oauth/token" \
  -d grant_type=authorization_code \
  -d code=$CODE \
  -d redirect_uri=$REDIR \
  -d client_id=$CLIENT_ID \
  | jq .
```

## Integration With Main App

Set environment variables before starting the app:

```sh
export FAKE_OAUTH_ENABLED=true
export FAKE_OAUTH_BASE_URL=http://127.0.0.1:9100
export FAKE_OAUTH_CLIENT_ID=fake-client-id
export FAKE_OAUTH_REDIRECT_PATH=/fake/oauth/callback
```

On the home page the button "Sign in Fake OAuth" will appear. The flow sets a non Secure session cookie (only in this mode).

## Optional id_token (JWT)

When `--issue-id-token` is enabled, the /oauth/token response includes `id_token` HS256 with claims: `iss`, `aud`, `sub`, `exp`, `iat`, `email`, `name`, `preferred_username` and `picture` (if avatar provided).

## Timeout Testing

Use `--latency` to simulate predictable delays:

```sh
fakeoauth --latency 2s --verbose
```

Allows validating client retry / cancellation behavior.

## Simulated Errors

Server returns standard JSON error bodies, e.g.:

```json
{"error": "invalid_grant", "error_description": "expired code"}
```

There are no flags to force errors yet; can be extended.

## Limitations and Future Extensions

- Does not validate redirect_uri audience against a whitelist (intentional for simplicity).
- Does not implement real refresh_token (dummy value only).
- Could be extended with /revoke or /introspect if needed.

## Final Warning

Educational and test support tool. **Never** use in production or exposed environments.
