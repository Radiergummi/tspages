package tsadapter

import (
	"context"
	"encoding/json"

	"tspages/internal/auth"

	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
)

// Adapter wraps a real tailscale local.Client to implement auth.WhoIsClient.
type Adapter struct {
	client *local.Client
}

func New(client *local.Client) *Adapter {
	return &Adapter{client: client}
}

func (a *Adapter) WhoIs(ctx context.Context, remoteAddr string) (*auth.WhoIsResult, error) {
	who, err := a.client.WhoIs(ctx, remoteAddr)
	if err != nil {
		return nil, err
	}
	return convertResponse(who), nil
}

func convertResponse(who *apitype.WhoIsResponse) *auth.WhoIsResult {
	result := &auth.WhoIsResult{
		CapMap: make(map[string][]json.RawMessage),
	}
	for k, v := range who.CapMap {
		raw := make([]json.RawMessage, len(v))
		for i, msg := range v {
			raw[i] = json.RawMessage(msg)
		}
		result.CapMap[string(k)] = raw
	}
	if who.UserProfile != nil {
		result.LoginName = who.UserProfile.LoginName
		result.DisplayName = who.UserProfile.DisplayName
		result.ProfilePicURL = who.UserProfile.ProfilePicURL
	}
	if who.Node != nil {
		result.NodeName = who.Node.Name
		result.Tags = who.Node.Tags
		if len(who.Node.Addresses) > 0 {
			result.NodeIP = who.Node.Addresses[0].Addr().String()
		}
		hi := who.Node.Hostinfo
		if hi.Valid() {
			result.OS = hi.OS()
			result.OSVersion = hi.OSVersion()
			if hi.DeviceModel() != "" {
				result.Device = hi.DeviceModel()
			} else {
				result.Device = hi.Machine()
			}
		}
	}
	return result
}
