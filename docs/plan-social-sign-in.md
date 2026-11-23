# Plan: Sign in with Google and GitHub

Status: **built** on branch `feat/social-sign-in` (2026-11-23), not yet released; needs the Google and GitHub clients set up (below). Not on Roadmap 2; about 1 build week, taken before #7 (reports sent straight to clients), which is
waiting on the consent-wording decision anyway.

People will be able to create an account and sign in with **Google** or **GitHub** as well as with an email address and password. When they come
back signed out, the button they used last carries a **"Last used"** badge, so they don't have to remember how they signed up.

**It is an addition, not a replacement.** The email and password form stays on both pages, works exactly as it does today, and stays the
default for anyone who prefers it. One account can use any mix: a password, Google, GitHub, or all three. Someone who signed up with Google can
set a password later, and someone with a password can connect Google later.

Why customers care: one click instead of choosing and remembering a password. HawkEye's audience lives in GitHub (developers) and Google
(small businesses and agencies). It also removes a step on the way to monitoring APIs: Google and GitHub vouch for the email address, so the
account starts **verified**, and test requests and checks that send POST, PUT, PATCH or DELETE need a verified address.

Not to be confused with `internal/oauth`, where HawkEye is the OAuth *provider* that Claude and Codex connect to. Here HawkEye is the *client*,
and the code lives in its own package (`internal/socialauth`).

## How sign-in works today

- `internal/auth`: email and password only. `users.password_hash` is `NOT NULL`. Register creates the user, a personal organization on Free
  and a verification email, then starts a session. Login answers the same way for an unknown address, a disabled account and a wrong password.
- Sessions are an HttpOnly, `SameSite=Lax` cookie. Every sign-in, success or failure, is written to the security log.
- Two-factor sign-in (`internal/twofactor`): after the password, Login returns `SecondFactorRequired`, and `CompleteSignIn` takes the code. An
  owner can require two-factor for their organization, with a 7-day grace period.
- **The password is re-checked** before sensitive actions: changing the password, setting up or turning off two-factor, and deleting the
  account (which also asks for the email address).
- Invitations are accepted while signed in with the invited address.
- Sign-up attribution (how the visitor found HawkEye) is sent with the sign-up form from `localStorage`.
- Wording is mixed: the routes are `/login` and `/register`, and the copy says both "Log in" and "Sign in".

## Decisions (recommended defaults; confirm before building)

1. **Buttons say "Continue with Google" and "Continue with GitHub"** on both the sign-in and the sign-up page, so nobody has to know whether they
   already have an account. Google's branding rules allow that wording. Everything else standardizes on **"Sign in"** ("Log in" is removed
   from the copy). URLs stay `/login` and `/register`, so existing links keep working.
2. **Linking to an existing account by email: automatic, only for a verified email.** If someone with a password account clicks "Continue with
   Google" and Google says the address is verified (or it is GitHub's verified primary address), the identity is linked and they're signed in.
   They get an email ("Google was connected to your HawkEye account") and a security log entry. An unverified provider email never links and
   never creates an account (an error explains to use a verified address). The alternative, asking for the password before linking, is safer
   against a compromised provider account but breaks the one-click promise; a provider that vouches for the address is the standard bar
   (it is what password reset by email already trusts).
3. **HawkEye's two-factor still applies.** Social sign-in replaces the password, not the second factor. Someone who turned on two-factor,
   or whose organization requires it, enters their code after Google or GitHub, through the same `CompleteSignIn` step. Otherwise an owner's
   requirement could be skipped with one click.
4. **Confirming it's you without a password.** For the sensitive actions above, an account without a password confirms by signing in again
   with its provider ("Confirm with Google"), which marks the session as freshly confirmed for 10 minutes. Google is asked to show its sign-in
   screen again (`prompt=login`); GitHub has no such switch, so a fresh GitHub authorization proves the person still controls that GitHub
   account. Accounts with a password keep typing it. Two-factor, when on, is asked as before.
5. **Keep the least data:** the provider's account ID and the email address. No name, no avatar, no access to anything else (Google scopes
   `openid email`; GitHub `user:email`). Provider tokens are used once, during sign-in, and never stored.
6. **People stay in control:** a **Sign-in methods** section in account settings lists the password and each connected provider, with
   **Connect**, **Disconnect** and **Set a password**. The last remaining way in can't be removed. Nothing changes for anyone who never
   uses it.
7. **"Last used" is per browser and holds only the method** (`google`, `github` or `password`, never the email), in `localStorage`. It is a
   functional preference, so it doesn't follow the analytics opt-out. Password sign-ins set it too, or the badge would mislead someone who
   switches back. Signing out keeps it (that is the point); clearing site data removes it.
8. **Google and GitHub only, for now.** Microsoft and Apple can follow the same code if customers ask.

## The design

### Flow

1. The button links to `GET /api/v1/auth/social/{provider}/start?intent=signin|signup|connect|confirm&next=/path`.
2. The API creates a random `state`, a PKCE verifier and, for Google, a `nonce`. It keeps them, with the intent, the `next` path and (on
   sign-up) the attribution, in a short-lived, HttpOnly, signed cookie (`SameSite=Lax`, 10 minutes, path `/api/v1/auth/social`). Then it
   redirects to the provider.
3. The provider sends the person back to `GET /api/v1/auth/social/{provider}/callback?code&state`. The API checks `state` against the cookie
   (and deletes the cookie), swaps the code for tokens with the PKCE verifier, and reads the identity:
   - **Google:** the OpenID Connect ID token, received straight from Google's token endpoint over TLS (which Google's guide says may be trusted without a signature check), with its issuer, audience, expiry and `nonce` checked. It gives `sub`,
     `email` and `email_verified`.
   - **GitHub:** `GET /user` gives the numeric `id` (the stable identity, never the login name, which can be renamed), and `GET /user/emails`
     gives the **primary, verified** address.
4. Then, by intent:
   - **Known identity** (provider + ID already linked): sign in that user.
   - **Unknown identity, verified email matches a user:** link it (decision 2) and sign in.
   - **Unknown identity, new email, sign-in or sign-up:** create the account as Register does (personal organization, attribution), already
     verified, with no password. Signing in with an unknown Google account simply creates one: "Continue with" means either.
   - **connect** (from settings, signed in): link to the signed-in user, unless the identity belongs to someone else (an error says so).
   - **confirm**: the identity must be the signed-in user's; mark the session confirmed.
5. With two-factor on (or required and set up), no session is started yet: the API redirects to the existing code page with a challenge,
   exactly as after a password.
6. On success it sets the session cookie and redirects to `next` (only a relative path inside the app; anything else becomes the dashboard)
   with `#signed-in=google`. The app stores the method for "Last used" and removes the fragment from the address bar.
7. Errors (the person canceled at Google, an unverified email, a disabled account, the identity belongs to another user) redirect to the sign-in
   page with a short code that the page turns into a sentence. Provider error text is never shown or logged as-is.

### Data

Migration:

- `users.password_hash` becomes nullable (no password for social-only accounts). Login treats a missing hash like a wrong password, so the
  answer still doesn't reveal whether the account exists or how it signs in.
- New table `user_identities`: `user_id` (cascade on delete), `provider` (`google` or `github`), `subject` (the provider's ID), `email` (as the
  provider gave it, for the settings page), `created_at`, `last_used_at`; unique on `(provider, subject)`, and one identity per provider per
  user.
- `sessions.confirmed_at` (nullable), for decision 4.

The account's own email address never changes because of a provider: if someone's Google address changes, the identity still matches by ID.

Data export includes the identities (provider, email, dates); account deletion removes them with the user.

### Where it touches existing code

- `internal/socialauth` (new): the two providers behind one interface, the state cookie, and the code-for-identity exchange. HTTP calls to
  Google and GitHub use a client with timeouts; these are fixed hosts, so the SSRF-safe monitoring client isn't needed.
- `internal/auth`: `SignInWithIdentity` (find, link or create, then the same two-factor check and session as Login), `ConfirmSession`, and
  `SetPassword` for accounts that have none. Re-authentication checks (`VerifyPassword` callers) accept a confirmed session instead when the
  account has no password.
- `internal/httpapi`: the start and callback routes (per-IP limits like the other auth routes) and the settings endpoints
  (`GET/DELETE /account/sign-in-methods/{provider}`, `POST /account/password` when there's none).
- Security log: `sign_in` gains `method`; new actions `identity_connected` and `identity_disconnected`.
- Emails (`internal/email`): "Google was connected" and "GitHub was disconnected", sent to the account address.
- Web: the buttons on `/login` and `/register` (daisyUI login buttons, with Google's logo and colors as its guidelines require); the "Last used"
  badge; Sign-in methods in account settings; "Confirm with Google/GitHub" in the password prompts for accounts without a password; the
  "Sign in" wording sweep; error messages for the callback codes.
- Settings: `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET` in Infisical. A provider without settings
  isn't offered. `DISABLE_SOCIAL_SIGN_IN` hides the buttons and refuses the routes (people who only have a social sign-in then use "Forgot
  password" to set one, which works because their address is verified).

## Security

- `state` (and PKCE) stop forged callbacks and stolen codes; Google's `nonce` and ID-token checks stop replayed tokens.
- The state cookie is signed, short-lived and single-use; `next` must be a relative app path (no open redirect).
- Accounts are matched by provider ID, never by email alone; email is only used to link, and only when verified (decision 2).
- A disabled account stays disabled whichever way the person signs in.
- Two-factor is never skipped (decision 3); an organization's requirement applies as today.
- Nothing from the provider is trusted as an instruction: names aren't stored, provider error text isn't shown.
- To add to `docs/security.md` and `docs/security-review.md`: the residual risk that a compromised Google or GitHub account can sign in to a
  linked HawkEye account that has no two-factor. That is the same as a compromised mailbox today, through password reset.

## Legal and provider setup (your part)

- **Privacy Policy:** add that, if you choose to, we receive your email address and account ID from Google or GitHub, only to sign you in;
  the Terms need no change. A note goes in `docs/legal-placeholders.md` for counsel. Google's API Services User Data Policy needs the privacy
  policy link on the consent screen.
- **Google:** an OAuth client (Web application) in Google Cloud, with the redirect URI
  `https://hawkeye.observer/api/v1/auth/social/google/callback`. The `openid email` scopes need no app review. Showing HawkEye's name
  and logo on Google's screen needs **brand verification** (domain verified in Search Console, home page and privacy policy links), which takes
  a few days, so start it early.
- **GitHub:** an OAuth App (not a GitHub App) under the HawkEye organization, with the matching callback URL.
- Separate clients for local development, pointing at `localhost`.

## Tests

- Unit: the ID-token checks (issuer, audience, expiry, nonce), GitHub's primary-verified-email rule, the `next` path rule, and the
  link-or-create decision table.
- Integration, with fake Google and GitHub servers (`httptest`): sign-up, sign-in, automatic linking, refusing an unverified email, a disabled
  account, two-factor after a social sign-in, an organization's requirement, connect and disconnect (not the last method), confirming a session
  for deletion, a forged or reused `state`, and the kill switch.
- Web: the buttons, the "Last used" badge (set by each method, kept after sign-out), the error messages, and Sign-in methods in settings.
- In the real app (run-hawkeye skill) with the development clients, before release.

## Order of work

1. Migration, `internal/socialauth`, the callback with sign-in, sign-up and linking, and tests with the fake providers.
2. Two-factor after a social sign-in, confirming the session, and accounts without a password.
3. Web: the buttons, "Last used", the error messages and the wording sweep.
4. Sign-in methods in account settings, the emails and security log entries.
5. Docs (`docs/api.md`, `docs/security.md`, `docs/architecture.md`, the Getting Started guide, `llms.txt`, README), the Privacy Policy update
   once approved, and the landing FAQ ("Can I sign in with Google or GitHub?").

## Decisions that are yours

- Automatic linking by verified email (decision 2), or asking for the password first.
- Confirming sensitive actions by signing in with the provider again (decision 4).
- The Privacy Policy wording, and when to send it to counsel.
- Whether Microsoft or Apple sign-in should follow later.
