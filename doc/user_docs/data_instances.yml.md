
## 实例配置文件

实例配置文件用于记录面板所有实例的配置, 可在停机后手动修改, 若修改实例名称则要确保同步修改授权文件中引用的实例名称.

```yaml
- name: ping
  group: test
  path: ./instances/
  command: ping 127.0.0.1 -t
  terminal: 3
  input_encoding: utf-8
  output_encoding: utf-8
  auto_start: true
  start_priority: 10
  auto_restart: true
  restart_interval: 1000
  tasks:
    - name: Task-1
      enabled: true
      expr: '*/5 * * * * *'
      action: restart
```
