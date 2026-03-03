// Package webhooks sends webhook notifications for site events and provides
// an admin UI for viewing delivery history and analytics.
package webhooks

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"tspages/internal/notify"
	"tspages/internal/storage"
)

var _ notify.Notifier = (*Notifier)(nil)

// Notifier sends webhook notifications for site events.
type Notifier struct {
	db          *sql.DB
	client      *http.Client
	retryDelays []time.Duration
	sem         chan struct{}
}

// NewNotifier creates a Notifier. The database must already have the webhook
// migrations applied (see Migrations).
func NewNotifier(db *sql.DB) *Notifier {
	return &Notifier{
		db:          db,
		client:      newSafeClient(),
		retryDelays: []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute},
		sem:         make(chan struct{}, 20),
	}
}

// Migrations is the ordered list of schema migrations for the webhook tables.
var Migrations = []func(*sql.Tx) error{
	// 1: baseline schema with all current columns.
	func(tx *sql.Tx) error {
		if _, err := tx.Exec(`
			CREATE TABLE IF NOT EXISTS webhook_deliveries (
				id         INTEGER PRIMARY KEY AUTOINCREMENT,
				webhook_id TEXT NOT NULL,
				event      TEXT NOT NULL,
				site       TEXT NOT NULL,
				url        TEXT NOT NULL,
				payload    TEXT NOT NULL,
				attempt    INTEGER NOT NULL,
				status     INTEGER,
				error      TEXT NOT NULL DEFAULT '',
				created_at  TEXT NOT NULL,
				signed      INTEGER NOT NULL DEFAULT 0,
				duration_ms INTEGER NOT NULL DEFAULT 0
			);
		`); err != nil {
			return err
		}
		// For pre-existing databases: add columns that may be missing.
		// Ignore "duplicate column" errors for DBs that already have them.
		_, _ = tx.Exec(`ALTER TABLE webhook_deliveries ADD COLUMN signed INTEGER NOT NULL DEFAULT 0`)
		_, _ = tx.Exec(`ALTER TABLE webhook_deliveries ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0`)
		return nil
	},
}

// SetClient overrides the HTTP client used for webhook delivery.
func (n *Notifier) SetClient(c *http.Client) { n.client = c }

// Fire sends a webhook notification asynchronously. It is a no-op if the
// config has no WebhookURL or the event is not in the configured event filter.
func (n *Notifier) Fire(event string, site string, cfg storage.SiteConfig, data map[string]any) {
	if cfg.WebhookURL == "" {
		return
	}
	if len(cfg.WebhookEvents) > 0 {
		found := false
		for _, ev := range cfg.WebhookEvents {
			if ev == event {
				found = true
				break
			}
		}
		if !found {
			return
		}
	}
	go n.deliver(event, site, cfg, data)
}

func (n *Notifier) deliver(event, site string, cfg storage.SiteConfig, data map[string]any) {
	msgID := "msg_" + randomHex(16)
	ts := time.Now().UTC()

	payload, err := json.Marshal(map[string]any{
		"type":      event,
		"timestamp": ts.Format(time.RFC3339),
		"data":      data,
	})
	if err != nil {
		slog.Error("webhook: marshal payload", "err", err)
		return
	}

	maxAttempts := 1 + len(n.retryDelays)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// Acquire a semaphore slot only for the network call so retries
		// (which sleep up to ~2.5 min total) don't hold a slot idle.
		select {
		case n.sem <- struct{}{}:
		default:
			slog.Warn("webhook: dropping delivery", "event", event, "attempt", attempt, "site", site, "reason", "too many pending deliveries")
			return
		}
		status, dur, sendErr := n.send(cfg.WebhookURL, cfg.WebhookSecret, msgID, ts, payload)
		<-n.sem

		errStr := ""
		if sendErr != nil {
			errStr = sendErr.Error()
		}
		n.logDelivery(msgID, event, site, cfg.WebhookURL, string(payload), attempt, status, errStr, cfg.WebhookSecret != "", dur.Milliseconds())

		if sendErr == nil && status >= 200 && status < 300 {
			return
		}

		// Don't retry on 406 — the receiver is explicitly rejecting the payload.
		if sendErr == nil && status == http.StatusNotAcceptable {
			return
		}

		if attempt < maxAttempts {
			time.Sleep(n.retryDelays[attempt-1])
		}
	}
}

func (n *Notifier) send(url, secret, msgID string, ts time.Time, payload []byte) (int, time.Duration, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", msgID)
	req.Header.Set("webhook-timestamp", fmt.Sprintf("%d", ts.Unix()))

	if secret != "" {
		wh, err := standardwebhooks.NewWebhook(strings.TrimPrefix(secret, "whsec_"))
		if err != nil {
			return 0, 0, fmt.Errorf("init webhook signer: %w", err)
		}
		sig, err := wh.Sign(msgID, ts, payload)
		if err != nil {
			return 0, 0, fmt.Errorf("sign webhook: %w", err)
		}
		req.Header.Set("webhook-signature", sig)
	}

	start := time.Now()
	resp, err := n.client.Do(req)
	dur := time.Since(start)
	if err != nil {
		return 0, dur, err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	return resp.StatusCode, dur, nil
}

func (n *Notifier) logDelivery(webhookID, event, site, url, payload string, attempt, status int, errStr string, signed bool, durationMs int64) {
	_, err := n.db.Exec(
		`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at, signed, duration_ms)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		webhookID, event, site, url, payload, attempt, status, errStr, time.Now().UTC().Format(time.RFC3339), signed, durationMs,
	)
	if err != nil {
		slog.Error("webhook: log delivery", "err", err)
	}
}

// Resend re-delivers the original payload for a webhook delivery, recording
// a new attempt. It returns the HTTP status code or an error.
func (n *Notifier) Resend(webhookID, secret string) (int, error) {
	var url, event, site, payload string
	err := n.db.QueryRow(
		`SELECT url, event, site, payload FROM webhook_deliveries WHERE webhook_id = ? ORDER BY attempt LIMIT 1`,
		webhookID,
	).Scan(&url, &event, &site, &payload)
	if err != nil {
		return 0, fmt.Errorf("resend: lookup original delivery: %w", err)
	}

	var maxAttempt int
	err = n.db.QueryRow(
		`SELECT MAX(attempt) FROM webhook_deliveries WHERE webhook_id = ?`,
		webhookID,
	).Scan(&maxAttempt)
	if err != nil {
		return 0, fmt.Errorf("resend: lookup max attempt: %w", err)
	}
	attempt := maxAttempt + 1

	retryMsgID := "msg_" + randomHex(16)
	ts := time.Now().UTC()
	status, dur, sendErr := n.send(url, secret, retryMsgID, ts, []byte(payload))

	errStr := ""
	if sendErr != nil {
		errStr = sendErr.Error()
	}
	n.logDelivery(webhookID, event, site, url, payload, attempt, status, errStr, secret != "", dur.Milliseconds())

	if sendErr != nil {
		return 0, sendErr
	}
	return status, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func newSafeClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return nil
			}
			if isPrivateIP(ip) {
				return fmt.Errorf("webhook: refusing to connect to private address %s", ip)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
	}
}

var privateNetworks []*net.IPNet

func init() {
	for _, cidr := range []string{
		"0.0.0.0/8",
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"::1/128",
		"fe80::/10",
		"fc00::/7",
	} {
		_, network, _ := net.ParseCIDR(cidr)
		privateNetworks = append(privateNetworks, network)
	}
}

func isPrivateIP(ip net.IP) bool {
	if ip.IsUnspecified() || ip.IsLoopback() {
		return true
	}
	for _, network := range privateNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
