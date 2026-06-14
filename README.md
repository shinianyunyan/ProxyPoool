# 代理池管理系统

一个基于Flask的代理池管理系统，支持从多个来源获取代理IP，并提供简单的API接口进行代理管理。

## 功能特点

- 支持从多个API源获取代理IP
- 自动验证代理有效性
- 提供简单的REST API接口
- 支持代理池自动更新
- 支持HTTP和HTTPS代理
- 实时显示代理池状态
- 跨平台支持（Windows/Linux）
- 多线程并发验证
- 自动清理过期代理

## 系统要求

- Python 3.7+
- pip包管理器
- Linux/Windows操作系统

## 安装步骤

1. 克隆项目到本地：
```bash
git clone [项目地址]
cd proxyPool
```

2. 安装依赖包：
```bash
pip install -r requirements.txt
```

3. 配置FOFA API信息：
   - 编辑 `fetchProxy/config.yaml` 文件
   - 填入您的FOFA邮箱和API Key
   - 配置其他参数（如需要）

## 使用方法

1. 启动服务：
```bash
# Linux
python3 app.py

# Windows
python app.py
```

2. 访问API接口：
   - 默认地址：`http://localhost:5010`
   - 支持以下接口：
     - `/` - 获取API信息
     - `/get` - 获取一个有效代理
     - `/all` - 获取所有有效代理
     - `/count` - 获取有效代理数量
     - `/update` - 手动更新代理池
     - `/auto_update` - 手动触发自动更新

3. 更新代理池：
   - 访问 `/update` 接口触发手动更新
   - 访问 `/auto_update` 接口触发自动更新
   - 系统会自动从配置的API源获取新代理
   - 更新状态可通过 `/` 接口查看

## API接口说明

### 获取API信息
- 请求：`GET /`
- 返回：包含所有API接口信息的JSON对象
- 包含下次自动更新时间

### 获取代理
- 请求：`GET /get`
- 参数：
  - `type`：代理类型（可选，默认为空）
- 返回：代理地址（如：`1.2.3.4:8080`）

### 获取所有代理
- 请求：`GET /all`
- 返回：包含所有有效代理的JSON数组
- 格式：`{"code": 200, "msg": "success", "data": ["代理1", "代理2", ...]}`

### 获取代理数量
- 请求：`GET /count`
- 返回：当前有效代理数量
- 格式：`{"code": 200, "msg": "success", "num": 数量}`

### 更新代理池
- 请求：`GET /update`
- 返回：更新状态信息
- 格式：`{"code": 200/400, "msg": "消息", "data": {"last_update_time": "时间"}}`

### 触发自动更新
- 请求：`GET /auto_update`
- 返回：更新状态信息
- 格式：`{"code": 200/400, "msg": "消息", "data": {"last_update_time": "时间"}}`

## 配置文件说明

`fetchProxy/config.yaml` 文件包含以下配置项：

```yaml
fofa:
  email: "您的FOFA邮箱"
  key: "您的FOFA API Key"
  query: "您的FOFA查询语句"
```

## 定时任务

系统包含以下定时任务：
1. 每30分钟验证一次所有代理的有效性
2. 每6小时自动从API更新一次代理池

## 文件说明

- `app.py` - 主程序文件
- `valid_proxies.json` - 有效代理存储文件
- `proxyPool.txt` - 代理池文件
- `api_urls.txt` - API地址文件
- `fetchProxy/` - 代理获取相关文件
  - `config.yaml` - 配置文件
  - `fetch_proxies.py` - 代理获取脚本
  - `update_status.txt` - 更新状态文件

## 注意事项

1. 确保配置文件中的FOFA API信息正确
2. 首次运行时会进行代理验证，可能需要一些时间
3. 建议定期检查日志，确保系统正常运行
4. 在Linux系统上运行时，确保有适当的文件权限
5. 代理验证过程会消耗一定的网络资源，请根据实际情况调整线程数

## 更新日志

### v1.0.0
- 初始版本发布
- 支持基本的代理池管理功能
- 提供REST API接口
- 支持代理池自动更新
