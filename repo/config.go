package repo

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/ndn"
	sec "github.com/named-data/ndnd/std/security"
	"github.com/named-data/ndnd/std/security/trust_schema"
)

// Default binary LVS schema bundled with the repo.
//
//go:embed config/schema.tlv
var defaultSchemaBytes []byte

type Config struct {
	// Name is the name of the repo service.
	Name string `json:"name"`
	// StorageDir is the directory to store data.
	StorageDir string `json:"storage_dir"`
	// URI specifying KeyChain location.
	KeyChainUri string `json:"keychain"`
	// List of trust anchor full names.
	TrustAnchors []string `json:"trust_anchors"`
	// IgnoreValidity skips validity period checks when fetching remote data (e.g. SVS snapshots).
	IgnoreValidity bool `json:"ignore_validity"`
	// CatalogName is the name of the catalog.
	CatalogName  string `json:"catalog_name"`
	CatalogNameN enc.Name
	// NameN is the parsed name of the repo service.
	NameN enc.Name
}

// (AI GENERATED DESCRIPTION): Parses the configuration by validating the repository name, ensuring a storage directory is specified, converting it to an absolute path, and creating the directory if necessary.
func (c *Config) Parse() (err error) {
	c.NameN, err = enc.NameFromStr(c.Name)
	if err != nil || len(c.NameN) == 0 {
		return fmt.Errorf("failed to parse or invalid repo name (%s): %w", c.Name, err)
	}

	if c.StorageDir == "" {
		return fmt.Errorf("storage-dir must be set")
	} else {
		path, err := filepath.Abs(c.StorageDir)
		if err != nil {
			return fmt.Errorf("failed to get absolute path: %w", err)
		}
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("failed to create storage directory: %w", err)
		}
		c.StorageDir = path
	}

	c.CatalogNameN, err = enc.NameFromStr(c.CatalogName)
	if err != nil || len(c.CatalogNameN) == 0 {
		return fmt.Errorf("failed to parse or invalid catalog name (%s): %w", c.CatalogName, err)
	}

	return nil
}

// (AI GENERATED DESCRIPTION): Returns the trust‑anchor names stored in the Config as parsed enc.Name objects, panicking if any string cannot be parsed.
func (c *Config) TrustAnchorNames() []enc.Name {
	res := make([]enc.Name, len(c.TrustAnchors))
	for i, ta := range c.TrustAnchors {
		var err error
		res[i], err = enc.NameFromStr(ta)
		if err != nil {
			panic(fmt.Sprintf("failed to parse trust anchor name (%s): %v", ta, err))
		}
	}
	return res
}

// SchemaBytes returns the loaded binary LVS schema.
func (c *Config) SchemaBytes() []byte {
	return defaultSchemaBytes
}

// NewTrustConfig builds a trust config from the embedded LVS schema.
func (c *Config) NewTrustConfig(keychain ndn.KeyChain) (*sec.TrustConfig, error) {
	schema, err := trust_schema.NewLvsSchema(c.SchemaBytes())
	if err != nil {
		return nil, fmt.Errorf("invalid embedded repo LVS schema: %w", err)
	}

	trust, err := sec.NewTrustConfig(keychain, schema, c.TrustAnchorNames())
	if err != nil {
		return nil, err
	}
	trust.UseDataNameFwHint = true
	return trust, nil
}

// (AI GENERATED DESCRIPTION): Returns a new Config with default placeholder values: empty Name and StorageDir strings, and a nil NameN slice.
func DefaultConfig() *Config {
	return &Config{
		Name:       "", // invalid
		StorageDir: "", // invalid

		NameN: nil,
	}
}
