package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultTimeout = 15 * time.Second

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type LoginResponse struct {
	Token string `json:"token"`
}

type VM struct {
	MachineName    string   `json:"machineName"`
	Name           string   `json:"name"`
	State          VMState  `json:"state"`
	CPUCount       int      `json:"cpuCount"`
	MemoryMB       int      `json:"memoryMB"`
	DiskSizeGB     int      `json:"diskSizeGB"`
	IP             []string `json:"ip"`
	Network        string   `json:"network"`
	NovncPort      string   `json:"novncPort"`
	VNCPassword    string   `json:"vncPassword"`
	VideoModelType string   `json:"videoModelType"`
	MachineType    string   `json:"machineType"`
	KVMHidden      bool     `json:"kvmHidden"`
	HyperVEnabled  bool     `json:"hyperVEnabled"`
}

type VMState string

type VMsResponse struct {
	VMs      []VM     `json:"vms"`
	Warnings []string `json:"warnings"`
}

type NFSShare struct {
	ID              int       `json:"id"`
	MachineName     string    `json:"machineName"`
	FolderPath      string    `json:"folderPath"`
	Source          string    `json:"source"`
	Target          string    `json:"target"`
	Name            string    `json:"name"`
	HostNormalMount string    `json:"hostNormalMount"`
	Status          NFSStatus `json:"status"`
}

type NFSStatus struct {
	Text            string
	Working         bool
	SpaceOccupiedGB int
	SpaceFreeGB     int
	SpaceTotalGB    int
}

func (s *VMState) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		*s = ""
		return nil
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		*s = VMState(text)
		return nil
	}

	var code int
	if err := json.Unmarshal(trimmed, &code); err == nil {
		*s = VMState(vmStateName(code))
		return nil
	}

	return fmt.Errorf("unsupported vm state value: %s", trimmed)
}

func vmStateName(code int) string {
	switch code {
	case 0:
		return "NOSTATE"
	case 1:
		return "RUNNING"
	case 2:
		return "BLOCKED"
	case 3:
		return "PAUSED"
	case 4:
		return "SHUTDOWN"
	case 5:
		return "SHUTOFF"
	case 6:
		return "CRASHED"
	case 7:
		return "PMSUSPENDED"
	default:
		return fmt.Sprintf("%d", code)
	}
}

func (r *VMsResponse) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return errors.New("empty vms response")
	}

	if trimmed[0] == '[' {
		var vms []VM
		if err := json.Unmarshal(trimmed, &vms); err != nil {
			return err
		}
		r.VMs = vms
		r.Warnings = nil
		return nil
	}

	type response VMsResponse
	var wrapped response
	if err := json.Unmarshal(trimmed, &wrapped); err != nil {
		return err
	}
	*r = VMsResponse(wrapped)
	return nil
}

func (s *NFSShare) UnmarshalJSON(data []byte) error {
	type simpleShare struct {
		ID              int             `json:"id"`
		MachineName     string          `json:"machineName"`
		FolderPath      string          `json:"folderPath"`
		Source          string          `json:"source"`
		Target          string          `json:"target"`
		Name            string          `json:"name"`
		HostNormalMount json.RawMessage `json:"hostNormalMount"`
		Status          NFSStatus       `json:"status"`
	}
	type wrappedShare struct {
		NFSShare struct {
			ID              int             `json:"Id"`
			MachineName     string          `json:"MachineName"`
			FolderPath      string          `json:"FolderPath"`
			Source          string          `json:"Source"`
			Target          string          `json:"Target"`
			Name            string          `json:"Name"`
			HostNormalMount json.RawMessage `json:"HostNormalMount"`
		} `json:"NfsShare"`
		Status NFSStatus `json:"Status"`
	}

	var wrapped wrappedShare
	if err := json.Unmarshal(data, &wrapped); err == nil && (wrapped.NFSShare.ID != 0 || wrapped.NFSShare.Name != "") {
		s.ID = wrapped.NFSShare.ID
		s.MachineName = wrapped.NFSShare.MachineName
		s.FolderPath = wrapped.NFSShare.FolderPath
		s.Source = wrapped.NFSShare.Source
		s.Target = wrapped.NFSShare.Target
		s.Name = wrapped.NFSShare.Name
		s.HostNormalMount = rawJSONToString(wrapped.NFSShare.HostNormalMount)
		s.Status = wrapped.Status
		return nil
	}

	var simple simpleShare
	if err := json.Unmarshal(data, &simple); err != nil {
		return err
	}
	s.ID = simple.ID
	s.MachineName = simple.MachineName
	s.FolderPath = simple.FolderPath
	s.Source = simple.Source
	s.Target = simple.Target
	s.Name = simple.Name
	s.HostNormalMount = rawJSONToString(simple.HostNormalMount)
	s.Status = simple.Status
	return nil
}

func (s *NFSStatus) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		*s = NFSStatus{}
		return nil
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		*s = NFSStatus{Text: text}
		return nil
	}

	var status struct {
		Working         bool `json:"working"`
		SpaceOccupiedGB int  `json:"spaceOccupiedGB"`
		SpaceFreeGB     int  `json:"spaceFreeGB"`
		SpaceTotalGB    int  `json:"spaceTotalGB"`
	}
	if err := json.Unmarshal(trimmed, &status); err != nil {
		return err
	}
	*s = NFSStatus{
		Working:         status.Working,
		SpaceOccupiedGB: status.SpaceOccupiedGB,
		SpaceFreeGB:     status.SpaceFreeGB,
		SpaceTotalGB:    status.SpaceTotalGB,
	}
	return nil
}

func (s NFSStatus) Display() string {
	if s.Text != "" {
		return s.Text
	}
	if s.Working {
		return "working"
	}
	return "not working"
}

func rawJSONToString(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}

	var text string
	if err := json.Unmarshal(trimmed, &text); err == nil {
		return text
	}
	var boolean bool
	if err := json.Unmarshal(trimmed, &boolean); err == nil {
		return fmt.Sprintf("%t", boolean)
	}
	return string(trimmed)
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTPClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

func (c *Client) Login(ctx context.Context, email, password string) (LoginResponse, error) {
	if c == nil {
		return LoginResponse{}, errors.New("api client is nil")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return LoginResponse{}, errors.New("api url is not configured")
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	body, err := json.Marshal(map[string]string{
		"email":    email,
		"password": password,
	})
	if err != nil {
		return LoginResponse{}, fmt.Errorf("encode login payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/login", bytes.NewReader(body))
	if err != nil {
		return LoginResponse{}, fmt.Errorf("build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	res, err := httpClient.Do(req)
	if err != nil {
		return LoginResponse{}, fmt.Errorf("login request failed: %w", err)
	}
	defer res.Body.Close()

	resBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return LoginResponse{}, fmt.Errorf("read login response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		message := strings.TrimSpace(string(resBody))
		if message == "" {
			message = res.Status
		}
		return LoginResponse{}, fmt.Errorf("login failed: %s", message)
	}

	var login LoginResponse
	if err := json.Unmarshal(resBody, &login); err != nil {
		return LoginResponse{}, fmt.Errorf("decode login response: %w", err)
	}
	if strings.TrimSpace(login.Token) == "" {
		return LoginResponse{}, errors.New("login response did not include a token")
	}
	return login, nil
}

func (c *Client) GetAllVMs(ctx context.Context, token string) (VMsResponse, error) {
	if c == nil {
		return VMsResponse{}, errors.New("api client is nil")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return VMsResponse{}, errors.New("api url is not configured")
	}
	if strings.TrimSpace(token) == "" {
		return VMsResponse{}, errors.New("token is not configured")
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/virsh/getallvms", nil)
	if err != nil {
		return VMsResponse{}, fmt.Errorf("build vms request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := httpClient.Do(req)
	if err != nil {
		return VMsResponse{}, fmt.Errorf("vms request failed: %w", err)
	}
	defer res.Body.Close()

	resBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return VMsResponse{}, fmt.Errorf("read vms response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		message := strings.TrimSpace(string(resBody))
		if message == "" {
			message = res.Status
		}
		return VMsResponse{}, fmt.Errorf("list vms failed: %s", message)
	}

	var vms VMsResponse
	if err := json.Unmarshal(resBody, &vms); err != nil {
		return VMsResponse{}, fmt.Errorf("decode vms response: %w", err)
	}
	return vms, nil
}

func (c *Client) ListNFS(ctx context.Context, token string) ([]NFSShare, error) {
	if c == nil {
		return nil, errors.New("api client is nil")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return nil, errors.New("api url is not configured")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("token is not configured")
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/nfs/list", nil)
	if err != nil {
		return nil, fmt.Errorf("build nfs request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nfs request failed: %w", err)
	}
	defer res.Body.Close()

	resBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read nfs response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		message := strings.TrimSpace(string(resBody))
		if message == "" {
			message = res.Status
		}
		return nil, fmt.Errorf("list nfs failed: %s", message)
	}

	var shares []NFSShare
	if err := json.Unmarshal(resBody, &shares); err != nil {
		return nil, fmt.Errorf("decode nfs response: %w", err)
	}
	return shares, nil
}

func (c *Client) AddSSHKey(ctx context.Context, token, vmName, sshKey string) error {
	if c == nil {
		return errors.New("api client is nil")
	}
	if strings.TrimSpace(c.BaseURL) == "" {
		return errors.New("api url is not configured")
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("token is not configured")
	}
	if strings.TrimSpace(vmName) == "" {
		return errors.New("vm name is required")
	}
	if strings.TrimSpace(sshKey) == "" {
		return errors.New("ssh key is required")
	}
	httpClient := clientWithoutTimeout(c.HTTPClient)

	body, err := json.Marshal(map[string]string{
		"ssh_key": sshKey,
	})
	if err != nil {
		return fmt.Errorf("encode ssh key payload: %w", err)
	}

	endpoint := c.BaseURL + "/virsh/add_ssh_key/" + url.PathEscape(vmName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build ssh key request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ssh key request failed: %w", err)
	}
	defer res.Body.Close()

	resBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read ssh key response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		message := strings.TrimSpace(string(resBody))
		if message == "" {
			message = res.Status
		}
		if res.StatusCode == http.StatusInternalServerError && strings.EqualFold(message, "internal error") {
			return fmt.Errorf("add ssh key failed: server returned internal error (HTTP %s); check HyperHive master/slave logs for add_ssh_key %q", res.Status, vmName)
		}
		return fmt.Errorf("add ssh key failed: %s (HTTP %s)", message, res.Status)
	}
	return nil
}

func clientWithoutTimeout(client *http.Client) *http.Client {
	if client == nil {
		return &http.Client{}
	}
	copy := *client
	copy.Timeout = 0
	return &copy
}
