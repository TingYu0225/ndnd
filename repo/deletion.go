package repo

import (
	"fmt"
	"time"

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
}

// CmdRepoDelete sends a command to the repo daemon that deletes one Data packet.
func CmdRepoDelete() *cobra.Command {
	t := RepoDeleteTool{
		fileName:       "",
		forwardingHint: enc.Name{},
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
	config := struct {
		Repo *Config `json:"repo"`
	}{
		Repo: DefaultConfig(),
	}

	toolutils.ReadYaml(&config, args[1])

	if err := config.Repo.Parse(); err != nil {
		log.Fatal(nil, "Configuration error", "err", err)
	}
	// Find the owner of target file
	// TODO check if this is good to go?
	target, err := findFileOwner(config.Repo.CatalogPath, t.fileName)
	if err != nil {
		fmt.Println("catalog lookup failed:", err)
		return
	}
	t.forwardingHint, _ = enc.NameFromStr(target)

	// Create a basic engine with a default client
	println("Creating engine...")
	app := engine.NewBasicEngine(engine.NewDefaultFace())
	if err := app.Start(); err != nil {
		fmt.Println("engine start failed:", err)
		return
	}
	defer app.Stop()

	cliStore := storage.NewMemoryStore()
	kc, err := keychain.NewKeyChain(config.Repo.KeyChainUri, cliStore)
	if err != nil {
		fmt.Println("keychain creation failed:", err)
		return
	}

	// Create trust config from the configured LVS schema.
	trust, err := newLvsTrustConfig(kc, config.Repo)
	if err != nil {
		fmt.Println("trust config creation failed:", err)
		return
	}

	client := object.NewClient(app, cliStore, trust)
	if err := client.Start(); err != nil {
		fmt.Println("client start failed:", err)
		return
	}
	client.AnnouncePrefix(ndn.Announcement{
		Name:   config.Repo.NameN,
		Expose: true,
	})
	defer client.Stop()

	// cmdName, err := enc.NameFromStr(config.Repo.Name)
	// cmdName = cmdName.Append(enc.NewKeywordComponent("insert")).
	// 	WithVersion(enc.VersionUnixMicro)
	name, err := enc.NameFromStr("/" + t.fileName)
	// Create BlobFetch command payload
	payload := (&tlv.RepoCmd{
		BlobFetch: &tlv.BlobFetch{
			Name: &spec.NameContainer{
				Name: name,
			},
			Data: [][]byte{
				[]byte("delete"),
				config.Repo.NameN.Bytes(),
			},
		},
	}).Encode()

	println("send BlobFetch command")
	// Send command to repo
	jobCh := make(chan string, 1)
	done := make(chan error, 1)

	cmdName := config.Repo.NameN.Append(enc.NewKeywordComponent("delete")).
		WithVersion(enc.VersionUnixMicro)
	client.ExpressCommand(t.forwardingHint, cmdName, payload, func(w enc.Wire, err error) {
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
			return
		}
	case jobName := <-jobCh:
		fmt.Printf("delete job started with job name: %s\n", jobName)
		fmt.Println("delete command success")
		// polling job status until it's done or timeout
		if err := waitJobDone(client, jobName); err != nil {
			fmt.Println("wait job failed:", err)
			return
		}
		fmt.Println("Delete success")
	case <-time.After(30 * time.Second):
		fmt.Printf("delete command timeout after %s\n", 30*time.Second)
	}

}
