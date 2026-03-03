package admin

import (
	"html/template"
	"net/http"

	"tspages/internal/analytics"
	"tspages/internal/auth"
	"tspages/internal/pushnotify"
	"tspages/internal/storage"
	"tspages/internal/web"
	"tspages/internal/webhooks"
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
	User      web.UserInfo `json:"user"`
	DNSSuffix string       `json:"dns_suffix"`
	Sites     []SiteStatus `json:"sites"`
}

// SiteDetailResponse is the JSON response for GET /sites/{site}.
type SiteDetailResponse struct {
	Site        SiteStatus               `json:"site"`
	Deployments []storage.DeploymentInfo `json:"deployments"`
}

// SiteEnsurer is the subset of multihost.Manager needed to start a site server.
type SiteEnsurer interface {
	EnsureServer(site string) error
}

// SiteHealthChecker is the subset of multihost.Manager needed for health checks.
type SiteHealthChecker interface {
	IsRunning(site string) bool
}

// DeliveryLister provides webhook delivery data for the site detail page.
type DeliveryLister interface {
	Fire(event string, site string, cfg storage.SiteConfig, data map[string]any)
	ListDeliveries(site, event, status string, limit, offset int) ([]webhooks.DeliverySummary, int, error)
}

// PushSubscriber manages push notification subscriptions.
type PushSubscriber interface {
	IsSubscribed(site, path, endpoint string) (bool, error)
	Subscribe(site, path, userLogin string, sub pushnotify.Subscription) error
	Unsubscribe(endpoint, site, path, userLogin string) error
}

// Config holds dependencies for admin route handlers.
type Config struct {
	Store     *storage.Store
	Recorder  *analytics.Recorder
	DNSSuffix string
	Ensurer   SiteEnsurer
	Checker   SiteHealthChecker
	Defaults  storage.SiteConfig
	Notifier  DeliveryLister
	Push      PushSubscriber
	VAPIDPub  string
}

// RegisterRoutes registers all admin UI routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, withAuth, withMutate, withQuery func(http.Handler) http.Handler, cfg Config) {
	sites := &SitesHandler{store: cfg.Store, recorder: cfg.Recorder, dnsSuffix: cfg.DNSSuffix, defaults: cfg.Defaults}
	site := &SiteHandler{store: cfg.Store, recorder: cfg.Recorder, dnsSuffix: cfg.DNSSuffix, defaults: cfg.Defaults, notifier: cfg.Notifier}
	deployment := &DeploymentHandler{store: cfg.Store, dnsSuffix: cfg.DNSSuffix}
	createSite := &CreateSiteHandler{store: cfg.Store, dnsSuffix: cfg.DNSSuffix, defaults: cfg.Defaults, ensurer: cfg.Ensurer, notifier: cfg.Notifier}
	deployments := &DeploymentsHandler{store: cfg.Store}
	siteDeployments := &SiteDeploymentsHandler{store: cfg.Store}
	help := &HelpHandler{}
	api := &APIHandler{}
	feed := &FeedHandler{store: cfg.Store, dnsSuffix: cfg.DNSSuffix}
	siteFeed := &SiteFeedHandler{store: cfg.Store, dnsSuffix: cfg.DNSSuffix}
	siteHealth := &SiteHealthHandler{store: cfg.Store, checker: cfg.Checker}
	siteConfig := &SiteConfigHandler{store: cfg.Store}
	updateConfig := &UpdateSiteConfigHandler{store: cfg.Store, ensurer: cfg.Ensurer}
	postConfig := &PostSiteConfigHandler{store: cfg.Store, defaults: cfg.Defaults, ensurer: cfg.Ensurer}
	vapidKey := &VAPIDKeyHandler{vapidPub: cfg.VAPIDPub}
	pushSubscribe := &PushSubscribeHandler{push: cfg.Push}

	// Site health check
	mux.Handle("GET /sites/{site}/healthz", withAuth(siteHealth))

	// Site creation
	mux.Handle("POST /sites", withMutate(createSite))

	// Browse routes
	mux.Handle("GET /sites", withQuery(sites))
	mux.Handle("GET /sites.json", withQuery(sites))
	mux.Handle("GET /sites/{site}", withQuery(site))
	mux.Handle("GET /sites/{site}/deployments", withQuery(siteDeployments))
	mux.Handle("GET /sites/{site}/deployments.json", withQuery(siteDeployments))
	mux.Handle("GET /sites/{site}/deployments/{id}", withQuery(deployment))

	// Site config
	mux.Handle("GET /sites/{site}/config", withAuth(siteConfig))
	mux.Handle("PATCH /sites/{site}/config", withMutate(updateConfig))
	mux.Handle("POST /sites/{site}/config", withMutate(postConfig))

	// Push notifications
	mux.Handle("GET /push/vapidkey", withAuth(vapidKey))
	mux.Handle("GET /push/subscribe", withAuth(pushSubscribe))
	mux.Handle("POST /push/subscribe", withMutate(pushSubscribe))
	mux.Handle("DELETE /push/subscribe", withMutate(pushSubscribe))

	// Service worker
	mux.Handle("GET /sw.js", ServiceWorkerHandler())

	// Feeds, help, assets, docs
	mux.Handle("GET /deployments", withQuery(deployments))
	mux.Handle("GET /deployments.json", withQuery(deployments))
	mux.Handle("GET /feed.atom", withQuery(feed))
	mux.Handle("GET /sites/{site}/feed.atom", withQuery(siteFeed))
	mux.Handle("GET /help", withAuth(help))
	mux.Handle("GET /help/{page...}", withAuth(help))
	mux.Handle("GET /assets/dist/{file...}", AssetHandler())
	mux.Handle("GET /api", withAuth(api))
	mux.Handle("GET /openapi.yaml", OpenAPIHandler())
	mux.Handle("GET /openapi", SwaggerUIHandler())
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
		web.RenderError(w, r, http.StatusNotFound, "")
		return
	}

	result, err := RenderDoc(slug)
	if err != nil {
		web.RenderError(w, r, http.StatusNotFound, "")
		return
	}

	web.RenderPage(w, r, helpTmpl, "help", struct {
		User    web.UserInfo
		Nav     []NavGroup
		Current DocPage
		Content template.HTML
		TOC     []TOCEntry
		Meta    DocMeta
	}{web.NewUserInfo(identity, caps), DocNav(), *current, result.Content, result.TOC, result.Meta})
}

// region GET /api

type APIHandler struct{}

func (h *APIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	caps := auth.CapsFromContext(r.Context())
	identity := auth.IdentityFromContext(r.Context())
	web.RenderPage(w, r, apiTmpl, "api", struct {
		User web.UserInfo
	}{web.NewUserInfo(identity, caps)})
}
