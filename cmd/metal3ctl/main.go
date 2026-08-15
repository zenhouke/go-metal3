package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/types"

	metal3sdk "github.com/zenhouke/go-metal3"
	sdkclient "github.com/zenhouke/go-metal3/pkg/client"
)

func main() {
	var kubeconfig string
	var namespace string
	var name string
	var action string
	flag.StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig; defaults to in-cluster or standard kubeconfig")
	flag.StringVar(&namespace, "namespace", "metal3", "BareMetalHost namespace")
	flag.StringVar(&name, "name", "", "BareMetalHost name")
	flag.StringVar(&action, "action", "get", "read-only action: info, list, or get")
	flag.Parse()

	if action == "get" && name == "" {
		fmt.Fprintln(os.Stderr, "-name is required")
		os.Exit(2)
	}
	config, err := sdkclient.LoadConfig(sdkclient.ConfigOptions{Kubeconfig: kubeconfig})
	if err != nil {
		fatal(err)
	}
	sdk, err := metal3sdk.New(metal3sdk.Options{Config: config})
	if err != nil {
		fatal(err)
	}
	ctx := context.Background()
	switch action {
	case "info":
		info, err := sdk.Cluster().Info(ctx)
		if err != nil {
			fatal(err)
		}
		printJSON(info)
	case "list":
		hosts, err := sdk.Hosts().List(ctx, metal3sdk.HostListOptions{Namespace: namespace})
		if err != nil {
			fatal(err)
		}
		printJSON(map[string]any{"items": hosts})
	case "get":
		host, err := sdk.Hosts().Get(ctx, types.NamespacedName{Namespace: namespace, Name: name})
		if err != nil {
			fatal(err)
		}
		printJSON(host)
	default:
		fmt.Fprintln(os.Stderr, "-action must be info, list, or get")
		os.Exit(2)
	}
}

func printJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
