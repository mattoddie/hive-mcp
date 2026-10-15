package hive

import (
	"encoding/json"
	"math/big"
	"os"
	"testing"
	"time"
)

// testdata/srp_vectors.json was generated with pyhive's own SRP helpers
// (Pyhass/Pyhive src/api/srp_crypto.py, composed as hive_auth.py does), so
// these tests check this implementation against the client that Home
// Assistant users log in to Hive with.
type srpVectors struct {
	SmallA          string `json:"a"`
	BigA            string `json:"A"`
	BigB            string `json:"B"`
	KHex            string `json:"k"`
	Timestamp       string `json:"timestamp"`
	SecretBlock     string `json:"secret_block"`
	UserSalt        string `json:"user_salt"`
	UserSignature   string `json:"user_signature"`
	DeviceSalt      string `json:"device_salt"`
	DeviceSignature string `json:"device_signature"`
	Verifier        string `json:"verifier"`
	VerifierSalt    string `json:"verifier_salt"`
}

func loadVectors(t *testing.T) srpVectors {
	t.Helper()
	b, err := os.ReadFile("testdata/srp_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v srpVectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSRPMatchesPyhive(t *testing.T) {
	v := loadVectors(t)
	if srpK.Text(16) != v.KHex {
		t.Fatalf("k = %s, want %s", srpK.Text(16), v.KHex)
	}
	s, err := srpSessionFrom(hexToInt(v.SmallA))
	if err != nil {
		t.Fatal(err)
	}
	if s.SRPA() != v.BigA {
		t.Fatalf("A differs")
	}
	now := time.Date(2026, 10, 6, 9, 5, 3, 0, time.UTC)
	user, err := s.claim("SamplePool", "user-id-for-srp", "correct horse", map[string]string{"SALT": v.UserSalt, "SRP_B": v.BigB, "SECRET_BLOCK": v.SecretBlock}, now)
	if err != nil {
		t.Fatal(err)
	}
	if user["TIMESTAMP"] != v.Timestamp || user["PASSWORD_CLAIM_SIGNATURE"] != v.UserSignature || user["PASSWORD_CLAIM_SECRET_BLOCK"] != v.SecretBlock {
		t.Errorf("user claim %v, want signature %s at %q", user, v.UserSignature, v.Timestamp)
	}
	dev, err := s.claim("-group", "eu-west-1_device", "devpass==", map[string]string{"SALT": v.DeviceSalt, "SRP_B": v.BigB, "SECRET_BLOCK": v.SecretBlock}, now)
	if err != nil {
		t.Fatal(err)
	}
	if dev["PASSWORD_CLAIM_SIGNATURE"] != v.DeviceSignature {
		t.Errorf("device signature %s, want %s", dev["PASSWORD_CLAIM_SIGNATURE"], v.DeviceSignature)
	}
	ver := deviceVerifier("-group", "eu-west-1_device", "devpass==", hexToInt(v.DeviceSalt))
	if ver.PasswordVerifier != v.Verifier || ver.Salt != v.VerifierSalt {
		t.Errorf("verifier %+v", ver)
	}
}

func TestPadHexAndTimestamp(t *testing.T) {
	for n, want := range map[int64]string{1: "01", 0xff: "00ff", 0x1a: "1a", 0xabc: "0abc", 0x7f00: "7f00"} {
		if got := padHex(big.NewInt(n)); got != want {
			t.Errorf("padHex(%x) = %s, want %s", n, got, want)
		}
	}
	for in, want := range map[time.Time]string{
		time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC):  "Fri Jan 2 15:04:05 UTC 2026",
		time.Date(2026, 11, 20, 3, 0, 9, 0, time.UTC): "Fri Nov 20 03:00:09 UTC 2026",
	} {
		if got := cognitoTimestamp(in); got != want {
			t.Errorf("timestamp %q, want %q", got, want)
		}
	}
}

func TestClaimRejectsBadChallenges(t *testing.T) {
	s, _ := newSRPSession()
	if _, err := s.claim("p", "u", "pw", map[string]string{"SALT": "01"}, time.Now()); err == nil {
		t.Error("missing SRP_B accepted")
	}
	if _, err := s.claim("p", "u", "pw", map[string]string{"SALT": "01", "SRP_B": srpN.Text(16), "SECRET_BLOCK": "AA=="}, time.Now()); err == nil {
		t.Error("B = N accepted")
	}
}
