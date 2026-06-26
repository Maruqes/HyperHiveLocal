package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestLoginPostsCredentialsAndReturnsToken(t *testing.T) {
	baseURL := "https://api.example.test/hyperhive"
	client := NewClient(baseURL + "/")
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.String() != baseURL+"/login" {
			t.Fatalf("url = %s, want %s", r.URL.String(), baseURL+"/login")
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content-type = %s, want application/json", got)
		}

		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if payload["email"] != "hyperhive@email.com" {
			t.Fatalf("email = %q", payload["email"])
		}
		if payload["password"] != "pass" {
			t.Fatalf("password = %q", payload["password"])
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"token":"jwt-token"}`)),
		}, nil
	})}

	login, err := client.Login(context.Background(), "hyperhive@email.com", "pass")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if login.Token != "jwt-token" {
		t.Fatalf("token = %q, want jwt-token", login.Token)
	}
}

func TestLoginRejectsMissingToken(t *testing.T) {
	client := NewClient("https://api.example.test/hyperhive")
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	})}
	if _, err := client.Login(context.Background(), "a@b.test", "secret"); err == nil {
		t.Fatal("Login returned nil error, want missing token error")
	}
}

func TestGetAllVMsSendsBearerTokenAndReturnsVMs(t *testing.T) {
	baseURL := "https://api.example.test/hyperhive"
	client := NewClient(baseURL)
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.String() != baseURL+"/virsh/getallvms" {
			t.Fatalf("url = %s, want %s", r.URL.String(), baseURL+"/virsh/getallvms")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer jwt-token" {
			t.Fatalf("authorization = %q, want Bearer jwt-token", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("accept = %s, want application/json", got)
		}

		body := `{
			"vms": [{
				"machineName": "slave-01",
				"name": "ubuntu-server",
				"state": "RUNNING",
				"cpuCount": 4,
				"memoryMB": 8192,
				"diskSizeGB": 50,
				"ip": ["192.168.122.15"],
				"network": "default",
				"novncPort": "5900",
				"vncPassword": "",
				"videoModelType": "virtio",
				"machineType": "q35",
				"kvmHidden": false,
				"hyperVEnabled": false
			}],
			"warnings": ["slow host"]
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	res, err := client.GetAllVMs(context.Background(), "jwt-token")
	if err != nil {
		t.Fatalf("GetAllVMs returned error: %v", err)
	}
	if len(res.VMs) != 1 {
		t.Fatalf("len(VMs) = %d, want 1", len(res.VMs))
	}
	vm := res.VMs[0]
	if vm.Name != "ubuntu-server" || vm.MachineName != "slave-01" || vm.State != "RUNNING" {
		t.Fatalf("vm = %#v", vm)
	}
	if vm.CPUCount != 4 || vm.MemoryMB != 8192 || vm.DiskSizeGB != 50 {
		t.Fatalf("vm resources = %#v", vm)
	}
	if len(vm.IP) != 1 || vm.IP[0] != "192.168.122.15" {
		t.Fatalf("vm IP = %#v", vm.IP)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "slow host" {
		t.Fatalf("warnings = %#v", res.Warnings)
	}
}

func TestGetAllVMsAcceptsArrayResponseAndNumericState(t *testing.T) {
	client := NewClient("https://api.example.test/hyperhive")
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `[{
			"AllocatedGb": 14,
			"DefinedCPUS": 4,
			"DefinedRam": 8192,
			"HyperVEnabled": false,
			"KVMHidden": false,
			"MachineType": "pc-q35-10.1",
			"VNCPassword": "",
			"VideoModelType": "virtio",
			"cpuCount": 4,
			"currentCpuUsage": 0,
			"currentMemoryUsageMB": 2015,
			"diskSizeGB": 50,
			"ip": ["192.168.76.129"],
			"machineName": "marques512sv",
			"memoryMB": 8192,
			"name": "proxmox",
			"network": "512rede",
			"novncPort": "35008",
			"state": 1
		}]`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	res, err := client.GetAllVMs(context.Background(), "jwt-token")
	if err != nil {
		t.Fatalf("GetAllVMs returned error: %v", err)
	}
	if len(res.VMs) != 1 {
		t.Fatalf("len(VMs) = %d, want 1", len(res.VMs))
	}
	vm := res.VMs[0]
	if vm.Name != "proxmox" {
		t.Fatalf("name = %q, want proxmox", vm.Name)
	}
	if vm.State != "RUNNING" {
		t.Fatalf("state = %q, want RUNNING", vm.State)
	}
	if vm.MachineName != "marques512sv" || vm.Network != "512rede" || vm.NovncPort != "35008" {
		t.Fatalf("vm = %#v", vm)
	}
}

func TestListNFSSendsBearerTokenAndReturnsShares(t *testing.T) {
	baseURL := "https://api.example.test/hyperhive"
	client := NewClient(baseURL)
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.String() != baseURL+"/nfs/list" {
			t.Fatalf("url = %s, want %s", r.URL.String(), baseURL+"/nfs/list")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer jwt-token" {
			t.Fatalf("authorization = %q, want Bearer jwt-token", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("accept = %s, want application/json", got)
		}

		body := `[{
			"NfsShare": {
				"Id": 1,
				"MachineName": "marques2673sv",
				"FolderPath": "/mnt/raid4t/raid4tyXLGlj",
				"Source": "192.168.76.55:/mnt/raid4t/raid4tyXLGlj",
				"Target": "/mnt/512SvMan/shared/marques2673sv_raid4tyXLGlj",
				"Name": "raid4t",
				"HostNormalMount": false
			},
			"Status": {
				"working": true,
				"spaceOccupiedGB": 406,
				"spaceFreeGB": 3320,
				"spaceTotalGB": 3726
			}
		}]`
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}

	shares, err := client.ListNFS(context.Background(), "jwt-token")
	if err != nil {
		t.Fatalf("ListNFS returned error: %v", err)
	}
	if len(shares) != 1 {
		t.Fatalf("len(shares) = %d, want 1", len(shares))
	}
	share := shares[0]
	if share.ID != 1 || share.MachineName != "marques2673sv" || share.Name != "raid4t" || share.Status.Display() != "working" {
		t.Fatalf("share = %#v", share)
	}
	if share.Source != "192.168.76.55:/mnt/raid4t/raid4tyXLGlj" || share.Target != "/mnt/512SvMan/shared/marques2673sv_raid4tyXLGlj" {
		t.Fatalf("share paths = %#v", share)
	}
	if share.HostNormalMount != "false" {
		t.Fatalf("HostNormalMount = %q, want false", share.HostNormalMount)
	}
	if share.Status.SpaceOccupiedGB != 406 || share.Status.SpaceFreeGB != 3320 || share.Status.SpaceTotalGB != 3726 {
		t.Fatalf("status = %#v", share.Status)
	}
}

func TestAddSSHKeyPostsPayloadAndBearerToken(t *testing.T) {
	baseURL := "https://api.example.test/hyperhive"
	client := NewClient(baseURL)
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.String() != baseURL+"/virsh/add_ssh_key/minha-vm" {
			t.Fatalf("url = %s, want %s", r.URL.String(), baseURL+"/virsh/add_ssh_key/minha-vm")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer jwt-token" {
			t.Fatalf("authorization = %q, want Bearer jwt-token", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content-type = %s, want application/json", got)
		}

		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if payload["ssh_key"] != "ssh-ed25519 AAAAC3Nza user@example" {
			t.Fatalf("ssh_key = %q", payload["ssh_key"])
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})}

	err := client.AddSSHKey(context.Background(), "jwt-token", "minha-vm", "ssh-ed25519 AAAAC3Nza user@example")
	if err != nil {
		t.Fatalf("AddSSHKey returned error: %v", err)
	}
}

func TestAddSSHKeyReportsAPIError(t *testing.T) {
	client := NewClient("https://api.example.test/hyperhive")
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusConflict,
			Status:     "409 Conflict",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("vm is running, shut it down first")),
		}, nil
	})}

	err := client.AddSSHKey(context.Background(), "jwt-token", "minha-vm", "ssh-ed25519 AAAAC3Nza user@example")
	if err == nil {
		t.Fatal("AddSSHKey returned nil error, want API error")
	}
	if !strings.Contains(err.Error(), "vm is running") {
		t.Fatalf("error = %q", err.Error())
	}
	if !strings.Contains(err.Error(), "HTTP 409 Conflict") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestAddSSHKeyReportsGenericInternalErrorAsServerSide(t *testing.T) {
	client := NewClient("https://api.example.test/hyperhive")
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Status:     "500 Internal Server Error",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("internal error\n")),
		}, nil
	})}

	err := client.AddSSHKey(context.Background(), "jwt-token", "online-projects", "ssh-ed25519 AAAAC3Nza user@example")
	if err == nil {
		t.Fatal("AddSSHKey returned nil error, want API error")
	}
	for _, want := range []string{"server returned internal error", "HTTP 500 Internal Server Error", "add_ssh_key \"online-projects\""} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, missing %q", err.Error(), want)
		}
	}
}

func TestClientWithoutTimeoutPreservesTransport(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})
	source := &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
	}

	got := clientWithoutTimeout(source)
	if got == source {
		t.Fatal("clientWithoutTimeout returned the original client, want copy")
	}
	if got.Timeout != 0 {
		t.Fatalf("Timeout = %s, want 0", got.Timeout)
	}
	if got.Transport == nil {
		t.Fatal("Transport = nil, want preserved transport")
	}
	if source.Timeout != 15*time.Second {
		t.Fatalf("source Timeout = %s, want unchanged", source.Timeout)
	}
}
