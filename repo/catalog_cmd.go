package repo

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/utils/toolutils"
	"github.com/spf13/cobra"
)

// CmdRepoCatalog starts the catalog daemon under the repo command namespace.
func CmdRepoCatalog() *cobra.Command {
	return &cobra.Command{
		Use:     "catalog CONFIG-FILE",
		Short:   "Run the repo catalog daemon",
		Long:    "Run a catalog daemon that accepts signed command packets for catalog lookups and updates.",
		Args:    cobra.ExactArgs(1),
		Example: "ndnd repo catalog ./repo.sample.yml",
		Run:     runCatalog,
	}
}

func runCatalog(_ *cobra.Command, args []string) {
	config := struct {
		Repo *Config `json:"repo"`
	}{
		Repo: DefaultConfig(),
	}
	toolutils.ReadYaml(&config, args[0])

	if err := config.Repo.Parse(); err != nil {
		log.Fatal(nil, "Configuration error", "err", err)
	}

	catalog := NewCatalog(config.Repo)
	if err := catalog.Start(); err != nil {
		log.Fatal(nil, "Failed to start catalog", "err", err)
	}
	defer catalog.Stop()

	sigChannel := make(chan os.Signal, 1)
	signal.Notify(sigChannel, os.Interrupt, syscall.SIGTERM)
	<-sigChannel
}
