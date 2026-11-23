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
sudo ./socratex --domain=example.com
```

### 方式二：一行命令（推荐）

```bash
# 编译后放到 PATH，或直接：
sudo go run . --domain=example.com
```

### 方式三：Docker（可选）

```bash
# 后续可自行构建镜像
docker build -t socratex .
docker run -d --restart=always \
  -p 80:80 -p 443:443 \
  -v ~/.socratex.json:/root/.socratex.json \
  -v ~/.cache/socratex-certs:/root/.cache/socratex-certs \
  socratex --domain=example.com
```

### 启动成功后的输出示例

```
[SOCRATEX v1.0.0] https://github.com/chuansuoyun/socratex

* Token authentication:
  - PAC:  https://example.com/proxy.pac?token=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
  - WPAD: https://example.com/wpad.dat?token=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
  - Log:  https://example.com/console?token=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx

* Basic authentication:   (如果指定了 --user 和 --password)
  - PAC:   https://user:pass@example.com/proxy.pac
  - WPAD:  https://user:pass@example.com/wpad.dat
  - Log:   https://user:pass@example.com/console
  - Proxy: https://user:pass@example.com
```

把 **PAC 地址** 复制到系统的「自动代理配置」里即可：

- **macOS**：系统设置 → 网络 → 详细信息 → 代理 → 自动代理配置
- **Windows**：设置 → 网络和 Internet → 代理 → 使用设置脚本
- **Android**：Wi-Fi 高级设置 → 代理 → 自动配置

## 命令行参数

| 参数        | 说明                                      | 默认值   |
|-------------|-------------------------------------------|----------|
| `--domain`  | 部署使用的域名（HTTPS 模式必填）          | -        |
| `--http`    | 仅使用 HTTP 模式（测试用，不推荐生产）    | false    |
| `--user`    | Basic Auth 用户名                         | -        |
| `--password`| Basic Auth 密码                           | -        |
| `--token`   | Token 认证字符串（不指定则自动生成）      | 自动生成 |
| `--port`    | 监听端口                                  | 443 / 80 |
| `--address` | 监听地址                                  | 全部接口 |
| `--version` | 显示版本                                  | -        |

示例：

```bash
# 最简单
sudo ./socratex --domain=proxy.example.com

# 同时开启 Basic Auth
sudo ./socratex --domain=proxy.example.com --user=alice --password=secret

# 指定固定 token
sudo ./socratex --domain=proxy.example.com --token=my-very-long-secret-token
```

配置会自动保存在 `~/.socratex.json`，下次启动会复用 token 和账号密码。

## 配置文件

路径：`~/.socratex.json`

```json
{
  "domain": "example.com",
  "token": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "user": "alice",
  "password": "secret"
}
```

证书缓存目录：`~/.cache/socratex-certs/`

## 与原版对比

| 功能              | 原版 (Node.js) | 本项目 (Go) |
|-------------------|----------------|-------------|
| HTTPS 真实代理    | ✅             | ✅          |
| 自动证书 (ACME)   | ✅             | ✅          |
| PAC / WPAD        | ✅             | ✅          |
| Token Auth        | ✅             | ✅          |
| Basic Auth        | ✅             | ✅          |
| GeoIP Bypass      | ✅             | 计划中      |
| 可编程 upstream   | ✅             | 计划中      |
| 单二进制部署      | ❌             | ✅          |
| 内存占用          | 较高           | 极低        |

## 注意事项

1. **必须用 root 运行**（或使用 `setcap` 赋予绑定特权端口的能力），因为要监听 80/443。
2. 域名必须正确解析到服务器，否则 ACME 证书申请会失败。
3. iOS 目前不支持 Secure Web Proxy，macOS / Windows / Android / Linux 可用。
4. 建议在境外服务器部署。

## 开发

```bash
go mod tidy
go build -o socratex .
./socratex --http --domain=127.0.0.1   # 本地测试（HTTP only）
```

## License

MIT

---

原项目：https://github.com/Leask/socratex  
本仓库：https://github.com/chuansuoyun/socratex
