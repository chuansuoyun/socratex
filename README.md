# Socratex (Golang) v2

基于配置文件驱动的 Secure Web Proxy，拆分为 **Server** 与 **Client**，支持上游出口（SOCKS5 / HTTP）。

## 架构

```
本地应用 / Client  --HTTPS CONNECT-->  Socratex Server  --直连或 upstream-->  目标网站
```

| 组件 | 作用 |
|------|------|
| **Server** | 公网 VPS：HTTPS 代理、PAC、自动/自定义证书、可选上游出口 |
| **Client** | 本机：暴露 SOCKS5/HTTP，软件填 `127.0.0.1:1080` 即可 |

---

## 编译

### 本机架构

```bash
git clone https://github.com/chuansuoyun/socratex.git
cd socratex
go build -o socratex ./cmd/socratex
# 或
make build
```

### 交叉编译（推荐）

```bash
# 方式一：脚本
chmod +x scripts/build.sh
./scripts/build.sh
# 产物在 dist/

# 只编部分平台
./scripts/build.sh linux-amd64 windows-amd64 darwin-arm64

# 方式二：Makefile
make all
make linux-amd64
make windows-amd64
make windows-386
make darwin-arm64
make release      # 全部 + SHA256SUMS
```

| 产物 | 平台 |
|------|------|
| `socratex-linux-amd64` | Linux 64 位（常见 VPS） |
| `socratex-linux-arm64` | Linux ARM64 |
| `socratex-windows-amd64.exe` | Windows 64 位 |
| `socratex-windows-386.exe` | Windows 32 位 |
| `socratex-darwin-arm64` | macOS Apple Silicon (M1/M2/M3) |
| `socratex-darwin-amd64` | macOS Intel |

打 tag 后 GitHub Actions 会自动发布（`.github/workflows/release.yml`）：

```bash
git tag v2.0.0 && git push origin v2.0.0
```

---

## systemd 部署（Linux Server）

```bash
./scripts/build.sh linux-amd64
sudo ./deploy/install-server.sh dist/socratex-linux-amd64
sudo nano /opt/socratex/config.json
sudo systemctl start socratex
sudo systemctl status socratex
journalctl -u socratex -f
```

手动安装：

```bash
sudo mkdir -p /opt/socratex
sudo cp dist/socratex-linux-amd64 /opt/socratex/socratex
sudo cp config.server.example.json /opt/socratex/config.json
sudo cp deploy/socratex.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now socratex
```

Client 可用 `deploy/socratex-client.service`。

---

## 配置

```bash
./socratex -example
./socratex -config config.json
```

Server / Client 示例见 `config.server.example.json`、`config.client.example.json`。

## License

MIT
