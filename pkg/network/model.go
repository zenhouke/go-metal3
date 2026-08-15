package network

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

// Config is an OpenStack network_data.json-compatible model.
type Config struct {
	Links    []Link    `json:"links"`
	Networks []Network `json:"networks"`
	Services []Service `json:"services,omitempty"`
}

type Link struct {
	ID                 string   `json:"id"`
	Type               string   `json:"type"`
	EthernetMACAddress string   `json:"ethernet_mac_address,omitempty"`
	MTU                int      `json:"mtu,omitempty"`
	BondLinks          []string `json:"bond_links,omitempty"`
	BondMode           string   `json:"bond_mode,omitempty"`
	BondXmitHashPolicy string   `json:"bond_xmit_hash_policy,omitempty"`
	VLANLink           string   `json:"vlan_link,omitempty"`
	VLANID             int      `json:"vlan_id,omitempty"`
}

type Network struct {
	ID        string  `json:"id"`
	Link      string  `json:"link"`
	Type      string  `json:"type"`
	IPAddress string  `json:"ip_address,omitempty"`
	Netmask   string  `json:"netmask,omitempty"`
	Gateway   string  `json:"gateway,omitempty"`
	NetworkID string  `json:"network_id,omitempty"`
	Routes    []Route `json:"routes,omitempty"`
}

type Route struct {
	Network string `json:"network"`
	Netmask string `json:"netmask"`
	Gateway string `json:"gateway"`
}

type Service struct {
	Type    string `json:"type"`
	Address string `json:"address"`
}

func Render(config Config) ([]byte, error) {
	if err := Validate(config); err != nil {
		return nil, err
	}
	return json.Marshal(config)
}

func Validate(config Config) error {
	links := make(map[string]struct{}, len(config.Links))
	macs := make(map[string]string, len(config.Links))
	for _, link := range config.Links {
		if link.ID == "" {
			return fmt.Errorf("network link ID is required")
		}
		if _, exists := links[link.ID]; exists {
			return fmt.Errorf("duplicate network link %q", link.ID)
		}
		links[link.ID] = struct{}{}
		switch link.Type {
		case "phy", "bond", "vlan":
		default:
			return fmt.Errorf("link %q has unsupported type %q", link.ID, link.Type)
		}
		if link.MTU != 0 && (link.MTU < 576 || link.MTU > 65535) {
			return fmt.Errorf("link %q has invalid MTU %d", link.ID, link.MTU)
		}
		if link.EthernetMACAddress != "" {
			parsed, err := net.ParseMAC(link.EthernetMACAddress)
			if err != nil {
				return fmt.Errorf("link %q has invalid MAC address: %w", link.ID, err)
			}
			normalized := strings.ToLower(parsed.String())
			if owner, exists := macs[normalized]; exists {
				return fmt.Errorf("links %q and %q use duplicate MAC address", owner, link.ID)
			}
			macs[normalized] = link.ID
		}
	}
	for _, link := range config.Links {
		if link.Type == "bond" {
			if len(link.BondLinks) == 0 {
				return fmt.Errorf("bond link %q requires bond_links", link.ID)
			}
			for _, member := range link.BondLinks {
				if member == link.ID {
					return fmt.Errorf("bond link %q cannot contain itself", link.ID)
				}
				if _, exists := links[member]; !exists {
					return fmt.Errorf("bond link %q references unknown member %q", link.ID, member)
				}
			}
		}
		if link.Type == "vlan" {
			if link.VLANID < 1 || link.VLANID > 4094 {
				return fmt.Errorf("VLAN link %q has invalid VLAN ID %d", link.ID, link.VLANID)
			}
			if _, exists := links[link.VLANLink]; !exists {
				return fmt.Errorf("VLAN link %q references unknown parent %q", link.ID, link.VLANLink)
			}
		}
	}
	networks := make(map[string]struct{}, len(config.Networks))
	for _, network := range config.Networks {
		if network.ID == "" {
			return fmt.Errorf("network ID is required")
		}
		if _, exists := networks[network.ID]; exists {
			return fmt.Errorf("duplicate network %q", network.ID)
		}
		networks[network.ID] = struct{}{}
		if _, exists := links[network.Link]; !exists {
			return fmt.Errorf("network %q references unknown link %q", network.ID, network.Link)
		}
		switch network.Type {
		case "ipv4_dhcp", "ipv6_dhcp":
			if network.IPAddress != "" || network.Netmask != "" || network.Gateway != "" {
				return fmt.Errorf("DHCP network %q must not contain static address fields", network.ID)
			}
		case "ipv4", "ipv6":
			ip := net.ParseIP(network.IPAddress)
			mask := net.ParseIP(network.Netmask)
			if ip == nil || mask == nil {
				return fmt.Errorf("static network %q requires valid IP address and netmask", network.ID)
			}
			wantIPv4 := network.Type == "ipv4"
			if (ip.To4() != nil) != wantIPv4 || (mask.To4() != nil) != wantIPv4 {
				return fmt.Errorf("network %q address family does not match type %q", network.ID, network.Type)
			}
			if network.Gateway != "" {
				gateway := net.ParseIP(network.Gateway)
				if gateway == nil || ((gateway.To4() != nil) != wantIPv4) {
					return fmt.Errorf("network %q has invalid gateway address family", network.ID)
				}
			}
		default:
			return fmt.Errorf("network %q has unsupported type %q", network.ID, network.Type)
		}
		for _, route := range network.Routes {
			if net.ParseIP(route.Network) == nil || net.ParseIP(route.Netmask) == nil || net.ParseIP(route.Gateway) == nil {
				return fmt.Errorf("network %q contains an invalid route", network.ID)
			}
		}
	}
	for _, service := range config.Services {
		if service.Type != "dns" {
			return fmt.Errorf("unsupported network service type %q", service.Type)
		}
		if net.ParseIP(service.Address) == nil {
			return fmt.Errorf("DNS service has invalid address %q", service.Address)
		}
	}
	return nil
}
