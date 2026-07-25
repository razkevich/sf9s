package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"

	"github.com/razkevich/sf9s/internal/config"
)

func TestUsageExplainsTheTool(t *testing.T) {
	fs := flag.NewFlagSet("sf9s", flag.ContinueOnError)
	fs.Bool("version", false, "print the version and exit")
	fs.String("o", "", "org alias or username to start on")

	var out bytes.Buffer
	usage(&out, fs, config.Paths{ConfigDir: "/cfg/sf9s", CacheDir: "/cache/sf9s"})
	got := out.String()

	for _, want := range []string{
		"k9s for Salesforce", // what it is
		"-version",           // every flag documented
		"-o",                 //
		"sf org login web",   // the prerequisite, with the fix
		"@salesforce/cli",    //
		"never stores",       // the trust claim
		"/cfg/sf9s",          // where its files live
		"/cache/sf9s",        //
		"queries.yaml",       //
		"command palette",    // how to get around
		"github.com/razkevich/sf9s",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("help output missing %q:\n%s", want, got)
		}
	}
}
