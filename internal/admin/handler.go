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

// RegisterRoutes registers all admin UI routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, withAuth, withMutate func(http.Handler) http.Handler,
	store *storage.Store, recorder *analytics.Recorder, dnsSuffix string,
	ensurer SiteEnsurer, checker SiteHealthChecker, defaults storage.SiteConfig,
	notifier *webhooks.Notifier, pushNotifier *pushnotify.Notifier, vapidPub string) {

	sites := &SitesHandler{store: store, recorder: recorder, dnsSuffix: dnsSuffix, defaults: defaults}
	site := &SiteHandler{store: store, recorder: recorder, dnsSuffix: dnsSuffix, defaults: defaults, notifier: notifier}
	deployment := &DeploymentHandler{store: store, dnsSuffix: dnsSuffix}
	createSite := &CreateSiteHandler{store: store, dnsSuffix: dnsSuffix, defaults: defaults, ensurer: ensurer, notifier: notifier}
	deployments := &DeploymentsHandler{store: store}
	siteDeployments := &SiteDeploymentsHandler{store: store}
	help := &HelpHandler{}
	api := &APIHandler{}
	feed := &FeedHandler{store: store, dnsSuffix: dnsSuffix}
	siteFeed := &SiteFeedHandler{store: store, dnsSuffix: dnsSuffix}
	siteHealth := &SiteHealthHandler{store: store, checker: checker}
	siteConfig := &SiteConfigHandler{store: store}
	updateConfig := &UpdateSiteConfigHandler{store: store, ensurer: ensurer}
	postConfig := &PostSiteConfigHandler{store: store, defaults: defaults, ensurer: ensurer}
	vapidKey := &VAPIDKeyHandler{vapidPub: vapidPub}
	pushSubscribe := &PushSubscribeHandler{push: pushNotifier}

	// Site health check
	mux.Handle("GET /sites/{site}/healthz", withAuth(siteHealth))

	// Site creation
	mux.Handle("POST /sites", withMutate(createSite))

	// Browse routes
	mux.Handle("GET /sites", withAuth(sites))
	mux.Handle("GET /sites.json", withAuth(sites))
	mux.Handle("GET /sites/{site}", withAuth(site))
	mux.Handle("GET /sites/{site}/deployments", withAuth(siteDeployments))
	mux.Handle("GET /sites/{site}/deployments.json", withAuth(siteDeployments))
	mux.Handle("GET /sites/{site}/deployments/{id}", withAuth(deployment))

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
	mux.Handle("GET /deployments", withAuth(deployments))
	mux.Handle("GET /deployments.json", withAuth(deployments))
	mux.Handle("GET /feed.atom", withAuth(feed))
	mux.Handle("GET /sites/{site}/feed.atom", withAuth(siteFeed))
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
