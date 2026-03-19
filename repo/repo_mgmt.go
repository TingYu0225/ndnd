package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/named-data/ndnd/repo/tlv"
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/object"
	sec "github.com/named-data/ndnd/std/security"
	"github.com/named-data/ndnd/std/security/trust_schema"
	"github.com/named-data/ndnd/std/types/optional"
)

// (AI GENERATED DESCRIPTION): Parses a repository management command from the received wire and dispatches it to the sync‑join handler if present, otherwise logs a warning about an unknown command.
func (r *Repo) onMgmtCmd(_ enc.Name, wire enc.Wire, reply func(enc.Wire) error) {
	cmd, err := tlv.ParseRepoCmd(enc.NewWireView(wire), false)
	if err != nil {
		log.Warn(r, "Failed to parse management command", "err", err)
		_ = reply((&tlv.RepoCmdRes{Status: 400, Message: "failed to parse management command"}).Encode())
		return
	}

	if cmd.SyncJoin != nil {
		go r.handleSyncJoin(cmd.SyncJoin, reply)
		return
	}

	if cmd.SyncLeave != nil {
		go r.handleSyncLeave(cmd.SyncLeave, reply)
		return
	}
	if cmd.RepoCmdDelete != nil {
		go r.handleDeletion(cmd.RepoCmdDelete, reply)
		return
	}
	if cmd.RepoCmdInsert != nil {
		go r.handleInsertion(cmd.RepoCmdInsert, reply)
		return
	}
	log.Warn(r, "Unknown management command received")
	_ = reply((&tlv.RepoCmdRes{Status: 400, Message: "unknown management command"}).Encode())
}

func (r *Repo) handleInsertion(cmd *tlv.RepoCmdInsert, reply func(enc.Wire) error) { // background goroutine to process the command and update status
	jobNamePrefix := r.config.NameN.Append(
		enc.NewGenericComponent("status"),
		enc.NewGenericComponent(fmt.Sprintf("%d", time.Now().UnixNano())),
	)
	currentName := jobNamePrefix.WithVersion(enc.VersionUnixMicro)

	if cmd.ForwardingHint == nil || len(cmd.ForwardingHint.Name) == 0 || cmd.InterestName == nil || len(cmd.InterestName.Name) == 0 {
		reply((&tlv.RepoCmdRes{Status: 400, Message: "invalid forwarding hint"}).Encode())
		return
	}
	reply((&tlv.RepoCmdRes{Status: 200, Message: currentName.String()}).Encode())

	// TODO: replace with better timeout calculation based on file size or other factors
	timeout := 5 * time.Second
	resultCh := make(chan []byte, 1)
	forwardingHint := cmd.ForwardingHint.Name
	fileName := cmd.InterestName.Name.At(-2).String()
	publishStatus(r.client, timeout, currentName, jobNamePrefix, resultCh)

	//  lookupCatalogEntries(r.client, r.config, fileName, forwardingHint) // check if file already exists in catalog

	//  build file for received file
	dirParts := []string{r.config.StorageDir}
	for i := 0; i < len(forwardingHint); i++ {
		dirParts = append(dirParts, forwardingHint.At(i).CanonicalString())
	}
	dirPath := filepath.Join(dirParts...)
	println("DEBUG: Constructed directory path for insertion:", dirPath)
	println(forwardingHint.String())

	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		payload, _ := json.Marshal(JobStatusPayload{Status: "failed", Message: err.Error()})
		resultCh <- payload
		return
	}

	filePath := filepath.Join(dirPath, fileName)

	file, err := os.Create(filePath)
	if err != nil {
		payload, _ := json.Marshal(JobStatusPayload{Status: "failed", Message: err.Error()})
		resultCh <- payload
		return
	}
	defer file.Close()

	interestName := forwardingHint.Append(cmd.InterestName.Name...)
	//interest
	println("Starting fetch for:", interestName.String())

	done := make(chan error, 1)
	println("DEBUG: About to call ConsumeExt for name:", interestName.String())
	r.client.ConsumeExt(ndn.ConsumeExtArgs{
		Name:           interestName,
		TryStore:       true,
		IgnoreValidity: optional.Some(r.config.IgnoreValidity),
		Callback: func(state ndn.ConsumeState) {
			println("DEBUG: Callback invoked - Error:", state.Error(), "Complete:", state.IsComplete(), "Progress:", state.Progress())
			if state.Error() != nil { // fetch failed
				println("DEBUG: Error in fetch:", state.Error().Error())
				select {
				case done <- state.Error():
				default:
				}
				return
			}
			for _, chunk := range state.Content() {
				println("DEBUG: Received chunk of size:", len(chunk))
				file.Write(chunk)
			}
			if state.IsComplete() { // fetch succeeded
				println("DEBUG: Fetch complete")
				select {
				case done <- nil:
				default:
				}
			}
		},
	})
	println("DEBUG: ConsumeExt returned, waiting for done channel...")

	select {
	case err := <-done: // fetch completed or failedq
		if err != nil {
			payload, _ := json.Marshal(JobStatusPayload{Status: "failed", Message: err.Error()})
			resultCh <- payload
			println("DEBUG: Fetch failed with error:", err.Error())
		}
	case <-time.After(8 * time.Second):
		payload, _ := json.Marshal(JobStatusPayload{Status: "failed", Message: "fetch timeout"})
		resultCh <- payload
		println("DEBUG: select timeout")
	}

	err = sendCatalogInsert(r.client, r.config, fileName, forwardingHint, r.config.NameN)
	if err != nil {
		payload, _ := json.Marshal(JobStatusPayload{Status: "failed", Message: err.Error()})
		resultCh <- payload
		return
	}
	// updateFileOwner(r.config.CatalogPath, fileName, r.config.Name) // add user name
	payload, _ := json.Marshal(JobStatusPayload{Status: "success"})
	resultCh <- payload
	println("DEBUG: reply completed")
}

func (r *Repo) handleDeletion(cmd *tlv.RepoCmdDelete, reply func(enc.Wire) error) {
	jobNamePrefix := r.config.NameN.Append(
		enc.NewGenericComponent("status"),
		enc.NewGenericComponent(fmt.Sprintf("%d", time.Now().UnixNano())),
	)
	currentName := jobNamePrefix.WithVersion(enc.VersionUnixMicro)
	if cmd.ForwardingHint == nil || len(cmd.ForwardingHint.Name) == 0 || cmd.FileName == "" {
		reply((&tlv.RepoCmdRes{Status: 400, Message: "invalid forwarding hint or file name"}).Encode())
		return
	}
	forwardingHint := cmd.ForwardingHint.Name
	reply((&tlv.RepoCmdRes{Status: 200, Message: currentName.String()}).Encode())
	resultCh := make(chan []byte, 1)
	// TODO: replace with better timeout calculation based on file size or other factors
	timeout := 1 * time.Second
	publishStatus(r.client, timeout, currentName, jobNamePrefix, resultCh)
	// defer close(doneCh)
	dirParts := []string{r.config.StorageDir}
	for i := 0; i < len(forwardingHint); i++ {
		dirParts = append(dirParts, forwardingHint.At(i).CanonicalString())
	}
	dirPath := filepath.Join(dirParts...)
	fileName := cmd.FileName
	filePath := filepath.Join(dirPath, fileName)

	// find status of this file
	if _, err := os.Stat(filePath); err != nil {
		// if file doesn't exit
		if os.IsNotExist(err) {
			payload, _ := json.Marshal(JobStatusPayload{Status: "failed", Message: "file not found"})
			resultCh <- payload
		} else {
			payload, _ := json.Marshal(JobStatusPayload{Status: "failed", Message: err.Error()})
			resultCh <- payload
		}
		return
	}

	if err := os.Remove(filePath); err != nil {
		payload, _ := json.Marshal(JobStatusPayload{Status: "failed", Message: err.Error()})
		resultCh <- payload
		return
	}
	err := sendCatalogDelete(r.client, r.config, fileName, forwardingHint, r.config.NameN)
	if err != nil {
		payload, _ := json.Marshal(JobStatusPayload{Status: "failed", Message: err.Error()})
		resultCh <- payload
		return
	}
	//deleteFileFromCatalog(r.config.CatalogPath, fileName)
	payload, _ := json.Marshal(JobStatusPayload{Status: "success"})
	resultCh <- payload
	println("delete file success")
}

// (AI GENERATED DESCRIPTION): Handles a `SyncJoin` command by starting an SVS session when the protocol is `SyncProtocolSvsV3`, or returning an error status if the protocol is unknown or the session fails to start.
func (r *Repo) handleSyncJoin(cmd *tlv.SyncJoin, reply func(enc.Wire) error) {
	res := tlv.RepoCmdRes{Status: 200}

	if cmd.Protocol != nil && cmd.Protocol.Name.Equal(tlv.SyncProtocolSvsV3) {
		if err := r.startSvs(cmd); err != nil {
			res.Status = 500
			log.Error(r, "Failed to start SVS", "err", err)
		}
		reply(res.Encode())
		return
	}

	log.Warn(r, "Unknown sync protocol specified in command", "protocol", cmd.Protocol)
	res.Status = 400
	reply(res.Encode())
}

// (AI GENERATED DESCRIPTION): Handles a SyncLeave command by stopping an SVS session for the specified group when the protocol is `SyncProtocolSvsV3`; otherwise, responds with an error status.
func (r *Repo) handleSyncLeave(cmd *tlv.SyncLeave, reply func(enc.Wire) error) {
	res := tlv.RepoCmdRes{Status: 200}

	if cmd.Protocol != nil && cmd.Protocol.Name.Equal(tlv.SyncProtocolSvsV3) {
		if err := r.stopSvs(cmd); err != nil {
			res.Status = 500
			res.Message = err.Error()
			log.Error(r, "Failed to stop SVS", "err", err)
		}
		reply(res.Encode())
		return
	}

	log.Warn(r, "Unknown sync protocol specified in command", "protocol", cmd.Protocol)
	res.Status = 400
	reply(res.Encode())
}

// (AI GENERATED DESCRIPTION): Starts a SyncJoin session for the specified group, initializing a new RepoSvs if one isn’t already active and storing it in the repository’s group session map.
func (r *Repo) startSvs(cmd *tlv.SyncJoin) error {
	if cmd.Group == nil || len(cmd.Group.Name) == 0 {
		return fmt.Errorf("missing group name")
	}

	var secCfg *tlv.SecurityConfigObject
	if cmd.SecurityConfig != nil && cmd.SecurityConfig.Name != nil && len(cmd.SecurityConfig.Name) > 0 {
		var err error
		secCfg, err = r.fetchSecurityConfig(cmd.SecurityConfig.Name)
		if err != nil {
			return err
		}
	} else {
		// No security config provided; use defaults later.
	}

	if secCfg == nil {
		secCfg = &tlv.SecurityConfigObject{}
	}
	if len(secCfg.Schema) == 0 && len(secCfg.Anchors) == 0 {
		// fallback to defaults handled in buildGroupTrust
	} else if len(secCfg.Schema) == 0 || len(secCfg.Anchors) == 0 {
		return fmt.Errorf("security config must include both schema and anchors")
	}

	var schema ndn.TrustSchema
	var anchors []ndn.Data
	var anchorWires []enc.Wire
	var anchorNames []enc.Name
	var secErrors error

	// Prepare schema
	if len(secCfg.Schema) > 0 {
		schema, secErrors = trust_schema.NewLvsSchema(secCfg.Schema)
		if secErrors != nil {
			return fmt.Errorf("invalid trust schema: %w", secErrors)
		}
	} else {
		schema = trust_schema.NewNullSchema()
	}

	// Prepare anchros
	for _, anchorBytes := range secCfg.Anchors {
		anchorData, _, secErrors := spec.Spec{}.ReadData(enc.NewBufferView(anchorBytes))
		if secErrors == nil {
			anchors = append(anchors, anchorData)
			anchorNames = append(anchorNames, anchorData.Name())
			anchorWires = append(anchorWires, enc.Wire{anchorBytes})
		}
	}

	// Update existing repo svs instance
	hash := cmd.Group.Name.TlvStr()
	if existing, ok := r.groupsSvs[hash]; ok && existing != nil {
		existing.Client().SetTrustSchema(schema)
		for idx, anchor := range anchors {
			existing.Client().PromoteTrustAnchor(anchor, anchorWires[idx])
		}
		return nil
	} else {
		for _, anchorBytes := range anchorWires {
			r.keychain.InsertCert(anchorBytes.Join())
		}

		// Start group with specific client
		groupTrust, secErrors := sec.NewTrustConfig(r.keychain, schema, anchorNames)
		if secErrors != nil {
			return fmt.Errorf("cannot initialize trust config: %w", secErrors)
		}
		groupClient := object.NewClient(r.engine, r.store, groupTrust)
		svs := NewRepoSvs(r.config, groupClient, cmd)
		if err := svs.Start(); err != nil {
			return err
		}
		r.groupsSvs[hash] = svs
		return nil
	}
}

// stopSvs stops an SVS instance for the specified group.
func (r *Repo) stopSvs(cmd *tlv.SyncLeave) error {
	if cmd.Group == nil || len(cmd.Group.Name) == 0 {
		return fmt.Errorf("missing group name")
	}

	hash := cmd.Group.Name.TlvStr()

	r.mutex.Lock()
	svs, ok := r.groupsSvs[hash]
	r.mutex.Unlock()
	if !ok {
		return fmt.Errorf("group not joined")
	}

	if err := svs.Stop(); err != nil {
		return err
	}

	r.mutex.Lock()
	delete(r.groupsSvs, hash)
	r.mutex.Unlock()

	return nil
}

// fetchSecurityConfig retrieves and parses the security config object.
func (r *Repo) fetchSecurityConfig(name enc.Name) (*tlv.SecurityConfigObject, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}
	if len(name) == 0 {
		return nil, fmt.Errorf("missing security config name")
	}

	var (
		wire enc.Wire
		err  error
		once sync.Once
		done = make(chan struct{})
	)

	// Repo should validate this as normal command
	r.client.ConsumeExt(ndn.ConsumeExtArgs{
		Name:           name,
		TryStore:       true,
		IgnoreValidity: optional.Some(r.config.IgnoreValidity),
		Callback: func(state ndn.ConsumeState) {
			wire = append(wire, state.Content()...)
			if state.Error() != nil {
				err = state.Error()
			}
			if state.IsComplete() || err != nil {
				once.Do(func() { close(done) })
			}
		},
	})

	<-done

	if err != nil {
		return nil, err
	}
	if len(wire) == 0 {
		return nil, fmt.Errorf("empty security config object")
	}

	return tlv.ParseSecurityConfigObject(enc.NewWireView(wire), false)
}
