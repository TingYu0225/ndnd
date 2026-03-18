package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	"github.com/named-data/ndnd/std/types/optional"
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

type JobStatusPayload struct {
	Status           string `json:"status"` // processing | done | failed
	NextInterestName string `json:"next_interest_name"`
	RetryAfter       string `json:"retry_after"`
	Message          string `json:"message,omitempty"`
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
	cmd.Flags().String("filename", "", "filename")
	return cmd
}

// checkInsertRequest checks the validity of the insert request and fills the command parameters.
func checkInsertRequest(t *RepoCommandParam, args []string, cmd *cobra.Command) bool {

	// check if file exists
	t.filePath = args[0]
	_, err := os.Stat(t.filePath)
	if err != nil {
		fmt.Println("read file failed:", err)
		return false
	}

	// if fileName is not provided, use the base name
	fileName, _ := cmd.Flags().GetString("filename")
	if cmd.Flags().Changed("filename") {
		fmt.Println("user provided --filename:", fileName)
		if filepath.Ext(fileName) == "" {
			fmt.Println("filename must include an extension")
			return false
		}
	} else {
		fmt.Println("--filename not provided")
		fileName = filepath.Base(t.filePath)
	}

	tempFileName, err := enc.NameFromStr(fileName) // decode packet name from string
	if err != nil {
		fmt.Println("invalid file name:", err)
		return false
	}
	t.fileName = tempFileName

	forwardingHint, err := enc.NameFromStr(args[1])
	if err != nil {
		fmt.Println("invalid server prefix:", err)
		return false
	}
	t.forwardingHint = forwardingHint

	fmt.Println("File:", t.filePath)
	fmt.Println("File Name:", t.fileName)
	fmt.Println("Server:", t.forwardingHint)

	return true

}
func waitJobDone(client ndn.Client, jobName string) error {
	fmt.Println("Start waiting for job done with job name:", jobName)

	jobInterest, err := enc.NameFromStr(jobName)
	if err != nil {
		return fmt.Errorf("invalid job name: %w", err)
	}
	lastVer := uint64(0)

	checkTimer := time.NewTimer(0 * time.Second)
	defer checkTimer.Stop()

	fetchText := func(name enc.Name) (string, error) {
		resultCh := make(chan string, 1)
		errCh := make(chan error, 1)

		client.ExpressR(ndn.ExpressRArgs{
			Name: name,
			Config: &ndn.InterestConfig{
				CanBePrefix: true,
				MustBeFresh: true,
				Lifetime:    optional.Some(2 * time.Second),
			},
			Retries: 2,
			Callback: func(args ndn.ExpressCallbackArgs) {
				if args.Result != ndn.InterestResultData {
					errCh <- fmt.Errorf("%s", args.Result)
					return
				}
				resultCh <- string(args.Data.Content().Join())
			},
		})

		select {
		case s := <-resultCh:
			return s, nil
		case err := <-errCh:
			return "", err
		}
	}

	for range checkTimer.C {
		fmt.Println("Checking job status...", jobInterest.String())

		status, err := fetchText(jobInterest)
		if err != nil {
			fmt.Printf("status check failed: %v\n", err)
			return fmt.Errorf("wait job timeout: %s", jobName)
		}
		var st JobStatusPayload
		if err := json.Unmarshal([]byte(status), &st); err != nil {
			return fmt.Errorf("invalid status payload: %w raw=%q", err, status)
		}

		switch st.Status {
		case "done":
			fmt.Println("job done")
			return nil

		case "failed":
			return fmt.Errorf("job failed: %s", st.Message)

		case "processing":
			println("job processing, will check again after", st.RetryAfter)
			println("next interest name for status check:", st.NextInterestName)
			checkTimeout, err := time.ParseDuration(st.RetryAfter)
			if err != nil {
				return fmt.Errorf("failed to parse processing time: %v\n", err)
			}
			nextName, err := enc.NameFromStr(strings.TrimSpace(st.NextInterestName))
			if err != nil {
				return fmt.Errorf("invalid next_interest_name %q: %w", st.NextInterestName, err)
			}
			ver := nextName.At(-1).NumberVal()
			if ver <= lastVer {
				return fmt.Errorf("next interest version not increasing: %d <= %d", ver, lastVer)
			}
			jobInterest = nextName
			checkTimer.Reset(checkTimeout)

		default:
			return fmt.Errorf("unexpected status: %s", st.Status)
		}
	}
	return fmt.Errorf("status loop ended unexpectedly")
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

	// Read file content and chunk if necessary
	content, err := os.ReadFile(t.filePath)
	if err != nil {
		fmt.Println("failed to read file:", err)
		return
	}

	dataName := t.fileName.WithVersion(enc.VersionUnixMicro)
	vname, err := client.Produce(ndn.ProduceArgs{
		Name:    config.Repo.NameN.Append(dataName...),
		Content: enc.Wire{content},
	})
	if err != nil {
		fmt.Println("failed to produce:", err)
		return
	}
	// calculate endBlockID based on content size (Produce will chunk the content into segments of size 8000)
	lastSeg := uint64(0)
	lastSeg = uint64((len(content) - 1) / 8000)
	t.endBlockID = lastSeg

	fmt.Printf("Produced Data with name: %s, segments: 0-%d\n", vname.String(), lastSeg)
	fmt.Printf("Data name: %s", dataName.String())
	// Announce prefix of client's name

	// Create command name
	cmdName := config.Repo.NameN.Append(enc.NewKeywordComponent("insert")).
		WithVersion(enc.VersionUnixMicro)

	// Create BlobFetch command payload
	payload := (&tlv.RepoCmd{
		BlobFetch: &tlv.BlobFetch{
			Name: &spec.NameContainer{
				Name: dataName,
			},
			Data: [][]byte{
				[]byte("insert"),
				config.Repo.NameN.Bytes(),
			},
		},
	}).Encode()

	println("send BlobFetch command")
	// Send command to repo
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
			fmt.Println("failed:", err)
			return
		}
	case jobName := <-jobCh:
		fmt.Printf("insert job started with job name: %s\n", jobName)
		fmt.Println("insert command success")
		// polling job status until it's done or timeout
		if err := waitJobDone(client, jobName); err != nil {
			fmt.Println("wait job failed:", err)
			return
		}
		fmt.Println("Insert success")
	case <-time.After(30 * time.Second):
		fmt.Printf("insert command timeout after %s\n", 30*time.Second)
	}

}
