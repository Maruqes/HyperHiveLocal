package cli

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Maruqes/HyperHiveLocal/internal/api"
	"github.com/Maruqes/HyperHiveLocal/internal/config"
)

func testServiceLogger(t *testing.T, logPath string) *log.Logger {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return log.New(f, "", log.LstdFlags)
}

type fakePasswordReader struct {
	password string
	err      error
}

func (f fakePasswordReader) ReadPassword(prompt string, stdin io.Reader, stdout io.Writer) (string, error) {
	if _, err := stdout.Write([]byte(prompt)); err != nil {
		return "", err
	}
	return f.password, f.err
}

type fakeAPIClient struct {
	loginFunc     func(ctx context.Context, email, password string) (api.LoginResponse, error)
	getAllVMsFunc func(ctx context.Context, token string) (api.VMsResponse, error)
	listNFSFunc   func(ctx context.Context, token string) ([]api.NFSShare, error)
	addSSHKeyFunc func(ctx context.Context, token, vmName, sshKey string) error
}

type recordedCommand struct {
	name string
	args []string
}

type fakeCommandRunner struct {
	commands []recordedCommand
	err      error
	errAt    int
}

func (f *fakeCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	f.commands = append(f.commands, recordedCommand{name: name, args: args})
	if f.err != nil && (f.errAt == 0 || f.errAt == len(f.commands)) {
		return f.err
	}
	return nil
}

func (f fakeAPIClient) Login(ctx context.Context, email, password string) (api.LoginResponse, error) {
	return f.loginFunc(ctx, email, password)
}

func (f fakeAPIClient) GetAllVMs(ctx context.Context, token string) (api.VMsResponse, error) {
	return f.getAllVMsFunc(ctx, token)
}

func (f fakeAPIClient) ListNFS(ctx context.Context, token string) ([]api.NFSShare, error) {
	return f.listNFSFunc(ctx, token)
}

func (f fakeAPIClient) AddSSHKey(ctx context.Context, token, vmName, sshKey string) error {
	return f.addSSHKeyFunc(ctx, token, vmName, sshKey)
}

func TestUsageIsShownInEnglishForNoArgsAndHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}} {
		var stdout, stderr strings.Builder
		code := run(args, strings.NewReader(""), &stdout, &stderr, deps{})
		if code != 0 {
			t.Fatalf("args = %#v, exit code = %d, stderr = %s", args, code, stderr.String())
		}

		output := stdout.String()
		for _, want := range []string{
			"Usage:",
			"hyperhive setup       Configure the API base URL",
			"hyperhive login       Log in and store email, password, and token",
			"hyperhive vms         List virtual machines",
			"hyperhive ssh         Add an SSH public key to a virtual machine",
			"hyperhive nfs         List NFS shares",
			"hyperhive install_nfs Mount all NFS shares from the API",
			"hyperhive remove_nfs  Unmount all NFS shares from the API",
			"hyperhive systemdexec Run as a systemd service to mount NFS shares",
			"hyperhive logs        Show systemdexec service logs",
			"Default path:",
			"Override:",
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("args = %#v, stdout = %q, missing %q", args, output, want)
			}
		}
		for _, forbidden := range []string{"Uso:", "Configura", "Faz login", "Lista as VMs", "Por defeito"} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("args = %#v, stdout = %q, contains Portuguese text %q", args, output, forbidden)
			}
		}
	}
}

func TestSetupSavesNormalizedBaseURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	baseURL := "https://configured-api.example.test/hyperhive"
	var saved config.Config
	d := deps{
		configPath: func() (string, error) { return path, nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{Email: "old@email.test", Password: "old-pass", Token: "old-token"}, nil
		},
		saveConfig: func(gotPath string, cfg config.Config) error {
			if gotPath != path {
				t.Fatalf("path = %q, want %q", gotPath, path)
			}
			saved = cfg
			return nil
		},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"setup"}, strings.NewReader(baseURL+"/\n"), &stdout, &stderr, d)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if saved.BaseURL != baseURL {
		t.Fatalf("BaseURL = %q", saved.BaseURL)
	}
	if saved.Email != "old@email.test" || saved.Password != "old-pass" || saved.Token != "old-token" {
		t.Fatalf("setup did not preserve credentials: %#v", saved)
	}
}

func TestLoginPostsAndStoresCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	configuredBaseURL := "https://configured-api.example.test/hyperhive"
	var saved config.Config
	var gotEmail, gotPassword string
	d := deps{
		configPath: func() (string, error) { return path, nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: configuredBaseURL}, nil
		},
		saveConfig: func(gotPath string, cfg config.Config) error {
			if gotPath != path {
				t.Fatalf("path = %q, want %q", gotPath, path)
			}
			saved = cfg
			return nil
		},
		newAPIClient: func(baseURL string) apiClient {
			if baseURL != configuredBaseURL {
				t.Fatalf("baseURL = %q", baseURL)
			}
			return fakeAPIClient{loginFunc: func(ctx context.Context, email, password string) (api.LoginResponse, error) {
				gotEmail = email
				gotPassword = password
				return api.LoginResponse{Token: "jwt-token"}, nil
			}}
		},
		passwordReader: fakePasswordReader{password: "pass"},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"login"}, strings.NewReader("hyperhive@email.com\n"), &stdout, &stderr, d)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if gotEmail != "hyperhive@email.com" {
		t.Fatalf("gotEmail = %q", gotEmail)
	}
	if gotPassword != "pass" {
		t.Fatalf("gotPassword = %q", gotPassword)
	}
	if saved.Email != "hyperhive@email.com" || saved.Password != "pass" || saved.Token != "jwt-token" {
		t.Fatalf("saved = %#v", saved)
	}
}

func TestLoginRequiresSetup(t *testing.T) {
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			t.Fatal("newAPIClient should not be called")
			return nil
		},
		passwordReader: fakePasswordReader{password: "pass"},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"login"}, strings.NewReader("hyperhive@email.com\n"), &stdout, &stderr, d)
	if code == 0 {
		t.Fatal("exit code = 0, want error")
	}
	if !strings.Contains(stderr.String(), "hyperhive setup") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestLoginPropagatesAPIError(t *testing.T) {
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://configured-api.example.test/hyperhive"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			return fakeAPIClient{loginFunc: func(ctx context.Context, email, password string) (api.LoginResponse, error) {
				return api.LoginResponse{}, errors.New("invalid credentials")
			}}
		},
		passwordReader: fakePasswordReader{password: "pass"},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"login"}, strings.NewReader("hyperhive@email.com\n"), &stdout, &stderr, d)
	if code == 0 {
		t.Fatal("exit code = 0, want error")
	}
	if !strings.Contains(stderr.String(), "invalid credentials") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestVMsListsFromConfiguredAPIWithStoredToken(t *testing.T) {
	configuredBaseURL := "https://configured-api.example.test/hyperhive"
	var gotToken string
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: configuredBaseURL, Token: "jwt-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(baseURL string) apiClient {
			if baseURL != configuredBaseURL {
				t.Fatalf("baseURL = %q", baseURL)
			}
			return fakeAPIClient{getAllVMsFunc: func(ctx context.Context, token string) (api.VMsResponse, error) {
				gotToken = token
				return api.VMsResponse{
					VMs: []api.VM{{
						MachineName: "slave-01",
						Name:        "ubuntu-server",
						State:       "RUNNING",
						CPUCount:    4,
						MemoryMB:    8192,
						DiskSizeGB:  50,
						IP:          []string{"192.168.122.15"},
						Network:     "default",
						NovncPort:   "5900",
					}},
					Warnings: []string{"slow host"},
				}, nil
			}}
		},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"vms"}, strings.NewReader(""), &stdout, &stderr, d)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if gotToken != "jwt-token" {
		t.Fatalf("token = %q", gotToken)
	}
	output := stdout.String()
	for _, want := range []string{"ubuntu-server", "slave-01", "RUNNING", "8192 MB", "192.168.122.15", "Aviso: slow host"} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, missing %q", output, want)
		}
	}
}

func TestVMsRequiresLogin(t *testing.T) {
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://configured-api.example.test/hyperhive"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			t.Fatal("newAPIClient should not be called")
			return nil
		},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"vms"}, strings.NewReader(""), &stdout, &stderr, d)
	if code == 0 {
		t.Fatal("exit code = 0, want error")
	}
	if !strings.Contains(stderr.String(), "hyperhive login") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestNFSListsSharesFromConfiguredAPIWithStoredToken(t *testing.T) {
	configuredBaseURL := "https://configured-api.example.test/hyperhive"
	var gotToken string
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: configuredBaseURL, Token: "jwt-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(baseURL string) apiClient {
			if baseURL != configuredBaseURL {
				t.Fatalf("baseURL = %q", baseURL)
			}
			return fakeAPIClient{listNFSFunc: func(ctx context.Context, token string) ([]api.NFSShare, error) {
				gotToken = token
				return []api.NFSShare{{
					ID:              1,
					MachineName:     "slave-01",
					FolderPath:      "/nfsshare/iso",
					Source:          "10.0.0.10:/nfsshare/iso",
					Target:          "/mnt/nfs/iso",
					Name:            "iso",
					HostNormalMount: "/nfsshare",
					Status: api.NFSStatus{
						Working:         true,
						SpaceOccupiedGB: 10,
						SpaceFreeGB:     20,
						SpaceTotalGB:    30,
					},
				}}, nil
			}}
		},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"nfs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if gotToken != "jwt-token" {
		t.Fatalf("token = %q", gotToken)
	}
	output := stdout.String()
	for _, want := range []string{"ID", "MACHINE", "SOURCE", "USED_GB", "slave-01", "iso", "working", "10", "20", "30", "10.0.0.10:/nfsshare/iso", "/mnt/nfs/iso"} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, missing %q", output, want)
		}
	}
}

func TestNFSRequiresLogin(t *testing.T) {
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://configured-api.example.test/hyperhive"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			t.Fatal("newAPIClient should not be called")
			return nil
		},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"nfs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code == 0 {
		t.Fatal("exit code = 0, want error")
	}
	if !strings.Contains(stderr.String(), "hyperhive login") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestInstallNFSMountsAllShares(t *testing.T) {
	configuredBaseURL := "https://configured-api.example.test/hyperhive"
	runner := &fakeCommandRunner{}
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: configuredBaseURL, Token: "jwt-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(baseURL string) apiClient {
			if baseURL != configuredBaseURL {
				t.Fatalf("baseURL = %q", baseURL)
			}
			return fakeAPIClient{listNFSFunc: func(ctx context.Context, token string) ([]api.NFSShare, error) {
				if token != "jwt-token" {
					t.Fatalf("token = %q", token)
				}
				return []api.NFSShare{
					{ID: 1, Source: "192.168.76.1:/mnt/ssd500/ssd500singleoKsLcz", Name: "ssd500"},
					{ID: 2, Source: "192.168.76.1:/mnt/ssd1tb/share", Name: "ssd1tb"},
				}, nil
			}}
		},
		commandRunner: runner,
	}

	var stdout, stderr strings.Builder
	code := run([]string{"install_nfs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}

	wantCommands := []recordedCommand{
		{name: "sudo", args: []string{"mkdir", "-p", "/mnt/hyperhive/ssd500"}},
		{name: "sudo", args: []string{"mount", "-t", "nfs", "192.168.76.1:/mnt/ssd500/ssd500singleoKsLcz", "/mnt/hyperhive/ssd500"}},
		{name: "sudo", args: []string{"mkdir", "-p", "/mnt/hyperhive/ssd1tb"}},
		{name: "sudo", args: []string{"mount", "-t", "nfs", "192.168.76.1:/mnt/ssd1tb/share", "/mnt/hyperhive/ssd1tb"}},
	}
	if len(runner.commands) != len(wantCommands) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, wantCommands)
	}
	for i, want := range wantCommands {
		if runner.commands[i].name != want.name || strings.Join(runner.commands[i].args, " ") != strings.Join(want.args, " ") {
			t.Fatalf("command[%d] = %#v, want %#v", i, runner.commands[i], want)
		}
	}

	out := stdout.String()
	for _, want := range []string{"Installing", "successfully", "/mnt/hyperhive/ssd500", "/mnt/hyperhive/ssd1tb"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, missing %q", out, want)
		}
	}
}

func TestInstallNFSReportsNoShares(t *testing.T) {
	runner := &fakeCommandRunner{}
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://api.test", Token: "jwt-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			return fakeAPIClient{listNFSFunc: func(context.Context, string) ([]api.NFSShare, error) {
				return nil, nil
			}}
		},
		commandRunner: runner,
	}

	var stdout, stderr strings.Builder
	code := run([]string{"install_nfs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %#v, want none", runner.commands)
	}
	if !strings.Contains(stdout.String(), "No NFS shares to install") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestInstallNFSRequiresSetup(t *testing.T) {
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) { return config.Config{}, nil },
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			t.Fatal("newAPIClient should not be called")
			return nil
		},
		commandRunner: &fakeCommandRunner{},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"install_nfs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code == 0 {
		t.Fatal("exit code = 0, want error")
	}
	if !strings.Contains(stderr.String(), "hyperhive setup") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestInstallNFSRequiresLogin(t *testing.T) {
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://api.test"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			t.Fatal("newAPIClient should not be called")
			return nil
		},
		commandRunner: &fakeCommandRunner{},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"install_nfs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code == 0 {
		t.Fatal("exit code = 0, want error")
	}
	if !strings.Contains(stderr.String(), "hyperhive login") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestInstallNFSStopsOnCommandError(t *testing.T) {
	runner := &fakeCommandRunner{err: errors.New("permission denied"), errAt: 2}
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://api.test", Token: "jwt-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			return fakeAPIClient{listNFSFunc: func(context.Context, string) ([]api.NFSShare, error) {
				return []api.NFSShare{
					{ID: 1, Source: "host:/a", Name: "a"},
					{ID: 2, Source: "host:/b", Name: "b"},
				}, nil
			}}
		},
		commandRunner: runner,
	}

	var stdout, stderr strings.Builder
	code := run([]string{"install_nfs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code == 0 {
		t.Fatal("exit code = 0, want error")
	}
	if len(runner.commands) != 2 {
		t.Fatalf("commands = %#v, want 2", runner.commands)
	}
	if !strings.Contains(stderr.String(), "permission denied") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRemoveNFSUnmountsAllShares(t *testing.T) {
	configuredBaseURL := "https://configured-api.example.test/hyperhive"
	runner := &fakeCommandRunner{}
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: configuredBaseURL, Token: "jwt-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(baseURL string) apiClient {
			if baseURL != configuredBaseURL {
				t.Fatalf("baseURL = %q", baseURL)
			}
			return fakeAPIClient{listNFSFunc: func(ctx context.Context, token string) ([]api.NFSShare, error) {
				if token != "jwt-token" {
					t.Fatalf("token = %q", token)
				}
				return []api.NFSShare{
					{ID: 1, Source: "host:/a", Name: "a"},
					{ID: 2, Source: "host:/b", Name: "b"},
				}, nil
			}}
		},
		commandRunner: runner,
	}

	var stdout, stderr strings.Builder
	code := run([]string{"remove_nfs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}

	wantCommands := []recordedCommand{
		{name: "sudo", args: []string{"umount", "/mnt/hyperhive/a"}},
		{name: "sudo", args: []string{"umount", "/mnt/hyperhive/b"}},
	}
	if len(runner.commands) != len(wantCommands) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, wantCommands)
	}
	for i, want := range wantCommands {
		if runner.commands[i].name != want.name || strings.Join(runner.commands[i].args, " ") != strings.Join(want.args, " ") {
			t.Fatalf("command[%d] = %#v, want %#v", i, runner.commands[i], want)
		}
	}

	out := stdout.String()
	for _, want := range []string{"Removing", "successfully", "/mnt/hyperhive/a", "/mnt/hyperhive/b"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, missing %q", out, want)
		}
	}
}

func TestRemoveNFSReportsNoShares(t *testing.T) {
	runner := &fakeCommandRunner{}
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://api.test", Token: "jwt-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			return fakeAPIClient{listNFSFunc: func(context.Context, string) ([]api.NFSShare, error) {
				return nil, nil
			}}
		},
		commandRunner: runner,
	}

	var stdout, stderr strings.Builder
	code := run([]string{"remove_nfs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %#v, want none", runner.commands)
	}
	if !strings.Contains(stdout.String(), "No NFS shares to remove") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRemoveNFSRequiresSetup(t *testing.T) {
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) { return config.Config{}, nil },
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			t.Fatal("newAPIClient should not be called")
			return nil
		},
		commandRunner: &fakeCommandRunner{},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"remove_nfs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code == 0 {
		t.Fatal("exit code = 0, want error")
	}
	if !strings.Contains(stderr.String(), "hyperhive setup") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRemoveNFSRequiresLogin(t *testing.T) {
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://api.test"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			t.Fatal("newAPIClient should not be called")
			return nil
		},
		commandRunner: &fakeCommandRunner{},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"remove_nfs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code == 0 {
		t.Fatal("exit code = 0, want error")
	}
	if !strings.Contains(stderr.String(), "hyperhive login") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRemoveNFSStopsOnCommandError(t *testing.T) {
	runner := &fakeCommandRunner{err: errors.New("not mounted"), errAt: 1}
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://api.test", Token: "jwt-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			return fakeAPIClient{listNFSFunc: func(context.Context, string) ([]api.NFSShare, error) {
				return []api.NFSShare{
					{ID: 1, Source: "host:/a", Name: "a"},
					{ID: 2, Source: "host:/b", Name: "b"},
				}, nil
			}}
		},
		commandRunner: runner,
	}

	var stdout, stderr strings.Builder
	code := run([]string{"remove_nfs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code == 0 {
		t.Fatal("exit code = 0, want error")
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %#v, want 1", runner.commands)
	}
	if !strings.Contains(stderr.String(), "not mounted") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestSSHAddsKeyToSelectedVM(t *testing.T) {
	configuredBaseURL := "https://configured-api.example.test/hyperhive"
	var gotToken, gotVMName, gotSSHKey string
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: configuredBaseURL, Token: "jwt-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(baseURL string) apiClient {
			if baseURL != configuredBaseURL {
				t.Fatalf("baseURL = %q", baseURL)
			}
			return fakeAPIClient{
				getAllVMsFunc: func(ctx context.Context, token string) (api.VMsResponse, error) {
					if token != "jwt-token" {
						t.Fatalf("get vms token = %q", token)
					}
					return api.VMsResponse{VMs: []api.VM{
						{Name: "proxmox", MachineName: "marques512sv", State: "RUNNING"},
						{Name: "tools", MachineName: "marques512sv", State: "SHUTOFF"},
					}}, nil
				},
				addSSHKeyFunc: func(ctx context.Context, token, vmName, sshKey string) error {
					gotToken = token
					gotVMName = vmName
					gotSSHKey = sshKey
					return nil
				},
			}
		},
		listSSHPublicKeys: func() ([]sshPublicKeyFile, error) {
			return []sshPublicKeyFile{{Label: "~/.ssh/id_ed25519.pub", Path: "/home/user/.ssh/id_ed25519.pub"}}, nil
		},
		readPublicKeyFile: func(path string) (string, error) {
			t.Fatalf("readPublicKeyFile should not be called for manual input, path = %q", path)
			return "", nil
		},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"ssh"}, strings.NewReader("2\n0\nssh-ed25519 AAAAC3Nza user@example\n"), &stdout, &stderr, d)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if gotToken != "jwt-token" {
		t.Fatalf("token = %q", gotToken)
	}
	if gotVMName != "tools" {
		t.Fatalf("vmName = %q", gotVMName)
	}
	if gotSSHKey != "ssh-ed25519 AAAAC3Nza user@example" {
		t.Fatalf("sshKey = %q", gotSSHKey)
	}
	output := stdout.String()
	for _, want := range []string{"1)  proxmox", "2)  tools", "0)  Type or paste manually", "1)  ~/.ssh/id_ed25519.pub", "SSH key added to tools."} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, missing %q", output, want)
		}
	}
	if strings.Contains(output, "All"+" virtual machines") {
		t.Fatalf("stdout = %q, should not include all-vms option", output)
	}
}

func TestSSHAddsKeyFromSelectedPublicKeyFile(t *testing.T) {
	var gotVMName, gotSSHKey string
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://configured-api.example.test/hyperhive", Token: "jwt-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(baseURL string) apiClient {
			return fakeAPIClient{
				getAllVMsFunc: func(ctx context.Context, token string) (api.VMsResponse, error) {
					return api.VMsResponse{VMs: []api.VM{{Name: "proxmox", MachineName: "marques512sv", State: "RUNNING"}}}, nil
				},
				addSSHKeyFunc: func(ctx context.Context, token, vmName, sshKey string) error {
					gotVMName = vmName
					gotSSHKey = sshKey
					return nil
				},
			}
		},
		listSSHPublicKeys: func() ([]sshPublicKeyFile, error) {
			return []sshPublicKeyFile{
				{Label: "~/.ssh/id_rsa.pub", Path: "/home/user/.ssh/id_rsa.pub"},
				{Label: "~/.ssh/id_ed25519.pub", Path: "/home/user/.ssh/id_ed25519.pub"},
			}, nil
		},
		readPublicKeyFile: func(path string) (string, error) {
			if path != "/home/user/.ssh/id_ed25519.pub" {
				t.Fatalf("path = %q", path)
			}
			return "ssh-ed25519 FROMFILE user@example\n", nil
		},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"ssh"}, strings.NewReader("1\n2\n"), &stdout, &stderr, d)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if gotVMName != "proxmox" {
		t.Fatalf("vmName = %q", gotVMName)
	}
	if gotSSHKey != "ssh-ed25519 FROMFILE user@example" {
		t.Fatalf("sshKey = %q", gotSSHKey)
	}
	if !strings.Contains(stdout.String(), "2)  ~/.ssh/id_ed25519.pub") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestSSHRejectsZeroSelection(t *testing.T) {
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://configured-api.example.test/hyperhive", Token: "jwt-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(baseURL string) apiClient {
			return fakeAPIClient{
				getAllVMsFunc: func(ctx context.Context, token string) (api.VMsResponse, error) {
					return api.VMsResponse{VMs: []api.VM{{Name: "proxmox", MachineName: "marques512sv", State: "RUNNING"}}}, nil
				},
				addSSHKeyFunc: func(ctx context.Context, token, vmName, sshKey string) error {
					t.Fatal("AddSSHKey should not be called for invalid selection")
					return nil
				},
			}
		},
	}

	var stdout, stderr strings.Builder
	code := run([]string{"ssh"}, strings.NewReader("0\n"), &stdout, &stderr, d)
	if code == 0 {
		t.Fatal("exit code = 0, want error")
	}
	if !strings.Contains(stderr.String(), "selection must be between 1 and 1") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestNormalizeSSHKey(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr string
	}{
		{
			name: "accepts supported key with comment",
			raw:  " ssh-ed25519 AAAAC3Nza user@example\n",
			want: "ssh-ed25519 AAAAC3Nza user@example",
		},
		{
			name:    "rejects multiline key",
			raw:     "ssh-ed25519 AAAAC3Nza\nuser@example",
			wantErr: "single line",
		},
		{
			name:    "rejects unsupported key type",
			raw:     "ssh-dss AAAAC3Nza user@example",
			wantErr: "unsupported ssh public key type",
		},
		{
			name:    "rejects invalid key data characters",
			raw:     "ssh-ed25519 AAAAC3Nza? user@example",
			wantErr: "invalid characters",
		},
		{
			name:    "rejects missing key data",
			raw:     "ssh-ed25519",
			wantErr: "key type and key data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeSSHKey(tt.raw)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("error = nil, want error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("got = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{name: "trims spaces and slash", raw: " https://configured-api.example.test/hyperhive/ ", want: "https://configured-api.example.test/hyperhive"},
		{name: "drops query and fragment", raw: "https://example.test/api?x=1#top", want: "https://example.test/api"},
		{name: "rejects empty", raw: "", wantErr: true},
		{name: "rejects missing scheme", raw: "configured-api.example.test/hyperhive", wantErr: true},
		{name: "rejects ftp", raw: "ftp://example.test/api", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeBaseURL(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("got = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractNFSIP(t *testing.T) {
	tests := []struct {
		source string
		want   string
	}{
		{"192.168.76.1:/mnt/ssd500", "192.168.76.1"},
		{"10.0.0.10:/nfsshare/iso", "10.0.0.10"},
		{"nfs.example.com:/share", "nfs.example.com"},
		{"no-colon", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractNFSIP(tt.source)
		if got != tt.want {
			t.Fatalf("source = %q, got = %q, want %q", tt.source, got, tt.want)
		}
	}
}

func TestAttemptMountAllMountsSharesAfterPing(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "service.log")
	runner := &fakeCommandRunner{}
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://api.test", Token: "jwt-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			return fakeAPIClient{listNFSFunc: func(context.Context, string) ([]api.NFSShare, error) {
				return []api.NFSShare{
					{ID: 1, Source: "192.168.76.1:/mnt/ssd500", Name: "ssd500"},
					{ID: 2, Source: "192.168.76.2:/mnt/ssd1tb", Name: "ssd1tb"},
				}, nil
			}}
		},
		commandRunner: runner,
		isMounted:     func(string) bool { return false },
	}

	logger := testServiceLogger(t, logPath)
	code := attemptMountAll(context.Background(), logger, d)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}

	wantCommands := []recordedCommand{
		{name: "ping", args: []string{"-c", "1", "-W", "2", "192.168.76.1"}},
		{name: "mkdir", args: []string{"-p", "/mnt/hyperhive/ssd500"}},
		{name: "mount", args: []string{"-t", "nfs", "192.168.76.1:/mnt/ssd500", "/mnt/hyperhive/ssd500"}},
		{name: "ping", args: []string{"-c", "1", "-W", "2", "192.168.76.2"}},
		{name: "mkdir", args: []string{"-p", "/mnt/hyperhive/ssd1tb"}},
		{name: "mount", args: []string{"-t", "nfs", "192.168.76.2:/mnt/ssd1tb", "/mnt/hyperhive/ssd1tb"}},
	}
	if len(runner.commands) != len(wantCommands) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, wantCommands)
	}
	for i, want := range wantCommands {
		if runner.commands[i].name != want.name || strings.Join(runner.commands[i].args, " ") != strings.Join(want.args, " ") {
			t.Fatalf("command[%d] = %#v, want %#v", i, runner.commands[i], want)
		}
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	logContent := string(logData)
	for _, want := range []string{"pinging", "mounted", "completed successfully"} {
		if !strings.Contains(logContent, want) {
			t.Fatalf("log = %q, missing %q", logContent, want)
		}
	}
}

func TestAttemptMountAllSkipsShareWhenPingFails(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "service.log")
	runner := &fakeCommandRunner{err: errors.New("host unreachable"), errAt: 1}
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://api.test", Token: "jwt-token"}, nil
		},
		newAPIClient: func(string) apiClient {
			return fakeAPIClient{listNFSFunc: func(context.Context, string) ([]api.NFSShare, error) {
				return []api.NFSShare{
					{ID: 1, Source: "10.0.0.1:/a", Name: "a"},
					{ID: 2, Source: "10.0.0.2:/b", Name: "b"},
				}, nil
			}}
		},
		commandRunner: runner,
		isMounted:     func(string) bool { return false },
	}

	logger := testServiceLogger(t, logPath)
	code := attemptMountAll(context.Background(), logger, d)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}

	wantCommands := []recordedCommand{
		{name: "ping", args: []string{"-c", "1", "-W", "2", "10.0.0.1"}},
		{name: "ping", args: []string{"-c", "1", "-W", "2", "10.0.0.2"}},
		{name: "mkdir", args: []string{"-p", "/mnt/hyperhive/b"}},
		{name: "mount", args: []string{"-t", "nfs", "10.0.0.2:/b", "/mnt/hyperhive/b"}},
	}
	if len(runner.commands) != len(wantCommands) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, wantCommands)
	}
	for i, want := range wantCommands {
		if runner.commands[i].name != want.name || strings.Join(runner.commands[i].args, " ") != strings.Join(want.args, " ") {
			t.Fatalf("command[%d] = %#v, want %#v", i, runner.commands[i], want)
		}
	}

	logData, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logData), "ping 10.0.0.1 failed") {
		t.Fatalf("log = %q, missing ping failure", string(logData))
	}
}

func TestAttemptMountAllLogsMountErrors(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "service.log")
	runner := &fakeCommandRunner{err: errors.New("mount error"), errAt: 3}
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://api.test", Token: "jwt-token"}, nil
		},
		newAPIClient: func(string) apiClient {
			return fakeAPIClient{listNFSFunc: func(context.Context, string) ([]api.NFSShare, error) {
				return []api.NFSShare{
					{ID: 1, Source: "10.0.0.1:/a", Name: "a"},
					{ID: 2, Source: "10.0.0.2:/b", Name: "b"},
				}, nil
			}}
		},
		commandRunner: runner,
		isMounted:     func(string) bool { return false },
	}

	logger := testServiceLogger(t, logPath)
	code := attemptMountAll(context.Background(), logger, d)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}

	logData, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logData), "mount 10.0.0.1:/a -> /mnt/hyperhive/a failed") {
		t.Fatalf("log = %q, missing mount failure", string(logData))
	}
	if !strings.Contains(string(logData), "mounted 10.0.0.2:/b -> /mnt/hyperhive/b") {
		t.Fatalf("log = %q, missing successful mount of second share", string(logData))
	}
}

func TestAttemptMountAllRequiresSetup(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "service.log")
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) { return config.Config{}, nil },
		newAPIClient: func(string) apiClient {
			t.Fatal("newAPIClient should not be called")
			return nil
		},
		commandRunner: &fakeCommandRunner{},
		isMounted:     func(string) bool { return false },
	}

	logger := testServiceLogger(t, logPath)
	code := attemptMountAll(context.Background(), logger, d)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	logData, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logData), "base URL not set") {
		t.Fatalf("log = %q, missing base URL not set", string(logData))
	}
}

func TestAttemptMountAllRequiresLogin(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "service.log")
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://api.test"}, nil
		},
		newAPIClient: func(string) apiClient {
			t.Fatal("newAPIClient should not be called")
			return nil
		},
		commandRunner: &fakeCommandRunner{},
		isMounted:     func(string) bool { return false },
	}

	logger := testServiceLogger(t, logPath)
	code := attemptMountAll(context.Background(), logger, d)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	logData, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logData), "token not set") {
		t.Fatalf("log = %q, missing token not set", string(logData))
	}
}

func TestAttemptServiceLoginRefreshesToken(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "service.log")
	path := filepath.Join(t.TempDir(), "config.json")
	var saved config.Config
	d := deps{
		configPath: func() (string, error) { return path, nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{
				BaseURL:  "https://api.test",
				Email:    "user@example.test",
				Password: "secret",
				Token:    "old-token",
			}, nil
		},
		saveConfig: func(gotPath string, cfg config.Config) error {
			if gotPath != path {
				t.Fatalf("path = %q, want %q", gotPath, path)
			}
			saved = cfg
			return nil
		},
		newAPIClient: func(baseURL string) apiClient {
			if baseURL != "https://api.test" {
				t.Fatalf("baseURL = %q", baseURL)
			}
			return fakeAPIClient{loginFunc: func(ctx context.Context, email, password string) (api.LoginResponse, error) {
				if email != "user@example.test" || password != "secret" {
					t.Fatalf("credentials = %q/%q", email, password)
				}
				return api.LoginResponse{Token: "new-token"}, nil
			}}
		},
	}

	logger := testServiceLogger(t, logPath)
	code := attemptServiceLogin(context.Background(), logger, d)
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if saved.Token != "new-token" || saved.Email != "user@example.test" || saved.Password != "secret" {
		t.Fatalf("saved config = %#v", saved)
	}
	logData, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logData), "login successful") {
		t.Fatalf("log = %q, missing login success", string(logData))
	}
}

func TestAttemptServiceLoginRequiresStoredCredentials(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "service.log")
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) {
			return config.Config{BaseURL: "https://api.test", Token: "old-token"}, nil
		},
		saveConfig: func(string, config.Config) error {
			t.Fatal("saveConfig should not be called")
			return nil
		},
		newAPIClient: func(string) apiClient {
			t.Fatal("newAPIClient should not be called")
			return nil
		},
	}

	logger := testServiceLogger(t, logPath)
	code := attemptServiceLogin(context.Background(), logger, d)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	logData, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logData), "email/password not set") {
		t.Fatalf("log = %q, missing credentials message", string(logData))
	}
}

func TestServiceScheduleMountsOnlyAfterSuccessfulLogin(t *testing.T) {
	start := time.Date(2026, 6, 26, 10, 0, 0, 0, time.UTC)
	schedule := newServiceSchedule(start)
	loginInterval := 10 * time.Minute
	mountInterval := 10 * time.Minute
	loginResults := []int{1, 0, 0}
	loginCalls := 0
	mountCalls := 0

	run := func(now time.Time) {
		schedule.runDue(now, loginInterval, mountInterval, func() int {
			result := loginResults[loginCalls]
			loginCalls++
			return result
		}, func() int {
			mountCalls++
			return 0
		})
	}

	run(start)
	if loginCalls != 1 || mountCalls != 0 {
		t.Fatalf("after failed login, loginCalls=%d mountCalls=%d; want 1/0", loginCalls, mountCalls)
	}
	if got := schedule.nextWaitUntil(); !got.Equal(start.Add(loginInterval)) {
		t.Fatalf("next wait = %s, want %s", got, start.Add(loginInterval))
	}

	run(start.Add(loginInterval))
	if loginCalls != 2 || mountCalls != 1 {
		t.Fatalf("after successful login, loginCalls=%d mountCalls=%d; want 2/1", loginCalls, mountCalls)
	}

	run(start.Add(2 * loginInterval))
	if loginCalls != 3 || mountCalls != 2 {
		t.Fatalf("after next interval, loginCalls=%d mountCalls=%d; want 3/2", loginCalls, mountCalls)
	}
}

func TestServiceIntervalsUseDefaultsConfigAndEnv(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	d := deps{
		configPath: func() (string, error) { return "/tmp/config.json", nil },
		loadConfig: func(string) (config.Config, error) { return config.Config{}, nil },
	}
	loginInterval, mountInterval := serviceIntervals(logger, d)
	if loginInterval != 10*time.Minute || mountInterval != 10*time.Minute {
		t.Fatalf("intervals = %s/%s, want 10m/10m", loginInterval, mountInterval)
	}

	d.loadConfig = func(string) (config.Config, error) {
		return config.Config{LoginIntervalMinutes: 3, MountIntervalMinutes: 4}, nil
	}
	loginInterval, mountInterval = serviceIntervals(logger, d)
	if loginInterval != 3*time.Minute || mountInterval != 4*time.Minute {
		t.Fatalf("intervals = %s/%s, want 3m/4m", loginInterval, mountInterval)
	}

	t.Setenv("HYPERHIVE_LOGIN_INTERVAL", "7")
	t.Setenv("HYPERHIVE_MOUNT_INTERVAL", "8")
	loginInterval, mountInterval = serviceIntervals(logger, d)
	if loginInterval != 7*time.Minute || mountInterval != 8*time.Minute {
		t.Fatalf("intervals = %s/%s, want 7m/8m", loginInterval, mountInterval)
	}
}

func TestIsMountPoint(t *testing.T) {
	if !isMountPoint("/") {
		t.Fatal("isMountPoint(\"/\") = false, want true")
	}
	if isMountPoint("/nonexistent/path/that/does/not/exist") {
		t.Fatal("isMountPoint(nonexistent) = true, want false")
	}
}

func TestLogsShowsLogContent(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "service.log")
	content := "2026-06-23 line 1\n2026-06-23 line 2\n"
	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	d := deps{
		logPath: func() (string, error) { return logPath, nil },
	}

	var stdout, stderr strings.Builder
	code := run([]string{"logs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "line 1") || !strings.Contains(stdout.String(), "line 2") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestLogsShowsMessageWhenNoLogFile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "nonexistent.log")
	d := deps{
		logPath: func() (string, error) { return logPath, nil },
	}

	var stdout, stderr strings.Builder
	code := run([]string{"logs"}, strings.NewReader(""), &stdout, &stderr, d)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No logs found") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
