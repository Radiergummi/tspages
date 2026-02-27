package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	ErrActiveDeployment   = errors.New("cannot delete active deployment")
	ErrDeploymentExists   = errors.New("deployment already exists")
	ErrDeploymentNotFound = errors.New("deployment not found")
	ErrSiteExists         = errors.New("site already exists")
)

type Store struct {
	dataDir string
}

type SiteInfo struct {
	Name               string `json:"name"`
	ActiveDeploymentID string `json:"active_deployment_id"`
}

func New(dataDir string) *Store {
	return &Store{dataDir: dataDir}
}

func NewDeploymentID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ValidSiteName reports whether name is a safe site identifier.
// Names must be valid DNS labels: lowercase alphanumeric and hyphens,
// no leading/trailing hyphens, max 63 characters.
func ValidSiteName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return name[0] != '-' && name[len(name)-1] != '-'
}

// MaxSiteNameLen returns the maximum site name length given a tailnet DNS suffix.
// A full hostname ({site}.{suffix}) must not exceed 253 characters.
// The result is capped at 63 (the DNS label limit).
func MaxSiteNameLen(dnsSuffix string) int {
	if dnsSuffix == "" {
		return 63
	}
	// 253 total - 1 dot - len(suffix)
	max := 253 - 1 - len(dnsSuffix)
	if max > 63 {
		return 63
	}
	if max < 1 {
		return 1
	}
	return max
}

// ValidSiteNameForSuffix is like ValidSiteName but also checks that
// {name}.{dnsSuffix} fits within the 253-character DNS name limit.
func ValidSiteNameForSuffix(name, dnsSuffix string) bool {
	return ValidSiteName(name) && len(name) <= MaxSiteNameLen(dnsSuffix)
}

// ValidDeploymentID reports whether id is safe to use as a deployment path component.
func ValidDeploymentID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, "/\\")
}

// CreateSite creates the directory structure for a new site.
// Returns ErrSiteExists if the site directory already exists.
func (s *Store) CreateSite(name string) error {
	if !ValidSiteName(name) {
		return fmt.Errorf("invalid site name: %q", name)
	}
	if err := os.MkdirAll(filepath.Join(s.dataDir, "sites"), 0755); err != nil {
		return fmt.Errorf("create sites dir: %w", err)
	}
	dir := filepath.Join(s.dataDir, "sites", name)
	if err := os.Mkdir(dir, 0755); err != nil {
		if os.IsExist(err) {
			return ErrSiteExists
		}
		return err
	}
	return os.Mkdir(filepath.Join(dir, "deployments"), 0755)
}

func (s *Store) CreateDeployment(site, id string) (string, error) {
	if !ValidSiteName(site) {
		return "", fmt.Errorf("invalid site name: %q", site)
	}
	parent := filepath.Join(s.dataDir, "sites", site, "deployments")
	if err := os.MkdirAll(parent, 0755); err != nil {
		return "", fmt.Errorf("create deployments dir: %w", err)
	}
	dir := filepath.Join(parent, id)
	if err := os.Mkdir(dir, 0755); err != nil {
		if os.IsExist(err) {
			return "", ErrDeploymentExists
		}
		return "", fmt.Errorf("create deployment dir: %w", err)
	}
	return dir, nil
}

func (s *Store) MarkComplete(site, id string) error {
	marker := filepath.Join(s.dataDir, "sites", site, "deployments", id, ".complete")
	return os.WriteFile(marker, nil, 0644)
}

func (s *Store) MarkFailed(site, id, reason string) error {
	marker := filepath.Join(s.dataDir, "sites", site, "deployments", id, ".failed")
	return os.WriteFile(marker, []byte(reason), 0644)
}

func (s *Store) ActivateDeployment(site, id string) error {
	if !ValidDeploymentID(id) {
		return ErrDeploymentNotFound
	}
	depDir := filepath.Join(s.dataDir, "sites", site, "deployments", id)
	if _, err := os.Stat(depDir); err != nil {
		return fmt.Errorf("deployment not found: %w", err)
	}

	link := filepath.Join(s.dataDir, "sites", site, "current")
	target := filepath.Join("deployments", id)

	// Atomic swap: create temp symlink, rename over current
	tmp := link + ".tmp"
	os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}
	if err := os.Rename(tmp, link); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("swap symlink: %w", err)
	}
	return nil
}

// Manifest holds metadata about a deployment.
type Manifest struct {
	Site            string    `json:"site"`
	ID              string    `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedBy       string    `json:"created_by"`
	CreatedByAvatar string    `json:"created_by_avatar,omitempty"`
	SizeBytes       int64     `json:"size_bytes"`
}

func (s *Store) WriteManifest(site, id string, m Manifest) error {
	path := filepath.Join(s.dataDir, "sites", site, "deployments", id, "manifest.json")
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

func (s *Store) ReadManifest(site, id string) (Manifest, error) {
	path := filepath.Join(s.dataDir, "sites", site, "deployments", id, "manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	return m, nil
}

func (s *Store) CurrentDeployment(site string) (string, error) {
	link := filepath.Join(s.dataDir, "sites", site, "current")
	target, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("no active deployment for site %q: %w", site, err)
	}
	return filepath.Base(target), nil
}

func (s *Store) SiteRoot(site string) string {
	return filepath.Join(s.dataDir, "sites", site, "current", "content")
}

// GetSite returns info for a single site, or an error if it doesn't exist.
func (s *Store) GetSite(name string) (SiteInfo, error) {
	if !ValidSiteName(name) {
		return SiteInfo{}, fmt.Errorf("invalid site name: %q", name)
	}
	dir := filepath.Join(s.dataDir, "sites", name)
	fi, err := os.Stat(dir)
	if err != nil {
		return SiteInfo{}, err
	}
	if !fi.IsDir() {
		return SiteInfo{}, fmt.Errorf("not a directory: %s", dir)
	}
	info := SiteInfo{Name: name}
	if id, err := s.CurrentDeployment(name); err == nil {
		info.ActiveDeploymentID = id
	}
	return info, nil
}

func (s *Store) ListSites() ([]SiteInfo, error) {
	sitesDir := filepath.Join(s.dataDir, "sites")
	entries, err := os.ReadDir(sitesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var sites []SiteInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info := SiteInfo{Name: e.Name()}
		if id, err := s.CurrentDeployment(e.Name()); err == nil {
			info.ActiveDeploymentID = id
		}
		sites = append(sites, info)
	}
	return sites, nil
}

type DeploymentInfo struct {
	ID              string    `json:"id"`
	Active          bool      `json:"active"`
	Failed          bool      `json:"failed,omitempty"`
	FailedReason    string    `json:"failed_reason,omitempty"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
	CreatedBy       string    `json:"created_by,omitempty"`
	CreatedByAvatar string    `json:"created_by_avatar,omitempty"`
	SizeBytes       int64     `json:"size_bytes,omitempty"`
}

// deploymentInfoFromManifest populates a DeploymentInfo from a Manifest.
func deploymentInfoFromManifest(d *DeploymentInfo, m Manifest) {
	d.CreatedAt = m.CreatedAt
	d.CreatedBy = m.CreatedBy
	d.CreatedByAvatar = m.CreatedByAvatar
	d.SizeBytes = m.SizeBytes
}

// FileInfo describes a single file within a deployment's content directory.
type FileInfo struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	Hash string `json:"hash"`
}

// ContentDir returns the path to the content directory for a deployment.
func (s *Store) ContentDir(site, id string) string {
	return filepath.Join(s.dataDir, "sites", site, "deployments", id, "content")
}

// WriteFileIndex persists a pre-computed file listing as files.json
// alongside the deployment's manifest.
func (s *Store) WriteFileIndex(site, id string, files []FileInfo) error {
	path := filepath.Join(s.dataDir, "sites", site, "deployments", id, "files.json")
	data, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("marshal file index: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// ReadFileIndex reads a cached file listing from files.json.
// Returns os.ErrNotExist if no cache exists.
func (s *Store) ReadFileIndex(site, id string) ([]FileInfo, error) {
	path := filepath.Join(s.dataDir, "sites", site, "deployments", id, "files.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var files []FileInfo
	if err := json.Unmarshal(data, &files); err != nil {
		return nil, fmt.Errorf("parse file index: %w", err)
	}
	return files, nil
}

// ListDeploymentFiles returns all files in a deployment's content directory,
// sorted alphabetically by path. Uses a cached files.json when available,
// falling back to walking and hashing the content directory.
func (s *Store) ListDeploymentFiles(site, id string) ([]FileInfo, error) {
	if !ValidSiteName(site) {
		return nil, fmt.Errorf("invalid site name: %q", site)
	}
	if !ValidDeploymentID(id) {
		return nil, ErrDeploymentNotFound
	}
	if cached, err := s.ReadFileIndex(site, id); err == nil {
		return cached, nil
	}
	contentDir := s.ContentDir(site, id)
	var files []FileInfo
	err := filepath.WalkDir(contentDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(contentDir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		files = append(files, FileInfo{Path: rel, Size: info.Size(), Hash: hex.EncodeToString(h.Sum(nil))})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func (s *Store) DeleteDeployment(site, id string) error {
	if !ValidSiteName(site) {
		return fmt.Errorf("invalid site name: %q", site)
	}
	if !ValidDeploymentID(id) {
		return ErrDeploymentNotFound
	}
	current, _ := s.CurrentDeployment(site)
	if id == current {
		return ErrActiveDeployment
	}
	dir := filepath.Join(s.dataDir, "sites", site, "deployments", id)
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrDeploymentNotFound
		}
		return fmt.Errorf("checking deployment: %w", err)
	}
	return os.RemoveAll(dir)
}

// DeleteInactiveDeployments removes all non-active deployments for a site.
// Returns the number of deployments deleted.
func (s *Store) DeleteInactiveDeployments(site string) (int, error) {
	deployments, err := s.ListDeployments(site)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, d := range deployments {
		if d.Active {
			continue
		}
		if err := s.DeleteDeployment(site, d.ID); err != nil {
			return deleted, fmt.Errorf("deleting %s: %w", d.ID, err)
		}
		deleted++
	}
	return deleted, nil
}

// CleanupOldDeployments removes the oldest deployments for a site,
// keeping at most `keep` deployments. The active deployment is never removed.
// Returns the number of deployments deleted.
func (s *Store) CleanupOldDeployments(site string, keep int) (int, error) {
	deployments, err := s.ListDeployments(site)
	if err != nil {
		return 0, err
	}
	if len(deployments) <= keep {
		return 0, nil
	}

	// Sort newest first.
	sort.Slice(deployments, func(i, j int) bool {
		return deployments[i].CreatedAt.After(deployments[j].CreatedAt)
	})

	deleted := 0
	for i, d := range deployments {
		if i < keep || d.Active {
			continue
		}
		if err := s.DeleteDeployment(site, d.ID); err != nil {
			return deleted, fmt.Errorf("deleting %s: %w", d.ID, err)
		}
		deleted++
	}
	return deleted, nil
}

func (s *Store) DeleteSite(site string) error {
	if !ValidSiteName(site) {
		return fmt.Errorf("invalid site name: %q", site)
	}
	return os.RemoveAll(filepath.Join(s.dataDir, "sites", site))
}

func (s *Store) ListDeployments(site string) ([]DeploymentInfo, error) {
	if !ValidSiteName(site) {
		return nil, fmt.Errorf("invalid site name: %q", site)
	}
	deploymentsDir := filepath.Join(s.dataDir, "sites", site, "deployments")
	entries, err := os.ReadDir(deploymentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	current, _ := s.CurrentDeployment(site)

	var deployments []DeploymentInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		depDir := filepath.Join(deploymentsDir, e.Name())
		completeMarker := filepath.Join(depDir, ".complete")
		failedMarker := filepath.Join(depDir, ".failed")

		_, completeErr := os.Stat(completeMarker)
		failedReason, failedErr := os.ReadFile(failedMarker)

		if completeErr != nil && failedErr != nil {
			continue // orphaned/in-progress
		}

		info := DeploymentInfo{
			ID:     e.Name(),
			Active: e.Name() == current,
		}
		if failedErr == nil {
			info.Failed = true
			info.FailedReason = string(failedReason)
		}
		if m, err := s.ReadManifest(site, e.Name()); err == nil {
			deploymentInfoFromManifest(&info, m)
		}
		deployments = append(deployments, info)
	}
	return deployments, nil
}

func (s *Store) CleanupOrphans() {
	sitesDir := filepath.Join(s.dataDir, "sites")
	siteEntries, err := os.ReadDir(sitesDir)
	if err != nil {
		return
	}
	for _, site := range siteEntries {
		if !site.IsDir() {
			continue
		}
		deploymentsDir := filepath.Join(sitesDir, site.Name(), "deployments")
		depEntries, err := os.ReadDir(deploymentsDir)
		if err != nil {
			continue
		}
		for _, dep := range depEntries {
			if !dep.IsDir() {
				continue
			}
			depDir := filepath.Join(deploymentsDir, dep.Name())
			completeMarker := filepath.Join(depDir, ".complete")
			failedMarker := filepath.Join(depDir, ".failed")
			_, completeErr := os.Stat(completeMarker)
			_, failedErr := os.Stat(failedMarker)
			if os.IsNotExist(completeErr) && os.IsNotExist(failedErr) {
				os.RemoveAll(depDir)
			}
		}
	}
}
