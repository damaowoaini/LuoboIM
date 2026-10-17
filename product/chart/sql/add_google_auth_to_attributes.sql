-- 为 attributes 表添加谷歌验证码字段
ALTER TABLE `attributes` 
ADD COLUMN `google_auth_secret` VARCHAR(64) DEFAULT '' COMMENT '谷歌验证码密钥' AFTER `register_type`,
ADD COLUMN `google_auth_enabled` TINYINT(1) DEFAULT 0 COMMENT '是否启用谷歌验证码' AFTER `google_auth_secret`;
