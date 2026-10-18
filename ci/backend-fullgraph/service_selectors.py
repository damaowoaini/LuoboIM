"""Reviewed expectations from the actual1555 constructors, never Ready claims.

Every runtime registration must independently match these selectors after the
original factory has sealed its actual DAO/provider/worker instances. No missing
entry is discovered from the untrusted registration and added on the fly.
"""
SERVICES = {
 'api': [], 'crontask': [], 'msgtransfer-managed': [],
 'msggateway': ['OpenIMServer.msggateway.msgGateway'],
 'push': ['OpenIMServer.push.PushMsgService'],
 'rpc-auth': ['OpenIMServer.auth.Auth'],
 'rpc-conversation': ['OpenIMServer.conversation.conversation'],
 'rpc-encryption': ['OpenIMServer.encryption.encryption'],
 'rpc-friend': ['OpenIMServer.friend.friend'],
 'rpc-group': ['OpenIMServer.group.group'],
 'rpc-msg': ['OpenIMServer.msg.msg'],
 'rpc-rtc': ['OpenIMServer.rtc.RtcService'],
 'rpc-third': ['OpenIMServer.third.third'],
 'rpc-user': ['OpenIMServer.user.user'],
}
COMPONENTS = {
 'api': ['exact-logout-recovery','history-reader','http-handlers','http-validator','sensitive-word-workers','source-ownership'],
 'crontask': ['cron-jobs','history-reader','kafka-producer','source-ownership'],
 'msggateway': ['provider-callbacks','rpc-handlers','source-ownership','websocket-admission','websocket-runtime'],
 'msgtransfer-managed': ['history-reader','kafka-consumer','namespace-writer','offline-runtime','online-runtime','provider-callbacks','source-ownership'],
 'push': ['rpc-handlers','source-ownership'],
 'rpc-auth': ['rpc-handlers','source-ownership'],
 'rpc-conversation': ['history-reader','rpc-handlers','source-ownership'],
 'rpc-encryption': ['rpc-handlers','source-ownership'],
 'rpc-friend': ['rpc-handlers','source-ownership'],
 'rpc-group': ['history-reader','namespace-writer','receiver-runtime','rpc-handlers','source-ownership'],
 'rpc-msg': ['history-reader','kafka-producer','namespace-writer','rpc-handlers','sensitive-word-workers','source-ownership'],
 'rpc-rtc': ['rpc-handlers','source-ownership'],
 'rpc-third': ['provider-callbacks','rpc-handlers','source-ownership'],
 'rpc-user': ['rpc-handlers','source-ownership'],
}
