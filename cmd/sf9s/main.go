package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/pkg/browser"

	"github.com/razkevich/sf9s/internal/api"
	"github.com/razkevich/sf9s/internal/config"
	"github.com/razkevich/sf9s/internal/sfcli"
	"github.com/razkevich/sf9s/internal/ui"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	org := flag.String("o", "", "org alias or username to select at startup")
	flag.Parse()

	if *showVersion {
		fmt.Println("sf9s " + version)
		return
	}

	paths, err := config.DefaultPaths()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot resolve config directories:", err)
		os.Exit(1)
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

	p := tea.NewProgram(ui.New(deps), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "sf9s crashed:", err)
		os.Exit(1)
	}
}
