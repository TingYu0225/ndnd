package repo

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/named-data/ndnd/repo/tlv"
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/engine"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/object"
	"github.com/named-data/ndnd/std/object/storage"
	"github.com/spf13/cobra"
)

type RepoCommandParam struct {
	file_path       string   // Input file path, or - for stdin
	file_name       enc.Name // Data packet name, or a name prefix of segmented Data packets.
	start_block_id  uint64   // Start block ID for segmented data(interest)
	end_block_id    uint64
	forwarding_hint enc.Name
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
		Use:   "insert FILE_PATH FILE_NAME SERVER_NDN_PREFIX",
		Short: "Insert one Data packet into the server",
		Long: `Create one Data packet from input bytes and ask the server to store it.
Input is read from stdin by default.`,
		Args:    cobra.ExactArgs(3),
		Example: "ndnd repo insert /my/data/path/ test server",
		Run:     t.run,
	}

	return cmd
}

// checkInsertRequest checks the validity of the insert request and fills the command parameters.
func checkInsertRequest(t *RepoCommandParam, filePath string, packetNameStr string, serverPrefixStr string) bool {
	_, err := os.Stat(filePath) // check if file exists
	if err != nil {
		fmt.Println("read file failed:", err)
		return false
	}

	fileName, err := enc.NameFromStr(packetNameStr) // decode packet name from string
	if err != nil {
		fmt.Println("invalid file name:", err)
		return false
	}

	forwardingHint, err := enc.NameFromStr(serverPrefixStr)
	if err != nil {
		fmt.Println("invalid server prefix:", err)
		return false
	}

	t.file_path = filePath
	// t.file_name = globalConfig.NameN.Append(t.file_name) // prepend repo name to the file name
	t.file_name = fileName
	t.forwarding_hint = forwardingHint
	return true
}

// func insert(name enc.Name, start_block_id uint64, end_block_id uint64, client ndn.Client) error {

// }

func (t *RepoCommandParam) run(_ *cobra.Command, args []string) {
	filePath := args[0]
	fileName := args[1]
	serverPrefix := args[2]

	fmt.Println("File:", filePath)
	fmt.Println("File Name:", fileName)
	fmt.Println("Server:", serverPrefix)

	if checkInsertRequest(t, filePath, fileName, serverPrefix) == false {
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
	client := object.NewClient(app, storage.NewMemoryStore(), nil)
	if err := client.Start(); err != nil {
		fmt.Println("client start failed:", err)
		return
	}
	defer client.Stop()

	// Read file content and chunk if necessary
	content, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Println("failed to read file:", err)
		return
	}
	vname, err := client.Produce(ndn.ProduceArgs{
		Name:    t.file_name.WithVersion(enc.VersionUnixMicro),
		Content: enc.Wire{content},
	})
	if err != nil {
		fmt.Println("failed to produce:", err)
		return
	}
	// calculate end_block_id based on content size (Produce will chunk the content into segments of size 8000)
	lastSeg := uint64(0)
	if len(content) > 0 {
		lastSeg = uint64((len(content) - 1) / 8000)
	}
	t.end_block_id = lastSeg

	fmt.Printf("Produced Data with name: %s, segments: 0-%d\n", vname.String(), lastSeg)

	// Announce prefix before BlobFetch so server can fetch segments.
	client.AnnouncePrefix(ndn.Announcement{
		Name:   vname,
		Expose: true,
	})
	defer client.WithdrawPrefix(vname, nil) // Withdraw prefix after command is done or timeout, so server won't fetch other data with the same prefix.

	announceStop := make(chan struct{}) // notify background goroutine to stop announcing when BlobFetch is done or timeout.
	announceDone := make(chan struct{}) // Stop announcement when BlobFetch is done or timeout.
	var stopOnce sync.Once
	stopAnnounce := func() {
		stopOnce.Do(func() {
			close(announceStop)
			<-announceDone // block until goroutine stop announceDone
		})
	}
	defer stopAnnounce()

	// Keep announcement alive in background while server is fetching.
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		defer close(announceDone)
		for {
			select {
			case <-announceStop:
				return
			case <-ticker.C:
				client.AnnouncePrefix(ndn.Announcement{
					Name:   vname,
					Expose: true,
				})
			}
		}
	}()

	// Create command name
	cmdName := t.file_name.
		Append(enc.NewKeywordComponent("insert")).
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
		done <- nil
	})

	// Wait for response or timeout
	select {
	case err := <-done:
		stopAnnounce()
		if err != nil {
			fmt.Println("failed:", err)
			return
		}
		fmt.Println("insert command success")
	case <-time.After(180 * time.Second):
		stopAnnounce()
		fmt.Println("insert command timeout")
	}
}
