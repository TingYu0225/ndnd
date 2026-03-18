package trust_schema

import (
	"time"

	enc "github.com/named-data/ndnd/std/encoding"
	"github.com/named-data/ndnd/std/log"
	"github.com/named-data/ndnd/std/ndn"

	spec "github.com/named-data/ndnd/std/ndn/spec_2022"
	"github.com/named-data/ndnd/std/security/signer"
)

// PrefixSchema accepts every packet/certificate pair and chooses a signer by
// longest-prefix matching the packet name against identities in the keychain.
type PrefixSchema struct{}

// NewPrefixSchema creates a prefix-based trust schema.
func NewPrefixSchema() *PrefixSchema {
	return &PrefixSchema{}
}

// Check accepts every packet/certificate pair.
func (*PrefixSchema) Check(pkt enc.Name, cert enc.Name) bool {
	return true
}

// Suggest picks the most specific matching identity, prefers a key with a
// currently valid certificate, and uses that certificate prefix as KeyLocator.
func (*PrefixSchema) Suggest(pkt enc.Name, keychain ndn.KeyChain) ndn.Signer {
	var chosen ndn.KeyChainIdentity
	// find the most specific identity that matches the packet name
	for _, id := range keychain.Identities() {
		if !id.Name().IsPrefix(pkt) {
			continue
		}
		if chosen == nil || len(id.Name()) > len(chosen.Name()) {
			chosen = id
		}
	}

	if chosen == nil {
		log.Error(nil, "PrefixSchema: no matching identity for pkt", "pkt", pkt.String())
		return nil
	}

	for _, key := range chosen.Keys() {
		for _, cert := range key.UniqueCerts() {
			wire, err := keychain.Store().Get(cert.Prefix(-1), true)
			if err != nil || wire == nil {
				log.Error(nil, "PrefixSchema: cert fetch from store failed", "prefix", cert.Prefix(-1).String())
				continue
			}

			certData, _, err := spec.Spec{}.ReadData(enc.NewWireView(enc.Wire{wire}))
			if err != nil || prefixSchemaCertExpired(certData) {
				continue
			}

			return signer.WithKeyLocator(key.Signer(), cert.Prefix(-1))
		}
	}

	log.Error(nil, "PrefixSchema: no valid certificate-backed signer found for identity:", chosen.Name().String())
	return nil
}

// prefixSchemaCertExpired reports whether the certificate is missing signature
// validity information or is outside its validity period.
func prefixSchemaCertExpired(cert ndn.Data) bool {
	if cert.Signature() == nil {
		return true
	}

	now := time.Now()
	notBefore, notAfter := cert.Signature().Validity()
	if val, ok := notBefore.Get(); !ok || now.Before(val) {
		return true
	}
	if val, ok := notAfter.Get(); !ok || now.After(val) {
		return true
	}
	return false
}
