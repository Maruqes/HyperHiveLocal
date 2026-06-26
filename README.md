# HyperHiveLocal

CLI local para configurar a API HyperHive e fazer login a partir do terminal.

## Comandos

```bash
hyperhive setup
hyperhive login
hyperhive vms
hyperhive ssh
hyperhive nfs
hyperhive install_nfs
hyperhive remove_nfs
hyperhive logs
```

`setup` pede o URL base da API, por exemplo:

```text
https://<api-url>/api
```

`login` pede email e password, envia:

```http
POST <api-url-configurado-no-setup>/login
Content-Type: application/json

{"email":"hyperhive@email.com","password":"pass"}
```

e guarda o `token` recebido.

`vms` usa o token guardado pelo `login` e chama:

```http
GET <api-url-configurado-no-setup>/virsh/getallvms
Authorization: Bearer <token-guardado>
```

Exemplo de saída:

```text
NAME           MACHINE   STATE    CPU  MEMORY   DISK   IP              NETWORK  NOVNC
ubuntu-server  slave-01  RUNNING  4    8192 MB  50 GB  192.168.122.15  default  5900
```

`nfs` usa o token guardado pelo `login` e chama:

```http
GET <api-url-configurado-no-setup>/nfs/list
Authorization: Bearer <token-guardado>
```

Exemplo de saída:

```text
ID  MACHINE        NAME    STATUS   USED_GB  FREE_GB  TOTAL_GB  SOURCE                                TARGET                                            FOLDER                       HOST_MOUNT
1   marques2673sv  raid4t  working  406      3320     3726      192.168.76.55:/mnt/raid4t/raid4tyXLGlj /mnt/512SvMan/shared/marques2673sv_raid4tyXLGlj /mnt/raid4t/raid4tyXLGlj false
```

`install_nfs` usa o token guardado pelo `login` e chama:

```http
GET <api-url-configurado-no-setup>/nfs/list
Authorization: Bearer <token-guardado>
```

Para cada NFS share devolvido, monta em `/mnt/hyperhive/{name}`:

```bash
sudo mkdir -p /mnt/hyperhive/<name>
sudo mount -t nfs <source> /mnt/hyperhive/<name>
```

Exemplo: para um share com `name=ssd500` e `source=192.168.76.1:/mnt/ssd500/ssd500singleoKsLcz`:

```bash
sudo mkdir -p /mnt/hyperhive/ssd500
sudo mount -t nfs 192.168.76.1:/mnt/ssd500/ssd500singleoKsLcz /mnt/hyperhive/ssd500
```

`remove_nfs` usa o token guardado pelo `login`, chama o mesmo endpoint `/nfs/list` e para cada share executa:

```bash
sudo umount /mnt/hyperhive/<name>
```

`ssh` lista as VMs em colunas alinhadas, permite escolher `1..n` para uma VM específica, e depois pede a chave pública SSH.

A chave pode ser:

- escrita/colada manualmente;
- selecionada de uma lista de ficheiros `~/.ssh/*.pub`;
- carregada a partir de um caminho indicado manualmente.

Depois chama:

```http
POST <api-url-configurado-no-setup>/virsh/add_ssh_key/<vm-name>
Authorization: Bearer <token-guardado>
Content-Type: application/json

{"ssh_key":"ssh-ed25519 AAAAC3Nza... user@example"}
```

## Configuração local

Por defeito, a CLI guarda a configuração em:

```text
~/.config/hyperhive/config.json
```

O diretório é criado com permissões `0700` e o ficheiro com permissões `0600`.
A password é guardada em texto nesse ficheiro, conforme pedido.

Podes mudar o caminho com:

```bash
HYPERHIVE_CONFIG=/caminho/config.json hyperhive login
```

## Desenvolvimento

```bash
go test ./...
go build -o hyperhive ./cmd/hyperhive
./hyperhive setup
./hyperhive login
./hyperhive vms
./hyperhive ssh
./hyperhive nfs
./hyperhive install_nfs
./hyperhive remove_nfs
```

Para instalar no sistema (build + binário global + serviço systemd):

```bash
make install
```

O `make install` faz build, instala o binário em `/usr/local/bin/hyperhive` e cria o serviço systemd `/etc/systemd/system/hyperhive.service` que executa `hyperhive systemdexec` como root. O serviço fica habilitado e iniciado.

Para alterar os intervalos via Makefile/systemd:

```bash
make install LOGIN_INTERVAL=5 MOUNT_INTERVAL=5
```

`hyperhive systemdexec` é executado pelo serviço systemd indefinidamente. Por defeito, tenta fazer login a cada 10 minutos. Só depois de um login bem-sucedido tenta montar os NFS a cada 10 minutos. Se a rede/API/NFS ainda não estiver disponível, continua a tentar nos ciclos seguintes até conseguir.

Em cada tentativa de login, usa o `email` e `password` guardados no config e atualiza o `token`. Em cada tentativa de montagem, para cada NFS share devolvido pela API:

1. Verifica se já está montado em `/mnt/hyperhive/{name}` (lê `/proc/mounts`) — se sim, salta
2. Extrai o IP do `source` (ex: `192.168.76.1:/mnt/...` -> `192.168.76.1`)
3. Faz `ping -c 1 -W 2 <ip>` para confirmar que o servidor NFS está acessível
4. Se o ping funcionar, executa `mkdir -p /mnt/hyperhive/{name}` e `mount -t nfs <source> /mnt/hyperhive/{name}` silenciosamente
5. Se o ping ou a montagem falhar, regista o erro no log e tenta novamente no próximo ciclo

Os intervalos podem ser configurados no `/etc/hyperhive/config.json` (ou no ficheiro definido por `HYPERHIVE_CONFIG`), em minutos:

```json
{
  "base_url": "https://api.example",
  "email": "user@example.com",
  "password": "secret",
  "token": "...",
  "login_interval_minutes": 10,
  "mount_interval_minutes": 10
}
```

Também podem ser configurados no systemd com variáveis de ambiente, que têm prioridade sobre o config:

```ini
[Service]
Environment=HYPERHIVE_LOGIN_INTERVAL=5
Environment=HYPERHIVE_MOUNT_INTERVAL=5
```

Os logs são escritos em `/var/log/hyperhive/service.log` e também no stderr (visível via `journalctl -u hyperhive`).

`hyperhive logs` mostra o conteúdo do ficheiro de logs do serviço.

Para remover:

```bash
make uninstall
```
