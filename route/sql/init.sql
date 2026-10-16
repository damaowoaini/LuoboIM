-- OpenIM Route Service Database Schema

SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- ----------------------------
-- Table: server_config (服务器配置表)
-- ----------------------------
CREATE TABLE IF NOT EXISTS `server_config` (
  `id` bigint NOT NULL AUTO_INCREMENT COMMENT '主键ID',
  `server_id` varchar(50) NOT NULL COMMENT '服务器ID',
  `server_name` varchar(100) NOT NULL COMMENT '服务器名称',
  `domains` text NOT NULL COMMENT '主域名列表（逗号分隔），前端自行拼接路径',
  `is_default` tinyint NOT NULL DEFAULT 0 COMMENT '是否默认服务器 1:是 0:否',
  `status` tinyint NOT NULL DEFAULT 1 COMMENT '状态 1:启用 0:禁用',
  `max_users` int DEFAULT 0 COMMENT '最大用户数 0:无限制',
  `current_users` int DEFAULT 0 COMMENT '当前用户数',
  `description` varchar(500) DEFAULT NULL COMMENT '描述',
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_server_id` (`server_id`),
  KEY `idx_status` (`status`),
  KEY `idx_is_default` (`is_default`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='服务器配置表';

-- ----------------------------
-- Table: invite_code (邀请码表)
-- ----------------------------
CREATE TABLE IF NOT EXISTS `invite_code` (
  `id` bigint NOT NULL AUTO_INCREMENT COMMENT '主键ID',
  `code` varchar(50) NOT NULL COMMENT '邀请码',
  `server_id` varchar(50) NOT NULL COMMENT '关联服务器ID',
  `max_use_count` int NOT NULL DEFAULT 0 COMMENT '最大使用次数 0:无限制',
  `used_count` int NOT NULL DEFAULT 0 COMMENT '已使用次数',
  `expire_time` timestamp NULL DEFAULT NULL COMMENT '过期时间',
  `status` tinyint NOT NULL DEFAULT 1 COMMENT '状态 1:启用 0:禁用',
  `created_by` varchar(50) DEFAULT NULL COMMENT '创建人',
  `remark` varchar(500) DEFAULT NULL COMMENT '备注',
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_code` (`code`),
  KEY `idx_server_id` (`server_id`),
  KEY `idx_status` (`status`),
  CONSTRAINT `fk_invite_server` FOREIGN KEY (`server_id`) REFERENCES `server_config` (`server_id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='邀请码表';

-- ----------------------------
-- Table: user_server_binding (用户服务器绑定表)
-- ----------------------------
CREATE TABLE IF NOT EXISTS `user_server_binding` (
  `id` bigint NOT NULL AUTO_INCREMENT COMMENT '主键ID',
  `user_id` varchar(50) NOT NULL COMMENT '用户ID',
  `server_id` varchar(50) NOT NULL COMMENT '服务器ID',
  `invite_code` varchar(50) DEFAULT NULL COMMENT '使用的邀请码',
  `bind_time` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '绑定时间',
  `last_login_time` timestamp NULL DEFAULT NULL COMMENT '最后登录时间',
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_user_id` (`user_id`),
  KEY `idx_server_id` (`server_id`),
  KEY `idx_invite_code` (`invite_code`),
  CONSTRAINT `fk_binding_server` FOREIGN KEY (`server_id`) REFERENCES `server_config` (`server_id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='用户服务器绑定表';

-- ----------------------------
-- 插入测试数据
-- ----------------------------

-- 插入服务器配置（支持多域名，逗号分隔，不带协议前缀）

-- Additive durable registration recovery metadata. Contains no credentials.
-- Pending reservations are counted in server_config.current_users and
-- invite_code.used_count; complete exactly once or release only a definitive
-- rejection. Never expire/release an uncertain upstream outcome automatically.
CREATE TABLE IF NOT EXISTS registration_intent (
  registration_id VARCHAR(36) NOT NULL PRIMARY KEY,
  account_key VARCHAR(50) NOT NULL,
  server_id VARCHAR(50) NOT NULL,
  invite_code VARCHAR(50) NOT NULL DEFAULT '',
  state VARCHAR(16) NOT NULL,
  upstream_user_id VARCHAR(100) NOT NULL DEFAULT '',
  lease_until DATETIME(3) NOT NULL,
  created_at DATETIME(3) NOT NULL,
  updated_at DATETIME(3) NOT NULL,
  UNIQUE KEY idx_registration_identity (account_key),
  KEY idx_registration_server (server_id),
  KEY idx_registration_state (state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
