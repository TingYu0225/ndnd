//go:generate gondn_tlv_gen
package tlv

import (
	enc "github.com/named-data/ndnd/std/encoding"
	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
)

var SyncProtocolSvsV3 = enc.Name{
	enc.NewKeywordComponent("ndn"),
	enc.NewKeywordComponent("svs"),
	enc.NewVersionComponent(3),
}

type RepoCmd struct {
	//+field:struct:SyncJoin
	SyncJoin *SyncJoin `tlv:"0x1DB0"`
	//+field:struct:SyncLeave
	SyncLeave *SyncLeave `tlv:"0x1DB1"`
	//+field:struct:BlobFetch
	BlobFetch *BlobFetch `tlv:"0x1DB2"`
	//+field:struct:RepoCmdDelete
	RepoCmdDelete *RepoCmdDelete `tlv:"0x1DB3"`
	//+field:struct:RepoCmdInsert
	RepoCmdInsert *RepoCmdInsert `tlv:"0x1DB4"`
}

type RepoCmdRes struct {
	//+field:natural
	Status uint64 `tlv:"0x291"`
	//+field:string
	Message string `tlv:"0x292"`
}

type RepoCmdInsert struct {
	//+field:struct:spec.NameContainer
	InterestName *spec.NameContainer `tlv:"0x1DB5"`
	//+field:struct:spec.NameContainer
	ForwardingHint *spec.NameContainer `tlv:"0x1DB6"`
}
type RepoCmdDelete struct {
	//+field:string
	FileName string `tlv:"0x1DB5"`
	//+field:struct:spec.NameContainer
	ForwardingHint *spec.NameContainer `tlv:"0x1DB6"`
}

type SyncJoin struct {
	//+field:struct:spec.NameContainer
	Protocol *spec.NameContainer `tlv:"0x191"`
	//+field:struct:spec.NameContainer
	Group *spec.NameContainer `tlv:"0x193"`
	//+field:struct:spec.NameContainer
	MulticastPrefix *spec.NameContainer `tlv:"0x194"`
	//+field:struct:HistorySnapshotConfig
	HistorySnapshot *HistorySnapshotConfig `tlv:"0x1A4"`
	//+field:struct:spec.NameContainer
	SecurityConfig *spec.NameContainer `tlv:"0x1DB4"`
}

type SyncLeave struct {
	//+field:struct:spec.NameContainer
	Protocol *spec.NameContainer `tlv:"0x191"`
	//+field:struct:spec.NameContainer
	Group *spec.NameContainer `tlv:"0x193"`
}

type HistorySnapshotConfig struct {
	//+field:natural
	Threshold uint64 `tlv:"0x1A5"`
}

type BlobFetch struct {
	//+field:struct:spec.NameContainer
	Name *spec.NameContainer `tlv:"0x1B8"`
	//+field:sequence:[]byte:binary:[]byte
	Data [][]byte `tlv:"0x1BA"`
}

type SecurityConfigObject struct {
	//+field:binary
	Schema []byte `tlv:"0x1A5"`
	//+field:sequence:[]byte:binary:[]byte
	Anchors [][]byte `tlv:"0x1BA"`
}

type CatalogCmd struct {
	//+field:struct:CatalogGetFileInfo
	GetFileInfo *CatalogGetFileInfo `tlv:"0x1E00"`
	//+field:struct:CatalogEntry
	Insert *CatalogEntry `tlv:"0x1E01"`
	//+field:struct:CatalogEntry
	Delete *CatalogEntry `tlv:"0x1E02"`
}

type CatalogGetFileInfo struct {
	//+field:string
	FileName string `tlv:"0x1E00"`
	//+field:struct:spec.NameContainer
	OwnerName *spec.NameContainer `tlv:"0x1E01"`
}

type CatalogEntry struct {
	//+field:string
	FileName string `tlv:"0x1E05"`
	//+field:struct:spec.NameContainer
	OwnerName *spec.NameContainer `tlv:"0x1E06"`
	//+field:struct:spec.NameContainer
	ServerName *spec.NameContainer `tlv:"0x1E07"`
}
