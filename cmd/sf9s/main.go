// Command sf9s is a terminal UI for the Salesforce orgs you have already
// authenticated with the sf CLI.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/pkg/browser"

	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/config"
	"github.com/razkevich/sf9s/internal/sfcli"
	"github.com/razkevich/sf9s/internal/ui"
)

var version = "dev"

func usage(out io.Writer, fs *flag.FlagSet, paths config.Paths) {
	fmt.Fprintf(out, `sf9s %s — k9s for Salesforce

A keyboard-driven cockpit for every org you are logged into: run SOQL with
autocomplete, browse schema, watch org limits, inspect metadata and recent
deployments, and tail Apex debug logs.

Usage:
  sf9s [flags]

Flags:
`, version)
	fs.SetOutput(out)
	fs.PrintDefaults()
	fmt.Fprintf(out, `
Getting around:
  :          command palette — jump to any view
  ?          every key for the current view
  /          filter any table
  tab        complete the object or field at the cursor (query view)
  ctrl+c     quit

Requires the Salesforce CLI on your PATH with at least one authenticated org:
  npm install -g @salesforce/cli
  sf org login web --alias my-org

sf9s reads your orgs and resolves tokens through that CLI, and never stores
credentials of its own.

Files:
  %s
      config.yaml, queries.yaml (saved query library), history.json
  %s
      describe and org-inventory caches; safe to delete

Docs: https://github.com/razkevich/sf9s
`, paths.ConfigDir, paths.CacheDir)
}

func main() {
	paths, err := config.DefaultPaths()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot resolve config directories:", err)
		os.Exit(1)
	}

	fs := flag.NewFlagSet("sf9s", flag.ExitOnError)
	showVersion := fs.Bool("version", false, "print the version and exit")
	org := fs.String("o", "", "org alias or username to start on (default: your sf default org)")
	fs.Usage = func() { usage(os.Stderr, fs, paths) }
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if *showVersion {
		fmt.Println("sf9s " + version)
		return
	}

	store := config.NewStore(paths)
	sf := sfcli.New(sfcli.ExecRunner{})

	deps := ui.Deps{
		SF:    sf,
		Store: store,
		NewAPI: func(username string) *api.Client {
			return api.NewClient(api.NewCachedTokenSource(func(ctx context.Context) (api.Credentials, error) {
				creds, err := sf.Credentials(ctx, username)
				if err != nil {
					return api.Credentials{}, err
				}
				return api.Credentials{
					AccessToken: creds.AccessToken,
					InstanceURL: creds.InstanceURL,
					APIVersion:  creds.APIVersion,
				}, nil
			}))
		},
		Clipboard:  clipboard.WriteAll,
		OpenURL:    browser.OpenURL,
		Version:    version,
		InitialOrg: *org,
	}

	// A dumb terminal cannot render styling; lipgloss only checks NO_COLOR.
	if t := os.Getenv("TERM"); t == "" || t == "dumb" {
		lipgloss.SetColorProfile(termenv.Ascii)
	}

	p := tea.NewProgram(ui.New(deps), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "sf9s crashed:", err)
		os.Exit(1)
	}
}
