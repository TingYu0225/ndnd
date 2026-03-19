package repo

import (
	"fmt"
	"os"
	"path/filepath"
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

type RepoCommandParam struct {
	filePath       string   // Input file path, or - for stdin
	fileName       enc.Name // Data packet name, or a name prefix of segmented Data packets.
	startBlockID   uint64   // Start block ID for segmented data(interest)
	endBlockID     uint64   // End block ID for segmented data(interest)
	forwardingHint enc.Name // target server's ndn prefix, e.g. /ndnd/server
}

func CmdRepoInsert() *cobra.Command {
	t := RepoCommandParam{
		filePath:       "",
		fileName:       enc.Name{}, // Data packet name, or a name prefix of segmented Data packets.
		startBlockID:   0,          // Start block ID for segmented data(interest)
		endBlockID:     0,
		forwardingHint: enc.Name{},
	}

	cmd := &cobra.Command{
		Use:     "insert FILE_PATH SERVER_NDN_PREFIX CONFIG_FILE_PATH",
		Short:   "Insert one file into the server",
		Long:    `Create Data packets from input file and ask the server to store it.`,
		Args:    cobra.ExactArgs(3),
		Example: "ndnd repo insert /my/data/path/ /ndnd/server ./repo.sample.yml",
		Run:     t.run,
	}
	// TODO: check if filename works well
	cmd.Flags().String("filename", "", "filename")
	return cmd
}

// checkInsertRequest checks the validity of the insert request and fills the command parameters.
func checkInsertRequest(t *RepoCommandParam, args []string, cmd *cobra.Command) bool {

	// check if file exists
	t.filePath = args[0]
	_, err := os.Stat(t.filePath)
	if err != nil {
		log.Error(nil, "Read file failed", "err", err)
		return false
	}

	// if fileName is not provided, use the base name
	fileName, _ := cmd.Flags().GetString("filename")
	if cmd.Flags().Changed("filename") {
		if filepath.Ext(fileName) == "" {
			log.Error(nil, "Filename must include an extension")
			return false
		}
	} else {
		fileName = filepath.Base(t.filePath)
	}

	tempFileName, err := enc.NameFromStr(fileName) // decode packet name from string
	if err != nil {
		log.Error(nil, "Invalid file name", "err", err)
		return false
	}
	t.fileName = tempFileName

	forwardingHint, err := enc.NameFromStr(args[1])
	if err != nil {
		log.Error(nil, "Invalid server prefix", "err", err)
		return false
	}
	t.forwardingHint = forwardingHint

	log.Info(nil, "Insertion parameters", "file", t.filePath, "name", t.fileName, "server", t.forwardingHint)

	return true

}

// func insert(name enc.Name, startBlockID uint64, endBlockID uint64, client ndn.Client) error {

// }

func (t *RepoCommandParam) run(cmd *cobra.Command, args []string) {
	config := struct {
		Repo *Config `json:"repo"`
	}{
		Repo: DefaultConfig(),
	}

	toolutils.ReadYaml(&config, args[2])

	if err := config.Repo.Parse(); err != nil {
		log.Fatal(nil, "Configuration error", "err", err)
	}

	if checkInsertRequest(t, args, cmd) == false {
		return
	}

	// Create a basic engine with a default client
	app := engine.NewBasicEngine(engine.NewDefaultFace())
	if err := app.Start(); err != nil {
		log.Error(nil, "Engine start failed", "err", err)
		return
	}
	defer app.Stop()

	cliStore := storage.NewMemoryStore()
	kc, err := keychain.NewKeyChain(config.Repo.KeyChainUri, cliStore)
	if err != nil {
		log.Error(nil, "Keychain creation failed", "err", err)
		return
	}

	// Create trust config from the configured LVS schema.
	trust, err := config.Repo.NewTrustConfig(kc)
	if err != nil {
		log.Error(nil, "Trust config creation failed", "err", err)
		return
	}

	client := object.NewClient(app, cliStore, trust)
	if err := client.Start(); err != nil {
		log.Error(nil, "Client start failed", "err", err)
		return
	}
	client.AnnouncePrefix(ndn.Announcement{
		Name:   config.Repo.NameN,
		Expose: true,
	})
	defer client.Stop()

	// Read file content and chunk if necessary
	content, err := os.ReadFile(t.filePath)
	if err != nil {
		log.Error(nil, "Failed to read file", "err", err)
		return
	}

	dataName := t.fileName.WithVersion(enc.VersionUnixMicro)
	vname, err := client.Produce(ndn.ProduceArgs{
		Name:    config.Repo.NameN.Append(dataName...),
		Content: enc.Wire{content},
	})
	if err != nil {
		log.Error(nil, "Failed to produce", "err", err)
		return
	}
	// calculate endBlockID based on content size (Produce will chunk the content into segments of size 8000)
	lastSeg := uint64(0)
	lastSeg = uint64((len(content) - 1) / 8000)
	t.endBlockID = lastSeg

	log.Info(nil, "Produced Data", "name", vname.String(), "segments", lastSeg)

	// Send command to repo
	cmdName := config.Repo.NameN.Append(enc.NewKeywordComponent("insert")).
		WithVersion(enc.VersionUnixMicro)

	payload := (&tlv.RepoCmd{
		RepoCmdInsert: &tlv.RepoCmdInsert{
			InterestName:   &spec.NameContainer{Name: dataName},
			ForwardingHint: &spec.NameContainer{Name: config.Repo.NameN},
		},
	}).Encode()

	jobCh := make(chan string, 1)
	done := make(chan error, 1)
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
			log.Error(nil, "Failed to express command", "err", err)
			return
		}
	case jobName := <-jobCh:
		log.Info(nil, "Insert job started", "jobName", jobName)
		// polling job status until it's done or timeout
		if _, err := waitJobResult(client, jobName); err != nil {
			log.Error(nil, "Failed to wait for job result", "err", err)
			return
		}
		log.Info(nil, "Insert success")
	case <-time.After(30 * time.Second):
		log.Error(nil, "Insert command timeout", "timeout", 30*time.Second)
	}

}
