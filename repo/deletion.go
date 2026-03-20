package repo

import (
	"fmt"

	"github.com/named-data/ndnd/repo/tlv"
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/engine"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/object"
	"github.com/named-data/ndnd/std/object/storage"
	"github.com/named-data/ndnd/std/security/keychain"
	"github.com/named-data/ndnd/std/utils/toolutils"
	"github.com/spf13/cobra"
)

type RepoDeleteTool struct {
	fileName       string
	forwardingHint enc.Name
	client         ndn.Client
	config         *Config
}

// CmdRepoDelete sends a command to the repo daemon that deletes one Data packet.
func CmdRepoDelete() *cobra.Command {
	t := RepoDeleteTool{
		fileName:       "",
		forwardingHint: enc.Name{},
		client:         nil,
		config:         nil,
	}

	cmd := &cobra.Command{
		Use:     "delete FILE_NAME CONFIG_FILE_PATH",
		Short:   "Delete one file from the repo daemon",
		Long:    `Delete one file from the repo daemon.`,
		Args:    cobra.ExactArgs(2),
		Example: "ndnd repo delete hello.txt ./repo.sample.yml",
		Run:     t.run,
	}
	return cmd
}

func (t *RepoDeleteTool) run(_ *cobra.Command, args []string) {
	t.fileName = args[0]
	tempConfig := struct {
		Repo *Config `json:"repo"`
	}{
		Repo: DefaultConfig(),
	}

	toolutils.ReadYaml(&tempConfig, args[1])
	t.config = tempConfig.Repo

	if err := t.config.Parse(); err != nil {
		log.Fatal(nil, "Configuration error", "err", err)
	}

	// Create a basic engine with a default client
	app := engine.NewBasicEngine(engine.NewDefaultFace())
	if err := app.Start(); err != nil {
		log.Error(nil, "Engine start failed", "err", err)
		return
	}
	defer app.Stop()

	cliStore := storage.NewMemoryStore()
	kc, err := keychain.NewKeyChain(t.config.KeyChainUri, cliStore)
	if err != nil {
		log.Error(nil, "Keychain creation failed", "err", err)
		return
	}

	// Create trust config from the configured LVS schema.
	trust, err := t.config.NewTrustConfig(kc)
	if err != nil {
		log.Error(nil, "Trust config creation failed", "err", err)
		return
	}

	t.client = object.NewClient(app, cliStore, trust)
	if err := t.client.Start(); err != nil {
		log.Error(nil, "Client start failed", "err", err)
		return
	}
	t.client.AnnouncePrefix(ndn.Announcement{
		Name:   t.config.NameN,
		Expose: true,
	})
	defer t.client.Stop()

	// Find the owner of target file
	result, err := lookupCatalogEntries(t.client, t.config, t.fileName, t.config.NameN)
	if err != nil {
		log.Error(nil, "Failed to lookup catalog entries", "err", err)
		return
	}
	if len(result) > 1 {
		log.Warn(nil, "More than one entries found for the file")
		return
	}
	t.forwardingHint, _ = enc.NameFromStr(result[0].Server)

	// send delete command to repo daemon
	if err := t.sendDeleteCommand(); err != nil {
		log.Error(nil, "send delete command failed", "err", err)
		return
	}
}

func (t *RepoDeleteTool) sendDeleteCommand() error {
	jobCh := make(chan string, 1)
	done := make(chan error, 1)
	payload := (&tlv.RepoCmd{
		RepoCmdDelete: &tlv.RepoCmdDelete{
			FileName:  t.fileName,
			OwnerName: &spec.NameContainer{Name: t.config.NameN},
		},
	}).Encode()

	cmdName := t.config.NameN.Append(enc.NewKeywordComponent("delete")).WithVersion(enc.VersionUnixMicro)
	t.client.ExpressCommand(t.forwardingHint, cmdName, payload, func(w enc.Wire, err error) {
		if err != nil {
			done <- err
			return
		}
		res, err := tlv.ParseRepoCmdRes(enc.NewWireView(w), false)
		if err != nil {
			done <- err
			return
		}
		if res.Status != 200 {
			done <- fmt.Errorf("status=%d msg=%s", res.Status, res.Message)
			return
		}
		if res.Status == 200 {
			jobCh <- res.Message
			return
		}
		done <- nil
	})

	// Wait for response or timeout
	select {
	case err := <-done:
		if err != nil {
			log.Error(nil, "Failed to express delete command", "err", err)
			return err
		}
	case jobName := <-jobCh:
		log.Info(nil, "Delete job started", "jobName", jobName)
		// polling job status until it's done or timeout
		if _, err := waitJobResult(t.client, jobName); err != nil {
			log.Error(nil, "Failed to wait for job result", "err", err)
			return err
		}
		log.Info(nil, "Delete success")
	}
	return nil
}
