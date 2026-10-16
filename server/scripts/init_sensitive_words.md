# 敏感词初始化说明

## 方式一：通过 API 添加

启动服务后，使用管理员 token 调用 API：

```bash
curl -X POST http://localhost:10002/sensitive_word/add \
  -H "Content-Type: application/json" \
  -H "token: YOUR_ADMIN_TOKEN" \
  -d '{
    "words": [
      "fuck",
      "shit",
      "damn",
      "政治敏感词1",
      "政治敏感词2",
      "色情",
      "赌博",
      "毒品"
    ]
  }'
```

## 方式二：直接插入 MongoDB

连接到 MongoDB 后执行：

```javascript
use openIM;

db.sensitive_word.insertMany([
  {
    "_id": ObjectId(),
    "word": "fuck",
    "status": 1,
    "create_time": new Date(),
    "update_time": new Date()
  },
  {
    "_id": ObjectId(),
    "word": "shit",
    "status": 1,
    "create_time": new Date(),
    "update_time": new Date()
  },
  {
    "_id": ObjectId(),
    "word": "damn",
    "status": 1,
    "create_time": new Date(),
    "update_time": new Date()
  },
  {
    "_id": ObjectId(),
    "word": "政治敏感词",
    "status": 1,
    "create_time": new Date(),
    "update_time": new Date()
  },
  {
    "_id": ObjectId(),
    "word": "色情",
    "status": 1,
    "create_time": new Date(),
    "update_time": new Date()
  },
  {
    "_id": ObjectId(),
    "word": "赌博",
    "status": 1,
    "create_time": new Date(),
    "update_time": new Date()
  },
  {
    "_id": ObjectId(),
    "word": "毒品",
    "status": 1,
    "create_time": new Date(),
    "update_time": new Date()
  }
]);
```

## 常用敏感词分类

### 1. 脏话类
- fuck, shit, damn, bitch, asshole
- 傻逼, 操, 妈的, 草泥马

### 2. 政治敏感
- 根据实际需求添加

### 3. 色情相关
- 色情, 黄色, 裸聊, 约炮

### 4. 赌博相关
- 赌博, 赌场, 博彩, 六合彩

### 5. 毒品相关
- 毒品, 大麻, 冰毒, 海洛因

### 6. 暴力相关
- 杀人, 自杀, 爆炸, 恐怖

## API 说明

### 1. 添加敏感词
POST /sensitive_word/add
```json
{
  "words": ["敏感词1", "敏感词2"]
}
```

### 2. 删除敏感词
POST /sensitive_word/delete
```json
{
  "ids": ["id1", "id2"]
}
```

### 3. 更新敏感词
POST /sensitive_word/update
```json
{
  "id": "word_id",
  "word": "新敏感词",
  "status": 1
}
```

### 4. 搜索敏感词
POST /sensitive_word/search
```json
{
  "keyword": "搜索关键词",
  "status": 1,
  "pageNumber": 1,
  "showNumber": 20
}
```

### 5. 重新加载缓存
POST /sensitive_word/reload
```json
{}
```

## 注意事项

1. 敏感词检查不区分大小写
2. 敏感词会自动每5分钟刷新一次缓存
3. 添加/删除/更新敏感词后会立即刷新缓存
4. status: 1=启用, 0=禁用
5. 只检查文本消息，其他类型消息不检查
