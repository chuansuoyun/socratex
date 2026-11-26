# Socratex (Golang) v2

基于配置文件驱动的 Secure Web Proxy，拆分为 **Server** 与 **Client**，支持上游出口（SOCKS5 / HTTP）。

原项目思路来自 [Leask/socratex](https://github.com/Leask/socratex)。

## 架构

```
本地应用 / Client  --HTTPS CONNECT-->  Socratex Server  --直连或 upstream-->  目标网站
```

| 组件 | 作用 |
|------|------|
| **Server** | 部署在境外 VPS，提供 HTTPS 代理、PAC、自动证书、可选上游出口 |
| **Client** | 本机运行，暴露本地 SOCKS5/HTTP，自动隧道到远程 Server |

## 编译

```bash
git clone https://github.com/chuansuoyun/socratex.git
cd socratex
go build -o socratex ./cmd/socratex
./socratex -example   # 生成 config.example.json
```

## 配置 config.json

```bash
./socratex -config config.json
```

### Server

```json
{
  "mode": "server",
  "server": {
    "listen": ":443",
    "http_listen": ":80",
    "domain": "proxy.example.com",
    "auto_cert": true,
    "cert_file": "",
    "key_file": "",
    "auth": { "username": "alice", "password": "change-me", "token": "" },
    "bypass": ["CN"],
    "upstream": {
      "enabled": false,
      "type": "socks5",
      "address": "1.2.3.4",
      "port": 1080,
      "username": "",
      "password": ""
    }
  }
}
```

| 字段 | 说明 |
|------|------|
| `listen` / `http_listen` | HTTPS / HTTP 监听 |
| `domain` | 域名（证书与 PAC） |
| `auto_cert` | Let's Encrypt 自动证书 |
| `cert_file` / `key_file` | 自定义 PEM 证书 |
| `auth` | Basic / Token 认证 |
| `bypass` | GeoIP 直连国家 |
| `upstream` | 出口 SOCKS5 或 HTTP 代理 |

### Client

```json
{
  "mode": "client",
  "client": {
    "listen": "127.0.0.1:1080",
    "type": "socks5",
    "server": "https://proxy.example.com:443",
    "username": "alice",
    "password": "change-me"
  }
}
```

本机软件填 `127.0.0.1:1080` 即可。

## 部署

**Server（VPS）**

```bash
cp config.server.example.json config.json
# 编辑 domain / auth / upstream
sudo ./socratex -config config.json
```

**Client（本机）**

```bash
cp config.client.example.json config.json
./socratex -config config.json
```

## License

MIT
