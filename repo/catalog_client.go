package repo

import (
	"encoding/json"
	"fmt"

	"github.com/named-data/ndnd/repo/tlv"
	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
)

func lookupCatalogEntries(client ndn.Client, config *Config, fileName string, ownerName enc.Name) ([]catalogEntry, error) {
	payload := (&tlv.CatalogCmd{GetFileInfo: &tlv.CatalogGetFileInfo{
		FileName:  fileName,
		OwnerName: &spec.NameContainer{Name: ownerName},
	}}).Encode()
	cmdName := config.NameN.Append(enc.NewKeywordComponent("getinfo")).WithVersion(enc.VersionUnixMicro)

	info, err := sendCatalogCommand(client, config, cmdName, payload)
	if err != nil {
		log.Error(client, "Failed to get catalog info", "err", err)
		return nil, err
	}
	var result []catalogEntry
	if err := json.Unmarshal([]byte(info), &result); err != nil {
		log.Error(client, "invalid job status payload", "err", err)
		return nil, err
	}
	if len(result) == 0 {
		log.Error(client, "file not found in catalog")
		return nil, fmt.Errorf("file not found in catalog")
	}

	log.Info(client, "Find server info success")
	return result, nil
}

func sendCatalogInsert(client ndn.Client, config *Config, fileName string, ownerName enc.Name, serverName enc.Name) error {
	payload := (&tlv.CatalogCmd{Insert: &tlv.CatalogEntry{
		FileName:   fileName,
		OwnerName:  &spec.NameContainer{Name: ownerName},
		ServerName: &spec.NameContainer{Name: serverName},
	}}).Encode()
	cmdName := config.NameN.Append(enc.NewKeywordComponent("insertcatalog")).WithVersion(enc.VersionUnixMicro)
	_, err := sendCatalogCommand(client, config, cmdName, payload)
	if err != nil {
		log.Error(client, "Failed to insert catalog info", "err", err)
		return err
	}

	log.Info(client, "Insert info success")
	return nil
}
func sendCatalogDelete(client ndn.Client, config *Config, fileName string, ownerName enc.Name, serverName enc.Name) error {
	payload := (&tlv.CatalogCmd{Delete: &tlv.CatalogEntry{
		FileName:   fileName,
		OwnerName:  &spec.NameContainer{Name: ownerName},
		ServerName: &spec.NameContainer{Name: serverName},
	}}).Encode()
	cmdName := config.NameN.Append(enc.NewKeywordComponent("deletecatalog")).WithVersion(enc.VersionUnixMicro)
	_, err := sendCatalogCommand(client, config, cmdName, payload)
	if err != nil {
		log.Error(client, "Failed to delete catalog info", "err", err)
		return err
	}

	log.Info(client, "Delete info success")
	return nil
}

func sendCatalogCommand(client ndn.Client, config *Config, cmdName enc.Name, payload enc.Wire) ([]byte, error) {
	jobCh := make(chan string, 1)
	errCh := make(chan error, 1)

	client.ExpressCommand(config.CatalogNameN, cmdName, payload, func(w enc.Wire, err error) {
		if err != nil {
			errCh <- err
			return
		}
		res, err := tlv.ParseRepoCmdRes(enc.NewWireView(w), false)
		if err != nil {
			errCh <- err
			return
		}
		if res.Status != 200 {
			errCh <- fmt.Errorf("status=%d msg=%s", res.Status, res.Message)
			return
		}
		if res.Status == 200 {
			jobCh <- res.Message
			return
		}
	})
	select {
	case err := <-errCh:
		return nil, err
	case jobName := <-jobCh:
		return waitJobResult(client, jobName)
	}
}
