package webhook

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"

	"tspages/internal/storage"
)

// Notifier sends webhook notifications for site events.
type Notifier struct {
	db          *sql.DB
	client      *http.Client
	retryDelays []time.Duration
	sem         chan struct{}
}

// NewNotifier creates a Notifier and runs the delivery log migration.
func NewNotifier(db *sql.DB) (*Notifier, error) {
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("webhook migration: %w", err)
	}
	return &Notifier{
		db:          db,
		client:      newSafeClient(),
		retryDelays: []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute},
		sem:         make(chan struct{}, 20),
	}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
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
			created_at TEXT NOT NULL
		);
	`)
	return err
}

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
	select {
	case n.sem <- struct{}{}:
		go func() {
			defer func() { <-n.sem }()
			n.deliver(event, site, cfg, data)
		}()
	default:
		log.Printf("webhook: dropping %s event for %s (too many pending deliveries)", event, site)
	}
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
		log.Printf("webhook: marshal payload: %v", err)
		return
	}

	maxAttempts := 1 + len(n.retryDelays)
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		status, sendErr := n.send(cfg.WebhookURL, cfg.WebhookSecret, msgID, ts, payload)

		errStr := ""
		if sendErr != nil {
			errStr = sendErr.Error()
		}
		n.logDelivery(msgID, event, site, cfg.WebhookURL, string(payload), attempt, status, errStr)

		if sendErr == nil && status >= 200 && status < 300 {
			return
		}

		if attempt < maxAttempts {
			time.Sleep(n.retryDelays[attempt-1])
		}
	}
}

func (n *Notifier) send(url, secret, msgID string, ts time.Time, payload []byte) (int, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", msgID)
	req.Header.Set("webhook-timestamp", fmt.Sprintf("%d", ts.Unix()))

	if secret != "" {
		wh, err := standardwebhooks.NewWebhook(strings.TrimPrefix(secret, "whsec_"))
		if err != nil {
			return 0, fmt.Errorf("init webhook signer: %w", err)
		}
		sig, err := wh.Sign(msgID, ts, payload)
		if err != nil {
			return 0, fmt.Errorf("sign webhook: %w", err)
		}
		req.Header.Set("webhook-signature", sig)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	return resp.StatusCode, nil
}

func (n *Notifier) logDelivery(webhookID, event, site, url, payload string, attempt, status int, errStr string) {
	_, err := n.db.Exec(
		`INSERT INTO webhook_deliveries (webhook_id, event, site, url, payload, attempt, status, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		webhookID, event, site, url, payload, attempt, status, errStr, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		log.Printf("webhook: log delivery: %v", err)
	}
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
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
	}
}

func isPrivateIP(ip net.IP) bool {
	privateRanges := []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"100.64.0.0/10",
		"169.254.0.0/16",
		"::1/128",
		"fe80::/10",
		"fc00::/7",
	}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
