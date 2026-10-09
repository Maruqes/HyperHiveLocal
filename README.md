# HyperHiveLocal

CLI tool to manage HyperHive API from the terminal.

## Install

```bash
make install
```

This builds the binary, installs it to `/usr/local/bin/hyperhive`, and sets up a systemd service.

To uninstall:

```bash
make uninstall
```

## Usage

### Setup

Configure the API URL:

```bash
hyperhive setup
```

You'll be prompted for the API base URL (e.g., `https://api.example.com/api`).

### Self-signed certificates

For a local API with a self-signed certificate:

```bash
hyperhive setup --insecure
hyperhive login
```

Enter the same API base URL during setup. This saves `"insecure_tls": true` in the config and applies to all API requests, including the systemd service. It disables TLS certificate and server identity verification; use it only on a trusted network. Verification is enabled by default. Restore it with `hyperhive setup --insecure=false`.

The service uses a separate config. To enable self-signed certificates for the service:

```bash
sudo env HYPERHIVE_CONFIG=/etc/hyperhive/config.json hyperhive setup --insecure
```

### Login

```bash
hyperhive login
```

You'll be prompted for your email and password. The authentication token is saved automatically.

### List VMs

```bash
hyperhive vms
```

Shows all available virtual machines with their status, resources, and network info.

### Add SSH Key

```bash
hyperhive ssh
```

Select a VM and add your SSH public key. You can paste it manually, select from `~/.ssh/*.pub`, or specify a path.

### Manage NFS Shares

List available NFS shares:

```bash
hyperhive nfs
```

Mount all shares:

```bash
hyperhive install_nfs
```

Unmount all shares:

```bash
hyperhive remove_nfs
```

Mounted shares are available at `/mnt/hyperhive/{name}`.

### View Logs

```bash
hyperhive logs
```

Shows the service log file.

## Configuration

Config is stored at `~/.config/hyperhive/config.json` by default.

Override with:

```bash
HYPERHIVE_CONFIG=/path/config.json hyperhive <command>
```

## Development

```bash
go test ./...
go build -o hyperhive ./cmd/hyperhive
```
