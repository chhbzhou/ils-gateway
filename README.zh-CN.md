# iLS Gateway

[English](README.md) | 简体中文

iLS Gateway 是面向自有或明确授权局域网设备的实验性 OpenWrt 网关。数据面包含：

- 基于 Linux `SO_ORIGINAL_DST` 的透明 TCP 接入路由，支持 IPv6；
- 有界 ClientHello 缓冲、SNI/端口策略，以及使用预签叶子证书的 TLS MITM；
- 对非目标、畸形、超限或无 SNI 的 TLS 连接进行逐字节原样旁路；
- HTTPS/WLoc 响应改写、SOCKS5 上游传输和资源限制；
- 通过 `locspoof-ca` 以 root 权限生成并管理 CA/叶子证书；
- fw4 nft 集合、本机 DNS 目标刷新与原子 nft 替换、procd fail-open watchdog 和 LuCI UCI 配置界面。

软件包只处理编译时限定的 WLoc 主机，并且只能用于操作者拥有或明确获得授权的网络和设备。

WLoc 目标地址通过路由器本机 DNS 监听地址 `127.0.0.1` 解析，并原子替换 fw4 目标集合。软件包不安装临时 dnsmasq include，也不依赖 dnsmasq nftset 副作用。

透明代理固定监听 IPv4 `0.0.0.0:10443` 和对应的 IPv6 地址；局域网 CA 安装入口固定监听 `0.0.0.0:10445`。由于 fw4 规则是静态的，这些端口不能通过 UCI 或 LuCI 修改。

LuCI 界面支持简体中文和英文。界面优先采用 LuCI 中配置的语言；语言设置为自动时，跟随浏览器首选语言。

## 构建与测试

```powershell
gofmt -w cmd internal
go test -count=1 ./...
go vet ./...
go test -race ./...
sh tools/test-locspoofctl.sh
sh tools/test-locspoof-watchdog.sh
sh tools/test-init-static.sh
# 在 Linux root 环境中验证真实 UID/GID PKI 权限边界。
sh tools/test-pki-permissions.sh
.\tools\build.ps1 -Arch amd64
.\tools\build.ps1 -Arch arm64
```

两个 fuzz target 每次至少运行 30 秒。Race 测试需要可用的 C 编译器。OpenWrt 软件包使用官方 `feeds/packages/lang/golang/golang-package.mk` 接口，必须在 Linux OpenWrt SDK 环境中构建；Windows 无法运行 SDK 内的 ELF 工具。

`max_buffered_bytes` 是按请求管理的缓冲预算：处理响应前，daemon 会为压缩正文、解压正文、改写正文和重新压缩正文的最坏情况预留空间。它不是进程总 RSS 上限，也不限制注入式改写回调自行产生的分配。

在带有 Docker 的 Linux 主机上构建 x86_64 OpenWrt IPK：

```bash
bash tools/build-openwrt-ipk.sh
```

脚本把可复用 SDK 和输出保存在 Git 工作区之外的 `${XDG_CACHE_HOME:-$HOME/.cache}/ils-gateway-build`。可通过 `ILS_BUILD_HOME` 指定其他持久目录。

脚本会验证官方 SDK 归档的校验和，并检出固定版本的 OpenWrt、packages 和 LuCI。如果旧缓存使用了不同的 feed revision，请换用新的构建目录，或者设置 `ILS_REBUILD_SDK=1` 执行一次重建。

## 安装与操作

支持目标、IPK 安装、首次配置、证书安装、升级和卸载步骤见[安装文档](docs/INSTALLATION.md)。新安装会以 `iLS Gateway` 作为配置描述文件签名者。升级时会保留 v41 及更早版本创建的有效签名身份，避免轮换已经部署的信任材料。

## CA 生命周期

`locspoof-ca` 以原子方式创建持久 CA 和固定主机叶子证书。CA 私钥权限为 `root:root`、`0600`；`locspoofd` 只能读取 CA 证书和叶子证书包。局域网安装端口 10445 提供 `/ca.pem` 和 `/fingerprint`。卸载软件包不会自动删除手机上的 CA，必须先从每台授权设备中移除描述文件。

## OpenWrt 生命周期

服务启动前清空 nft 设备集合，等待 control/ready marker 后再应用设备集合。启动失败、叶子证书缺失、daemon 异常、watchdog 健康检查失败、停止、reload 或 disable 都会清空集合。`bypass_on` 创建由 root watchdog 消费的 marker；`bypass_off` 重新应用集合。

通过软件包管理器安装或升级后，先配置 `main` 和 `profile` UCI section，再启用服务。卸载前停止并禁用服务；软件包 stop hook 会清理 IPv4、IPv6、MAC 和临时 nft 状态。轮换或卸载前，应先从每台设备中移除 CA 信任描述文件。

设备必须能被路由器 LAN bridge（默认 `br-lan`）直接看到，nftables 才能匹配设备 MAC 和当前地址。下级 NAT/路由器会隐藏手机 MAC，不能安全地自动启用。请配置明确的 MAC 和经过验证的 IP 限制；服务会拒绝非法或不完整的 profile。

选择器采用 OR 匹配：MAC 规则匹配以太网帧，IP 规则匹配数据包。因此同时配置 MAC 和 IP 表示增加一组匹配条件，而不是联合身份断言。`lan_interface` 列表默认包含 `br-lan`，加载 nft 接口集合前会先进行校验。

## 外部验证

本仓库不声明已经验证 iOS 信任、WLoc 兼容性或 Core Location 消费。必须使用真实授权的 iPhone/iPad 和 OpenWrt 路由器，分别记录 `response_modified`、`core_location_changed` 和 `app_consumed`。证据清单见[验证文档](docs/VALIDATION.md)。

## 隐私与安全

服务不包含遥测。运行策略、证书和成功改写活动都保存在路由器本地。只有操作者点击海拔补全按钮时，才会把输入的经纬度发送给 Open-Meteo Elevation API。启用拦截前请阅读[隐私说明](docs/PRIVACY.md)和[安全策略](SECURITY.md)。

## 贡献与许可证

贡献方式见 [CONTRIBUTING.md](CONTRIBUTING.md)。项目原创代码采用 MIT License。随包浏览器资源保留各自的上游许可证，详见[第三方声明](THIRD_PARTY_NOTICES.md)。
