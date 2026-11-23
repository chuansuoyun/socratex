# 🧱 Socratex (Golang)

A Secure Web Proxy. Which is fast, secure, and easy to use.

本项目是 [Leask/socratex](https://github.com/Leask/socratex) 的 **Go 语言重写版**。

Socratex 是一个真正的 **HTTPS 代理**（Secure Web Proxy），不是「HTTP over HTTPS」那种传统代理。它在 TCP 层工作，配合系统原生的自动代理配置（PAC / WPAD），让客户端无需安装任何 App 即可安全上网。

## 原理简述

1. 监听 **443** 端口，提供真正的 HTTPS 代理（支持 `CONNECT` 隧道）。
2. 监听 **80** 端口，用于 Let's Encrypt 证书申请 + 提供 PAC / WPAD 文件。
3. 使用 `golang.org/x/crypto/acme/autocert` 自动申请并续期证书。
4. 支持 **Token 认证** 和 **Basic Auth**。
5. 动态生成 PAC 文件，本地/内网地址直连，其他流量走代理。
6. **GeoIP Bypass**：按目标 IP 所属国家自动将域名加入 PAC 直连列表，减少不必要的代理流量。

客户端只需把 PAC 地址填到系统「自动代理配置」即可，对审查方看起来就像普通 HTTPS 流量。

## 快速部署

### 前置条件

- 一台有公网 IP 的服务器（建议不在中国大陆）
- 一个域名，并已将 A 记录指向服务器 IP
- 服务器已开放 **80** 和 **443** 端口

### 方式一：直接编译运行

```bash
# 1. 安装 Go 1.22+
# 2. 克隆仓库
git clone https://github.com/chuansuoyun/socratex.git
cd socratex

# 3. 编译
go build -o socratex .

# 4. 运行（需要 root 权限绑定 80/443）
# 常用：绕过中国大陆 IP，减少流量
sudo ./socratex --domain=example.com --bypass=CN
```

### 方式二：一行命令（推荐）

```bash
sudo go run . --domain=example.com --bypass=CN
```

### 方式三：Docker（可选）

```bash
docker build -t socratex .
docker run -d --restart=always \
  -p 80:80 -p 443:443 \
  -v ~/.socratex.json:/root/.socratex.json \
  -v ~/.cache/socratex-certs:/root/.cache/socratex-certs \
  socratex --domain=example.com --bypass=CN
```

### 启动成功后的输出示例

```
[SOCRATEX v1.1.0] https://github.com/chuansuoyun/socratex

* Token authentication:
  - PAC:  https://example.com/proxy.pac?token=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
  - WPAD: https://example.com/wpad.dat?token=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
  - Log:  https://example.com/console?token=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx

* Basic authentication:   (如果指定了 --user 和 --password)
  - PAC:   https://user:pass@example.com/proxy.pac
  - WPAD:  https://user:pass@example.com/wpad.dat
  - Log:   https://user:pass@example.com/console
  - Proxy: https://user:pass@example.com

* GeoIP Bypass countries: CN
```

把 **PAC 地址** 复制到系统的「自动代理配置」里即可。

## 命令行参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `--domain` | 部署使用的域名（HTTPS 模式必填） | - |
| `--http` | 仅使用 HTTP 模式（测试用） | false |
| `--user` | Basic Auth 用户名 | - |
| `--password` | Basic Auth 密码 | - |
| `--token` | Token 认证（不指定则自动生成） | 自动生成 |
| `--bypass` | 按国家绕过代理（ISO 代码，可重复） | - |
| `--port` | 监听端口 | 443 / 80 |
| `--address` | 监听地址 | 全部接口 |
| `--version` | 显示版本 | - |

示例：

```bash
# 绕过中国大陆 IP（最常用）
sudo ./socratex --domain=proxy.example.com --bypass=CN

# 绕过多个国家
sudo ./socratex --domain=proxy.example.com --bypass=CN --bypass=HK --bypass=TW

# 或用逗号
sudo ./socratex --domain=proxy.example.com --bypass=CN,US,JP

# 同时开启 Basic Auth
sudo ./socratex --domain=proxy.example.com --user=alice --password=secret --bypass=CN
```

配置会自动保存在 `~/.socratex.json`。

## 配置文件

路径：`~/.socratex.json`

```json
{
  "domain": "example.com",
  "token": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "user": "alice",
  "password": "secret",
  "bypass": ["CN"],
  "bypassHosts": {
    "www.baidu.com": 1730000000,
    "qq.com": 1730000001
  }
}
```

- `bypass`：要绕过的国家 ISO 代码列表
- `bypassHosts`：运行时学习到的「应直连」域名及最后访问时间（会自动写入 PAC）

证书缓存目录：`~/.cache/socratex-certs/`

## GeoIP Bypass 工作原理

1. 启动时通过 `--bypass=CN` 指定要绕过的国家（可多个）。
2. 客户端通过代理访问某个网站时，程序解析目标 IP 的国家（使用内嵌的 GeoIP 数据，无需额外下载数据库）。
3. 如果目标 IP 属于 bypass 国家，则将该**域名**加入 `bypassHosts`。
4. 之后生成的 PAC 文件会让这些域名 **DIRECT（直连）**，不再走代理。
5. 超过 7 天未再访问的域名会自动从列表中清理。

查看当前学习到的 bypass 列表：访问 `/console?token=你的token`。

## 与原版对比

| 功能 | 原版 (Node.js) | 本项目 (Go) |
|------|----------------|-------------|
| HTTPS 真实代理 | ✅ | ✅ |
| 自动证书 (ACME) | ✅ | ✅ |
| PAC / WPAD | ✅ | ✅ |
| Token Auth | ✅ | ✅ |
| Basic Auth | ✅ | ✅ |
| GeoIP Bypass | ✅ | ✅ |
| 可编程 upstream | ✅ | ❌（按需求未实现） |
| 单二进制部署 | ❌ | ✅ |
| 内存占用 | 较高 | 极低 |
| 无需外部 GeoIP 库 | 需 fast-geoip | 内嵌数据 |

## 注意事项

1. **必须用 root 运行**（或使用 `setcap`），因为要监听 80/443。
2. 域名必须正确解析到服务器，否则 ACME 证书申请会失败。
3. iOS 目前不支持 Secure Web Proxy。
4. 建议在境外服务器部署。

## 开发

```bash
go mod tidy
go build -o socratex .
./socratex --http --domain=127.0.0.1 --bypass=CN
```

## License

MIT

---

原项目：https://github.com/Leask/socratex  
本仓库：https://github.com/chuansuoyun/socratex
