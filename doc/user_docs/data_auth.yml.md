
## 授权文件

授权文件用于记录面板所有的用户密码和权限等配置, 可在停机后手动修改, 其中密码若填写非 `HASH/` 开头的字符串将会在面板启动时自动转换为哈希值.

面板首次启动时会自动创建 admin 账户, 并生成随机密码, 可在控制台中查看.

```yaml
- user: admin
  pass: HASH/P2/$2a$10$uHoAbJEIkrZPeUf7uqkXje5oPVwKLX7lc0BFU1P6JpxFLdynw.RHS
  perm: 7

```
