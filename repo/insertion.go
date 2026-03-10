package repo

import (
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
	sec "github.com/named-data/ndnd/std/security"
	"github.com/named-data/ndnd/std/security/keychain"
	"github.com/named-data/ndnd/std/security/trust_schema"
	"github.com/named-data/ndnd/std/types/optional"
	"github.com/named-data/ndnd/std/utils/toolutils"
	"github.com/spf13/cobra"
)

type RepoCommandParam struct {
	file_path       string   // Input file path, or - for stdin
	file_name       enc.Name // Data packet name, or a name prefix of segmented Data packets.
	start_block_id  uint64   // Start block ID for segmented data(interest)
	end_block_id    uint64   // End block ID for segmented data(interest)
	forwarding_hint enc.Name // target server's ndn prefix, e.g. /ndnd/server
}

func CmdRepoInsert() *cobra.Command {
	t := RepoCommandParam{
		file_path:       "",
		file_name:       enc.Name{}, // Data packet name, or a name prefix of segmented Data packets.
		start_block_id:  0,          // Start block ID for segmented data(interest)
		end_block_id:    0,
		forwarding_hint: enc.Name{},
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
	t.file_path = args[0]
	_, err := os.Stat(t.file_path)
	if err != nil {
		fmt.Println("read file failed:", err)
		return false
	}

	// if fileName is not provided, use the base name
	fileName, _ := cmd.Flags().GetString("filename")
	if cmd.Flags().Changed("filename") {
		fmt.Println("user provided --filename:", fileName)
	} else {
		fmt.Println("--filename not provided")
		base := filepath.Base(t.file_path)
		fileName = base[:len(base)-len(filepath.Ext(base))]
	}

	tempFileName, err := enc.NameFromStr(fileName) // decode packet name from string
	if err != nil {
		fmt.Println("invalid file name:", err)
		return false
	}
	t.file_name = tempFileName

	forwardingHint, err := enc.NameFromStr(args[1])
	if err != nil {
		fmt.Println("invalid server prefix:", err)
		return false
	}
	t.forwarding_hint = forwardingHint

	fmt.Println("File:", t.file_path)
	fmt.Println("File Name:", t.file_name)
	fmt.Println("Server:", t.forwarding_hint)

	return true

}

func waitJobDone(client ndn.Client, jobName string, serverPrefix enc.Name) error {
	fmt.Println("Start waiting for job done with job name:", jobName)
	jobBase, err := enc.NameFromStr(jobName)
	if err != nil {
		return fmt.Errorf("invalid job name: %w", err)
	}

	resultPrefix := jobBase.Append(enc.NewGenericComponent("result"))
	heartbeatPrefix := jobBase.Append(enc.NewGenericComponent("heartbeat"))

	// deadline := time.After(timeout)
	ticker := time.NewTicker(2 * time.Second) // check job status every second
	idleTimeout := 10 * time.Second           // if job is processing for more than 5 seconds without status update, consider it failed and timeout
	idleTimer := time.NewTimer(idleTimeout)
	defer ticker.Stop()
	defer idleTimer.Stop()
	lastHeartbeat := ""

	fetchText := func(name enc.Name) (string, error) { // send interest to fetch
		resultCh := make(chan string, 1)
		errCh := make(chan error, 1)

		client.ExpressR(ndn.ExpressRArgs{
			Name: name,
			Config: &ndn.InterestConfig{
				CanBePrefix: true,
				MustBeFresh: true,
				Lifetime:    optional.Some(2 * time.Second),
			},
			Retries: 0,
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
		case <-time.After(3 * time.Second):
			return "", fmt.Errorf("local wait timeout")
		}
	}

	for {
		select {
		case <-idleTimer.C:
			return fmt.Errorf("wait job timeout: %s", jobName)

		case <-ticker.C:
			fmt.Println("Checking job result...", resultPrefix.String())

			result, err := fetchText(resultPrefix) // fetch job result
			if err == nil {
				switch {
				case result == "done":
					fmt.Println("job done")
					return nil
				case strings.HasPrefix(result, "failed:"):
					return fmt.Errorf("job failed: %s", strings.TrimPrefix(result, "failed:"))
				default:
					fmt.Printf("unexpected job result: %s\n", result)
				}
			}

			fmt.Println("Checking heartbeat...", heartbeatPrefix.String())

			hb, hbErr := fetchText(heartbeatPrefix)
			if hbErr != nil {
				fmt.Printf("heartbeat check failed: %v\n", hbErr)
				continue
			}

			fmt.Printf("heartbeat: %s\n", hb)

			if hb != "" && hb != lastHeartbeat {
				lastHeartbeat = hb
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(idleTimeout)
			}
		}
	}
}

// func insert(name enc.Name, start_block_id uint64, end_block_id uint64, client ndn.Client) error {

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

	// Create a basic engine with a default face
	app := engine.NewBasicEngine(engine.NewDefaultFace())
	if err := app.Start(); err != nil {
		fmt.Println("engine start failed:", err)
		return
	}
	defer app.Stop()

	// Create a client
	// TODO: use trust config
	cliStore := storage.NewMemoryStore()
	kc, err := keychain.NewKeyChain(config.Repo.KeyChainUri, cliStore)
	if err != nil {
		fmt.Println("keychain creation failed:", err)
		return
	}

	// TODO: enforce trust schema defined by repo provider
	schema := trust_schema.NewNullSchema()

	// TODO: handle app-specific case
	anchors := config.Repo.TrustAnchorNames()

	// Create trust config
	trust, err := sec.NewTrustConfig(kc, schema, anchors)
	if err != nil {
		fmt.Println("trust config creation failed:", err)
		return
	}
	trust.UseDataNameFwHint = true

	client := object.NewClient(app, storage.NewMemoryStore(), trust)
	if err := client.Start(); err != nil {
		fmt.Println("client start failed:", err)
		return
	}
	defer client.Stop()

	// Read file content and chunk if necessary
	content, err := os.ReadFile(t.file_path)
	if err != nil {
		fmt.Println("failed to read file:", err)
		return
	}
	vname, err := client.Produce(ndn.ProduceArgs{
		Name:    config.Repo.NameN.Append(t.file_name...).WithVersion(enc.VersionUnixMicro),
		Content: enc.Wire{content},
	})
	if err != nil {
		fmt.Println("failed to produce:", err)
		return
	}
	// calculate end_block_id based on content size (Produce will chunk the content into segments of size 8000)
	lastSeg := uint64(0)
	lastSeg = uint64((len(content) - 1) / 8000)
	t.end_block_id = lastSeg

	fmt.Printf("Produced Data with name: %s, segments: 0-%d\n", vname.String(), lastSeg)
	// Announce prefix before BlobFetch so server can fetch segments.
	client.AnnouncePrefix(ndn.Announcement{
		Name:   config.Repo.NameN,
		Expose: true,
	})

	// Create command name
	cmdName, err := enc.NameFromStr(config.Repo.Name)
	cmdName = cmdName.Append(enc.NewKeywordComponent("insert")).
		WithVersion(enc.VersionUnixMicro)

	// Create BlobFetch command payload
	payload := (&tlv.RepoCmd{
		BlobFetch: &tlv.BlobFetch{
			Name: &spec.NameContainer{
				Name: vname,
			},
			Data: [][]byte{[]byte("insert")},
		},
	}).Encode()

	println("send BlobFetch command")
	// Send command to repo
	jobCh := make(chan string, 1)
	done := make(chan error, 1)
	client.ExpressCommand(t.forwarding_hint, cmdName, payload, func(w enc.Wire, err error) {
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
		if err := waitJobDone(client, jobName, t.forwarding_hint); err != nil {
			fmt.Println("wait job failed:", err)
			return
		}
		fmt.Println("Insert success")
	case <-time.After(30 * time.Second):
		fmt.Printf("insert command timeout after %s\n", 30*time.Second)
	}

}
