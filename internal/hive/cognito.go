package hive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/mattoddie/hive-mcp/internal/config"
)

// cognito calls the handful of AWS Cognito Identity Provider actions the
// login needs. They're all unauthenticated JSON calls (InitiateAuth and
// RespondToAuthChallenge take no credentials, and the device calls take the
// user's access token in the body), so plain HTTP is enough: no AWS SDK and
// no AWS credentials.
type cognito struct {
	http     *http.Client
	url      string
	poolID   string
	clientID string
}

// CognitoError is an error returned by Cognito, such as NotAuthorizedException.
type CognitoError struct {
	Status  int
	Type    string
	Message string
}

func (e *CognitoError) Error() string {
	if e.Message == "" {
		return e.Type
	}
	return e.Type + ": " + e.Message
}

func isCognito(err error, types ...string) bool {
	var ce *CognitoError
	if !errors.As(err, &ce) {
		return false
	}
	for _, t := range types {
		if ce.Type == t {
			return true
		}
	}
	return false
}

// poolName is the part of the pool ID after the region, used in the SRP claim.
func (c *cognito) poolName() string {
	_, name, _ := strings.Cut(c.poolID, "_")
	return name
}

func (c *cognito) call(ctx context.Context, action string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "AWSCognitoIdentityProviderService."+action)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Hive's login service (AWS Cognito): %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Type    string `json:"__type"`
			Message string `json:"message"`
			Msg     string `json:"Message"`
		}
		_ = json.Unmarshal(data, &e)
		ce := &CognitoError{Status: resp.StatusCode, Type: e.Type, Message: e.Message}
		if ce.Message == "" {
			ce.Message = e.Msg
		}
		// Types can come namespaced, e.g. "com.amazonaws...#NotAuthorizedException".
		if i := strings.LastIndex(ce.Type, "#"); i >= 0 {
			ce.Type = ce.Type[i+1:]
		}
		if ce.Type == "" {
			ce.Type = fmt.Sprintf("HTTP %d", resp.StatusCode)
			ce.Message = truncate(strings.TrimSpace(string(data)), 200)
		}
		return ce
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("unexpected response from Cognito %s: %w", action, err)
	}
	return nil
}

type authenticationResult struct {
	IdToken           string `json:"IdToken"`
	AccessToken       string `json:"AccessToken"`
	RefreshToken      string `json:"RefreshToken"`
	ExpiresIn         int64  `json:"ExpiresIn"`
	NewDeviceMetadata *struct {
		DeviceKey      string `json:"DeviceKey"`
		DeviceGroupKey string `json:"DeviceGroupKey"`
	} `json:"NewDeviceMetadata"`
}

// authResponse is what InitiateAuth and RespondToAuthChallenge return:
// either a challenge to answer or the tokens.
type authResponse struct {
	ChallengeName        string                `json:"ChallengeName"`
	ChallengeParameters  map[string]string     `json:"ChallengeParameters"`
	Session              string                `json:"Session"`
	AuthenticationResult *authenticationResult `json:"AuthenticationResult"`
}

func (c *cognito) initiateAuth(ctx context.Context, flow string, params map[string]string) (*authResponse, error) {
	var out authResponse
	err := c.call(ctx, "InitiateAuth", map[string]any{"AuthFlow": flow, "ClientId": c.clientID, "AuthParameters": params}, &out)
	return &out, err
}

func (c *cognito) respond(ctx context.Context, challenge, session string, responses map[string]string) (*authResponse, error) {
	in := map[string]any{"ChallengeName": challenge, "ClientId": c.clientID, "ChallengeResponses": responses}
	if session != "" {
		in["Session"] = session
	}
	var out authResponse
	err := c.call(ctx, "RespondToAuthChallenge", in, &out)
	return &out, err
}

func (c *cognito) confirmDevice(ctx context.Context, accessToken, deviceKey, name string, v DeviceVerifier) error {
	return c.call(ctx, "ConfirmDevice", map[string]any{
		"AccessToken": accessToken, "DeviceKey": deviceKey, "DeviceName": name, "DeviceSecretVerifierConfig": v,
	}, nil)
}

func (c *cognito) rememberDevice(ctx context.Context, accessToken, deviceKey string) error {
	return c.call(ctx, "UpdateDeviceStatus", map[string]any{
		"AccessToken": accessToken, "DeviceKey": deviceKey, "DeviceRememberedStatus": "remembered",
	}, nil)
}

func (c *cognito) forgetDevice(ctx context.Context, accessToken, deviceKey string) error {
	return c.call(ctx, "ForgetDevice", map[string]any{"AccessToken": accessToken, "DeviceKey": deviceKey}, nil)
}

// ssoAssignment matches the window.X="..." assignments on Hive's sign-in page.
var ssoAssignment = regexp.MustCompile(`window\.(\w+)\s*=\s*["']([^"']*)["']`)

// discoverCognito reads the Cognito pool and client IDs from Hive's sign-in page.
func discoverCognito(ctx context.Context, hc *http.Client, ssoURL string) (poolID, clientID string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ssoURL+"/", nil)
	if err != nil {
		return "", "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("could not reach Hive's sign-in page %s to look up its login settings: %w", ssoURL, err)
	}
	defer resp.Body.Close()
	page, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode/100 != 2 {
		return "", "", fmt.Errorf("hive's sign-in page %s returned HTTP %d", ssoURL, resp.StatusCode)
	}
	for _, m := range ssoAssignment.FindAllStringSubmatch(string(page), -1) {
		switch m[1] {
		case "HiveSSOPoolId":
			poolID = m[2]
		case "HiveSSOPublicCognitoClientId":
			clientID = m[2]
		}
	}
	if config.PoolRegion(poolID) == "" || clientID == "" {
		return "", "", fmt.Errorf("could not find Hive's login settings (HiveSSOPoolId and HiveSSOPublicCognitoClientId) on %s. Hive may have changed its sign-in page; set HIVE_COGNITO_POOL_ID and HIVE_COGNITO_CLIENT_ID", ssoURL)
	}
	return poolID, clientID, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
