# ProxyPool

一个面向本地代理池管理的 glider 二开版本。ProxyPool 将资产发现、指纹代理源采集、代理校验、去重、黑名单和 glider 转发服务整合到一个独立的本地 GUI 中。

[English version](README_EN.md)

## 项目定位

ProxyPool 不是通用云代理服务，也不会把你的查询密钥上传到第三方平台。它运行在本机，通过浏览器访问回环地址管理 glider，并把配置、代理池和日志保存在可控的数据目录中。

核心转发能力来自 [nadoo/glider](https://github.com/nadoo/glider)，代理源采集工作流参考了 [gmeier909/socks5_proxy](https://github.com/gmeier909/socks5_proxy)。ProxyPool 的 GUI、FOFA/Hunter 适配、采集调度、运行状态和持久化逻辑由本项目维护。

## 主要创新

- **双引擎资产发现**：FOFA 和 Hunter 可以分别配置，也可以同时调用；支持官方接口和兼容接口，自定义查询语句、接口地址、结果上限和本地密钥。
- **目标合并与来源追踪**：FOFA/Hunter 返回的重复目标只保留一份，同时保留全部提供方标记，避免来源信息丢失。
- **并发指纹源采集**：对手工源和资产发现目标使用有界并发；每个源都有独立超时、成功/失败、耗时和有效代理数量。
- **严格代理去重**：按协议、规范化主机和端口跨源去重，处理协议大小写、域名尾点、IPv4-mapped IPv6 等情况。
- **两层可用性状态**：采集端只接收上游标记为 `validated=true` 的代理；运行后再显示 glider 自身的健康检查、延迟、失败次数和最近检查时间。
- **真实转发节点显示**：客户端实际建立连接后，顶部显示 glider 当前选中的真实代理，而不是仅显示监听地址。
- **可控黑名单**：顶部当前代理和代理池列表均可拉黑/解除拉黑；拉黑代理不会再被应用到 glider 配置。
- **面向长期运行的日志**：GUI 和托管 glider 输出写入 `glider.log`，按 5 MiB 轮转并保留 3 个备份；网页只读取有限尾部，避免日志无限占用内存。
- **独立 Windows 运行包**：发布的 `glider-gui.exe` 内嵌 GUI 资源和默认采集源，不依赖源码目录；运行数据默认写入可执行文件旁的 `data` 目录。

## 快速开始

### Windows GUI

从 [Releases](https://github.com/shinianyunyan/ProxyPoool/releases) 下载 `glider-gui.exe`，然后运行：

```powershell
.\\glider-gui.exe
```

默认管理地址：`http://127.0.0.1:8088`

启动服务后，可以使用 SOCKS5 入口测试：

```powershell
curl.exe -x socks5h://127.0.0.1:8443 http://cip.cc
```

顶部“当前代理”会在客户端真正发起请求并被 glider 选中代理后显示；没有客户端连接时显示 `—` 是正常的。

### GUI 参数

```powershell
.\\glider-gui.exe -gui-no-open
.\\glider-gui.exe -gui-address 127.0.0.1:18088
.\\glider-gui.exe -gui-data-dir D:\\ProxyPoolData
```

源码模式仍可使用：

```powershell
go run . -gui
```

## 使用流程

1. 在 **FOFA** 或 **Hunter** 页面填写查询语句、查询接口、最大结果数和 API Key。
2. 点击保存配置；配置文件路径可通过界面中的 `?` 查看。
3. 点击搜索并保存目标。已配置的提供方会被调用，结果按规范化 URL 降重。
4. 在 **指纹代理源** 页面调整源超时、并发数和 SOCKS5/HTTP 协议。
5. 点击“拉取并应用到代理池”，系统会采集、筛选、去重并重载 glider。
6. 在代理池中查看 glider 健康状态、延迟、失败次数、来源，并按列排序或分页。

未配置 Key 的提供方不会被调用。Key 只保存在本地配置文件中；环境变量仅作为兼容性的备用来源。

## 数据与安全

默认数据目录：

```text
data/
  fofa.json
  hunter.json
  discovery-targets.json
  proxies.json
  blacklist.json
  managed.conf
  runtime-status.json
  glider.log
```

发布包不会包含真实 Key、代理列表、黑名单、生成配置或运行日志。请不要把这些运行时文件提交到 Git，也不要把 API Key 写入 README、Issue 或截图。

管理接口默认只监听回环地址。若要让其他设备访问，请先自行增加认证和网络隔离，不要直接暴露未认证管理 API。

## 构建与测试

需要 Go 1.26：

```powershell
go test ./...
go build -trimpath -ldflags "-s -w" -o dist\\glider-gui.exe
```

前端脚本检查：

```powershell
node --check guiweb\\app.js
```

## 作者、历史与致谢

本仓库不是通过 GitHub Fork 按钮创建的，而是将上游代码拉取后在独立仓库中维护。因此 GitHub 的 Contributors 页面会保留 glider 原始提交历史中的作者，这是历史事实，不代表这些作者参与了 ProxyPool 的新增 GUI 和代理池功能。

ProxyPool 新增代码由本项目维护者提交。`gmeier909` 不会被伪造成当前提交作者；其仓库在 GitHub 上没有声明可识别的开源许可证，因此本项目只在致谢中说明参考关系，没有把其 Python 文件直接作为本项目源码分发。如需直接复用该仓库的源码，应先取得原作者许可。

感谢以下项目：

- [nadoo/glider](https://github.com/nadoo/glider)：代理转发、负载策略和健康检查核心。
- [gmeier909/socks5_proxy](https://github.com/gmeier909/socks5_proxy)：代理池指纹源采集流程和 `/proxies_status` 数据约定的参考。

更多边界说明见 [NOTICE](NOTICE)。

## 许可证

本项目沿用 glider 的 **GNU General Public License v3.0 (GPL-3.0)**，许可证全文见 [LICENSE](LICENSE)。当前许可证不是 MIT；由于项目包含 GPL-3.0 的 glider 核心，不能仅凭新增 GUI 就把整体改成 MIT。

如果未来单独抽取完全独立、没有 GPL 代码依赖的新模块，可以再为该模块单独选择许可证；当前仓库整体按 GPL-3.0 发布。
