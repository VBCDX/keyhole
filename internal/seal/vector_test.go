package seal

import (
	"bytes"
	"crypto/ecdh"
	"encoding/hex"
	"testing"
)

// The v2 vector in docs/resolve.md: fixed keys, nonce and tool definition,
// so the sidecar side (vbcdx-core) can check its AAD and its open against
// these exact bytes. An independent Python implementation (the
// cryptography package) gives the same digest, AAD and ciphertext.
const (
	vectorRecipientPub = "e06Qm75__kTEZaIgA31gjuNYl9Me-XLwf3SJLLD3PxM"
	vectorEPK          = "D6poTtKIZ7l_Smot7l34zpdOdrcBjj8iocTPJnhXDyA"
	vectorNonce        = "MzMzMzMzMzMzMzMz"
	vectorToolDigest   = "36f6359e8bc01be18445221a09fcbb24c9afd94229e98a4d1eb9001bb109084c"
	vectorAAD          = "76626364782d6b6579686f6c652d7365616c2d76320000000a7265715f766563746f7200000009745f6c6974656c6c6d0000000b6c6974656c6c6d5f6b657900000001310000002036f6359e8bc01be18445221a09fcbb24c9afd94229e98a4d1eb9001bb109084c"
	vectorCT           = "SYi5wggpMgN6f1seVz3zdQYiw1corvpc6S_7RZoXRWp2m_kMW-y4Bw"
	vectorPlaintext    = "sk-test-vector-not-a-key"
)

var vectorDef = ToolDef{
	Version: 1, BaseURL: "https://litellm.example.invalid", TimeoutMs: 60000,
	SlotLocation: "header", SlotField: "Authorization", SlotScheme: "Bearer",
	PrivateAllow: []string{"10.0.0.20/32"},
	Actions:      []Action{{"GET", "/v1/models"}, {"POST", "/v1/chat/completions"}},
}

func TestDocumentedVector(t *testing.T) {
	rsk, _ := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{0x11}, 32))
	esk, _ := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{0x22}, 32))
	if got := b64.EncodeToString(rsk.PublicKey().Bytes()); got != vectorRecipientPub {
		t.Fatalf("recipient pub = %s", got)
	}
	if d := ToolDigest(vectorDef); hex.EncodeToString(d[:]) != vectorToolDigest {
		t.Fatalf("tool digest = %x; the documented vector changed", d)
	}
	aad := AAD("req_vector", "t_litellm", "litellm_key", vectorDef)
	if hex.EncodeToString(aad) != vectorAAD {
		t.Fatalf("aad = %x; the documented vector changed", aad)
	}
	s, err := sealWith(esk, bytes.Repeat([]byte{0x33}, 12), rsk.PublicKey(), aad, []byte(vectorPlaintext))
	if err != nil {
		t.Fatal(err)
	}
	if s.EPK != vectorEPK || s.Nonce != vectorNonce || s.CT != vectorCT || s.Alg != Alg {
		t.Fatalf("sealed = %+v; the documented vector changed", s)
	}
	got, err := Open(rsk, Sealed{Alg, vectorEPK, vectorNonce, vectorCT}, aad)
	if err != nil || string(got) != vectorPlaintext {
		t.Fatalf("open = %q, %v", got, err)
	}
}
