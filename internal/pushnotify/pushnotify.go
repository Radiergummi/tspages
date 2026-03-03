// Package pushnotify sends browser push notifications for form submissions.
package pushnotify

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	webpush "github.com/SherClockHolmes/webpush-go"

	"tspages/internal/notify"
	"tspages/internal/storage"
)

var _ notify.Notifier = (*Notifier)(nil)

// Migrations is the ordered list of schema migrations for the push
// subscriptions table. It is exported so that the db package can include it in
// the unified migration sequence.
var Migrations = []func(*sql.Tx) error{
	func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			CREATE TABLE IF NOT EXISTS push_subscriptions (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				site       TEXT NOT NULL,
				path       TEXT NOT NULL,
				user_login TEXT NOT NULL,
				endpoint   TEXT NOT NULL,
				key_p256dh TEXT NOT NULL,
				key_auth   TEXT NOT NULL,
				created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
				UNIQUE(endpoint, site, path)
			)
		`); err != nil {
			return err
		}
		_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_push_subs_site_path ON push_subscriptions(site, path)`)
		return err
	},
}

// Subscription holds the browser push subscription fields.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	KeyP256dh string `json:"key_p256dh"`
	KeyAuth  string `json:"key_auth"`
}

// Notifier sends Web Push notifications for form submissions.
type Notifier struct {
	db        *sql.DB
	vapidPub  string
	vapidPriv string
	// sender is an optional override for sending push notifications.
	// If nil, webpush.SendNotification is used. Exposed for testing.
	sender func(payload []byte, sub dbSubscription) (*http.Response, error)
}

// NewNotifier creates a Notifier using the provided database connection. The
// database must already have the push migrations applied (see Migrations).
func NewNotifier(db *sql.DB, vapidPub, vapidPriv string) *Notifier {
	return &Notifier{db: db, vapidPub: vapidPub, vapidPriv: vapidPriv}
}

// Fire sends push notifications for form.submission events. It is a no-op
// for all other event types.
func (n *Notifier) Fire(event string, site string, _ storage.SiteConfig, data map[string]any) {
	if event != "form.submission" {
		return
	}

	path, _ := data["path"].(string)

	subs, err := n.subscriptionsForEvent(site, path)
	if err != nil {
		slog.Error("push: listing subscriptions", "site", site, "path", path, "err", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	submitter, _ := data["submitter"].(string)
	id, _ := data["id"].(string)

	payload, _ := json.Marshal(map[string]string{
		"title": "New submission on " + site,
		"body":  formattedBody(path, submitter),
		"url":   "/sites/" + site + "/submissions/" + id,
		"site":  site,
		"path":  path,
	})

	for _, sub := range subs {
		go n.sendPush(sub, payload)
	}
}

func formattedBody(path, submitter string) string {
	if submitter != "" {
		return path + " \u2014 " + submitter
	}
	return path
}

type dbSubscription struct {
	id       int64
	endpoint string
	p256dh   string
	auth     string
}

func (n *Notifier) subscriptionsForEvent(site, path string) ([]dbSubscription, error) {
	rows, err := n.db.Query(
		`SELECT id, endpoint, key_p256dh, key_auth FROM push_subscriptions WHERE site = ? AND path = ?`, site, path,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []dbSubscription
	for rows.Next() {
		var s dbSubscription
		if err := rows.Scan(&s.id, &s.endpoint, &s.p256dh, &s.auth); err != nil {
			return nil, err
		}
		subs = append(subs, s)
	}
	return subs, rows.Err()
}

func (n *Notifier) sendPush(sub dbSubscription, payload []byte) {
	var resp *http.Response
	var err error
	if n.sender != nil {
		resp, err = n.sender(payload, sub)
	} else {
		resp, err = webpush.SendNotification(payload, &webpush.Subscription{
			Endpoint: sub.endpoint,
			Keys: webpush.Keys{
				P256dh: sub.p256dh,
				Auth:   sub.auth,
			},
		}, &webpush.Options{
			VAPIDPublicKey:  n.vapidPub,
			VAPIDPrivateKey: n.vapidPriv,
			Subscriber:      "mailto:noreply@example.com",
		})
	}
	if err != nil {
		slog.Error("push: send failed", "endpoint", sub.endpoint, "err", err)
		return
	}
	resp.Body.Close()

	// 404 or 410 means the subscription is expired — clean it up.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		slog.Info("push: removing expired subscription", "endpoint", sub.endpoint)
		n.deleteByID(sub.id)
	}
}

func (n *Notifier) deleteByID(id int64) {
	if _, err := n.db.Exec(`DELETE FROM push_subscriptions WHERE id = ?`, id); err != nil {
		slog.Error("push: delete subscription", "id", id, "err", err)
	}
}

// Subscribe stores or updates a push subscription for the given site, path, and user.
func (n *Notifier) Subscribe(site, path, userLogin string, sub Subscription) error {
	_, err := n.db.Exec(
		`INSERT INTO push_subscriptions (site, path, user_login, endpoint, key_p256dh, key_auth)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(endpoint, site, path) DO UPDATE SET
			user_login = excluded.user_login,
			key_p256dh = excluded.key_p256dh,
			key_auth = excluded.key_auth`,
		site, path, userLogin, sub.Endpoint, sub.KeyP256dh, sub.KeyAuth,
	)
	return err
}

// Unsubscribe removes a push subscription for a specific site, path, and user.
func (n *Notifier) Unsubscribe(endpoint, site, path, userLogin string) error {
	_, err := n.db.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ? AND site = ? AND path = ? AND user_login = ?`, endpoint, site, path, userLogin)
	return err
}

// IsSubscribed reports whether a subscription exists for the given site, path, and endpoint.
func (n *Notifier) IsSubscribed(site, path, endpoint string) (bool, error) {
	var count int
	err := n.db.QueryRow(
		`SELECT COUNT(*) FROM push_subscriptions WHERE site = ? AND path = ? AND endpoint = ?`,
		site, path, endpoint,
	).Scan(&count)
	return count > 0, err
}

// LoadOrCreateVAPIDKeys reads VAPID keys from {dataDir}/vapid.json,
// generating new ones if the file doesn't exist.
func LoadOrCreateVAPIDKeys(dataDir string) (pub, priv string, err error) {
	path := filepath.Join(dataDir, "vapid.json")

	data, err := os.ReadFile(path)
	if err == nil {
		var keys struct {
			PublicKey  string `json:"public_key"`
			PrivateKey string `json:"private_key"`
		}
		if err := json.Unmarshal(data, &keys); err != nil {
			return "", "", fmt.Errorf("vapid: parse %s: %w", path, err)
		}
		return keys.PublicKey, keys.PrivateKey, nil
	}
	if !os.IsNotExist(err) {
		return "", "", fmt.Errorf("vapid: read %s: %w", path, err)
	}

	priv, pub, err = webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", "", fmt.Errorf("vapid: generate keys: %w", err)
	}

	out, _ := json.MarshalIndent(struct {
		PublicKey  string `json:"public_key"`
		PrivateKey string `json:"private_key"`
	}{pub, priv}, "", "  ")

	// Write atomically via temp file + rename.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return "", "", fmt.Errorf("vapid: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", "", fmt.Errorf("vapid: rename %s: %w", path, err)
	}

	return pub, priv, nil
}
