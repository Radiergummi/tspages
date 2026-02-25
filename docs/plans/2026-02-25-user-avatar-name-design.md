# User Avatar & Display Name Design

## Problem

Users are shown inconsistently across the UI:
- Navbar shows DisplayName (or LoginName fallback) as plain text, no avatar
- "Deployed by" columns store and show LoginName (email), never DisplayName
- Only the analytics "Top visitors" section renders avatars with initials fallback

Tailscale's WhoIs API provides `DisplayName`, `LoginName`, and `ProfilePicURL` — all three are already extracted in `tsadapter` but `ProfilePicURL` doesn't reach the templates.

## Design

### 1. Add `ProfilePicURL` to `Identity`

The `auth.Identity` struct gains a `ProfilePicURL` field. The middleware already has this data from `WhoIsResult`; it just needs to copy it over.

### 2. Pass structured user data to templates

Replace the plain `.User` string with a struct containing `Name` (DisplayName with LoginName fallback) and `ProfilePicURL`. All handlers pass this instead of calling `userName()`.

### 3. Store DisplayName and avatar in manifests

- Change `deploy/handler.go` to write `DisplayName` (with LoginName fallback) into `Manifest.CreatedBy`
- Add `CreatedByAvatar` field to `Manifest` and `DeploymentInfo` for the avatar URL
- No migration needed — app hasn't shipped yet

### 4. Reusable avatar template partial

Extract the avatar img + initials fallback pattern (already in analytics template) into a shared `{{template "avatar"}}` partial. Use it in:
- Navbar (layout.gohtml)
- Sites list (sites.gohtml)
- Site detail deployments table (site.gohtml)
- Global deployments feed (deployments.gohtml)
- Deployment detail (deployment.gohtml)

### 5. Analytics already works

The analytics "Top visitors" section already renders avatars correctly. No changes needed there beyond potentially reusing the shared partial for consistency.
