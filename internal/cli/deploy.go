package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tailscale.com/client/local"
)

// resolveServer returns the control plane URL from flag, env, or auto-discovery.
func resolveServer(flagVal, envVal string, discover func() (string, error)) string {
	if flagVal != "" {
		return strings.TrimRight(flagVal, "/")
	}
	if envVal != "" {
		return strings.TrimRight(envVal, "/")
	}
	if discover != nil {
		if url, err := discover(); err == nil && url != "" {
			return url
		}
	}
	return ""
}

// discoverServer uses the local Tailscale daemon to find the control plane URL.
func discoverServer() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var lc local.Client
	status, err := lc.StatusWithoutPeers(ctx)
	if err != nil {
		return "", fmt.Errorf("tailscale status: %w", err)
	}
	if status.CurrentTailnet == nil || status.CurrentTailnet.MagicDNSSuffix == "" {
		return "", fmt.Errorf("no tailnet found")
	}
	return "https://pages." + status.CurrentTailnet.MagicDNSSuffix, nil
}

// Deploy is the entrypoint for `tspages deploy`.
func Deploy(args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ExitOnError)
	serverFlag := fs.String("server", "", "control plane URL (default: auto-discover)")
	noActivate := fs.Bool("no-activate", false, "upload without activating")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: tspages deploy <path> <site> [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Upload a directory or file to a tspages site.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if fs.NArg() < 2 {
		fs.Usage()
		return fmt.Errorf("requires <path> and <site> arguments")
	}

	path := fs.Arg(0)
	site := fs.Arg(1)

	server := resolveServer(*serverFlag, os.Getenv("TSPAGES_SERVER"), discoverServer)
	if server == "" {
		return fmt.Errorf("cannot determine server URL; use --server or set TSPAGES_SERVER")
	}

	body, filename, err := prepareBody(path)
	if err != nil {
		return err
	}

	deployURL := server + "/deploy/" + url.PathEscape(site)
	if filename != "" {
		deployURL += "/" + url.PathEscape(filename)
	}
	if *noActivate {
		deployURL += "?activate=false"
	}

	req, err := http.NewRequest("PUT", deployURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.ContentLength = int64(len(body))

	fmt.Fprintf(os.Stderr, "Deploying to %s...\n", site)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("deploy failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var result struct {
		DeploymentID string `json:"deployment_id"`
		Site         string `json:"site"`
		URL          string `json:"url"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Deployed %s (%s)\n", result.Site, result.DeploymentID)
	if result.URL != "" {
		fmt.Println(result.URL)
	}
	return nil
}

// prepareBody reads the path and returns the upload body and an optional
// filename hint (for single-file format detection). If path is a directory,
// it zips it and returns no filename.
func prepareBody(path string) ([]byte, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read %s: %w", path, err)
	}

	if info.IsDir() {
		data, err := zipDir(path)
		if err != nil {
			return nil, "", fmt.Errorf("zipping directory: %w", err)
		}
		return data, "", nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("reading file: %w", err)
	}
	return data, filepath.Base(path), nil
}

// zipDir creates an in-memory ZIP of a directory's contents.
func zipDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		f, err := w.Create(rel)
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(f, src)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
