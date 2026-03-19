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
	"github.com/named-data/ndnd/std/engine"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	"github.com/named-data/ndnd/std/object"
	"github.com/named-data/ndnd/std/object/storage"
	sec "github.com/named-data/ndnd/std/security"
	"github.com/named-data/ndnd/std/security/keychain"
)

type Catalog struct {
	config      *Config
	engine      ndn.Engine
	store       ndn.Store
	client      ndn.Client
	prefix      enc.Name
	mutex       sync.Mutex
	keychain    ndn.KeyChain
	trust       *sec.TrustConfig
	catalogPath string
}

type catalogEntry struct {
	File   string
	Owner  string
	Server string
}

func NewCatalog(config *Config) *Catalog {
	return &Catalog{
		config: config,
		prefix: config.NameN,
	}
}

func (c *Catalog) String() string {
	return "catalog"
}

func (c *Catalog) Start() error {
	var err error

	c.store = storage.NewMemoryStore()
	c.engine = engine.NewBasicEngine(engine.NewDefaultFace())
	if err = c.engine.Start(); err != nil {
		return err
	}

	kc, err := keychain.NewKeyChain(c.config.KeyChainUri, c.store)
	if err != nil {
		c.engine.Stop()
		return err
	}
	c.keychain = kc

	trust, err := c.config.NewTrustConfig(kc)
	if err != nil {
		c.engine.Stop()
		return err
	}
	c.trust = trust

	c.client = object.NewClient(c.engine, c.store, trust)
	if err := c.client.Start(); err != nil {
		c.engine.Stop()
		return err
	}

	statusPrefix := c.config.NameN.Append(enc.NewGenericComponent("status"))
	if err := c.engine.AttachHandler(statusPrefix, func(args ndn.InterestHandlerArgs) {
		intName := args.Interest.Name()

		wire, err := c.store.Get(intName, args.Interest.CanBePrefix())
		if err != nil || wire == nil {
			return
		}
		args.Reply(enc.Wire{wire})
	}); err != nil {
		return err
	}

	if err := c.client.AttachCommandHandler(c.prefix, c.onCmd); err != nil {
		c.client.Stop()
		c.engine.Stop()
		return err
	}

	c.client.AnnouncePrefix(ndn.Announcement{
		Name:   c.prefix,
		Expose: true,
	})

	c.catalogPath = filepath.Join(c.config.StorageDir, "catalog.csv")
	file, err := os.OpenFile(c.catalogPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			log.Info(c, "Catalog file alredy exists, using existaing catalog", "path", c.catalogPath)
		} else {
			log.Error(c, "Failed to create catalog file", "err", err)
			return err
		}
	}
	defer file.Close()

	log.Info(c, "Catalog daemon started", "prefix", c.prefix)
	return nil
}

func (c *Catalog) Stop() error {
	if c.client != nil {
		c.client.WithdrawPrefix(c.prefix, nil)
		_ = c.client.DetachCommandHandler(c.prefix)
		_ = c.client.Stop()
	}
	if c.engine != nil {
		c.engine.Stop()
	}
	return nil
}

func (c *Catalog) onCmd(_ enc.Name, wire enc.Wire, reply func(enc.Wire) error) {
	cmd, err := tlv.ParseCatalogCmd(enc.NewWireView(wire), false)
	if err != nil {
		log.Warn(c, "Failed to parse management command", "err", err)
		_ = reply((&tlv.RepoCmdRes{Status: 400, Message: "failed to parse management command"}).Encode())
		return
	}

	if cmd.GetFileInfo != nil {
		go c.handleGetFileInfo(cmd.GetFileInfo, reply)
		return
	} else if cmd.Insert != nil {
		go c.handleCatalogInsert(cmd.Insert, reply)
		return
	} else if cmd.Delete != nil {
		go c.handleCatalogDelete(cmd.Delete, reply)
		return
	}
	log.Warn(c, "Unknown management command received")
	_ = reply((&tlv.RepoCmdRes{Status: 400, Message: "unknown management command"}).Encode())
}

func (c *Catalog) handleGetFileInfo(cmd *tlv.CatalogGetFileInfo, reply func(enc.Wire) error) {
	log.Info(c, "Handle GetFileInfo command for file", "file", cmd.FileName, "owner", cmd.OwnerName)
	if cmd.FileName == "" {
		reply((&tlv.RepoCmdRes{Status: 400, Message: "file name are required"}).Encode())
		return
	}
	jobNamePrefix := c.config.NameN.Append(
		enc.NewGenericComponent("status"),
		enc.NewGenericComponent(fmt.Sprintf("%d", time.Now().UnixNano())),
	)
	currentName := jobNamePrefix.WithVersion(enc.VersionUnixMicro)
	reply((&tlv.RepoCmdRes{Status: 200, Message: currentName.String()}).Encode())

	resultCh := make(chan []byte, 1)
	publishStatus(c.client, 1*time.Second, currentName, jobNamePrefix, resultCh)

	owner := ""
	if cmd.OwnerName != nil {
		owner = cmd.OwnerName.Name.String()
	}
	c.mutex.Lock()
	data, err := c.findFileInfo(cmd.FileName, owner)
	if err != nil {
		payload, _ := json.Marshal(JobStatusPayload{Status: "failed", Message: err.Error()})
		resultCh <- payload
		return
	}
	c.mutex.Unlock()
	dataBytes, _ := json.Marshal(data)
	payload, _ := json.Marshal(JobStatusPayload{Status: "success", Result: dataBytes})
	resultCh <- payload
}
func (c *Catalog) handleCatalogInsert(cmd *tlv.CatalogEntry, reply func(enc.Wire) error) {
	log.Info(c, "Handle CatalogInsert command for file", "file", cmd.FileName, "owner", cmd.OwnerName.Name.String(), "server", cmd.ServerName.Name.String())
	if cmd.FileName == "" || cmd.OwnerName == nil || cmd.ServerName == nil || cmd.OwnerName.Name.String() == "" || cmd.ServerName.Name.String() == "" {
		reply((&tlv.RepoCmdRes{Status: 400, Message: "file name, owner name and server name are required"}).Encode())
		return
	}

	jobNamePrefix := c.config.NameN.Append(
		enc.NewGenericComponent("status"),
		enc.NewGenericComponent(fmt.Sprintf("%d", time.Now().UnixNano())),
	)
	currentName := jobNamePrefix.WithVersion(enc.VersionUnixMicro)

	reply((&tlv.RepoCmdRes{Status: 200, Message: currentName.String()}).Encode())
	resultCh := make(chan []byte, 1)
	publishStatus(c.client, 1*time.Second, currentName, jobNamePrefix, resultCh)

	c.mutex.Lock()
	if err := c.updateFileServer(cmd.FileName, cmd.OwnerName.Name.String(), cmd.ServerName.Name.String()); err != nil {
		payload, _ := json.Marshal(JobStatusPayload{Status: "failed", Message: err.Error()})
		resultCh <- payload
		return
	}
	c.mutex.Unlock()
	payload, _ := json.Marshal(JobStatusPayload{Status: "success"})
	resultCh <- payload
}

func (c *Catalog) handleCatalogDelete(cmd *tlv.CatalogEntry, reply func(enc.Wire) error) {
	log.Info(c, "Handle CatalogDelete command for file", "file", cmd.FileName, "owner", cmd.OwnerName.Name.String(), "server", cmd.ServerName.Name.String())
	if cmd.FileName == "" || cmd.OwnerName == nil || cmd.ServerName == nil || cmd.OwnerName.Name.String() == "" || cmd.ServerName.Name.String() == "" {
		reply((&tlv.RepoCmdRes{Status: 400, Message: "file name, owner name and server name are required"}).Encode())
		return
	}

	jobNamePrefix := c.config.NameN.Append(
		enc.NewGenericComponent("status"),
		enc.NewGenericComponent(fmt.Sprintf("%d", time.Now().UnixNano())),
	)
	currentName := jobNamePrefix.WithVersion(enc.VersionUnixMicro)
	reply((&tlv.RepoCmdRes{Status: 200, Message: currentName.String()}).Encode())

	resultCh := make(chan []byte, 1)
	publishStatus(c.client, 1*time.Second, currentName, jobNamePrefix, resultCh)

	c.mutex.Lock()
	if err := c.deleteFileFromCatalog(cmd.FileName, cmd.OwnerName.Name.String(), cmd.ServerName.Name.String()); err != nil {
		payload, _ := json.Marshal(JobStatusPayload{Status: "failed", Message: err.Error()})
		resultCh <- payload
		return
	}
	c.mutex.Unlock()
	payload, _ := json.Marshal(JobStatusPayload{Status: "success"})
	resultCh <- payload
}
