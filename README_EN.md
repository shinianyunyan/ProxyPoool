# ProxyPool

ProxyPool is a local proxy-pool management distribution built on top of glider. It combines asset discovery, fingerprint-source collection, validation, deduplication, blacklisting, and glider forwarding in one self-contained GUI.

[中文版 README](README.md)

## What It Adds

- **FOFA and Hunter discovery** with independent queries, endpoints, result limits, and local key files. Official and compatible endpoints can be configured.
- **Provider provenance**: duplicate FOFA/Hunter targets are merged while all contributing providers remain visible.
- **Bounded concurrent collection** from manual sources and discovered targets, with per-source timeout, status, duration, and eligible-proxy counts.
- **Cross-source proxy deduplication** using normalized protocol, host, and port, including protocol case, trailing-dot hostnames, and IPv4-mapped IPv6 normalization.
- **Two availability layers**: only upstream `validated=true` proxies are imported, then glider reports its own health state, latency, failures, and last check.
- **Actual forwarder visibility**: after a client request, the GUI shows the proxy glider really selected for that connection.
- **Blacklist controls** for the current forwarder and pool rows, with confirmation for the top-level action.
- **Persistent rolling logs** in `glider.log`, limited to a bounded tail in the UI so long-running sessions do not grow memory without bound.
- **Self-contained Windows packaging**: GUI assets and default sources are embedded; runtime data is stored beside the executable by default.

## Quick Start

Download `glider-gui.exe` from the [Releases](https://github.com/shinianyunyan/ProxyPoool/releases) page:

```powershell
.\\glider-gui.exe
```

Default management address: `http://127.0.0.1:8088`

Start the service in the GUI, then test the SOCKS5 listener:

```powershell
curl.exe -x socks5h://127.0.0.1:8443 http://cip.cc
```

The current-forwarder field stays `—` until a client request causes glider to select a real proxy.

Optional arguments:

```powershell
.\\glider-gui.exe -gui-no-open
.\\glider-gui.exe -gui-address 127.0.0.1:18088
.\\glider-gui.exe -gui-data-dir D:\\ProxyPoolData
```

## Typical Workflow

1. Configure FOFA, Hunter, or both in the Discovery section.
2. Save each provider configuration and search for targets.
3. Review the deduplicated targets and their provider badges.
4. Configure source timeout, bounded concurrency, and SOCKS5/HTTP collection mode.
5. Fetch and apply the validated, deduplicated proxy pool.
6. Inspect glider health, latency, failures, provenance, sorting, pagination, and blacklist state.

An unconfigured provider is not called. Keys remain in local configuration files; environment variables are only compatibility fallbacks.

## Runtime Data and Security

The default data directory contains:

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

Release artifacts do not include real provider keys, proxy lists, blacklists, generated configs, or logs. Do not commit runtime data or publish API keys in documentation, issues, or screenshots.

The management API listens on loopback by default. Add authentication and network isolation before exposing it beyond the local machine.

## Build and Test

Go 1.26 is required:

```powershell
go test ./...
go build -trimpath -ldflags "-s -w" -o dist\\glider-gui.exe
node --check guiweb\\app.js
```

## Authors, History, and Acknowledgements

This repository was created by pulling the upstream source into a separate repository rather than using GitHub's Fork button. GitHub therefore retains the original glider commit history and its contributors. Those historical contributors are not being represented as authors of ProxyPool's new GUI and proxy-pool features.

The `gmeier909/socks5_proxy` repository has no identifiable open-source license in its GitHub metadata. ProxyPool acknowledges its workflow and `/proxies_status` contract as a reference, but does not redistribute its Python source as part of this repository. Obtain permission before directly reusing that source.

Thank you to:

- [nadoo/glider](https://github.com/nadoo/glider) for the forwarding engine, scheduling, and health checks.
- [gmeier909/socks5_proxy](https://github.com/gmeier909/socks5_proxy) for the fingerprint-source collection workflow and proxy-pool API reference.

See [NOTICE](NOTICE) for the attribution and licensing boundary.

## License

ProxyPool remains licensed under the **GNU General Public License v3.0 (GPL-3.0)** inherited from the glider core. See [LICENSE](LICENSE). 

An independent module with no GPL code dependency could receive its own license in the future; the repository as a whole is released under GPL-3.0.
