from flask import Flask, jsonify, request
import random
import requests
import os
import json
import time
import subprocess
from threading import Lock, Thread
import re
import yaml
from multiprocessing import Pool, Manager
import schedule
import datetime
from concurrent.futures import ThreadPoolExecutor
from queue import Queue, Empty
import urllib3
from pathlib import Path
import sys

# 禁用HTTPS警告
urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

app = Flask(__name__)

# 全局变量
proxy_pool = set()
proxy_lock = Lock()
update_status = "completed"
last_update_time = 0
api_urls = set()
valid_proxies = {}
valid_proxies_lock = Lock()
proxy_check_queue = Queue()
proxy_check_threads = []
THREAD_COUNT = 100
PROXY_EXPIRE_TIME = 300

# 获取项目根目录
BASE_DIR = Path(__file__).resolve().parent

# 修改文件路径定义
VALID_PROXIES_FILE = str(BASE_DIR / 'valid_proxies.json')
PROXY_POOL_FILE = str(BASE_DIR / 'proxyPool.txt')
API_URLS_FILE = str(BASE_DIR / 'api_urls.txt')
CONFIG_FILE = str(BASE_DIR / 'fetchProxy' / 'config.yaml')
STATUS_FILE = str(BASE_DIR / 'fetchProxy' / 'update_status.txt')

# 在全局变量部分添加文件锁
file_lock = Lock()

# 在文件开头添加颜色定义
class Colors:
    GREEN = '\033[92m'
    RED = '\033[91m'
    YELLOW = '\033[93m'
    BLUE = '\033[94m'
    ENDC = '\033[0m'
    BOLD = '\033[1m'

def load_valid_proxies():
    """加载验证过的代理"""
    try:
        if os.path.exists(VALID_PROXIES_FILE):
            with open(VALID_PROXIES_FILE, 'r', encoding='utf-8') as f:
                data = json.load(f)
                current_time = time.time()
                # 加载时也进行过期检查
                valid_data = {
                    proxy: (is_valid, float(check_time))
                    for proxy, (is_valid, check_time) in data.items()
                    if current_time - float(check_time) <= PROXY_EXPIRE_TIME
                }
                valid_proxies.update(valid_data)
            print(f"从文件加载了 {len(valid_proxies)} 个验证过的代理")
    except Exception as e:
        print(f"加载验证结果文件出错: {str(e)}")

def save_valid_proxies():
    """保存验证过的代理到文件"""
    try:
        with valid_proxies_lock:
            current_time = time.time()
            # 清理过期代理
            expired_proxies = [
                proxy for proxy, (_, check_time) in valid_proxies.items()
                if current_time - check_time > PROXY_EXPIRE_TIME
            ]
            for proxy in expired_proxies:
                del valid_proxies[proxy]
            
            # 只保存有效的代理
            valid_data = {
                proxy: (is_valid, str(check_time))
                for proxy, (is_valid, check_time) in valid_proxies.items()
                if is_valid
            }
            
            # 确保目录存在
            os.makedirs(os.path.dirname(VALID_PROXIES_FILE), exist_ok=True)
            
            # 使用临时文件进行保存
            temp_file = VALID_PROXIES_FILE + '.tmp'
            with open(temp_file, 'w', encoding='utf-8') as f:
                json.dump(valid_data, f, ensure_ascii=False, indent=2)
            
            # 原子替换文件
            if os.path.exists(VALID_PROXIES_FILE):
                os.remove(VALID_PROXIES_FILE)
            os.rename(temp_file, VALID_PROXIES_FILE)
            
            print(f"已保存 {len(valid_data)} 个有效代理到文件")
    except Exception as e:
        print(f"保存验证结果文件出错: {str(e)}")
        if os.path.exists(temp_file):
            os.remove(temp_file)

def clean_expired_proxies():
    """定期清理过期代理"""
    try:
        with valid_proxies_lock:
            current_time = time.time()
            expired_proxies = [
                proxy for proxy, (_, check_time) in valid_proxies.items()
                if current_time - check_time > PROXY_EXPIRE_TIME
            ]
            for proxy in expired_proxies:
                del valid_proxies[proxy]
            if expired_proxies:
                print(f"清理了 {len(expired_proxies)} 个过期代理")
                save_valid_proxies()  # 保存清理后的结果
    except Exception as e:
        print(f"清理过期代理出错: {str(e)}")

def load_proxies():
    """加载代理池"""
    global proxy_pool
    if os.path.exists(PROXY_POOL_FILE):
        with open(PROXY_POOL_FILE, 'r', encoding='utf-8') as f:
            proxy_pool = set(line.strip() for line in f if line.strip())
    else:
        proxy_pool = set()
        print(f"代理文件不存在: {PROXY_POOL_FILE}")

def initialize_proxy_pool():
    """初始化代理池（确保只启动一次线程）"""
    global proxy_check_threads
    if not proxy_check_threads:  # 仅当线程未启动时初始化
        start_proxy_check_threads()
        check_all_proxies()  # 首次验证

def load_api_urls():
    """加载已保存的API地址"""
    global api_urls
    if os.path.exists(API_URLS_FILE):
        with open(API_URLS_FILE, 'r', encoding='utf-8') as f:
            api_urls = set(line.strip() for line in f if line.strip())
    else:
        api_urls = set()
        print(f"API地址文件不存在: {API_URLS_FILE}")

def save_api_urls():
    """保存API地址到文件"""
    with open(API_URLS_FILE, 'w', encoding='utf-8') as f:
        for url in sorted(api_urls):
            f.write(url + '\n')

def test_proxy(proxy):
    """测试代理是否有效"""
    try:
        proxy_dict = {
            "http": f"socks5://{proxy}",
            "https": f"socks5://{proxy}"
        }
        
        # 尝试多个测试URL
        test_urls = [
            "http://www.baidu.com",
            "http://httpbin.org/ip",
            "http://cip.cc"
        ]
        
        headers = {
            "User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
        }
        
        # 减少超时时间
        timeout = 5
        
        for url in test_urls:
            try:
                response = requests.get(url, proxies=proxy_dict, timeout=timeout, 
                                      headers=headers, verify=False)
                if response.status_code == 200:
                    print(f"{Colors.GREEN}[+] 有效代理: {proxy} (通过 {url}){Colors.ENDC}")
                    return True
            except Exception as e:
                # 继续尝试下一个URL
                continue
        
        # 如果所有URL都失败，尝试使用socket直接连接
        try:
            import socket
            import socks
            
            # 解析代理地址
            ip, port = proxy.split(':')
            port = int(port)
            
            # 创建SOCKS5代理
            s = socks.socksocket()
            s.set_proxy(socks.SOCKS5, ip, port)
            s.settimeout(timeout)
            
            # 尝试连接到百度
            s.connect(("www.baidu.com", 80))
            s.close()
            
            print(f"{Colors.GREEN}[+] 有效代理: {proxy} (通过socket直接连接){Colors.ENDC}")
            return True
        except Exception as e:
            print(f"{Colors.RED}[-] 代理无效 {proxy} - 所有测试方法都失败{Colors.ENDC}")
            return False
            
    except requests.exceptions.Timeout:
        print(f"{Colors.RED}[-] 代理超时 {proxy}{Colors.ENDC}")
        return False
    except Exception as e:
        print(f"{Colors.RED}[-] 代理异常 {proxy} - {str(e)}{Colors.ENDC}")
        return False

def save_valid_proxies_immediately():
    """实时保存有效代理（修复版）"""
    try:
        with valid_proxies_lock:
            current_time = time.time()
            valid_data = {
                proxy: (is_valid, check_time)
                for proxy, (is_valid, check_time) in valid_proxies.items()
                if is_valid and (current_time - check_time) < PROXY_EXPIRE_TIME
            }
            
            # 确保目录存在
            os.makedirs(os.path.dirname(VALID_PROXIES_FILE), exist_ok=True)
            
            temp_file = VALID_PROXIES_FILE + '.tmp'
            # 使用utf-8编码写入
            with open(temp_file, 'w', encoding='utf-8') as f:
                json.dump(
                    {k: (v[0], str(v[1])) for k, v in valid_data.items()},
                    f,
                    ensure_ascii=False,
                    indent=2
                )
            
            # 原子替换文件
            if os.path.exists(VALID_PROXIES_FILE):
                os.replace(temp_file, VALID_PROXIES_FILE)
            else:
                os.rename(temp_file, VALID_PROXIES_FILE)
            print(f"{Colors.GREEN}成功保存 {len(valid_data)} 个代理到文件{Colors.ENDC}")
                
    except Exception as e:
        print(f"{Colors.RED}保存失败: {str(e)}{Colors.ENDC}")
        # 打印堆栈信息以便调试
        import traceback
        traceback.print_exc()
        # 清理临时文件
        if os.path.exists(temp_file):
            os.remove(temp_file)

def proxy_check_worker():
    """验证线程工作函数"""
    while True:
        try:
            proxy = proxy_check_queue.get(timeout=30)
            if proxy is None:  # 收到停止信号
                proxy_check_queue.task_done()
                break
                
            # 测试代理
            is_valid = test_proxy(proxy)
            
            # 使用全局文件锁
            with file_lock:
                # 确保文件存在
                if not os.path.exists(VALID_PROXIES_FILE):
                    with open(VALID_PROXIES_FILE, 'w', encoding='utf-8') as f:
                        json.dump([], f)
                
                # 读取当前有效代理
                try:
                    with open(VALID_PROXIES_FILE, 'r', encoding='utf-8') as f:
                        valid_proxies = set(json.load(f))
                except (json.JSONDecodeError, Exception) as e:
                    print(f"读取有效代理文件出错: {str(e)}")
                    valid_proxies = set()
                
                # 更新代理集合
                if is_valid:
                    valid_proxies.add(proxy)
                elif proxy in valid_proxies:
                    valid_proxies.remove(proxy)
                
                # 写入文件
                try:
                    # 使用临时文件
                    temp_file = VALID_PROXIES_FILE + '.tmp'
                    with open(temp_file, 'w', encoding='utf-8') as f:
                        json.dump(list(valid_proxies), f, ensure_ascii=False)
                    
                    # 原子替换
                    if os.path.exists(VALID_PROXIES_FILE):
                        os.remove(VALID_PROXIES_FILE)
                    os.rename(temp_file, VALID_PROXIES_FILE)
                except Exception as e:
                    print(f"写入有效代理文件出错: {str(e)}")
                    if os.path.exists(temp_file):
                        os.remove(temp_file)
            
            proxy_check_queue.task_done()
            break  # 验证完成后销毁进程
            
        except Empty:
            break
        except Exception as e:
            print(f"验证线程出错: {str(e)}")
            proxy_check_queue.task_done()
            break

def start_proxy_check_threads():
    """启动代理验证线程"""
    global proxy_check_threads
    # 先清理旧的线程
    if proxy_check_threads:
        for _ in range(len(proxy_check_threads)):
            proxy_check_queue.put(None)
        for t in proxy_check_threads:
            t.join(timeout=5)
    
    # 启动新的线程
    proxy_check_threads = []
    for _ in range(THREAD_COUNT):
        thread = Thread(target=proxy_check_worker, daemon=True)
        thread.start()
        proxy_check_threads.append(thread)
    print(f"启动了 {THREAD_COUNT} 个验证线程")

def check_all_proxies():
    """验证所有代理"""
    global update_status
    
    if update_status == "running":
        return
        
    try:
        update_status = "running"
        print(f"开始验证 {datetime.datetime.now()}")
        
        # 将代理放入队列
        with proxy_lock:
            proxies = list(proxy_pool)
        
        print(f"待验证代理总数: {len(proxies)}")
        
        # 为每个代理创建新的验证线程
        threads = []
        for proxy in proxies:
            thread = Thread(target=proxy_check_worker)
            thread.start()
            threads.append(thread)
            proxy_check_queue.put(proxy)
        
        # 等待所有验证完成
        for thread in threads:
            thread.join(timeout=30)  # 设置超时时间
        
        print(f"所有代理验证完成 {datetime.datetime.now()}")
        update_status = "completed"
        
    except Exception as e:
        print(f"验证过程出错: {str(e)}")
        update_status = "completed"

def save_valid_proxies_to_file():
    """保存有效代理到文件"""
    try:
        with valid_proxies_lock:
            # 获取所有有效代理
            valid_proxy_list = [
                proxy for proxy, (is_valid, _) in valid_proxies.items() 
                if is_valid
            ]
            
            if not valid_proxy_list:
                print(f"{Colors.YELLOW}没有有效代理可保存{Colors.ENDC}")
                return
            
            # 确保目录存在
            os.makedirs(os.path.dirname(VALID_PROXIES_FILE), exist_ok=True)
            
            # 保存到文件
            with open(VALID_PROXIES_FILE, 'w', encoding='utf-8') as f:
                for proxy in valid_proxy_list:
                    f.write(proxy + '\n')
            
            print(f"{Colors.GREEN}已保存 {len(valid_proxy_list)} 个有效代理到文件 {VALID_PROXIES_FILE}{Colors.ENDC}")
    except Exception as e:
        print(f"{Colors.RED}保存有效代理文件出错: {str(e)}{Colors.ENDC}")

def get_valid_proxy(proxy_type=''):
    """获取一个有效代理"""
    try:
        if os.path.exists(VALID_PROXIES_FILE) and os.path.getsize(VALID_PROXIES_FILE) > 0:
            with open(VALID_PROXIES_FILE, 'r', encoding='utf-8') as f:
                data = f.read().strip()
                if data:  # 确保文件内容不为空
                    valid_proxies = json.loads(data)
                    if valid_proxies:
                        return random.choice(valid_proxies)
    except (json.JSONDecodeError, Exception) as e:
        print(f"获取有效代理出错: {str(e)}")
    return None

def fetch_proxies_from_api(api_url):
    """从API获取代理"""
    try:
        response = requests.get(api_url, timeout=10, verify=False)
        if response.status_code == 200:
            try:
                data = response.json()
                print(f"成功访问API: {api_url}")
                return data
            except json.JSONDecodeError:
                print(f"JSON解析错误: {api_url}")
                return None
    except Exception as e:
        print(f"访问API出错 {api_url}: {str(e)}")
    return None

def process_proxy_data(data):
    """处理代理数据"""
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
                            print(f"找到有效代理: {proxy_str}")
                        # 处理ip字段包含ip:port的情况
                        elif 'ip' in proxy and ':' in proxy['ip']:
                            valid_proxies.append(proxy['ip'])
                            print(f"找到有效代理: {proxy['ip']}")
            # 处理旧的API响应格式
            elif data.get('last_status') is True:
                proxy = data.get('proxy')
                if proxy:
                    valid_proxies.append(proxy)
                    print(f"找到有效代理: {proxy}")
            # 处理代理列表
            elif isinstance(data.get('data'), list):
                for item in data['data']:
                    if isinstance(item, str) and re.match(r'^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+$', item):
                        valid_proxies.append(item)
                        print(f"找到有效代理: {item}")
        elif isinstance(data, list):
            for item in data:
                if isinstance(item, str) and re.match(r'^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+$', item):
                    valid_proxies.append(item)
                    print(f"找到有效代理: {item}")
                elif isinstance(item, dict):
                    if item.get('last_status') is True:
                        proxy = item.get('proxy')
                        if proxy:
                            valid_proxies.append(proxy)
                            print(f"找到有效代理: {proxy}")
    except Exception as e:
        print(f"处理数据时出错: {str(e)}")
    return valid_proxies

def save_proxies(proxies):
    """保存代理到文件"""
    with open(PROXY_POOL_FILE, 'w', encoding='utf-8') as f:
        for proxy in sorted(proxies):
            f.write(proxy + '\n')
    print(f"保存了 {len(proxies)} 个代理到文件")

def auto_update_from_apis():
    """从已保存的API地址自动更新代理池"""
    global update_status
    
    if update_status == "running":
        print("更新正在进行中，跳过自动更新")
        return
    
    print("开始自动从API更新代理池...")
    update_status = "running"
    
    # 加载已保存的API地址
    load_api_urls()
    
    if not api_urls:
        print("没有可用的API地址")
        update_status = "completed"
        return
    
    print(f"从 {len(api_urls)} 个API地址获取代理")
    
    # 获取所有代理
    all_proxies = set()
    
    # 使用线程池并发请求
    with Pool(processes=min(20, len(api_urls))) as pool:
        results = pool.map(fetch_proxies_from_api, api_urls)
        
        for result in results:
            if result:
                valid_proxies = process_proxy_data(result)
                all_proxies.update(valid_proxies)
    
    # 保存新的代理到文件
    save_proxies(all_proxies)
    
    print(f"自动更新完成，新增 {len(all_proxies)} 个代理")
    update_status = "completed"

def update_proxy_pool():
    """更新代理池（手动调用FOFA获取API地址）"""
    global update_status, last_update_time, api_urls
    
    # 检查是否正在更新
    if update_status == "running":
        return {
            "status": "error",
            "message": "更新正在进行中",
            "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
        }
    
    # 检查配置文件
    if not os.path.exists(CONFIG_FILE):
        return {
            "status": "error",
            "message": "配置文件不存在",
            "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
        }
    
    try:
        with open(CONFIG_FILE, 'r', encoding='utf-8') as f:
            config = yaml.safe_load(f)
            if not config:
                return {
                    "status": "error",
                    "message": "配置文件为空",
                    "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
                }
            
            # 检查FOFA配置
            fofa_config = config.get('fofa', {})
            if not fofa_config:
                return {
                    "status": "error",
                    "message": "配置文件中缺少FOFA配置",
                    "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
                }
            
            # 检查邮箱和API Key
            email = fofa_config.get('email', '').strip()
            key = fofa_config.get('key', '').strip()
            
            if not email:
                return {
                    "status": "error",
                    "message": "FOFA邮箱未配置",
                    "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
                }
            
            if not key:
                return {
                    "status": "error",
                    "message": "FOFA API Key未配置",
                    "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
                }
            
            # 检查查询语句
            fofa_query = config.get('fofa_query', '').strip()
            if not fofa_query:
                return {
                    "status": "error",
                    "message": "FOFA查询语句未配置",
                    "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
                }
    except Exception as e:
        return {
            "status": "error",
            "message": f"配置文件读取失败: {str(e)}",
            "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
        }
    
    # 获取当前代理池中的代理数量
    current_proxy_count = 0
    if os.path.exists(PROXY_POOL_FILE):
        with open(PROXY_POOL_FILE, 'r', encoding='utf-8') as f:
            current_proxy_count = len([line.strip() for line in f if line.strip()])
    
    # 启动更新进程
    update_status = "running"
    last_update_time = time.time()
    
    # 获取Python解释器路径
    python_path = sys.executable
    if not python_path:
        python_path = 'python3'  # 如果无法获取解释器路径，使用python3命令
    
    # 在后台运行更新脚本
    script_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'fetchProxy', 'fetch_proxies.py')
    subprocess.Popen([python_path, script_path])
    
    return {
        "status": "success",
        "message": f"更新已启动，当前代理池数量: {current_proxy_count}",
        "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)),
        "current_proxy_count": current_proxy_count
    }

def check_update_status():
    """检查更新状态，不触发更新"""
    global update_status, last_update_time
    
    # 检查更新状态文件
    if os.path.exists(STATUS_FILE):
        try:
            with open(STATUS_FILE, 'r') as f:
                status = f.read().strip()
                if status == 'completed':
                    update_status = 'completed'
                    # 重新加载代理池
                    load_proxies()
                    # 加载API地址
                    load_api_urls()
        except:
            pass
    
    # 检查配置文件
    if not os.path.exists(CONFIG_FILE):
        return {
            "status": "error",
            "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
        }
    
    try:
        with open(CONFIG_FILE, 'r', encoding='utf-8') as f:
            config = yaml.safe_load(f)
            if not config.get('fofa', {}).get('email') or not config.get('fofa', {}).get('key'):
                return {
                    "status": "error",
                    "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
                }
    except:
        return {
            "status": "error",
            "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
        }
    
    return {
        "status": update_status,
        "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
    }

def run_scheduler():
    """运行定时任务"""
    while True:
        schedule.run_pending()
        time.sleep(60)  # 每分钟检查一次

@app.route('/')
def index():
    """返回API信息"""
    # 获取当前更新状态，不触发更新
    status_info = check_update_status()
    
    # 计算下次更新时间
    next_update = None
    if last_update_time > 0:
        next_update = datetime.datetime.fromtimestamp(last_update_time) + datetime.timedelta(hours=6)
    
    api_info = {
        "url": [
            {
                "desc": "get a proxy",
                "url": "/get"
            },
            {
                "desc": "get all proxy from proxy pool",
                "url": "/all"
            },
            {
                "desc": "return proxy count",
                "url": "/count"
            },
            {
                "desc": "update proxy pool (manual FOFA search)",
                "url": "/update",
                "status": status_info["status"],
                "last_update_time": status_info["last_update_time"]
            },
            {
                "desc": "auto update from saved APIs",
                "url": "/auto_update",
                "next_update": next_update.strftime("%Y-%m-%d %H:%M:%S") if next_update else "Not scheduled"
            }
        ]
    }
    return jsonify(api_info)

@app.route('/get')
def get_proxy():
    """获取一个代理"""
    proxy_type = request.args.get('type', '')
    proxy = get_valid_proxy(proxy_type)
    
    if proxy:
        return proxy
    else:
        return "no valid proxy found", 404

@app.route('/all')
def get_all_proxies():
    """获取所有有效代理"""
    try:
        if os.path.exists(VALID_PROXIES_FILE) and os.path.getsize(VALID_PROXIES_FILE) > 0:
            with open(VALID_PROXIES_FILE, 'r', encoding='utf-8') as f:
                data = f.read().strip()
                if data:  # 确保文件内容不为空
                    valid_proxies = json.loads(data)
                    return jsonify({
                        "code": 200,
                        "msg": "success",
                        "data": valid_proxies
                    })
        return jsonify({
            "code": 200,
            "msg": "success",
            "data": []
        })
    except (json.JSONDecodeError, Exception) as e:
        return jsonify({
            "code": 500,
            "msg": str(e),
            "data": []
        })

@app.route('/count')
def get_proxy_count():
    """获取有效代理数量"""
    try:
        if os.path.exists(VALID_PROXIES_FILE) and os.path.getsize(VALID_PROXIES_FILE) > 0:
            with open(VALID_PROXIES_FILE, 'r', encoding='utf-8') as f:
                data = f.read().strip()
                if data:  # 确保文件内容不为空
                    valid_proxies = json.loads(data)
                    return jsonify({
                        "code": 200,
                        "msg": "success",
                        "num": len(valid_proxies)
                    })
        return jsonify({
            "code": 200,
            "msg": "success",
            "num": 0
        })
    except (json.JSONDecodeError, Exception) as e:
        return jsonify({
            "code": 500,
            "msg": str(e),
            "num": 0
        })

@app.route('/update')
def update_proxies():
    """更新代理池（手动调用FOFA获取API地址）"""
    result = update_proxy_pool()
    return jsonify({
        "code": 400 if result["status"] == "error" else 200,
        "msg": result["message"],
        "data": {
            "last_update_time": result["last_update_time"]
        }
    })

@app.route('/auto_update')
def trigger_auto_update():
    """手动触发从API自动更新"""
    if update_status == "running":
        return jsonify({
            "code": 400,
            "msg": "更新正在进行中",
            "data": {
                "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(last_update_time)) if last_update_time > 0 else None
            }
        })
    
    # 启动自动更新
    Thread(target=auto_update_from_apis).start()
    
    return jsonify({
        "code": 200,
        "msg": "自动更新已启动",
        "data": {
            "last_update_time": time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(time.time()))
        }
    })

def main():
    # 加载数据
    load_proxies()
    load_api_urls()
    
    # 启动Flask线程
    flask_thread = Thread(target=lambda: app.run(host='0.0.0.0', port=5010), daemon=True)
    flask_thread.start()
    time.sleep(2)  # 等待Flask启动
    
    # 立即执行第一次验证
    print("开始首次验证...")
    check_all_proxies()
    
    # 定时任务配置
    schedule.every(30).minutes.do(check_all_proxies)  # 每30分钟验证一次所有代理
    schedule.every(6).hours.do(auto_update_from_apis)  # 每6小时从API更新一次代理池
    
    # 主循环
    try:
        while True:
            schedule.run_pending()
            time.sleep(1)
    except KeyboardInterrupt:
        print("服务关闭中...")

if __name__ == '__main__':
    # 禁用SSL警告
    urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)
    
    # 启动应用
    main() 