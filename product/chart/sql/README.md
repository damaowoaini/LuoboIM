# Chart 数据库迁移说明

## 新服务器部署

如果是在新服务器上部署 chart 服务，需要执行以下 SQL：

### 1. 添加谷歌验证码字段（必须）

```bash
mysql -u root -p your_database_name < add_google_auth_to_attributes.sql
```

或者直接执行：

```sql
ALTER TABLE `attributes` 
ADD COLUMN `google_auth_secret` VARCHAR(64) DEFAULT '' COMMENT '谷歌验证码密钥' AFTER `register_type`,
ADD COLUMN `google_auth_enabled` TINYINT(1) DEFAULT 0 COMMENT '是否启用谷歌验证码' AFTER `google_auth_secret`;
```

## 已有服务器升级

如果是升级已有的 chart 服务，同样需要执行上面的 SQL。

## 验证

执行完 SQL 后，可以验证字段是否添加成功：

```sql
DESC attributes;
```

应该能看到：
- `google_auth_secret` VARCHAR(64)
- `google_auth_enabled` TINYINT(1)

## 注意事项

1. 这个 SQL 是幂等的，重复执行会报错但不影响数据
2. 如果表中已经有这两个字段，会提示 "Duplicate column name"，可以忽略
3. 建议在执行前先备份数据库
