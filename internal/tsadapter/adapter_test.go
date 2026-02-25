package tsadapter

import (
	"encoding/json"
	"net/netip"
	"testing"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func TestConvertResponse_FullProfile(t *testing.T) {
	who := &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{
			LoginName:   "alice@example.com",
			DisplayName: "Alice Smith",
		},
		CapMap: tailcfg.PeerCapMap{
			"example.com/cap/pages": []tailcfg.RawMessage{
				tailcfg.RawMessage(`{"access":"deploy","sites":["docs"]}`),
			},
		},
	}

	result := convertResponse(who)

	if result.LoginName != "alice@example.com" {
		t.Errorf("LoginName = %q, want %q", result.LoginName, "alice@example.com")
	}
	if result.DisplayName != "Alice Smith" {
		t.Errorf("DisplayName = %q, want %q", result.DisplayName, "Alice Smith")
	}
	raw, ok := result.CapMap["example.com/cap/pages"]
	if !ok || len(raw) != 1 {
		t.Fatalf("CapMap missing expected key or wrong length: %v", result.CapMap)
	}
	var cap struct {
		Access string   `json:"access"`
		Sites  []string `json:"sites"`
	}
	if err := json.Unmarshal(raw[0], &cap); err != nil {
		t.Fatalf("unmarshal cap: %v", err)
	}
	if cap.Access != "deploy" {
		t.Errorf("cap access = %q, want %q", cap.Access, "deploy")
	}
	if len(cap.Sites) != 1 || cap.Sites[0] != "docs" {
		t.Errorf("cap sites = %v, want [docs]", cap.Sites)
	}
}

func TestConvertResponse_NilProfile(t *testing.T) {
	who := &apitype.WhoIsResponse{
		CapMap: tailcfg.PeerCapMap{},
	}

	result := convertResponse(who)

	if result.LoginName != "" {
		t.Errorf("LoginName = %q, want empty", result.LoginName)
	}
	if result.DisplayName != "" {
		t.Errorf("DisplayName = %q, want empty", result.DisplayName)
	}
}

func TestConvertResponse_NilCapMap(t *testing.T) {
	who := &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{
			LoginName: "bob@example.com",
		},
	}

	result := convertResponse(who)

	if result.LoginName != "bob@example.com" {
		t.Errorf("LoginName = %q, want %q", result.LoginName, "bob@example.com")
	}
	if len(result.CapMap) != 0 {
		t.Errorf("CapMap = %v, want empty", result.CapMap)
	}
}

func TestConvertResponse_NodeInfo(t *testing.T) {
	hi := (&tailcfg.Hostinfo{
		OS:        "linux",
		OSVersion: "6.1.0",
		Machine:   "x86_64",
	}).View()
	who := &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: "alice@example.com"},
		Node: &tailcfg.Node{
			Name:      "alice-laptop.tail1234.ts.net.",
			Tags:      []string{"tag:dev"},
			Addresses: []netip.Prefix{netip.MustParsePrefix("100.64.0.1/32")},
			Hostinfo:  hi,
		},
	}

	result := convertResponse(who)

	if result.NodeName != "alice-laptop.tail1234.ts.net." {
		t.Errorf("NodeName = %q", result.NodeName)
	}
	if result.NodeIP != "100.64.0.1" {
		t.Errorf("NodeIP = %q", result.NodeIP)
	}
	if result.OS != "linux" {
		t.Errorf("OS = %q", result.OS)
	}
	if result.OSVersion != "6.1.0" {
		t.Errorf("OSVersion = %q", result.OSVersion)
	}
	if result.Device != "x86_64" {
		t.Errorf("Device = %q", result.Device)
	}
	if len(result.Tags) != 1 || result.Tags[0] != "tag:dev" {
		t.Errorf("Tags = %v", result.Tags)
	}
}

func TestConvertResponse_NodeInfo_DeviceModel(t *testing.T) {
	hi := (&tailcfg.Hostinfo{
		OS:          "android",
		DeviceModel: "Pixel 7",
		Machine:     "aarch64",
	}).View()
	who := &apitype.WhoIsResponse{
		Node: &tailcfg.Node{
			Name:     "phone.ts.net.",
			Hostinfo: hi,
		},
	}

	result := convertResponse(who)

	if result.Device != "Pixel 7" {
		t.Errorf("Device = %q, want Pixel 7", result.Device)
	}
}

func TestConvertResponse_MultipleCaps(t *testing.T) {
	who := &apitype.WhoIsResponse{
		UserProfile: &tailcfg.UserProfile{LoginName: "ci@example.com"},
		CapMap: tailcfg.PeerCapMap{
			"example.com/cap/pages": []tailcfg.RawMessage{
				tailcfg.RawMessage(`{"access":"deploy","sites":["a"]}`),
				tailcfg.RawMessage(`{"access":"view"}`),
			},
		},
	}

	result := convertResponse(who)

	raw := result.CapMap["example.com/cap/pages"]
	if len(raw) != 2 {
		t.Fatalf("got %d caps, want 2", len(raw))
	}
}
