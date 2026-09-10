
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"pixi_game_server/internal/config"
	"pixi_game_server/internal/liveconfig"
)

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  configctl list                       List every live config key and its current value
  configctl get <key>                  Print one key's current value
  configctl set <key> <value>          Set a key and notify running servers instantly
  configctl set-many k1=v1 k2=v2 ...   Set several related keys in one atomic, consistent change

Keys:`)
	keys := append([]string(nil), config.LiveConfigKeys...)
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintln(os.Stderr, "  "+k)
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := liveconfig.Connect(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "failed to connect to postgres/redis:", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.EnsureSchema(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "failed to ensure schema:", err)
		os.Exit(1)
	}

	switch os.Args[1] {
	case "list":
		rows, err := store.LoadAll(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "failed to list config:", err)
			os.Exit(1)
		}
		keys := make([]string, 0, len(rows))
		for k := range rows {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%-40s %s\n", k, rows[k])
		}

	case "get":
		if len(os.Args) != 3 {
			usage()
			os.Exit(1)
		}
		rows, err := store.LoadAll(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "failed to read config:", err)
			os.Exit(1)
		}
		value, ok := rows[os.Args[2]]
		if !ok {
			fmt.Fprintln(os.Stderr, "key not set:", os.Args[2])
			os.Exit(1)
		}
		fmt.Println(value)

	case "set":
		if len(os.Args) != 4 {
			usage()
			os.Exit(1)
		}
		key, value := os.Args[2], os.Args[3]

		if err := (&config.LiveNetConfig{}).ApplyKey(key, value); err != nil {
			fmt.Fprintln(os.Stderr, "invalid key or value:", err)
			os.Exit(1)
		}
		if err := store.SetKey(ctx, key, value); err != nil {
			fmt.Fprintln(os.Stderr, "failed to set config:", err)
			os.Exit(1)
		}
		fmt.Printf("%s = %s (all connected servers notified)\n", key, value)

	case "set-many":
		if len(os.Args) < 3 {
			usage()
			os.Exit(1)
		}
		patch := make(map[string]string, len(os.Args)-2)
		for _, arg := range os.Args[2:] {
			key, value, ok := strings.Cut(arg, "=")
			if !ok {
				fmt.Fprintln(os.Stderr, "invalid k=v pair:", arg)
				os.Exit(1)
			}
			patch[key] = value
		}
		liveCfg := &config.LiveNetConfig{}
		for key, value := range patch {
			if err := liveCfg.ApplyKey(key, value); err != nil {
				fmt.Fprintf(os.Stderr, "invalid key or value %q: %v\n", key, err)
				os.Exit(1)
			}
		}
		if err := store.SetKeys(ctx, patch); err != nil {
			fmt.Fprintln(os.Stderr, "failed to set config:", err)
			os.Exit(1)
		}
		keys := make([]string, 0, len(patch))
		for k := range patch {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Printf("%s applied atomically (all connected servers notified)\n", strings.Join(keys, ", "))

	default:
		usage()
		os.Exit(1)
	}
}
