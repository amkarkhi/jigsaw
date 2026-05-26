# Testing GitLab SSO

Step-by-step commands for verifying the GitLab OAuth login flow end-to-end
against a self-hosted GitLab (or `gitlab.com`).

## Prerequisites

- A running GitLab instance you can reach from the dashboard host.
- Admin (or `Maintainer`) access on that GitLab — needed to create an OAuth
  application.
- The `jigsaw` binary built locally:

```bash
go build -o bin/jigsaw ./cmd/jigsaw
```

## 1. Register the OAuth application on GitLab

In the GitLab UI, go to:

- **User profile → Preferences → Applications** for a personal app, or
- **Admin → Applications** for an instance-wide app.

Fill in:

| Field | Value |
|---|---|
| Name | `jigsaw-dashboard` (anything) |
| Redirect URI | `http://localhost:3300/auth/gitlab/callback` (match exactly) |
| Scopes | `read_user` |
| Confidential | yes |

Save. Copy the **Application ID** (= client id) and **Secret**.

## 2. Initialize the dashboard auth file

The dashboard's SSO flow auto-provisions users *into* an existing auth file —
it does not create the file itself. Initialize it once per config tree:

```bash
# Master key gates user/token CLI commands later. Pick anything ≥ 16 chars.
./bin/jigsaw user init --master-key "$(openssl rand -hex 24)"
```

This writes `./configs/.jigsaw/auth.json`. You can confirm:

```bash
cat ./configs/.jigsaw/auth.json | jq '{version, users: .users | length}'
```

You don't need to create any password user — SSO will fill in `.users` on
first login.

## 3. Start the dashboard with SSO enabled

Two equivalent ways: CLI flags or env vars.

### Option A — CLI flags

```bash
./bin/jigsaw dashboard \
  --mode=server \
  --edit \
  --listen=127.0.0.1:3300 \
  --gitlab-base-url=https://gitlab.example.com \
  --gitlab-client-id=<APPLICATION_ID> \
  --gitlab-client-secret=<SECRET> \
  --gitlab-redirect-url=http://localhost:3300/auth/gitlab/callback \
  --gitlab-default-role=viewer
```

### Option B — env vars

```bash
export JIGSAW_GITLAB_BASE_URL=https://gitlab.example.com
export JIGSAW_GITLAB_CLIENT_ID=<APPLICATION_ID>
export JIGSAW_GITLAB_CLIENT_SECRET=<SECRET>
export JIGSAW_GITLAB_REDIRECT_URL=http://localhost:3300/auth/gitlab/callback
export JIGSAW_GITLAB_DEFAULT_ROLE=viewer

./bin/jigsaw dashboard --mode=server --edit --listen=127.0.0.1:3300
```

You should see this log line on startup:

```
INF dashboard.gitlab_oauth_enabled base=https://gitlab.example.com
```

If you don't, one of the four required fields is missing.

## 4. Verify the discovery endpoint

The SPA polls this to decide whether to render the "Sign in with GitLab"
button. It's unauthenticated:

```bash
curl -s http://localhost:3300/api/auth-info | jq
# expected: {"password": true, "gitlab": true}
```

## 5. Walk the OAuth flow in a browser

Open <http://localhost:3300/> — `AuthGate` will bounce to `/login` because
you're not authenticated. You should see the password form **and** an orange
**Sign in with GitLab** button at the bottom.

Click it. You'll be redirected to:

```
https://gitlab.example.com/oauth/authorize?client_id=...&redirect_uri=...&state=...&scope=read_user
```

Approve. GitLab redirects back to
`http://localhost:3300/auth/gitlab/callback?code=...&state=...`.
The dashboard:

1. validates the state cookie,
2. exchanges the code for an access token at `/oauth/token`,
3. fetches `/api/v4/user`,
4. provisions you in `auth.json` with role `viewer` if you're new,
5. sets `jigsaw_session` cookie, redirects to `/`.

You should land on the dashboard logged in. Confirm:

```bash
curl -s -b "jigsaw_session=<PASTE_FROM_BROWSER_DEVTOOLS>" \
  http://localhost:3300/api/me | jq
# expected: {"authenticated": true, "label": "<your gitlab username>", "role": "viewer"}
```

## 6. Confirm auto-provisioning

After your first successful login, `auth.json` should have a new entry:

```bash
jq '.users[] | {username, role, created_at}' ./configs/.jigsaw/auth.json
```

You'll see your GitLab username with `"role": "viewer"`. The stored `hash`
is a random bcrypt digest (no usable password), so SSO is now the only way
to log in as that account.

## 7. Promote a user to admin (optional)

There's no dedicated "change role" CLI; two pragmatic options:

**a) Edit `auth.json` directly** (single-line patch with `jq`):

```bash
jq '(.users[] | select(.username == "<gitlab-username>") .role) = "admin"' \
  ./configs/.jigsaw/auth.json > /tmp/auth.json && \
  mv /tmp/auth.json ./configs/.jigsaw/auth.json
```

**b) Delete + re-provision via SSO** with a stronger default role:

```bash
./bin/jigsaw user delete --username <gitlab-username> --master-key '<your master key>'
# restart the dashboard with --gitlab-default-role=admin, then SSO again
```

Either way, log out and back in for the new role to take effect on
existing sessions (or wait 12 hours for the session to expire).

## Failure-mode quick checks

| Symptom | Likely cause |
|---|---|
| `GitLab SSO is not configured` (503) | One of the four config fields is empty. Check `/api/auth-info`. |
| `missing OAuth state cookie` | Stale browser tab — start over from `/login`. |
| `OAuth state mismatch` | Same. Or a redirect URI mismatch between GitLab and `--gitlab-redirect-url`. |
| `token endpoint returned 401` | `client_secret` doesn't match what GitLab issued. |
| `GitLab user has no username` | Scope is missing `read_user` on the application. |
| Sign-in succeeds but `/api/me` still says unauthenticated | Cookie scope mismatch — make sure the redirect URL host matches the dashboard's `--listen` host. |

## Cleanup / re-test

To start over from scratch (lose all users):

```bash
rm ./configs/.jigsaw/auth.json
./bin/jigsaw user init --master-key '...'
```

To wipe just one user without resetting:

```bash
./bin/jigsaw user delete --username <username> --master-key '...'
```

Restart the dashboard, click the GitLab button again, and you'll be
re-provisioned on the next login.
