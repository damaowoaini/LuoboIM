-- 为管理员表添加谷歌验证码字段
-- 执行时间：2024-12-10

ALTER TABLE `admins` 
ADD COLUMN `google_auth_enabled` TINYINT(1) NOT NULL DEFAULT 0 COMMENT '是否启用谷歌验证码' AFTER `create_time`,
ADD COLUMN `google_auth_secret` VARCHAR(64) DEFAULT NULL COMMENT '谷歌验证码密钥' AFTER `google_auth_enabled`;

-- 添加索引以提高查询性能
CREATE INDEX `idx_google_auth_enabled` ON `admins` (`google_auth_enabled`);
