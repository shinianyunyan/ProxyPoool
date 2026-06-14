import requests
import json
import time
import yaml
import os
import base64
from concurrent.futures import ThreadPoolExecutor, as_completed
from requests.adapters import HTTPAdapter
from urllib3.util.retry import Retry
from threading import Lock
import threading
import re

# 颜色代码
class Colors:
    GREEN = '\033[92m'
    RED = '\033[91m'
    YELLOW = '\033[93m'
    ENDC = '\033[0m'

# 全局计数器
counter = 0
counter_lock = Lock()
total_apis = 0
completed_tasks = set()  # 用于记录已完成的任务

def load_config():
    """加载配置文件"""
    # 获取脚本所在目录的绝对路径
    script_dir = os.path.dirname(os.path.abspath(__file__))
    config_path = os.path.join(script_dir, 'config.yaml')
    
    if not os.path.exists(config_path):
        print(f"{Colors.RED}[-] 配置文件不存在: {config_path}{Colors.ENDC}")
        return None
    
    try:
        with open(config_path, 'r', encoding='utf-8') as f:
            return yaml.safe_load(f)
    except Exception as e:
        print(f"{Colors.RED}[-] 加载配置文件失败: {str(e)}{Colors.ENDC}")
        return None

def reset_counter():
    global counter
    with counter_lock:
        counter = 0

def get_next_counter():
    global counter
    with counter_lock:
        counter += 1
        return counter

def create_session(config):
    session = requests.Session()
    
    # 配置重试策略
    retry_strategy = Retry(
        total=3,
        backoff_factor=1,
        status_forcelist=[500, 502, 503, 504]
    )
    
    adapter = HTTPAdapter(max_retries=retry_strategy)
    session.mount("http://", adapter)
    session.mount("https://", adapter)
    return session

def fetch_fofa_results(config):
    """从FOFA获取结果"""
    email = config['fofa']['email']
    key = config['fofa']['key']
    query = config['fofa_query']
    
    if not email or not key:
        print(f"{Colors.RED}[-] 请在config.yaml中配置FOFA邮箱和API Key{Colors.ENDC}")
        return []
    
    # 构建FOFA API请求
    base64_query = base64.b64encode(query.encode()).decode()
    url = f"https://fofa.info/api/v1/search/all?email={email}&key={key}&qbase64={base64_query}"
    
    try:
        session = create_session(config)
        response = session.get(url)
        if response.status_code == 200:
            data = response.json()
            if data.get('errmsg'):
                print(f"{Colors.RED}[-] FOFA API错误: {data['errmsg']}{Colors.ENDC}")
                return []
            
            results = data.get('results', [])
            hosts = []
            for result in results:
                if len(result) >= 1:
                    host = result[0]
                    if ':' in host:
                        hosts.append(host)
                    else:
                        # 如果没有端口，添加默认端口
                        hosts.append(f"{host}:5010")
            
            print(f"{Colors.GREEN}[+] 从FOFA获取到 {len(hosts)} 个主机{Colors.ENDC}")
            return hosts
    except Exception as e:
        print(f"{Colors.RED}[-] FOFA查询失败: {str(e)}{Colors.ENDC}")
    
    return []

def fetch_proxy_from_api(api_url, config):
    session = create_session(config)
    try:
        response = session.get(api_url, timeout=10, verify=False)
        if response.status_code == 200:
            try:
                data = response.json()
                print(f"{Colors.GREEN}[+] 成功访问: {api_url}{Colors.ENDC}")
                return data
            except json.JSONDecodeError:
                print(f"{Colors.RED}[-] JSON解析错误: {api_url}{Colors.ENDC}")
                return None
    except requests.exceptions.SSLError:
        print(f"{Colors.RED}[-] SSL错误: {api_url}{Colors.ENDC}")
    except requests.exceptions.Timeout:
        print(f"{Colors.RED}[-] 请求超时: {api_url}{Colors.ENDC}")
    except requests.exceptions.ConnectionError:
        print(f"{Colors.RED}[-] 连接错误: {api_url}{Colors.ENDC}")
    except requests.exceptions.ProxyError:
        print(f"{Colors.RED}[-] 代理错误: {api_url}{Colors.ENDC}")
    except Exception as e:
        print(f"{Colors.RED}[-] 其他错误 {api_url}: {str(e)}{Colors.ENDC}")
    return None

def process_proxy_data(data):
    if not data:
        return []
    
    valid_proxies = []
    try:
        if isinstance(data, dict):
            # 处理新的API响应格式
            if data.get('success') is True and isinstance(data.get('proxies'), list):
                for proxy in data['proxies']:
                    # 只处理socks5协议的代理
                    if proxy.get('protocol') == 'socks5':
                        # 处理ip和port分开的情况
                        if 'ip' in proxy and 'port' in proxy:
                            proxy_str = f"{proxy['ip']}:{proxy['port']}"
                            valid_proxies.append(proxy_str)
                            print(f"{Colors.GREEN}[+] 找到有效代理: {proxy_str}{Colors.ENDC}")
                        # 处理ip字段包含ip:port的情况
                        elif 'ip' in proxy and ':' in proxy['ip']:
                            valid_proxies.append(proxy['ip'])
                            print(f"{Colors.GREEN}[+] 找到有效代理: {proxy['ip']}{Colors.ENDC}")
            # 处理旧的API响应格式
            elif data.get('last_status') is True:
                proxy = data.get('proxy')
                if proxy:
                    valid_proxies.append(proxy)
                    print(f"{Colors.GREEN}[+] 找到有效代理: {proxy}{Colors.ENDC}")
            # 处理代理列表
            elif isinstance(data.get('data'), list):
                for item in data['data']:
                    if isinstance(item, str) and re.match(r'^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+$', item):
                        valid_proxies.append(item)
                        print(f"{Colors.GREEN}[+] 找到有效代理: {item}{Colors.ENDC}")
        elif isinstance(data, list):
            for item in data:
                if isinstance(item, str) and re.match(r'^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+$', item):
                    valid_proxies.append(item)
                    print(f"{Colors.GREEN}[+] 找到有效代理: {item}{Colors.ENDC}")
                elif isinstance(item, dict):
                    if item.get('last_status') is True:
                        proxy = item.get('proxy')
                        if proxy:
                            valid_proxies.append(proxy)
                            print(f"{Colors.GREEN}[+] 找到有效代理: {proxy}{Colors.ENDC}")
    except Exception as e:
        print(f"{Colors.RED}[-] 处理数据时出错: {str(e)}{Colors.ENDC}")
    return valid_proxies

def load_existing_proxies(config):
    """加载已存在的代理列表"""
    proxy_pool_path = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), config['paths']['proxy_pool'])
    if not os.path.exists(proxy_pool_path):
        return set()
    
    with open(proxy_pool_path, 'r', encoding='utf-8') as f:
        return set(line.strip() for line in f if line.strip())

def save_proxies(proxies, config):
    """保存代理列表到文件"""
    # 直接使用当前目录
    proxy_pool_path = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'proxyPool.txt')
    
    # 确保所有代理都是IP:端口格式
    valid_proxies = set()
    for proxy in proxies:
        if re.match(r'^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+$', proxy):
            valid_proxies.add(proxy)
    
    with open(proxy_pool_path, 'w', encoding='utf-8') as f:
        for proxy in sorted(valid_proxies):
            f.write(proxy + '\n')

def main():
    global total_apis
    
    # 加载配置
    config = load_config()
    if not config:
        return
    
    # 禁用SSL警告
    import urllib3
    urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)
    
    print("开始获取代理...")
    
    # 从FOFA获取主机列表
    hosts = fetch_fofa_results(config)
    
    # 生成API URLs
    api_urls = [f"http://{host}/proxies_status" for host in hosts]
    
    # 如果存在proxyAPI.txt，也读取其中的URLs
    proxy_api_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), config['paths']['proxy_api'])
    if os.path.exists(proxy_api_path):
        with open(proxy_api_path, 'r', encoding='utf-8') as f:
            api_urls.extend([line.strip() for line in f if line.strip()])
    
    total_apis = len(api_urls)
    print(f"共读取到 {total_apis} 个API地址")
    
    # 保存API地址到api_urls.txt
    api_urls_file = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'api_urls.txt')
    with open(api_urls_file, 'w', encoding='utf-8') as f:
        for url in sorted(api_urls):
            f.write(url + '\n')
    print(f"已保存 {len(api_urls)} 个API地址到 {api_urls_file}")
    
    # 加载已存在的代理
    existing_proxies = load_existing_proxies(config)
    print(f"原有代理池数量: {len(existing_proxies)} 个")
    
    all_valid_proxies = set()
    
    # 使用线程池并发请求
    with ThreadPoolExecutor(max_workers=20) as executor:
        # 提交所有任务
        futures = []
        for url in api_urls:
            futures.append(executor.submit(fetch_proxy_from_api, url, config))
        
        # 使用as_completed来按完成顺序处理结果
        for future in as_completed(futures):
            try:
                result = future.result()
                if result:
                    valid_proxies = process_proxy_data(result)
                    all_valid_proxies.update(valid_proxies)
            except Exception as e:
                print(f"{Colors.RED}[-] 处理结果时出错: {str(e)}{Colors.ENDC}")
    
    # 计算新增代理数量
    new_proxies = all_valid_proxies - existing_proxies
    print(f"\n本次获取的有效代理数量: {len(all_valid_proxies)} 个")
    print(f"其中新增代理数量: {len(new_proxies)} 个")
    
    # 合并新旧代理
    all_proxies = existing_proxies.union(all_valid_proxies)
    
    # 保存结果
    save_proxies(all_proxies, config)
    
    print(f"\n处理完成！")
    print(f"{Colors.GREEN}本次新增代理数量: {len(new_proxies)} 个{Colors.ENDC}")
    print(f"{Colors.GREEN}当前代理池总数: {len(all_proxies)} 个{Colors.ENDC}")
    
    # 更新状态
    with open(os.path.join(os.path.dirname(os.path.abspath(__file__)), 'update_status.txt'), 'w') as f:
        f.write('completed')

if __name__ == '__main__':
    main() 