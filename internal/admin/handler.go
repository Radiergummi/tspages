package admin

import (
	"html/template"
	"net/http"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/storage"
	"tspages/internal/webhook"
)

// SiteStatus is the per-site data returned by the sites list endpoint.
type SiteStatus struct {
	Name                 string `json:"name"`
	ActiveDeploymentID   string `json:"active_deployment_id,omitempty"`
	Requests             int64  `json:"requests"`
	Sparkline            string `json:"sparkline,omitempty"`
	LastDeployedBy       string `json:"last_deployed_by,omitempty"`
	LastDeployedByAvatar string `json:"last_deployed_by_avatar,omitempty"`
	LastDeployedAt       string `json:"last_deployed_at,omitempty"`
	CanDeploy            bool   `json:"can_deploy,omitempty"`
	OGTitle              string `json:"og_title,omitempty"`
	OGDescription        string `json:"og_description,omitempty"`
	OGImage              string `json:"og_image,omitempty"`
	Favicon              string `json:"favicon,omitempty"`
}

// SitesResponse is the JSON response for GET /sites.
type SitesResponse struct {
	Admin     bool         `json:"admin"`
	User      UserInfo     `json:"user"`
	DNSSuffix string       `json:"dns_suffix"`
	Sites     []SiteStatus `json:"sites"`
}

// SiteDetailResponse is the JSON response for GET /sites/{site}.
type SiteDetailResponse struct {
	Site        SiteStatus               `json:"site"`
	Deployments []storage.DeploymentInfo `json:"deployments"`
}

// region shared deps

type handlerDeps struct {
	store     *storage.Store
	recorder  *analytics.Recorder
	dnsSuffix string
	defaults  storage.SiteConfig
}

// multipleFieldKeys returns the set of field keys configured as multiple
// across all form endpoints for the given site. It reads config from disk;
// use multipleFieldKeysFromConfig when you already have a merged config.
func (d *handlerDeps) multipleFieldKeys(site string) map[string]bool {
	if d.store == nil {
		return nil
	}
	deployCfg, _ := d.store.ReadCurrentDeploymentConfig(site)
	siteCfg, _ := d.store.ReadSiteConfig(site)
	merged := deployCfg.Merge(siteCfg).Merge(d.defaults)
	return multipleFieldKeysFromConfig(merged)
}

// multipleFieldKeysFromConfig returns the set of field keys configured as
// multiple across all form endpoints in the given config.
func multipleFieldKeysFromConfig(cfg storage.SiteConfig) map[string]bool {
	if cfg.Forms == nil {
		return nil
	}
	var keys map[string]bool
	for _, ep := range cfg.Forms.Endpoints {
		for _, f := range ep.Fields {
			if f.Multiple != nil && *f.Multiple {
				if keys == nil {
					keys = make(map[string]bool)
				}
				keys[f.Key] = true
			}
		}
	}
	return keys
}

// analyticsEnabled reports whether analytics are enabled for the given site
// by reading the current deployment's config and merging with server defaults.
func (d *handlerDeps) analyticsEnabled(site string) bool {
	deployCfg, _ := d.store.ReadCurrentDeploymentConfig(site)
	siteCfg, _ := d.store.ReadSiteConfig(site)
	merged := deployCfg.Merge(siteCfg).Merge(d.defaults)
	if merged.Analytics == nil {
		return true
	}
	return *merged.Analytics
}

// UserInfo holds user display data for templates.
type UserInfo struct {
	Name          string `json:"name"`
	ProfilePicURL string `json:"profile_pic_url,omitempty"`
	Admin         bool   `json:"admin,omitempty"`
	CanDeploy     bool   `json:"can_deploy,omitempty"`
}

func userInfo(identity auth.Identity, caps []auth.Cap) UserInfo {
	name := identity.DisplayName
	if name == "" {
		name = identity.LoginName
	}
	return UserInfo{Name: name, ProfilePicURL: identity.ProfilePicURL, Admin: auth.HasAdminCap(caps), CanDeploy: auth.HasDeployCap(caps)}
}

// SiteEnsurer is the subset of multihost.Manager needed to start a site server.
type SiteEnsurer interface {
	EnsureServer(site string) error
}

// SiteHealthChecker is the subset of multihost.Manager needed for health checks.
type SiteHealthChecker interface {
	IsRunning(site string) bool
}

// Handlers groups all admin HTTP handlers.
type Handlers struct {
	Sites            *SitesHandler
	Site             *SiteHandler
	Deployment       *DeploymentHandler
	CreateSite       *CreateSiteHandler
	Deployments      *DeploymentsHandler
	Analytics        *AnalyticsHandler
	PurgeAnalytics   *PurgeAnalyticsHandler
	AllAnalytics     *AllAnalyticsHandler
	Webhooks         *WebhooksHandler
	WebhookDetail    *WebhookDetailHandler
	WebhookRetry     *WebhookRetryHandler
	SiteWebhooks     *SiteWebhooksHandler
	SiteDeployments  *SiteDeploymentsHandler
	Help             *HelpHandler
	API              *APIHandler
	Feed             *FeedHandler
	SiteFeed         *SiteFeedHandler
	SiteHealth       *SiteHealthHandler
	SiteConfig            *SiteConfigHandler
	UpdateSiteConfig      *UpdateSiteConfigHandler
	PostSiteConfig        *PostSiteConfigHandler
	SubmissionsPage       *SubmissionsPageHandler
	Submissions           *SubmissionsHandler
	SubmissionDetail      *SubmissionDetailHandler
	DeleteSubmission      *DeleteSubmissionHandler
	BulkDeleteSubmissions *BulkDeleteSubmissionsHandler
	ExportSubmissions     *ExportSubmissionsHandler
	SubmissionAnalytics   *SubmissionAnalyticsHandler
}

func NewHandlers(store *storage.Store, recorder *analytics.Recorder, dnsSuffix string, ensurer SiteEnsurer, checker SiteHealthChecker, defaults storage.SiteConfig, notifier *webhook.Notifier) *Handlers {
	d := handlerDeps{store: store, recorder: recorder, dnsSuffix: dnsSuffix, defaults: defaults}
	wh := &WebhooksHandler{handlerDeps: d, notifier: notifier}
	return &Handlers{
		Sites:            &SitesHandler{d},
		Site:             &SiteHandler{handlerDeps: d, notifier: notifier},
		Deployment:       &DeploymentHandler{d},
		CreateSite:       &CreateSiteHandler{handlerDeps: d, ensurer: ensurer, notifier: notifier},
		Deployments:      &DeploymentsHandler{d},
		Analytics:        &AnalyticsHandler{d},
		PurgeAnalytics:   &PurgeAnalyticsHandler{d},
		AllAnalytics:     &AllAnalyticsHandler{d},
		Webhooks:         wh,
		WebhookDetail:    &WebhookDetailHandler{handlerDeps: d, notifier: notifier},
		WebhookRetry:     &WebhookRetryHandler{handlerDeps: d, notifier: notifier},
		SiteWebhooks:     &SiteWebhooksHandler{WebhooksHandler: wh},
		SiteDeployments:  &SiteDeploymentsHandler{d},
		Help:             &HelpHandler{},
		API:              &APIHandler{},
		Feed:             &FeedHandler{d},
		SiteFeed:         &SiteFeedHandler{d},
		SiteHealth:       &SiteHealthHandler{handlerDeps: d, checker: checker},
		SiteConfig:            &SiteConfigHandler{d},
		UpdateSiteConfig:      &UpdateSiteConfigHandler{handlerDeps: d, ensurer: ensurer},
		PostSiteConfig:        &PostSiteConfigHandler{handlerDeps: d, ensurer: ensurer},
		SubmissionsPage:       &SubmissionsPageHandler{d},
		Submissions:           &SubmissionsHandler{d},
		SubmissionDetail:      &SubmissionDetailHandler{d},
		DeleteSubmission:      &DeleteSubmissionHandler{d},
		BulkDeleteSubmissions: &BulkDeleteSubmissionsHandler{d},
		ExportSubmissions:     &ExportSubmissionsHandler{d},
		SubmissionAnalytics:   &SubmissionAnalyticsHandler{d},
	}
}

// region GET /help/{page...}

type HelpHandler struct{}

func (h *HelpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())

	pages := DocPages()

	slug := r.PathValue("page")
	if slug == "" {
		slug = pages[0].Slug
	}

	var current *DocPage
	for i := range pages {
		if pages[i].Slug == slug {
			current = &pages[i]
			break
		}
	}
	if current == nil {
		RenderError(w, r, http.StatusNotFound, "")
		return
	}

	result, err := RenderDoc(slug)
	if err != nil {
		RenderError(w, r, http.StatusNotFound, "")
		return
	}

	renderPage(w, r, helpTmpl, "help", struct {
		User    UserInfo
		Nav     []NavGroup
		Current DocPage
		Content template.HTML
		TOC     []TOCEntry
		Meta    DocMeta
	}{userInfo(identity, caps), DocNav(), *current, result.Content, result.TOC, result.Meta})
}

// region GET /api

type APIHandler struct{}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	renderPage(w, r, apiTmpl, "api", struct {
		User UserInfo
	}{userInfo(identity, caps)})
}
