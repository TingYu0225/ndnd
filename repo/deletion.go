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
	// Find the owner of target file
	// TODO check if this is good to go?

	// Create a basic engine with a default client
	println("Creating engine...")
	app := engine.NewBasicEngine(engine.NewDefaultFace())
	if err := app.Start(); err != nil {
		fmt.Println("engine start failed:", err)
		return
	}
	defer app.Stop()

	cliStore := storage.NewMemoryStore()
	kc, err := keychain.NewKeyChain(t.config.KeyChainUri, cliStore)
	if err != nil {
		fmt.Println("keychain creation failed:", err)
		return
	}

	// Create trust config from the configured LVS schema.
	trust, err := t.config.NewTrustConfig(kc)
	if err != nil {
		fmt.Println("trust config creation failed:", err)
		return
	}

	t.client = object.NewClient(app, cliStore, trust)
	if err := t.client.Start(); err != nil {
		fmt.Println("client start failed:", err)
		return
	}
	t.client.AnnouncePrefix(ndn.Announcement{
		Name:   t.config.NameN,
		Expose: true,
	})
	defer t.client.Stop()

	// ask catalog daemon for the target file's owner and server
	result, err := lookupCatalogEntries(t.client, t.config, t.fileName, t.config.NameN)
	if err != nil {
		fmt.Println("lookup catalog entries failed:", err)
		return
	}
	if len(result) > 1 {
		fmt.Println("More than one entries found for the file")
		return
	}
	t.forwardingHint, _ = enc.NameFromStr(result[0].Server)

	// send delete command to repo daemon
	if err := t.sendDeleteCommand(); err != nil {
		fmt.Println("send delete command failed:", err)
		return
	}
}

func (t *RepoDeleteTool) sendDeleteCommand() error {
	jobCh := make(chan string, 1)
	done := make(chan error, 1)
	payload := (&tlv.RepoCmd{
		RepoCmdDelete: &tlv.RepoCmdDelete{
			FileName:       t.fileName,
			ForwardingHint: &spec.NameContainer{Name: t.config.NameN},
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
			fmt.Println("failed:", err)
			return err
		}
	case jobName := <-jobCh:
		fmt.Printf("delete job started with job name: %s\n", jobName)
		fmt.Println("delete command success")
		// polling job status until it's done or timeout
		if _, err := waitJobResult(t.client, jobName); err != nil {
			fmt.Println("wait job failed:", err)
			return err
		}
		fmt.Println("Delete success")
	}
	return nil
}
