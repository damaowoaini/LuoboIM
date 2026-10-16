import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter/services.dart';
import 'package:flutter_openim_sdk/flutter_openim_sdk.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/core/app_runtime.dart';
import 'package:imapp/core/config/app_config.dart';
import 'package:imapp/core/config/route_config_loader.dart';
import 'package:imapp/core/im/open_im_gateway.dart';
import 'package:imapp/core/storage/consent_store.dart';
import 'package:imapp/core/tenant/session_coordinator.dart';
import 'package:imapp/core/tenant/session_credentials.dart';

final class _MemoryCredentials implements CredentialStore {
  SessionCredentials? value;
  @override
  Future<SessionCredentials?> read() async => value;
  @override
  Future<void> write(SessionCredentials credentials) async {
    value = credentials;
  }

  @override
  Future<void> clear() async {
    value = null;
  }
}

final class _Consent implements ConsentStore {
  @override
  Future<bool> read() async => true;
  @override
  Future<void> accept() async {}
}

/// Test fixture at the actual Flutter codec boundary; no production fake data.
final class SdkHarness {
  SdkHarness({
    this.operationTimeout = const Duration(seconds: 15),
    this.interactiveRevision,
    this.hasInteractiveAccess,
  });
  final Duration operationTimeout;
  final int Function()? interactiveRevision;
  final bool Function()? hasInteractiveAccess;
  static var _serial = 0;
  final channel = MethodChannel('business_sdk_test_${++_serial}');
  final calls = <MethodCall>[];
  final conversations = <Map<String, dynamic>>[];
  final messages = <Map<String, dynamic>>[];
  Future<dynamic> Function(MethodCall)? overrideCall;
  late final Directory directory;
  late final OpenImGateway gateway;
  late final SessionCoordinator session;
  AppRuntime? _runtime;
  String userId = 'alice';
  var _messageId = 0;
  bool _closed = false;
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;

  Future<void> start() async {
    directory = await Directory.systemTemp.createTemp('luobo-business-test-');
    messenger.setMockMethodCallHandler(channel, (call) async {
      calls.add(call);
      if (overrideCall != null) return overrideCall!(call);
      return respond(call);
    });
    gateway = OpenImGateway(
      hasPrivacyConsent: () => true,
      managerFactory: () => IMManager(channel),
      supportDirectory: () async => directory,
      platformId: 2,
      operationTimeout: operationTimeout,
      interactiveRevision: interactiveRevision,
      hasInteractiveAccess: hasInteractiveAccess,
    );
    session = SessionCoordinator(store: _MemoryCredentials(), im: gateway);
    await session.signIn((_) async => credentials);
  }

  SessionCredentials get credentials => SessionCredentials(
    userId: userId,
    tenantId: 'fixture-tenant',
    tenantName: '测试租户',
    domains: ['im.example.com'],
    chatToken: 'fixture-chat',
    imToken: 'fixture-im',
  );

  AppRuntime get runtime => _runtime ??= AppRuntime(
    config: const AppConfig(),
    consent: _Consent(),
    routes: RouteConfigLoader(config: const AppConfig()),
    session: session,
    auth: (_, _) async =>
        throw UnimplementedError('No HTTP auth in SDK fixture'),
  );

  dynamic respond(MethodCall call) {
    final args = call.arguments as Map;
    switch (call.method) {
      case 'initSDK':
        return true;
      case 'getSelfUserInfo':
        return jsonEncode({'userID': userId});
      case 'getMultipleConversation':
        return jsonEncode(
          conversations
              .where(
                (item) => (args['conversationIDList'] as List).contains(
                  item['conversationID'],
                ),
              )
              .toList(),
        );
      case 'getConversationListSplit':
        return jsonEncode(
          conversations
              .skip(args['offset'] as int)
              .take(args['count'] as int)
              .toList(),
        );
      case 'getAdvancedHistoryMessageList':
        return jsonEncode({
          'messageList': messages,
          'isEnd': true,
          'lastMinSeq': 1,
          'errCode': 0,
        });
      case 'createTextMessage':
        return jsonEncode(
          message(
            id: 'created-${++_messageId}',
            text: args['text'] as String,
            sender: userId,
            status: 1,
          ),
        );
      case 'createQuoteMessage':
        final value = message(
          id: 'created-${++_messageId}',
          text: '',
          sender: userId,
          status: 1,
        );
        value['contentType'] = 114;
        value['quoteElem'] = {
          'text': args['quoteText'],
          'quoteMessage': args['quoteMessage'],
        };
        return jsonEncode(value);
      case 'sendMessage':
        final value = Map<String, dynamic>.from(args['message'] as Map)
          ..['status'] = 2;
        messages.removeWhere(
          (item) => item['clientMsgID'] == value['clientMsgID'],
        );
        messages.add(value);
        return jsonEncode(value);
      case 'markConversationMessageAsRead':
        for (final item in conversations) {
          if (item['conversationID'] == args['conversationID']) {
            item['unreadCount'] = 0;
          }
        }
      case 'setConversationDraft':
        for (final item in conversations) {
          if (item['conversationID'] == args['conversationID']) {
            item['draftText'] = args['draftText'];
          }
        }
      case 'pinConversation':
        for (final item in conversations) {
          if (item['conversationID'] == args['conversationID']) {
            item['isPinned'] = args['isPinned'];
          }
        }
      case 'setConversationRecvMessageOpt':
        for (final item in conversations) {
          if (item['conversationID'] == args['conversationID']) {
            item['recvMsgOpt'] = args['status'];
          }
        }
      case 'hideConversation':
      case 'deleteConversationAndDeleteAllMsg':
        conversations.removeWhere(
          (item) => item['conversationID'] == args['conversationID'],
        );
      case 'deleteMessageFromLocalAndSvr':
        messages.removeWhere(
          (item) => item['clientMsgID'] == args['clientMsgID'],
        );
      case 'clearConversationAndDeleteAllMsg':
      case 'deleteAllMsgFromLocalAndSvr':
        messages.clear();
    }
    return null;
  }

  static Map<String, dynamic> conversation({
    String id = 'single-alice-bob',
    String title = '测试联系人',
    String draft = '',
    bool group = false,
  }) => {
    'conversationID': id,
    'showName': title,
    'userID': group ? '' : 'bob',
    'groupID': group ? 'group-a' : '',
    'conversationType': group ? 3 : 1,
    'unreadCount': 3,
    'recvMsgOpt': 0,
    'latestMsgSendTime': 1700000000000,
    'latestMsg': message(id: 'incoming', text: '收到的真实通道测试消息'),
    'draftText': draft,
    'isPinned': false,
    'isNotInGroup': false,
  };

  static Map<String, dynamic> message({
    required String id,
    required String text,
    String sender = 'bob',
    int status = 2,
  }) => {
    'clientMsgID': id,
    'sendID': sender,
    'senderNickname': sender,
    'recvID': sender == 'alice' ? 'bob' : 'alice',
    'sessionType': 1,
    'sendTime': 1700000000000,
    'createTime': 1700000000000,
    'status': status,
    'contentType': 101,
    'textElem': {'content': text},
  };

  Future<void> callback(String method, Map<String, dynamic> arguments) async {
    final completer = Completer<void>();
    // ignore: deprecated_member_use
    messenger.handlePlatformMessage(
      channel.name,
      const StandardMethodCodec().encodeMethodCall(
        MethodCall(method, arguments),
      ),
      (_) => completer.complete(),
    );
    await completer.future;
    await Future<void>.delayed(Duration.zero);
  }

  Future<void> dispose() async {
    if (_closed) return;
    _closed = true;
    overrideCall = null;
    await session.dispose();
    await gateway.dispose();
    messenger.setMockMethodCallHandler(channel, null);
    await directory.delete(recursive: true);
  }
}
