package network

import (
	"encoding/json"
	"testing"
)

func TestRenderBondNetworkData(t *testing.T) {
	t.Parallel()
	config := Config{
		Links: []Link{
			{ID: "eth0", Type: "phy", EthernetMACAddress: "00:11:22:33:44:55"},
			{ID: "eth1", Type: "phy", EthernetMACAddress: "00:11:22:33:44:66"},
			{ID: "bond0", Type: "bond", BondLinks: []string{"eth0", "eth1"}, BondMode: "802.3ad"},
		},
		Networks: []Network{{ID: "provisioning", Link: "bond0", Type: "ipv4_dhcp"}},
		Services: []Service{{Type: "dns", Address: "192.0.2.53"}},
	}
	raw, err := Render(config)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded["links"].([]any)) != 3 {
		t.Fatalf("unexpected JSON: %s", raw)
	}
}

func TestValidateRejectsInvalidTopology(t *testing.T) {
	t.Parallel()
	tests := []Config{
		{Links: []Link{{ID: "bond0", Type: "bond", BondLinks: []string{"missing"}}}},
		{Links: []Link{{ID: "eth0", Type: "phy", EthernetMACAddress: "00:11:22:33:44:55"}, {ID: "eth1", Type: "phy", EthernetMACAddress: "00:11:22:33:44:55"}}},
		{Links: []Link{{ID: "eth0", Type: "phy"}}, Networks: []Network{{ID: "n0", Link: "eth0", Type: "ipv4_dhcp", IPAddress: "192.0.2.10"}}},
	}
	for i, config := range tests {
		if err := Validate(config); err == nil {
			t.Fatalf("case %d unexpectedly passed", i)
		}
	}
}
