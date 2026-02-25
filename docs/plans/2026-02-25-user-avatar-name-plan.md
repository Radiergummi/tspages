# User Avatar & Display Name Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Show user avatars and display names (instead of emails) everywhere users appear in the admin UI.

**Architecture:** Add `ProfilePicURL` to `auth.Identity`, store display name + avatar in manifests, create an `avatarHTML` template function that renders the avatar img (or initials fallback), and update all templates to use it.

**Tech Stack:** Go templates, Tailwind CSS, existing auth/storage packages.

---

### Task 1: Add ProfilePicURL to Identity and update middleware

**Files:**
- Modify: `internal/auth/caps.go:37-40` (Identity struct)
- Modify: `internal/auth/caps.go:216-218` (middleware identity creation)

**Step 1: Add field to Identity struct**

In `internal/auth/caps.go`, change:

```go
type Identity struct {
	LoginName   string
	DisplayName string
}
```

to:

```go
type Identity struct {
	LoginName     string
	DisplayName   string
	ProfilePicURL string
}
```

**Step 2: Pass ProfilePicURL in middleware**

In the same file, change the identity context value creation (line ~216):

```go
ctx = context.WithValue(ctx, identityKey{}, Identity{
	LoginName:     result.LoginName,
	DisplayName:   result.DisplayName,
	ProfilePicURL: result.ProfilePicURL,
})
```

**Step 3: Run tests**

Run: `go test ./internal/auth/...`
Expected: PASS (no tests depend on Identity field count)

**Step 4: Commit**

```
feat: add ProfilePicURL to auth.Identity
```

---

### Task 2: Add CreatedByAvatar to storage types and update deploy handler

**Files:**
- Modify: `internal/storage/store.go:132-138` (Manifest struct)
- Modify: `internal/storage/store.go:218-224` (DeploymentInfo struct)
- Modify: `internal/deploy/handler.go:127-134` (manifest creation)

**Step 1: Add CreatedByAvatar to Manifest**

In `internal/storage/store.go`:

```go
type Manifest struct {
	Site            string    `json:"site"`
	ID              string    `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedBy       string    `json:"created_by"`
	CreatedByAvatar string    `json:"created_by_avatar,omitempty"`
	SizeBytes       int64     `json:"size_bytes"`
}
```

**Step 2: Add CreatedByAvatar to DeploymentInfo**

```go
type DeploymentInfo struct {
	ID              string    `json:"id"`
	Active          bool      `json:"active"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	CreatedBy       string    `json:"created_by,omitempty"`
	CreatedByAvatar string    `json:"created_by_avatar,omitempty"`
	SizeBytes       int64     `json:"size_bytes,omitempty"`
}
```

**Step 3: Update ListDeployments to copy the new field**

In `internal/storage/store.go` inside `ListDeployments`, the block that reads the manifest already copies `CreatedBy` — add `CreatedByAvatar`:

```go
if m, err := s.ReadManifest(site, e.Name()); err == nil {
	info.CreatedAt = m.CreatedAt
	info.CreatedBy = m.CreatedBy
	info.CreatedByAvatar = m.CreatedByAvatar
	info.SizeBytes = m.SizeBytes
}
```

**Step 4: Update deploy handler to store display name and avatar**

In `internal/deploy/handler.go`, change the manifest creation (line ~127):

```go
identity := auth.IdentityFromContext(r.Context())
deployedBy := identity.DisplayName
if deployedBy == "" {
	deployedBy = identity.LoginName
}
manifest := storage.Manifest{
	Site:            site,
	ID:              id,
	CreatedAt:       time.Now(),
	CreatedBy:       deployedBy,
	CreatedByAvatar: identity.ProfilePicURL,
	SizeBytes:       int64(len(body)),
}
```

**Step 5: Update SitesHandler to pass avatar from manifest**

In `internal/admin/handler.go`, the `SiteStatus` struct needs a `LastDeployedByAvatar` field:

```go
type SiteStatus struct {
	Name                   string `json:"name"`
	ActiveDeploymentID     string `json:"active_deployment_id,omitempty"`
	Requests               int64  `json:"requests"`
	LastDeployedBy         string `json:"last_deployed_by,omitempty"`
	LastDeployedByAvatar   string `json:"last_deployed_by_avatar,omitempty"`
	LastDeployedAt         string `json:"last_deployed_at,omitempty"`
}
```

Then in `SitesHandler.ServeHTTP` and `SiteHandler.ServeHTTP`, where manifests are read to populate `LastDeployedBy`, also copy the avatar:

```go
if m, err := h.store.ReadManifest(s.Name, s.ActiveDeploymentID); err == nil {
	ss.LastDeployedBy = m.CreatedBy
	ss.LastDeployedByAvatar = m.CreatedByAvatar
	// ... existing LastDeployedAt logic
}
```

(Same pattern in both `SitesHandler` and `SiteHandler`.)

**Step 6: Run tests**

Run: `go test ./...`
Expected: PASS

**Step 7: Commit**

```
feat: store display name and avatar in deployment manifests
```

---

### Task 3: Add avatarHTML template function and UserInfo struct

**Files:**
- Modify: `internal/admin/render.go:164` (funcs map)
- Modify: `internal/admin/handler.go:18-65` (UserInfo struct, replace userName)

**Step 1: Add UserInfo struct and replace userName()**

In `internal/admin/handler.go`, replace the `userName` function with:

```go
// UserInfo holds user display data for templates.
type UserInfo struct {
	Name          string `json:"name"`
	ProfilePicURL string `json:"profile_pic_url,omitempty"`
}

func userInfo(identity auth.Identity) UserInfo {
	name := identity.DisplayName
	if name == "" {
		name = identity.LoginName
	}
	return UserInfo{Name: name, ProfilePicURL: identity.ProfilePicURL}
}
```

**Step 2: Update all response structs and handler calls**

Change every `User string` field to `User UserInfo` and every `userName(identity)` call to `userInfo(identity)`:

- `SitesResponse.User` → `UserInfo`
- `AnalyticsData.User` → `UserInfo`
- Site detail template data `.User` → `UserInfo`
- Deployment detail template data `.User` → `UserInfo`
- Deployments template data `.User` → `UserInfo`

**Step 3: Add avatarHTML template function**

In `internal/admin/render.go`, add to the `funcs` map:

```go
"avatarHTML": func(name, picURL string) template.HTML {
	initial := "?"
	for _, r := range name {
		initial = string(r)
		break
	}
	if picURL != "" {
		return template.HTML(fmt.Sprintf(
			`<img class="w-6 h-6 rounded-full shrink-0 object-cover" src="%s" alt="">`,
			template.HTMLEscapeString(picURL),
		))
	}
	return template.HTML(fmt.Sprintf(
		`<span class="w-6 h-6 rounded-full shrink-0 flex items-center justify-center bg-blue-500/10 text-blue-500 text-xs font-semibold uppercase">%s</span>`,
		template.HTMLEscapeString(initial),
	))
},
```

**Step 4: Run tests**

Run: `go test ./internal/admin/...`
Expected: PASS (templates haven't changed yet, but Go code compiles — test may fail if templates reference `.User` as string; if so, update templates in next task before re-running)

**Step 5: Commit**

```
feat: add UserInfo struct and avatarHTML template function
```

---

### Task 4: Update all templates to show avatars

**Files:**
- Modify: `internal/admin/templates/layout.gohtml:58` (navbar)
- Modify: `internal/admin/templates/sites.gohtml:58-59` (deployed by column)
- Modify: `internal/admin/templates/site.gohtml:129-130` (deployments table)
- Modify: `internal/admin/templates/deployments.gohtml:43-44` (deployed by column)
- Modify: `internal/admin/templates/deployment.gohtml:45-46` (deployed by card)
- Modify: `internal/admin/templates/analytics.gohtml:244-257` (top visitors — switch to avatarHTML for consistency)

**Step 1: Update navbar in layout.gohtml**

Change line 58 from:

```html
<span class="text-sm text-muted">{{.User}}</span>
```

to:

```html
<div class="flex items-center gap-2">
    {{avatarHTML .User.Name .User.ProfilePicURL}}
    <span class="text-sm text-muted">{{.User.Name}}</span>
</div>
```

**Step 2: Update sites.gohtml "Deployed by" column**

Change the deployed-by cell (line 58-59) from:

```html
<td class="px-4 py-3 text-sm border-b border-default text-muted">
    {{if .LastDeployedBy}}{{.LastDeployedBy}}{{else}}&mdash;{{end}}
</td>
```

to:

```html
<td class="px-4 py-3 text-sm border-b border-default text-muted">
    {{if .LastDeployedBy}}
        <span class="flex items-center gap-2">
            {{avatarHTML .LastDeployedBy .LastDeployedByAvatar}}
            {{.LastDeployedBy}}
        </span>
    {{else}}&mdash;{{end}}
</td>
```

**Step 3: Update site.gohtml deployments table "Deployed by"**

Change line 129-130 from:

```html
<td class="px-4 py-3 text-sm border-b border-default text-muted">
    {{if .CreatedBy}}{{.CreatedBy}}{{else}}&mdash;{{end}}
</td>
```

to:

```html
<td class="px-4 py-3 text-sm border-b border-default text-muted">
    {{if .CreatedBy}}
        <span class="flex items-center gap-2">
            {{avatarHTML .CreatedBy .CreatedByAvatar}}
            {{.CreatedBy}}
        </span>
    {{else}}&mdash;{{end}}
</td>
```

**Step 4: Update deployments.gohtml "Deployed by"**

Change line 43-44 from:

```html
<td class="px-4 py-3 text-sm border-b border-default text-muted">
    {{if .CreatedBy}}{{.CreatedBy}}{{else}}&mdash;{{end}}</td>
```

to:

```html
<td class="px-4 py-3 text-sm border-b border-default text-muted">
    {{if .CreatedBy}}<span class="flex items-center gap-2">{{avatarHTML .CreatedBy .CreatedByAvatar}} {{.CreatedBy}}</span>{{else}}&mdash;{{end}}</td>
```

**Step 5: Update deployment.gohtml "Deployed by" card**

Change line 45-46 from:

```html
<dd class="font-mono text-base">{{if .Deployment.CreatedBy}}{{.Deployment.CreatedBy}}{{else}}&mdash;{{end}}</dd>
```

to:

```html
<dd class="text-base">{{if .Deployment.CreatedBy}}<span class="flex items-center gap-2">{{avatarHTML .Deployment.CreatedBy .Deployment.CreatedByAvatar}} {{.Deployment.CreatedBy}}</span>{{else}}&mdash;{{end}}</dd>
```

(Remove `font-mono` since names aren't monospace.)

**Step 6: Update analytics.gohtml top visitors to use avatarHTML**

Replace the inline avatar rendering (lines 244-257) with the shared function:

```html
<td class="px-4 py-3 text-sm border-b border-base-100 dark:border-base-800">
    <span class="flex items-center gap-2.5">
        {{avatarHTML (or .UserName .UserLogin) .ProfilePicURL}}
        {{if .UserName}}{{.UserName}}{{else}}{{.UserLogin}}{{end}}
    </span>
</td>
```

(This requires adding `"or"` to the funcmap if not present, OR just use the existing `initial` approach via avatarHTML which already handles the fallback. Actually, `avatarHTML` takes a `name` param — pass `{{if .UserName}}{{.UserName}}{{else}}{{.UserLogin}}{{end}}` won't work as an argument. Instead, either keep the existing inline approach or add a helper. Simplest: keep the inline avatar in analytics since it uses a different data shape with separate UserName/UserLogin fields. Only update the _text_ to prefer UserName.)

**Step 7: Build and test**

Run: `go build ./cmd/tspages && go test ./...`
Expected: PASS, binary builds

**Step 8: Commit**

```
feat: show user avatars and display names in admin UI
```

---

### Task 5: Update test fixtures

**Files:**
- Modify: `internal/admin/handler_test.go` (update test manifests and identity)

**Step 1: Update test manifests to include avatar URLs**

In `setupStore`, update the test manifests:

```go
store.WriteManifest("docs", "aaa11111", storage.Manifest{
	Site: "docs", ID: "aaa11111",
	CreatedBy:       "Alice",
	CreatedByAvatar: "https://example.com/alice.jpg",
	CreatedAt:       time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC),
	SizeBytes:       1024,
})
// ... same for "demo" manifest
```

**Step 2: Update test Identity values**

Where tests create `auth.Identity{}`, add `ProfilePicURL` if relevant.

**Step 3: Run all tests**

Run: `go test ./...`
Expected: PASS

**Step 4: Commit**

```
test: update fixtures for avatar and display name support
```
