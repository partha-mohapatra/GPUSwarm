package p2p

import (
	"fmt"
	"os"
	"path/filepath"

	crypto "github.com/libp2p/go-libp2p/core/crypto"
)

type Libp2pIdentity struct {
	PrivKey crypto.PrivKey
	PubKey  crypto.PubKey
}

func LoadOrCreateLibp2pIdentity(path string) (*Libp2pIdentity, error) {
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			pk, err := crypto.UnmarshalPrivateKey(b)
			if err != nil {
				return nil, fmt.Errorf("unmarshal libp2p key: %w", err)
			}
			return &Libp2pIdentity{PrivKey: pk, PubKey: pk.GetPublic()}, nil
		}
	}
	pk, pub, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		return nil, err
	}
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		enc, err := crypto.MarshalPrivateKey(pk)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, enc, 0o600); err != nil {
			return nil, err
		}
	}
	return &Libp2pIdentity{PrivKey: pk, PubKey: pub}, nil
}
