package p2p

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

type Identity struct {
	PeerID  string
	PrivKey ed25519.PrivateKey
	PubKey  ed25519.PublicKey
}

func LoadOrCreateIdentity(path string) (*Identity, error) {
	if path == "" {
		priv, pub, err := newIdentity()
		if err != nil {
			return nil, err
		}
		return &Identity{PeerID: peerIDFromPub(pub), PrivKey: priv, PubKey: pub}, nil
	}
	if b, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(b)
		if block == nil {
			return nil, fmt.Errorf("invalid identity pem")
		}
		priv := ed25519.PrivateKey(block.Bytes)
		pub := priv.Public().(ed25519.PublicKey)
		return &Identity{PeerID: peerIDFromPub(pub), PrivKey: priv, PubKey: pub}, nil
	}
	priv, pub, err := newIdentity()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "INFERMESH ED25519 PRIVATE KEY", Bytes: []byte(priv)})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		return nil, err
	}
	return &Identity{PeerID: peerIDFromPub(pub), PrivKey: priv, PubKey: pub}, nil
}

func newIdentity() (ed25519.PrivateKey, ed25519.PublicKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return priv, pub, nil
}

func peerIDFromPub(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return base64.RawURLEncoding.EncodeToString(h[:16])
}
