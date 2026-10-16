# UserLoginInfo 组件使用说明

## 📌 功能说明

该组件用于显示用户的最新登录信息，包括：
- 设备型号
- IP 地址
- 登录地区
- 登录时间
- **同一设备多账户登录警告**（自动高亮）

## 🎯 使用场景

### 1. 在好友列表中显示（内联模式）

```tsx
import UserLoginInfo from "@/components/UserLoginInfo";

// 在好友列表项中
<UserLoginInfo userID={friend.userID} showInline={true} />
```

效果：
- 紧凑的单行显示
- 显示设备型号和地区
- 多账户登录时自动显示⚠️图标和黄色高亮背景

### 2. 在用户详情页中显示（完整模式）

```tsx
import UserLoginInfo from "@/components/UserLoginInfo";

// 在用户详情页
<UserLoginInfo userID={userID} />
```

效果：
- 完整的卡片样式
- 显示所有登录信息
- 多账户登录时显示警告徽章和高亮背景

### 3. 显示登录记录列表

```tsx
import UserLoginRecordList from "@/components/UserLoginRecordList";

// 在用户详情或设置页
<UserLoginRecordList userID={userID} />
```

效果：
- 表格形式显示所有登录记录
- 支持分页
- 自动高亮多账户登录的记录

## 🎨 样式自定义

可以通过覆盖 CSS 变量来自定义样式：

```scss
// 在您的样式文件中
.user-login-info {
  --warning-color: #ff6b00; // 警告颜色
  --warning-bg: #fff3cd;    // 警告背景色
}
```

## 📊 多账户登录检测

当检测到同一设备有多个账户登录时：
- ✅ 自动显示⚠️警告图标
- ✅ 背景变为黄色高亮
- ✅ 显示"多账户登录"徽章
- ✅ 显示同设备其他账户数量

## 🔒 权限说明

- **普通用户**：只能查看自己和好友的登录记录
- **管理员**：可以查看任意用户的登录记录

API 会自动进行权限校验。

