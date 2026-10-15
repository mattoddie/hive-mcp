package hive

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

// This file implements the client side of AWS Cognito's flavour of SRP-6a
// (Secure Remote Password), which Hive's login uses. It follows
// amazon-cognito-identity-js's AuthenticationHelper and the Python port in
// pycognito/pyhiveapi exactly, because Cognito checks a signature built from
// these values byte for byte:
//
//   - N is the 3072-bit prime from RFC 5054 and g = 2.
//   - Hashes are SHA-256 over byte strings decoded from hex. Numbers are
//     turned into hex with padHex, which adds a leading "00" when the top bit
//     is set (Java/JS BigInteger sign handling), so the same number always
//     hashes the same way on both sides.
//   - k = H(pad(N) | pad(g)), u = H(pad(A) | pad(B)),
//     x = H(pad(salt) | H(<pool name><username>:<password>)).
//   - The session key S = (B - k*g^x)^(a + u*x) mod N is stretched with
//     HKDF-SHA256 (salt = pad(u), info = "Caldera Derived Key") to 16 bytes.
//   - The claim is HMAC-SHA256(key, <pool name><username><secret block><timestamp>).
//
// Device authentication (DEVICE_SRP_AUTH) is the same with the device group
// key in place of the pool name, the device key in place of the username and
// a random device password, whose verifier is registered with ConfirmDevice.

const nHex = "FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD1" +
	"29024E088A67CC74020BBEA63B139B22514A08798E3404DD" +
	"EF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245" +
	"E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED" +
	"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3D" +
	"C2007CB8A163BF0598DA48361C55D39A69163FA8FD24CF5F" +
	"83655D23DCA3AD961C62F356208552BB9ED529077096966D" +
	"670C354E4ABC9804F1746C08CA18217C32905E462E36CE3B" +
	"E39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9" +
	"DE2BCBF6955817183995497CEA956AE515D2261898FA0510" +
	"15728E5A8AAAC42DAD33170D04507A33A85521ABDF1CBA64" +
	"ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7" +
	"ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6B" +
	"F12FFA06D98A0864D87602733EC86A64521F2B18177B200C" +
	"BBE117577A615D6C770988C0BAD946E208E24FA074E5AB31" +
	"43DB5BFCE0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF"

const infoBits = "Caldera Derived Key"

var (
	srpN, _ = new(big.Int).SetString(nHex, 16)
	srpG    = big.NewInt(2)
	srpK    = hexToInt(hexHash("00" + nHex + "0" + "2"))
)

func hexToInt(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return new(big.Int)
	}
	return n
}

// padHex returns n as hex with an even number of digits, prefixed with "00"
// when the first digit has its top bit set.
func padHex(n *big.Int) string {
	return padHexString(n.Text(16))
}

func padHexString(s string) string {
	if len(s)%2 == 1 {
		return "0" + s
	}
	if s != "" && strings.ContainsRune("89abcdefABCDEF", rune(s[0])) {
		return "00" + s
	}
	return s
}

// hashHex is SHA-256 of b as 64 lowercase hex digits.
func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// hexHash hashes the bytes that a hex string encodes.
func hexHash(s string) string {
	b, _ := hex.DecodeString(s)
	return hashHex(b)
}

func hkdf(ikm, salt []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	prk := mac.Sum(nil)
	mac = hmac.New(sha256.New, prk)
	mac.Write([]byte(infoBits))
	mac.Write([]byte{1})
	return mac.Sum(nil)[:16]
}

// srpSession is one login attempt's secret a and public A.
type srpSession struct {
	a, A *big.Int
}

func newSRPSession() (*srpSession, error) {
	b := make([]byte, 128)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return srpSessionFrom(new(big.Int).SetBytes(b))
}

// srpSessionFrom uses a fixed secret, for tests against known values.
func srpSessionFrom(a *big.Int) (*srpSession, error) {
	a = new(big.Int).Mod(a, srpN)
	A := new(big.Int).Exp(srpG, a, srpN)
	if A.Sign() == 0 {
		return nil, errors.New("SRP safety check for A failed")
	}
	return &srpSession{a: a, A: A}, nil
}

// SRPA is the public value sent as SRP_A.
func (s *srpSession) SRPA() string { return s.A.Text(16) }

// authKey derives the 16-byte HKDF key from the server's B and salt for a
// secret "<prefix><name>:<password>".
func (s *srpSession) authKey(prefix, name, password, bHex, saltHex string) ([]byte, error) {
	B := hexToInt(bHex)
	if B.Sign() == 0 || new(big.Int).Mod(B, srpN).Sign() == 0 {
		return nil, errors.New("the server sent an invalid SRP_B")
	}
	u := hexToInt(hexHash(padHex(s.A) + padHex(B)))
	if u.Sign() == 0 {
		return nil, errors.New("SRP u is zero")
	}
	// The salt is read as a number first, so leading zero bytes are dropped (as amazon-cognito-identity-js does).
	x := hexToInt(hexHash(padHex(hexToInt(saltHex)) + hashHex([]byte(prefix+name+":"+password))))
	gx := new(big.Int).Exp(srpG, x, srpN)
	base := new(big.Int).Sub(B, new(big.Int).Mul(srpK, gx))
	base.Mod(base, srpN)
	exp := new(big.Int).Add(s.a, new(big.Int).Mul(u, x))
	S := new(big.Int).Exp(base, exp, srpN)
	ikm, _ := hex.DecodeString(padHex(S))
	salt, _ := hex.DecodeString(padHex(u))
	return hkdf(ikm, salt), nil
}

var leadingZeroDay = regexp.MustCompile(` 0(\d) `)

// cognitoTimestamp formats t the way Cognito expects: "Mon Jan 2 15:04:05 UTC 2006", with no leading zero on the day.
func cognitoTimestamp(t time.Time) string {
	return leadingZeroDay.ReplaceAllString(t.UTC().Format("Mon Jan 02 15:04:05 UTC 2006"), " $1 ")
}

// claim builds the PASSWORD_VERIFIER (or DEVICE_PASSWORD_VERIFIER) response parameters.
// For a user, prefix is the pool name (the part of the pool ID after "_"),
// name the USER_ID_FOR_SRP and password the account password. For a device,
// prefix is the device group key, name the device key and password the device password.
func (s *srpSession) claim(prefix, name, password string, challenge map[string]string, now time.Time) (map[string]string, error) {
	for _, k := range []string{"SALT", "SRP_B", "SECRET_BLOCK"} {
		if challenge[k] == "" {
			return nil, fmt.Errorf("the login challenge is missing %s", k)
		}
	}
	key, err := s.authKey(prefix, name, password, challenge["SRP_B"], challenge["SALT"])
	if err != nil {
		return nil, err
	}
	secretBlock, err := base64.StdEncoding.DecodeString(challenge["SECRET_BLOCK"])
	if err != nil {
		return nil, errors.New("the login challenge's SECRET_BLOCK is not valid base64")
	}
	timestamp := cognitoTimestamp(now)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(prefix))
	mac.Write([]byte(name))
	mac.Write(secretBlock)
	mac.Write([]byte(timestamp))
	return map[string]string{
		"TIMESTAMP":                   timestamp,
		"PASSWORD_CLAIM_SECRET_BLOCK": challenge["SECRET_BLOCK"],
		"PASSWORD_CLAIM_SIGNATURE":    base64.StdEncoding.EncodeToString(mac.Sum(nil)),
	}, nil
}

// DeviceVerifier is what ConfirmDevice registers for a remembered device.
type DeviceVerifier struct {
	PasswordVerifier string `json:"PasswordVerifier"` // base64
	Salt             string `json:"Salt"`             // base64
}

// newDeviceSecret makes a random device password and its verifier for ConfirmDevice.
func newDeviceSecret(groupKey, deviceKey string) (password string, v DeviceVerifier, err error) {
	pw := make([]byte, 40)
	saltBytes := make([]byte, 16)
	if _, err = rand.Read(pw); err != nil {
		return
	}
	if _, err = rand.Read(saltBytes); err != nil {
		return
	}
	password = base64.StdEncoding.EncodeToString(pw)
	v = deviceVerifier(groupKey, deviceKey, password, new(big.Int).SetBytes(saltBytes))
	return
}

func deviceVerifier(groupKey, deviceKey, password string, saltInt *big.Int) DeviceVerifier {
	salt := padHex(saltInt)
	x := hexToInt(hexHash(salt + hashHex([]byte(groupKey+deviceKey+":"+password))))
	verifier, _ := hex.DecodeString(padHex(new(big.Int).Exp(srpG, x, srpN)))
	saltBytes, _ := hex.DecodeString(salt)
	return DeviceVerifier{
		PasswordVerifier: base64.StdEncoding.EncodeToString(verifier),
		Salt:             base64.StdEncoding.EncodeToString(saltBytes),
	}
}
