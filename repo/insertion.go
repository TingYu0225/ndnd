package repo

import (
	"fmt"
	"os"
	"time"

	"github.com/named-data/ndnd/repo/tlv"
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/engine"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/object"
	"github.com/named-data/ndnd/std/object/storage"
	"github.com/spf13/cobra"
)

type RepoCommandParam struct {
	file_path       string   // Input file path, or - for stdin
	file_name       enc.Name // Data packet name, or a name prefix of segmented Data packets.
	start_block_id  uint64   // Start block ID for segmented data(interest)
	end_id          uint64
	forwarding_hint enc.Name
}

func CmdRepoInsert() *cobra.Command {
	t := RepoCommandParam{
		file_path:       "",
		file_name:       enc.Name{}, // Data packet name, or a name prefix of segmented Data packets.
		start_block_id:  0,          // Start block ID for segmented data(interest)
		end_id:          0,
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
	client := object.NewClient(app, storage.NewMemoryStore(), nil)
	if err := client.Start(); err != nil {
		fmt.Println("client start failed:", err)
		return
	}
	defer client.Stop()

	// // Read file content
	// content, err := os.ReadFile(filePath)
	// if err != nil {
	// 	fmt.Println("failed to read file:", err)
	// 	return
	// }

	// // Produce the data so repo can fetch it
	// client.Produce(ndn.ProduceArgs{
	// 	Prefix:  t.file_name,
	// 	Content: content,
	// })

	// Create command name
	cmdName := t.file_name.
		Append(enc.NewKeywordComponent("insert")).
		WithVersion(enc.VersionUnixMicro)

	// Create BlobFetch command payload
	payload := (&tlv.RepoCmd{
		BlobFetch: &tlv.BlobFetch{
			Name: &spec.NameContainer{
				Name: t.file_name,
			},
		},
	}).Encode()

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
		if err != nil {
			fmt.Println("failed:", err)
			return
		}
		fmt.Println("insert command success")
	case <-time.After(4 * time.Second):
		fmt.Println("insert command timeout")
	}
}
