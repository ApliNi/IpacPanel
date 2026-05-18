
## 配置文件

面板所有配置项都存放在 `./data/config.yml` 中, 可在停机后手动修改, 也可通过网页面板的设置页面进行修改.

当配置出现错误时, 面板会自动将其修正到一个合理值, 具体修正逻辑请见 `./doc/输入约束.md`.

以下为默认配置内容和对应注释:
```yaml
# 配置和数据版本号, 用于更新时迁移配置, 不要修改
data_version: 1
# 网页标题文本
web_title: IpacPanel
# 面板监听地址和端口
listen: 127.0.0.1:25555
# 每个实例的内存历史终端内容存储大小, 单位 KB
history_size: 27
# 开机自启实例的启动间隔, 单位毫秒
auto_start_interval: 200
# 每个实例的重启等待时间, 单位毫秒
auto_restart_interval: 1000
# 每个实例的自动更新目录, 为每个实例的相对路径
instance_update_staging_dir: ./!InstanceUpdate/
# 信任的代理 IP 列表, 用于获取真实 IP
trusted_proxy_ips:
  - 127.0.0.1
# 仪表板页面相关配置
metrics:
  # 启用仪表板页面
  enabled: true
  # 公开仪表板页面, 公开后直接访问仪表板页面的 URL 可以普通用户的权限查看内容
  public_dashboard: false
  # 仪表板数据存储模式, 可选择 memory/sqlite
  storage_mode: memory
  # memory 存储模式下的最长存储时间, 单位分钟, 不推荐超过 30 分钟
  memory_max_min: 30
  # sqlite 存储模式下的最大存储时间, 单位天, 超过该时间的数据会被删除
  sqlite_max_day: 7
  # sqlite 存储模式下的数据压缩时间, 单位天, 超过该时间的数据会被压缩
  sqlite_compact_after_day: 2
# 网页面板相关配置
web:
  # 启用 HTTPS, 需要配置证书
  enable_https: false
  # 强制使用 HTTPS, HTTP 连接将被强制跳转到 HTTPS
  force_https: false
  # 公钥和私钥文件路径
  private_key_path: ./data/cert/key
  public_key_path: ./data/cert/pem
# 登录时使用的工作量证明相关配置
pow:
  # 启用 POW
  enabled: true
  # 生成指定数量的计算任务
  task_count: 24
  # 每个计算任务的难度, 不推荐高于 5
  difficulty: 3
  # 要求在指定时间内完成所有计算, 单位秒
  timestamp_max_skew: 90
# 面板调试模式, 启用可能影响性能
debug: false

```
