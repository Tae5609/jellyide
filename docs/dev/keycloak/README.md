# Keycloak dev stack for JellyIDE SSO

A single-container Keycloak 26 (`quay.io/keycloak/keycloak:26.0`,
`start-dev` mode, host port **8081**) with step-by-step setup of a realm
plus one **OIDC** client and one **SAML** client, and the matching
JellyIDE `.env` lines. Both providers can run at the same time — the
login page shows one button per enabled provider.

## Table of contents

- [1. Start Keycloak](#1-start-keycloak)
- [2. Create the realm](#2-create-the-realm)
- [3. Create a test user](#3-create-a-test-user)
- [4. OIDC client](#4-oidc-client)
- [5. SAML client](#5-saml-client)
- [6. Point JellyIDE at Keycloak](#6-point-jellyide-at-keycloak)
- [Troubleshooting](#troubleshooting)

## 1. Start Keycloak

```sh
cd docs/dev/keycloak
docker compose up -d
docker compose logs -f   # wait for "started"
```

Open <http://localhost:8081> and log in as `admin` / `admin`.

> Note: JellyIDE must be reachable on `http://localhost:8080` (the dev
> compose default) and must run on the **same host** as the browser,
> because the redirect URIs below are `http://localhost:8080/...`.

## 2. Create the realm

1. Top right: *Select realm* → **Create realm** → name it `jellyide`.
2. Stay on the realm (top right shows *jellyide*). Everything below is
   configured in this realm.

## 3. Create a test user

1. *Users* → **Create user** → username `alice` (any valid username),
   email optional, *Enabled* on.
2. *Credentials* tab → set a password (uncheck *Temporary*).
3. For the admin-group demos below: *Groups* → create a group
   `admin` → click the group → *User assignment* → assign `alice`
   (or keep a second non-admin user to compare).

## 4. OIDC client

1. *Clients* → **Create client**:
   - Client ID: `jellyide`
   - Client type: `openid-connect`
   - Client authentication: **on** (confidential client — JellyIDE
     requires a client secret)
2. *Settings* tab:
   - Valid redirect URIs: `http://localhost:8080/api/auth/oidc/callback`
   - *Standard flow*: **on** (authorization code flow)
   - *Code flow challenge method*: `S256` (PKCE — JellyIDE always sends
     a verifier)
3. *Client credentials* tab → copy the **client secret**.
4. Group mapping so `OIDC_ADMIN_GROUP` can work (Keycloak does not send
   groups in the token by default):
   - *Client scopes* → **Create scope** → name `groups`, add a
     **Group** protocol mapper: Token Claim Name `groups`, Claim Type
     `Group`, Multivalued **on**.
   - Back to the `jellyide` client → *Client scopes* tab → add the
     `groups` scope to the client.

### JellyIDE `.env` (OIDC)

```sh
OIDC_ENABLED=true
OIDC_NAME=Keycloak
OIDC_ISSUER=http://localhost:8081/realms/jellyide
OIDC_CLIENT_ID=jellyide
OIDC_CLIENT_SECRET=<secret from step 3>
# make realm group "admin" grant JellyIDE admin rights:
OIDC_ADMIN_GROUP=admin
```

Restart JellyIDE; the login page shows a **Keycloak** button.

## 5. SAML client

1. Fetch the SP metadata from a **running** JellyIDE (SAML must already
   be enabled — see step 6 first if not, then come back):

   ```sh
   curl -fsS http://localhost:8080/api/auth/saml/metadata
   ```

   Save the XML to a file, e.g. `sp-metadata.xml`.
2. *Clients* → **Create client**:
   - Client ID: `jellyide-saml`
   - Client type: `saml`
3. *SAML* tab → **SAML client signature** off; fill in (or import from
   the SP metadata XML, which pre-fills the URLs):
   - Assertion Consumer Service URL:
     `http://localhost:8080/api/auth/saml/acs`
   - Single Sign-On Service URL:
     `http://localhost:8080/api/auth/saml/login`
   - Entity ID: copy it from the `entityID` attribute of the SP
     metadata XML.
   - *Force NameID format*: `email` (or whatever your IdP policy uses;
     JellyIDE falls back to the NameID for the username when no username
     attribute is set).
4. *SAML Keys* tab: import JellyIDE's SP certificate
   (`<MUSIC_DIR>/saml/sp.crt` from the JellyIDE container) so Keycloak
   can sign assertions and validate the SP.
5. Attribute release: *Mappers* → verify that at least a mapper exists
   for `email` (Keycloak ships default mappers for `email` and
   `displayName`); add a `groups` mapper (same Group mapper as in step 4)
   if you use `SAML_ADMIN_GROUP`.

### JellyIDE `.env` (SAML)

```sh
SAML_ENABLED=true
SAML_NAME=Keycloak
# point at the realm's SAML endpoint (works while Keycloak is up):
SAML_IDP_METADATA_URL=http://localhost:8081/realms/jellyide/protocol/saml
# or, offline:
# SAML_IDP_METADATA_FILE=/mnt/keycloak/idp-metadata.xml
# grant admin rights to realm group "admin":
# SAML_ADMIN_GROUP=admin
```

> SAML note: with `COOKIE_SECURE=false` (plain HTTP dev) the SP request
> tracker cookie stays `SameSite=Lax`, so the IdP must be reachable from
> the same site as the browser's view of JellyIDE — true here because
> both are `localhost`.

Restart JellyIDE; the login page now shows **Keycloak** (OIDC) and
**Keycloak** (SAML) — rename one via `OIDC_NAME` / `SAML_NAME` to tell
the buttons apart.

## 6. Point JellyIDE at Keycloak

1. Put the `.env` lines from steps 4 and 5 into your JellyIDE
   environment (the dev compose passes all `OIDC_*`/`SAML_*` variables
   through from your shell or a `.env` file next to `docker-compose.yml`).
2. `docker compose up -d` (JellyIDE).
3. Log in through either button. First login auto-creates the local
   account (`OIDC_AUTO_CREATE` / `SAML_AUTO_CREATE` default on).

## Troubleshooting

| Symptom | Check |
|---|---|
| `invalid sso state` on the login page | JellyIDE restarted between click and callback (state is in-memory, 10-min TTL), or the redirect URI in Keycloak does not byte-for-byte match `http://localhost:8080/api/auth/oidc/callback`. |
| Keycloak shows "Invalid redirect URI" | The *Valid redirect URIs* entry differs from the one JellyIDE sends (scheme/host/port/typo). |
| SAML: JellyIDE fails to start with a metadata error | `SAML_IDP_METADATA_URL` is unreachable from the JellyIDE container (use the container-reachable host name, e.g. `http://host.docker.internal:8081/...` if Keycloak runs on the Docker host and JellyIDE in a compose network). |
| SAML login redirects back with `SAML login failed` | Keycloak signed with a different key than the imported SP cert, or the ACS URL in the client does not match. Re-import the SP cert and compare URLs. |
| User is not an admin after SSO login | The group mapper is missing, or `OIDC_ADMIN_GROUP`/`SAML_ADMIN_GROUP` does not exactly match the group name. |
