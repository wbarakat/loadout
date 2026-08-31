package remote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// clientTimeout bounds every request this client makes, so an
// unreachable or hung loadoutd cannot block a sync forever.
const clientTimeout = 30 * time.Second

// Client is a thin HTTP client over the Task 4 loadoutd API. It never
// logs or prints Token; every method that fails names the remote's
// url, never the token.
type Client struct {
	URL   string
	Token string
	HTTP  *http.Client
}

// NewClient builds a Client for url, authenticating with token.
func NewClient(url, token string) *Client {
	return &Client{URL: url, Token: token, HTTP: &http.Client{Timeout: clientTimeout}}
}

// ConflictError is what PutSnapshot returns when the remote's latest
// version has moved past the parent the caller built its snapshot on:
// another device pushed first. Latest is the remote's current latest
// version; the caller pulls it, merges, and retries.
type ConflictError struct {
	Latest string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("the remote moved ahead to version %q", e.Latest)
}

// Device is one entry of the remote's bootstrap device roster: a
// name and an age recipient (a public key, safe to hold in
// plaintext).
type Device struct {
	Name      string `json:"name"`
	Recipient string `json:"recipient"`
}

// networkErr is the fixed grammar every unreachable-remote failure
// uses: the url, the cause, and the fix. A caller never has to guess
// why a request failed to even reach loadoutd.
func networkErr(url string, err error) error {
	return fmt.Errorf("the remote at %s is not reachable: %v. Fix: check the url and that loadoutd runs.", url, err)
}

// statusErr turns an unexpected (non-200, non-409-where-409-is-
// handled) HTTP status into an error naming the remote's url and
// whatever the server's JSON error body says, falling back to the
// bare HTTP status text when the body is not the expected shape.
func statusErr(url string, resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	msg := body.Error
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("the remote at %s returned an error: %s", url, msg)
}

// newRequest builds an authenticated request against the client's
// url, wrapping a failure to even construct it in the network-error
// grammar: a caller of RegisterDevice, Latest, PutSnapshot,
// GetSnapshot, or ListDevices never has to build its own error text.
func (c *Client) newRequest(method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, c.URL+path, body)
	if err != nil {
		return nil, networkErr(c.URL, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	return req, nil
}

// RegisterDevice upserts this device's name and age recipient into
// the remote's bootstrap device roster. It is idempotent: a device
// that registers twice leaves one roster entry behind.
func (c *Client) RegisterDevice(name, recipient string) error {
	data, err := json.Marshal(map[string]string{"name": name, "recipient": recipient})
	if err != nil {
		return err
	}
	req, err := c.newRequest(http.MethodPost, "/v1/devices", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return networkErr(c.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusErr(c.URL, resp)
	}
	return nil
}

// Latest reports the remote's current latest snapshot version and the
// parent it was built on. A remote that has never received a
// snapshot reports an empty version.
func (c *Client) Latest() (version, parent string, err error) {
	req, err := c.newRequest(http.MethodGet, "/v1/snapshots/latest", nil)
	if err != nil {
		return "", "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", "", networkErr(c.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", statusErr(c.URL, resp)
	}
	var out struct {
		Version string `json:"version"`
		Parent  string `json:"parent"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", "", fmt.Errorf("the remote at %s sent an unreadable response: %v", c.URL, err)
	}
	return out.Version, out.Parent, nil
}

// PutSnapshot stores blob as a new version built on parent. It
// returns a *ConflictError, never a generic error, when the remote's
// latest version has moved past parent: the caller pulls
// ConflictError.Latest, merges, and retries.
func (c *Client) PutSnapshot(blob []byte, parent string) (version string, err error) {
	req, err := c.newRequest(http.MethodPost, "/v1/snapshots", bytes.NewReader(blob))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Loadout-Parent", parent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", networkErr(c.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		var out struct {
			Latest string `json:"latest"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return "", fmt.Errorf("the remote at %s sent an unreadable conflict response: %v", c.URL, err)
		}
		return "", &ConflictError{Latest: out.Latest}
	}
	if resp.StatusCode != http.StatusOK {
		return "", statusErr(c.URL, resp)
	}
	var out struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("the remote at %s sent an unreadable response: %v", c.URL, err)
	}
	return out.Version, nil
}

// GetSnapshot returns the raw, still-encrypted blob bytes stored for
// version. It never decrypts or otherwise inspects the blob.
func (c *Client) GetSnapshot(version string) ([]byte, error) {
	req, err := c.newRequest(http.MethodGet, "/v1/snapshots/"+version, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, networkErr(c.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr(c.URL, resp)
	}
	return io.ReadAll(resp.Body)
}

// ListDevices returns every device in the remote's bootstrap roster.
func (c *Client) ListDevices() ([]Device, error) {
	req, err := c.newRequest(http.MethodGet, "/v1/devices", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, networkErr(c.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr(c.URL, resp)
	}
	var out struct {
		Devices []Device `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("the remote at %s sent an unreadable response: %v", c.URL, err)
	}
	return out.Devices, nil
}
