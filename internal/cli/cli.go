package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Maruqes/HyperHiveLocal/internal/api"
	"github.com/Maruqes/HyperHiveLocal/internal/config"
	"github.com/Maruqes/HyperHiveLocal/internal/terminal"
)

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

const (
	servicePingTimeout  = 5 * time.Second
	serviceMkdirTimeout = 30 * time.Second
	serviceMountTimeout = 2 * time.Minute
)

type apiClient interface {
	Login(ctx context.Context, email, password string) (api.LoginResponse, error)
	GetAllVMs(ctx context.Context, token string) (api.VMsResponse, error)
	ListNFS(ctx context.Context, token string) ([]api.NFSShare, error)
	AddSSHKey(ctx context.Context, token, vmName, sshKey string) error
}

type deps struct {
	configPath        func() (string, error)
	loadConfig        func(string) (config.Config, error)
	saveConfig        func(string, config.Config) error
	newAPIClient      func(string) apiClient
	passwordReader    terminal.PasswordReader
	listSSHPublicKeys func() ([]sshPublicKeyFile, error)
	readPublicKeyFile func(string) (string, error)
	commandRunner     commandRunner
	logPath           func() (string, error)
	isMounted         func(string) bool
}

func defaultDeps() deps {
	return deps{
		configPath:        config.Path,
		loadConfig:        config.Load,
		saveConfig:        config.Save,
		newAPIClient:      func(baseURL string) apiClient { return api.NewClient(baseURL) },
		passwordReader:    terminal.Reader{},
		listSSHPublicKeys: discoverSSHPublicKeyFiles,
		readPublicKeyFile: readPublicKeyFile,
		commandRunner:     execRunner{},
		logPath:           defaultLogPath,
		isMounted:         isMountPoint,
	}
}

type sshPublicKeyFile struct {
	Label string
	Path  string
}

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return run(args, stdin, stdout, stderr, defaultDeps())
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, d deps) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}

	switch args[0] {
	case "setup":
		if err := setup(stdin, stdout, d); err != nil {
			fmt.Fprintf(stderr, "Erro: %v\n", err)
			return 1
		}
		return 0
	case "login":
		if err := login(stdin, stdout, d); err != nil {
			fmt.Fprintf(stderr, "Erro: %v\n", err)
			return 1
		}
		return 0
	case "vms":
		if err := vms(stdout, d); err != nil {
			fmt.Fprintf(stderr, "Erro: %v\n", err)
			return 1
		}
		return 0
	case "ssh":
		if err := ssh(stdin, stdout, d); err != nil {
			fmt.Fprintf(stderr, "Erro: %v\n", err)
			return 1
		}
		return 0
	case "nfs":
		if err := nfs(stdout, d); err != nil {
			fmt.Fprintf(stderr, "Erro: %v\n", err)
			return 1
		}
		return 0
	case "install_nfs":
		if err := installNFS(stdout, d); err != nil {
			fmt.Fprintf(stderr, "Erro: %v\n", err)
			return 1
		}
		return 0
	case "remove_nfs":
		if err := removeNFS(stdout, d); err != nil {
			fmt.Fprintf(stderr, "Erro: %v\n", err)
			return 1
		}
		return 0
	case "systemdexec":
		return systemdExec(stderr, d)
	case "logs":
		if err := logs(stdout, d); err != nil {
			fmt.Fprintf(stderr, "Erro: %v\n", err)
			return 1
		}
		return 0
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "Comando desconhecido: %s\n\n", args[0])
		printUsage(stderr)
		return 1
	}
}

func setup(stdin io.Reader, stdout io.Writer, d deps) error {
	path, err := d.configPath()
	if err != nil {
		return err
	}
	cfg, err := d.loadConfig(path)
	if err != nil {
		return err
	}

	rawBaseURL, err := terminal.ReadLine("API URL: ", stdin, stdout)
	if err != nil {
		return fmt.Errorf("read api url: %w", err)
	}
	baseURL, err := normalizeBaseURL(rawBaseURL)
	if err != nil {
		return err
	}

	cfg.BaseURL = baseURL
	if err := d.saveConfig(path, cfg); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Configuração guardada em %s\n", path)
	return nil
}

func login(stdin io.Reader, stdout io.Writer, d deps) error {
	path, err := d.configPath()
	if err != nil {
		return err
	}
	cfg, err := d.loadConfig(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("executa primeiro: hyperhive setup")
	}

	email, err := terminal.ReadLine("Email: ", stdin, stdout)
	if err != nil {
		return fmt.Errorf("read email: %w", err)
	}
	if strings.TrimSpace(email) == "" {
		return errors.New("email não pode estar vazio")
	}

	password, err := d.passwordReader.ReadPassword("Password: ", stdin, stdout)
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if strings.TrimSpace(password) == "" {
		return errors.New("password não pode estar vazia")
	}

	login, err := d.newAPIClient(cfg.BaseURL).Login(context.Background(), email, password)
	if err != nil {
		return err
	}

	cfg.Email = email
	cfg.Password = password
	cfg.Token = login.Token
	if err := d.saveConfig(path, cfg); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Login feito. Credenciais guardadas em %s\n", path)
	return nil
}

func vms(stdout io.Writer, d deps) error {
	path, err := d.configPath()
	if err != nil {
		return err
	}
	cfg, err := d.loadConfig(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("executa primeiro: hyperhive setup")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return errors.New("executa primeiro: hyperhive login")
	}

	res, err := d.newAPIClient(cfg.BaseURL).GetAllVMs(context.Background(), cfg.Token)
	if err != nil {
		return err
	}

	printVMs(stdout, res)
	return nil
}

func printVMs(out io.Writer, res api.VMsResponse) {
	if len(res.VMs) == 0 {
		fmt.Fprintln(out, "Nenhuma VM encontrada.")
	} else {
		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tMACHINE\tSTATE\tCPU\tMEMORY\tDISK\tIP\tNETWORK\tNOVNC")
		for _, vm := range res.VMs {
			fmt.Fprintf(
				w,
				"%s\t%s\t%s\t%d\t%d MB\t%d GB\t%s\t%s\t%s\n",
				vm.Name,
				vm.MachineName,
				vm.State,
				vm.CPUCount,
				vm.MemoryMB,
				vm.DiskSizeGB,
				strings.Join(vm.IP, ","),
				vm.Network,
				vm.NovncPort,
			)
		}
		_ = w.Flush()
	}

	for _, warning := range res.Warnings {
		fmt.Fprintf(out, "Aviso: %s\n", warning)
	}
}

func nfs(stdout io.Writer, d deps) error {
	path, err := d.configPath()
	if err != nil {
		return err
	}
	cfg, err := d.loadConfig(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("executa primeiro: hyperhive setup")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return errors.New("executa primeiro: hyperhive login")
	}

	shares, err := d.newAPIClient(cfg.BaseURL).ListNFS(context.Background(), cfg.Token)
	if err != nil {
		return err
	}

	printNFS(stdout, shares)
	return nil
}

func printNFS(out io.Writer, shares []api.NFSShare) {
	if len(shares) == 0 {
		fmt.Fprintln(out, "No NFS shares found.")
		return
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tMACHINE\tNAME\tSTATUS\tUSED_GB\tFREE_GB\tTOTAL_GB\tSOURCE\tTARGET\tFOLDER\tHOST_MOUNT")
	for _, share := range shares {
		fmt.Fprintf(
			w,
			"%d\t%s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\t%s\n",
			share.ID,
			share.MachineName,
			share.Name,
			share.Status.Display(),
			share.Status.SpaceOccupiedGB,
			share.Status.SpaceFreeGB,
			share.Status.SpaceTotalGB,
			share.Source,
			share.Target,
			share.FolderPath,
			share.HostNormalMount,
		)
	}
	_ = w.Flush()
}

func installNFS(stdout io.Writer, d deps) error {
	path, err := d.configPath()
	if err != nil {
		return err
	}
	cfg, err := d.loadConfig(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("executa primeiro: hyperhive setup")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return errors.New("executa primeiro: hyperhive login")
	}

	shares, err := d.newAPIClient(cfg.BaseURL).ListNFS(context.Background(), cfg.Token)
	if err != nil {
		return err
	}

	if len(shares) == 0 {
		fmt.Fprintln(stdout, "No NFS shares to install.")
		return nil
	}

	for _, share := range shares {
		target := nfsMountTarget(share)
		fmt.Fprintf(stdout, "Installing %s -> %s\n", share.Source, target)
		if err := d.commandRunner.Run(context.Background(), "sudo", "mkdir", "-p", target); err != nil {
			return fmt.Errorf("mkdir %s: %w", target, err)
		}
		if err := d.commandRunner.Run(context.Background(), "sudo", "mount", "-t", "nfs", share.Source, target); err != nil {
			return fmt.Errorf("mount %s: %w", target, err)
		}
	}
	fmt.Fprintln(stdout, "NFS shares installed successfully.")
	return nil
}

func removeNFS(stdout io.Writer, d deps) error {
	path, err := d.configPath()
	if err != nil {
		return err
	}
	cfg, err := d.loadConfig(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("executa primeiro: hyperhive setup")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return errors.New("executa primeiro: hyperhive login")
	}

	shares, err := d.newAPIClient(cfg.BaseURL).ListNFS(context.Background(), cfg.Token)
	if err != nil {
		return err
	}

	if len(shares) == 0 {
		fmt.Fprintln(stdout, "No NFS shares to remove.")
		return nil
	}

	for _, share := range shares {
		target := nfsMountTarget(share)
		fmt.Fprintf(stdout, "Removing %s\n", target)
		if err := d.commandRunner.Run(context.Background(), "sudo", "umount", target); err != nil {
			return fmt.Errorf("umount %s: %w", target, err)
		}
	}
	fmt.Fprintln(stdout, "NFS shares removed successfully.")
	return nil
}

func systemdExec(stderr io.Writer, d deps) int {
	logFilePath, err := d.logPath()
	if err != nil {
		fmt.Fprintf(stderr, "Erro ao obter caminho do log: %v\n", err)
		return 1
	}

	logFile, err := openServiceLog(logFilePath)
	if err != nil {
		fmt.Fprintf(stderr, "Erro ao abrir log: %v\n", err)
		return 1
	}
	defer logFile.Close()

	logger := log.New(io.MultiWriter(logFile, stderr), "", log.LstdFlags)
	logger.Println("systemdexec: starting")

	loginInterval, mountInterval := serviceIntervals(logger, d)
	logger.Printf("systemdexec: login interval: %s", loginInterval)
	logger.Printf("systemdexec: mount interval: %s", mountInterval)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	schedule := newServiceSchedule(time.Now())
	for {
		now := time.Now()
		schedule.runDue(now, loginInterval, mountInterval,
			func() int { return attemptServiceLogin(ctx, logger, d) },
			func() int { return attemptMountAll(ctx, logger, d) },
		)

		wait := time.Until(schedule.nextWaitUntil())
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			logger.Println("systemdexec: stopping")
			return 0
		case <-timer.C:
		}
	}
}

type serviceSchedule struct {
	nextLogin time.Time
	nextMount time.Time
	loginOK   bool
}

func newServiceSchedule(now time.Time) serviceSchedule {
	return serviceSchedule{nextLogin: now}
}

func (s *serviceSchedule) runDue(now time.Time, loginInterval, mountInterval time.Duration, login func() int, mount func() int) {
	if !now.Before(s.nextLogin) {
		s.loginOK = login() == 0
		if s.loginOK && s.nextMount.IsZero() {
			s.nextMount = now
		}
		s.nextLogin = now.Add(loginInterval)
	}
	if s.loginOK && !now.Before(s.nextMount) {
		mount()
		s.nextMount = now.Add(mountInterval)
	}
}

func (s serviceSchedule) nextWaitUntil() time.Time {
	if !s.loginOK {
		return s.nextLogin
	}
	return earliestTime(s.nextLogin, s.nextMount)
}

func attemptServiceLogin(ctx context.Context, logger *log.Logger, d deps) int {
	path, err := d.configPath()
	if err != nil {
		logger.Printf("systemdexec: config path error before login: %v", err)
		return 1
	}
	cfg, err := d.loadConfig(path)
	if err != nil {
		logger.Printf("systemdexec: config load error before login: %v", err)
		return 1
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		logger.Println("systemdexec: base URL not set, cannot login; run: hyperhive setup")
		return 1
	}
	if strings.TrimSpace(cfg.Email) == "" || strings.TrimSpace(cfg.Password) == "" {
		logger.Println("systemdexec: email/password not set, cannot refresh login; run: hyperhive login")
		return 1
	}

	logger.Printf("systemdexec: logging in as %s", cfg.Email)
	login, err := d.newAPIClient(cfg.BaseURL).Login(ctx, cfg.Email, cfg.Password)
	if err != nil {
		logger.Printf("systemdexec: login error: %v", err)
		return 1
	}
	latestCfg, err := d.loadConfig(path)
	if err != nil {
		logger.Printf("systemdexec: config reload after login failed, saving token to previously loaded config: %v", err)
		latestCfg = cfg
	}
	latestCfg.Token = login.Token
	if err := d.saveConfig(path, latestCfg); err != nil {
		logger.Printf("systemdexec: save refreshed token error: %v", err)
		return 1
	}
	logger.Println("systemdexec: login successful; token refreshed")
	return 0
}

func attemptMountAll(ctx context.Context, logger *log.Logger, d deps) int {
	path, err := d.configPath()
	if err != nil {
		logger.Printf("systemdexec: config path error: %v", err)
		return 1
	}
	cfg, err := d.loadConfig(path)
	if err != nil {
		logger.Printf("systemdexec: config load error: %v", err)
		return 1
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		logger.Println("systemdexec: base URL not set, run: hyperhive setup")
		return 1
	}
	if strings.TrimSpace(cfg.Token) == "" {
		logger.Println("systemdexec: token not set, run: hyperhive login")
		return 1
	}

	shares, err := d.newAPIClient(cfg.BaseURL).ListNFS(ctx, cfg.Token)
	if err != nil {
		logger.Printf("systemdexec: list nfs error: %v", err)
		return 1
	}

	if len(shares) == 0 {
		logger.Println("systemdexec: no nfs shares to mount")
		return 0
	}

	failed := false
	for _, share := range shares {
		target := nfsMountTarget(share)

		if d.isMounted(target) {
			logger.Printf("systemdexec: already mounted %s, skipping", target)
			continue
		}

		ip := extractNFSIP(share.Source)
		if ip == "" {
			logger.Printf("systemdexec: could not extract IP from %q, skipping", share.Source)
			failed = true
			continue
		}

		logger.Printf("systemdexec: pinging %s for %s", ip, target)
		if err := runServiceCommand(ctx, d, servicePingTimeout, "ping", "-c", "1", "-W", "2", ip); err != nil {
			logger.Printf("systemdexec: ping %s failed: %v, skipping %s", ip, err, target)
			failed = true
			continue
		}

		logger.Printf("systemdexec: mounting %s -> %s", share.Source, target)
		if err := runServiceCommand(ctx, d, serviceMkdirTimeout, "mkdir", "-p", target); err != nil {
			logger.Printf("systemdexec: mkdir %s failed: %v", target, err)
			failed = true
			continue
		}
		if err := runServiceCommand(ctx, d, serviceMountTimeout, "mount", "-t", "nfs", share.Source, target); err != nil {
			logger.Printf("systemdexec: mount %s -> %s failed: %v", share.Source, target, err)
			failed = true
			continue
		}
		logger.Printf("systemdexec: mounted %s -> %s", share.Source, target)
	}

	if failed {
		logger.Println("systemdexec: completed with errors")
		return 1
	}
	logger.Println("systemdexec: completed successfully")
	return 0
}

func runServiceCommand(parent context.Context, d deps, timeout time.Duration, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return d.commandRunner.Run(ctx, name, args...)
}

func logs(stdout io.Writer, d deps) error {
	path, err := d.logPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(stdout, "No logs found at %s\n", path)
			return nil
		}
		return err
	}
	fmt.Fprint(stdout, string(data))
	return nil
}

func openServiceLog(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
}

func extractNFSIP(source string) string {
	idx := strings.Index(source, ":")
	if idx == -1 {
		return ""
	}
	return source[:idx]
}

func nfsMountTarget(share api.NFSShare) string {
	return filepath.Join("/mnt/hyperhive", share.Name)
}

func defaultLogPath() (string, error) {
	return "/var/log/hyperhive/service.log", nil
}

func serviceIntervals(logger *log.Logger, d deps) (time.Duration, time.Duration) {
	loginMinutes := 10
	mountMinutes := 10
	path, err := d.configPath()
	if err != nil {
		logger.Printf("systemdexec: config path error while reading intervals, using defaults: %v", err)
	} else if cfg, err := d.loadConfig(path); err != nil {
		logger.Printf("systemdexec: config load error while reading intervals, using defaults: %v", err)
	} else {
		if cfg.LoginIntervalMinutes > 0 {
			loginMinutes = cfg.LoginIntervalMinutes
		}
		if cfg.MountIntervalMinutes > 0 {
			mountMinutes = cfg.MountIntervalMinutes
		}
	}

	if n := positiveEnvMinutes("HYPERHIVE_LOGIN_INTERVAL"); n > 0 {
		loginMinutes = n
	}
	if n := positiveEnvMinutes("HYPERHIVE_MOUNT_INTERVAL"); n > 0 {
		mountMinutes = n
	}

	return time.Duration(loginMinutes) * time.Minute, time.Duration(mountMinutes) * time.Minute
}

func positiveEnvMinutes(name string) int {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func earliestTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func isMountPoint(target string) bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	clean := strings.TrimRight(target, "/")
	if clean == "" {
		clean = "/"
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == clean {
			return true
		}
	}
	return false
}

func ssh(stdin io.Reader, stdout io.Writer, d deps) error {
	path, err := d.configPath()
	if err != nil {
		return err
	}
	cfg, err := d.loadConfig(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return errors.New("executa primeiro: hyperhive setup")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return errors.New("executa primeiro: hyperhive login")
	}

	client := d.newAPIClient(cfg.BaseURL)
	res, err := client.GetAllVMs(context.Background(), cfg.Token)
	if err != nil {
		return err
	}

	lineInput := bufio.NewReader(stdin)
	target, label, err := promptVMSelection(lineInput, stdout, res.VMs)
	if err != nil {
		return err
	}

	sshKey, err := promptSSHKey(lineInput, stdout, d)
	if err != nil {
		return err
	}

	if err := client.AddSSHKey(context.Background(), cfg.Token, target, sshKey); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "SSH key added to %s.\n", label)
	return nil
}

func promptVMSelection(stdin io.Reader, stdout io.Writer, vms []api.VM) (target string, label string, err error) {
	fmt.Fprintln(stdout, "Select a VM:")
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  #\tNAME\tMACHINE\tSTATE")
	for i, vm := range vms {
		name := vm.Name
		if strings.TrimSpace(name) == "" {
			name = "(unnamed)"
		}
		fmt.Fprintf(w, "  %d)\t%s\t%s\t%s\n", i+1, name, vm.MachineName, vm.State)
	}
	_ = w.Flush()
	if len(vms) == 0 {
		return "", "", errors.New("no virtual machines found")
	}

	rawSelection, err := terminal.ReadLine("Selection: ", stdin, stdout)
	if err != nil {
		return "", "", fmt.Errorf("read vm selection: %w", err)
	}
	selection, err := strconv.Atoi(strings.TrimSpace(rawSelection))
	if err != nil {
		return "", "", errors.New("selection must be a number")
	}
	if selection < 1 || selection > len(vms) {
		return "", "", fmt.Errorf("selection must be between 1 and %d", len(vms))
	}

	vm := vms[selection-1]
	if strings.TrimSpace(vm.Name) == "" {
		return "", "", errors.New("selected vm has no name")
	}
	return vm.Name, vm.Name, nil
}

func promptSSHKey(stdin io.Reader, stdout io.Writer, d deps) (string, error) {
	files, err := d.listSSHPublicKeys()
	if err != nil {
		return "", fmt.Errorf("list ssh public keys: %w", err)
	}

	fmt.Fprintln(stdout, "Select an SSH public key:")
	w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  #\tSOURCE")
	fmt.Fprintln(w, "  0)\tType or paste manually")
	for i, file := range files {
		fmt.Fprintf(w, "  %d)\t%s\n", i+1, file.Label)
	}
	fmt.Fprintln(w, "  p)\tEnter a public key file path")
	_ = w.Flush()

	rawSelection, err := terminal.ReadLine("Selection: ", stdin, stdout)
	if err != nil {
		return "", fmt.Errorf("read ssh key selection: %w", err)
	}
	selection := strings.TrimSpace(rawSelection)
	if selection == "0" {
		sshKey, err := terminal.ReadLine("SSH public key: ", stdin, stdout)
		if err != nil {
			return "", fmt.Errorf("read ssh public key: %w", err)
		}
		return normalizeSSHKey(sshKey)
	}
	if strings.EqualFold(selection, "p") {
		path, err := terminal.ReadLine("Public key file path: ", stdin, stdout)
		if err != nil {
			return "", fmt.Errorf("read public key file path: %w", err)
		}
		return readSelectedPublicKeyFile(d, path)
	}

	index, err := strconv.Atoi(selection)
	if err != nil {
		return "", errors.New("ssh key selection must be a number or p")
	}
	if index < 1 || index > len(files) {
		return "", fmt.Errorf("ssh key selection must be 0, p, or between 1 and %d", len(files))
	}
	return readSelectedPublicKeyFile(d, files[index-1].Path)
}

func readSelectedPublicKeyFile(d deps, path string) (string, error) {
	expandedPath, err := expandUserPath(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}

	sshKey, err := d.readPublicKeyFile(expandedPath)
	if err != nil {
		return "", fmt.Errorf("read public key file: %w", err)
	}
	return normalizeSSHKey(sshKey)
}

func expandUserPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("public key file path is required")
	}
	if path == "~" {
		return os.UserHomeDir()
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func normalizeSSHKey(sshKey string) (string, error) {
	trimmed := strings.TrimSpace(sshKey)
	if trimmed == "" {
		return "", errors.New("ssh key is required")
	}
	if strings.ContainsAny(trimmed, "\r\n\x00") {
		return "", errors.New("ssh public key must be a single line")
	}

	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return "", errors.New("ssh public key must contain key type and key data")
	}

	keyType := fields[0]
	if !supportedSSHKeyTypes[keyType] {
		return "", fmt.Errorf("unsupported ssh public key type %q", keyType)
	}

	keyData := fields[1]
	if keyData == "" {
		return "", errors.New("ssh public key data is empty")
	}
	for _, r := range keyData {
		if !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '/' || r == '=') {
			return "", errors.New("ssh public key data contains invalid characters")
		}
	}
	return trimmed, nil
}

var supportedSSHKeyTypes = map[string]bool{
	"ssh-rsa":             true,
	"ssh-ed25519":         true,
	"ecdsa-sha2-nistp256": true,
	"ecdsa-sha2-nistp384": true,
	"ecdsa-sha2-nistp521": true,
}

func discoverSSHPublicKeyFiles() ([]sshPublicKeyFile, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	pattern := filepath.Join(home, ".ssh", "*.pub")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)

	files := make([]sshPublicKeyFile, 0, len(matches))
	for _, match := range matches {
		files = append(files, sshPublicKeyFile{
			Label: filepath.Join("~", ".ssh", filepath.Base(match)),
			Path:  match,
		})
	}
	return files, nil
}

func readPublicKeyFile(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("public key file path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func normalizeBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("api url não pode estar vazio")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("api url inválido: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("api url tem de começar por http:// ou https://")
	}
	if parsed.Host == "" {
		return "", errors.New("api url precisa de host")
	}

	parsed.Fragment = ""
	parsed.RawQuery = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, `HyperHive CLI

Usage:
  hyperhive setup       Configure the API base URL
  hyperhive login       Log in and store email, password, and token
  hyperhive vms         List virtual machines
  hyperhive ssh         Add an SSH public key to a virtual machine
  hyperhive nfs         List NFS shares
  hyperhive install_nfs Mount all NFS shares from the API
  hyperhive remove_nfs  Unmount all NFS shares from the API
  hyperhive systemdexec Run as a systemd service to mount NFS shares and refresh login
  hyperhive logs        Show systemdexec service logs

Config:
  Default path: ~/.config/hyperhive/config.json
  Override:     HYPERHIVE_CONFIG=/path/to/config.json
  Service intervals default to 10 minutes and can be changed with
  login_interval_minutes / mount_interval_minutes in config.json or with
  HYPERHIVE_LOGIN_INTERVAL / HYPERHIVE_MOUNT_INTERVAL environment variables`)
}
