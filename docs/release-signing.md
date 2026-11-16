# Release signing

How a release becomes something a gateway will install, what the maintainer
has to set up once, how keys rotate, and what the device checks.

## The design in plain language

A gateway in someone's garage decides on its own whether to install an
update. The only thing stopping an attacker who controls the network, the
download server or even this GitHub repository from pushing malicious code to
10,000 devices is a signature the attacker cannot forge. So the private key
that makes that signature is the most valuable secret the project has, and
the design is built around never exposing it to CI and being able to replace
it if it leaks anyway.

Four ideas:

1. **Build and sign are separate jobs.** The `build` job in
   `.github/workflows/release.yml` compiles everything and writes
   `manifest.json`, a list of artifact names, sizes and SHA-256 hashes with a
   version, a monotonic version number and an expiry date. It has no secrets
   at all, so it could run on a pull request from a fork without risk. The
   `sign` job does one thing: sign that manifest. Devices download the
   manifest, verify its signature, then verify every artifact's hash against
   the manifest. Signing the manifest signs everything.

2. **The online key never leaves Infisical.** The release key is an Ed25519
   key created inside the project's self-hosted Infisical KMS with
   `isExportable: false`. The sign job sends the manifest bytes to the KMS
   sign endpoint and receives a signature back. There is no key file on the
   runner to leak, log or exfiltrate; the worst a compromised job can do is
   obtain signatures for files it already has, during the minutes a human
   approved it. Infisical Community edition supports this (ED25519 sign and
   verify are not plan gated; only the post-quantum ML-DSA algorithms are).

3. **Two tiers of keys.** An **offline root key** lives on hardware that is
   never on the network (a laptop that boots from a USB stick). It signs
   exactly one kind of file: a **delegation** that says "this online key is
   allowed to sign manifests until this date". If the online key is ever
   misused, the root signs a new delegation naming a replacement and the old
   key is useless for every device that has fetched the new delegation.
   Devices pin the root public key and a **backup root** so that losing the
   primary root is survivable too.

4. **A human approves every signature.** The sign job runs in a GitHub
   Environment named `release-signing` with required reviewers. Nothing gets
   signed while a maintainer is asleep. The job authenticates to Infisical
   with GitHub's OIDC token, bound to this repository and this environment,
   so a workflow in another repository or another branch cannot use the key
   even if it knew the Infisical URL and the key id.

The signature format is deliberately the simplest thing that works: a raw
Ed25519 signature over the exact bytes of the signed file, base64 encoded, on
one line in a `.sig` file next to it. Public keys are the 32 raw bytes, base64
encoded, on one line in a `.pub` file (lines starting with `#` are comments).
The verifier on the device is `crypto/ed25519.Verify` from the Go standard
library plus a base64 decode: no PKI, no certificate parsing, no third-party
crypto. The same format is produced by Infisical KMS (online), by
`openssl pkeyutl -rawin` (offline root) and checked by both.

## Key hierarchy

```
keys/root.pub            offline root, primary           pinned in the device
keys/root-backup.pub     offline root, backup            pinned in the device
        |
        | signs (offline, openssl)
        v
keys/delegation.json             names the online key(s), each with an id,
keys/delegation.json.sig         the public key and a not_after date
        |
        | authorises
        v
keys/release.pub                 online release key (public half)
Infisical KMS key                online release key (private half, not exportable,
                                 never leaves Infisical)
        |
        | signs, in CI, per release, server-side
        v
manifest.json.sig                covers manifest.json, which lists every
                                 artifact with its sha256 and size
```

Files committed under `keys/` are public. `.gitignore` already refuses
`*.key` and `*.pem` anywhere in the tree, so a private key cannot be added by
accident; `.pub`, `.json` and `.sig` files are plain text and commit
normally. Both roots and the delegation are code-owned by the maintainer
(`.github/CODEOWNERS`) so a change needs an explicit review.

### Key ids

A key id is the first 16 hex characters of SHA-256 over the 32 raw public key
bytes:

```sh
grep -v '^#' keys/release.pub | tail -n1 | base64 -d | sha256sum | cut -c1-16
```

It is a stable handle for the `revoked` list and for log lines; it carries no
trust by itself.

### delegation.json

```json
{
  "schema": 1,
  "issued": "2026-11-16T00:00:00Z",
  "online_keys": [
    {
      "id": "<key id as above>",
      "pub": "<the base64 line from keys/release.pub>",
      "not_after": "2027-11-16T00:00:00Z",
      "comment": "release key 2026-11, Infisical KMS"
    }
  ],
  "revoked": []
}
```

The sign job verifies `delegation.json.sig` with `keys/root.pub` (or the
backup), refuses to run if `keys/release.pub` is not in `online_keys`, is in
`revoked`, or is past `not_after`, and then additionally fetches the KMS key's
public key and refuses if it differs from `keys/release.pub`. Devices do the
same delegation check before they trust a manifest signature.

### manifest.json

Written by the build job. Fields:

| Field | Meaning |
| --- | --- |
| `schema` | Manifest format version, `1`. |
| `version` | Semver string from the tag, without the `v`. |
| `version_number` | Integer `MAJOR*1000000 + MINOR*10000 + PATCH*100 + T` where `T` is `99` for a release and the rc number for `-rc.N`. Devices only install a higher number than the one they run. This blocks rollback to an older signed release. |
| `created`, `expires` | RFC 3339 UTC. Devices reject a manifest past `expires` (90 days after build). This blocks a "freeze" attack where an attacker serves an old manifest forever to keep devices on a vulnerable release. |
| `commit` | Git commit the release was built from. |
| `artifacts[]` | `name`, `os`, `arch`, `sha256`, `size` for each bundle. |
| `images` | Container image references and digests, informational. |

`manifest.json.sig` is the Ed25519 signature over the exact bytes of
`manifest.json`. Any reformatting of the file invalidates it, which is
intended.

## One-time operator steps

Everything below is done by the maintainer, once. Nothing in CI works until
the last step flips the variable. OpenSSL 3 is assumed (`openssl version`).

### 1. Generate the root keys, offline

On a machine that is not connected to a network (boot a live USB, or use an
air-gapped laptop):

```sh
openssl genpkey -algorithm ed25519 -out root.pem
openssl genpkey -algorithm ed25519 -out root-backup.pem
# Encrypt them at rest; you will be asked for a passphrase each.
openssl pkey -in root.pem -aes-256-cbc -out root.enc.pem && shred -u root.pem
openssl pkey -in root-backup.pem -aes-256-cbc -out root-backup.enc.pem && shred -u root-backup.pem
# Public halves in the project's raw format.
openssl pkey -in root.enc.pem -pubout -outform DER | tail -c 32 | base64 -w0 > root.pub; echo >> root.pub
openssl pkey -in root-backup.enc.pem -pubout -outform DER | tail -c 32 | base64 -w0 > root-backup.pub; echo >> root-backup.pub
```

Use long passphrases and store them separately from the key files (password
manager for the phrases, two USB sticks in two places for the `.enc.pem`
files). Copy only `root.pub` and `root-backup.pub` to a networked machine and
commit them as `keys/root.pub` and `keys/root-backup.pub`, each with a
leading `# pws-gateway root 2026` comment line if you like.

The private halves never leave the offline media. They are used twice a year
at most.

### 2. Create the online release key in Infisical KMS

In the self-hosted Infisical, in a project dedicated to release signing:

1. KMS, Add key: name `pws-gateway-release-2026-11`, key usage
   **Sign/Verify**, algorithm **ED25519**, **exportable: off**, delete
   protection on. Through the API this is
   `POST /api/v1/kms/keys` with `{"keyUsage": "SIGN_VERIFY", "algorithm":
   "ED25519", "isExportable": false, "hasDeleteProtection": true, ...}`.
2. Record the key id (a UUID).
3. Fetch its public key and convert to the project format:

   ```sh
   curl -sS -H "Authorization: Bearer $TOKEN" \
     "$INFISICAL_URL/api/v1/kms/keys/$KEY_ID/public-key" \
     | jq -r .publicKey | base64 -d | tail -c 32 | base64 -w0 > release.pub; echo >> release.pub
   ```

   (`publicKey` is base64 of the DER SubjectPublicKeyInfo, 44 bytes for
   Ed25519; the last 32 are the raw key.) Commit as `keys/release.pub`.
4. Create a **machine identity**, for example
   `github-pws-gateway-release-signing`, add it to the project with a role
   that grants only **KMS: Sign**, **KMS: Verify** and **KMS: Read** (public
   key) on that key. No secrets access at all.
5. Add an **OIDC auth** method to the identity:
   - OIDC discovery URL and issuer:
     `https://token.actions.githubusercontent.com`
   - Subject (exact, no glob):
     `repo:wxalerts@245692446/pws-gateway@1460271447:environment:release-signing`
     This is GitHub's immutable subject form with the owner and repository
     ids, which this repository uses. Confirm the prefix with
     `gh api repos/wxalerts/pws-gateway/actions/oidc/customization/sub`.
     If the environment is ever renamed, this value changes with it.
   - Audience: a fixed string of your choosing, for example
     `pws-gateway-release-signing`. The workflow requests a token with this
     audience, so it must match the repository variable below exactly.
   - Access token TTL: 300 seconds. The job uses the token for four API
     calls and then revokes it. Max TTL the same; number of uses 10 if the
     instance offers a limit.
6. Record the identity id (a UUID).

### 3. Write and sign the delegation, offline

Back on the offline machine, write `delegation.json` as above with the
release key's id and public key line, then:

```sh
openssl pkeyutl -sign -rawin -inkey root.enc.pem -in delegation.json -out delegation.sig
base64 -w0 delegation.sig > delegation.json.sig; echo >> delegation.json.sig
```

Check it before leaving the offline machine:

```sh
{ printf '\x30\x2a\x30\x05\x06\x03\x2b\x65\x70\x03\x21\x00'; base64 -d root.pub; } > root.der
openssl pkeyutl -verify -rawin -pubin -keyform DER -inkey root.der -in delegation.json -sigfile delegation.sig
```

Commit `keys/delegation.json` and `keys/delegation.json.sig`.

### 4. Bridge the LAN

Infisical is LAN only. A GitHub hosted runner cannot reach it. Pick one
before enabling signing:

- **Tailscale ephemeral node in the job (recommended by the audit).** Create
  an OAuth client in the tailnet admin console scoped to `auth_keys` with the
  tag `tag:ci-release-signing`, and an ACL that lets that tag reach only the
  Infisical host on port 443. Put the client id and secret as **environment
  secrets** on `release-signing` named `TS_OAUTH_CLIENT_ID` and
  `TS_OAUTH_SECRET`, and set the repository variable
  `RELEASE_SIGNING_BRIDGE=tailscale`. The job joins as an ephemeral node
  (removed when the job ends) and `INFISICAL_URL` is the host's tailnet
  address. These secrets grant network reach only; without the OIDC token
  from this environment they cannot sign anything.
- **A self-hosted runner on the LAN**, selected with `runs-on` for the sign
  job only. Understand the risk first: a self-hosted runner in a public
  repository runs whatever workflow code reaches it. It must be ephemeral
  (one job per registration), on its own VLAN with access to Infisical and
  nothing else, and the label must never appear in `ci.yml`. GitHub also
  recommends against self-hosted runners on public repositories in general.

The `sign` job marks both the bridge step and the Infisical calls as
placeholders until this is done.

### 5. Create the GitHub Environment

Repository settings, Environments, New environment: `release-signing`.

- Required reviewers: the maintainer (and a second person once there is
  one). GitHub allows up to six.
- Deployment branches and tags: "Selected branches and tags", add a tag rule
  `v*` and nothing else. Combined with the workflow's own `if`, this means
  only a `v*` tag can ever enter the environment.
- Wait timer: 0. The reviewer gate is the control.
- Environment secrets: only the Tailscale OAuth pair if that bridge is used.
  No signing material of any kind.

### 6. Set repository variables

Settings, Secrets and variables, Actions, Variables (not Secrets; none of
these are confidential and keeping them visible makes the workflow
auditable):

| Variable | Value |
| --- | --- |
| `RELEASE_SIGNING_ENABLED` | `false` until everything above is done, then `true`. |
| `RELEASE_SIGNING_BRIDGE` | `tailscale` to enable the tailnet step, otherwise unset. |
| `INFISICAL_URL` | Base URL the runner can reach, no trailing slash. |
| `INFISICAL_IDENTITY_ID` | The machine identity's UUID. |
| `INFISICAL_OIDC_AUDIENCE` | The audience string configured on the identity. |
| `INFISICAL_KMS_KEY_ID` | The KMS key's UUID. |
| `RELEASE_BUCKET` | Object storage destination for device downloads, once chosen. Leave unset to skip. |

### 7. Enable

Set `RELEASE_SIGNING_ENABLED` to `true`, push a `v0.0.1-rc.1` tag, approve
the environment prompt and check the run summary. The "Signing status" job
prints in plain words whether signing ran. The publish job creates an
unsigned release as a **draft**, so a mistake here never becomes the
"latest" release.

## What the sign job does, step by step

1. Verifies `keys/delegation.json.sig` with `keys/root.pub`, then
   `keys/root-backup.pub`, using `openssl pkeyutl -verify -rawin`.
2. Checks `keys/release.pub` is in `online_keys`, not in `revoked`, and not
   past `not_after`.
3. Joins the tailnet if `RELEASE_SIGNING_BRIDGE=tailscale`.
4. Requests a GitHub OIDC token with `aud=$INFISICAL_OIDC_AUDIENCE`.
5. `POST /api/v1/auth/oidc-auth/login` with `{"identityId", "jwt"}`;
   receives `accessToken`.
6. `GET /api/v1/kms/keys/{keyId}/public-key`; strips the 12 byte SPKI prefix
   and compares the raw key with `keys/release.pub`. Mismatch fails the job.
7. `POST /api/v1/kms/keys/{keyId}/sign` with `{"data": base64(manifest.json),
   "signingAlgorithm": "ED25519", "isDigest": false}`; writes the returned
   base64 `signature` to `manifest.json.sig`. (Ed25519 signs the full
   message; Infisical rejects `isDigest: true` for it.)
8. `POST /api/v1/kms/keys/{keyId}/verify` with the same data and signature;
   requires `signatureValid: true`.
9. `POST /api/v1/auth/token/revoke` to end the session.
10. Verifies `manifest.json.sig` locally with OpenSSL against
    `keys/release.pub`, independent of Infisical. This is the device's check.
11. Uploads the signature and the `keys/` files for the publish job.

The call shapes were read from the Infisical backend source
(`backend/src/server/routes/v1/cmek-router.ts`, mounted at `/api/v1/kms`,
and `identity-oidc-auth-router.ts`) on 2026-11-16. Re-check them against the
instance's own `/api/docs` before the first real run.

## Fallback: a pulled online key

If server-side KMS signing turns out to be unavailable on the instance, the
fallback is the earlier design: store an online private key as an Infisical
**secret**, give the identity read access to that one secret path, and have
the sign job fetch it with
`GET /api/v3/secrets/raw/{secretName}?workspaceId=...&environment=...&secretPath=...`
into a `mktemp` file with `umask 077`, sign, then `shred -u` it in an
`if: always()` step.

Keep the signature format identical so the device does not change: generate
the key with `openssl genpkey -algorithm ed25519`, store the PEM as the
secret, and sign with `openssl pkeyutl -sign -rawin -inkey key.pem`. The
original design named minisign for this role; minisign would also work but
its default signature mode prehashes with BLAKE2b and its files carry trusted
comments, which means a second verifier format on the device. Do not mix the
two.

The fallback is weaker than KMS signing in one way: the private key exists on
the runner for a few seconds, so a compromised action in the job could copy
it. Rotate the key as soon as KMS signing is available again.

## Rotation

### Online release key (planned, yearly, or on suspicion of misuse)

1. Create a new KMS key (step 2) and commit its `keys/release.pub`.
2. On the offline machine, add the new key to `online_keys` in
   `delegation.json`, move the old key's id into `revoked`, sign (step 3).
3. Commit `keys/release.pub`, `keys/delegation.json` and
   `keys/delegation.json.sig` in one pull request. CODEOWNERS makes the
   maintainer review it. Update `INFISICAL_KMS_KEY_ID`.
4. Tag a release. Devices fetch the new delegation with the manifest and
   from then on reject the old key. Disable the old KMS key in Infisical
   after that release is out.

If misuse is confirmed rather than suspected, also set
`RELEASE_SIGNING_ENABLED=false` until the rotation lands, and tell users
through the announcements channel which releases are affected. Infisical's
audit log shows every sign call with the identity that made it.

### Root key (rare: compromise, or hardware loss)

The backup root exists for this. Sign the next delegation with
`root-backup.enc.pem` instead; the sign job and the devices accept either
root. Then generate a fresh pair of roots offline, commit them as the new
`root.pub` and `root-backup.pub`, and ship a release whose delegation is
signed by the **old backup** root (so current devices accept it) and whose
bundle contains the **new** roots. From the following release on, sign with
the new root. Devices that skipped the transition release can still verify
the chain because the transition release remains signed by a root they know.

Losing **both** roots means devices in the field can never again verify an
update; the only recovery is reflashing SD cards. Keep the two root key
sticks in different buildings.

## What the device verifies

In order, stopping at the first failure. Nothing is installed until all pass.

1. Download `manifest.json`, `manifest.json.sig`, `delegation.json` and
   `delegation.json.sig` from the release channel on object storage.
2. Verify `delegation.json.sig` against the pinned `root.pub`, then
   `root-backup.pub` if that fails (`ed25519.Verify` over the file bytes).
   Pinned means compiled into the binary and compared against the files
   shipped in the bundle; a bundle that tries to change the roots is
   rejected unless it is itself signed by a current root.
3. Parse the delegation. For each key in `online_keys` that is not in
   `revoked` and whose `not_after` is in the future, try to verify
   `manifest.json.sig` over the bytes of `manifest.json`. One must succeed.
4. Parse the manifest. Check `expires` is in the future and `version_number`
   is greater than the running version's number. Both use the device clock,
   which is why the gateway never trusts time until NTP has synced (README).
5. Download the artifact for the device's architecture, compute SHA-256,
   compare with the manifest entry, compare the size too.
6. Stage it into the inactive slot, reboot into it once, and only mark it
   permanent after the new version reports healthy. Otherwise roll back
   automatically.

The device has no other trust input: no key from the server, no key from the
web UI, no trust on first use. Everything reduces to the two root public keys
and the clock.
