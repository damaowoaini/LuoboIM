import 'dart:convert';

import 'package:flutter/services.dart';
import 'package:imapp/features/chat/data/chat_repository.dart';
import 'package:imapp/features/chat/presentation/chat_controller.dart';
import 'package:imapp/features/chat/presentation/chat_batch_selection.dart';

import '../../core/im/sdk_harness.dart';

class BatchHarness {
  BatchHarness({Duration timeout = const Duration(seconds: 15)})
    : sdk = SdkHarness(operationTimeout: timeout) {
    sdk.conversations.addAll([
      SdkHarness.conversation(title: '原会话'),
      {
        ...SdkHarness.conversation(id: 'target-chat', title: '目标会话'),
        'userID': 'carol',
      },
    ]);
    sdk.messages.addAll([
      for (var i = 1; i <= 3; i++)
        {
          ...SdkHarness.message(id: 'm$i', text: '原消息$i'),
          'createTime': 1700000000000 + i,
          'keyVersion': 8,
          'cloudCustomData': 'extension-$i',
        },
    ]);
    sdk.overrideCall = (call) async =>
        intercept == null ? respond(call) : intercept!(call);
  }
  final SdkHarness sdk;
  Future<dynamic> Function(MethodCall)? intercept;
  late final ChatRepository source;
  late final ChatController chat;
  late final ChatBatchSelection selection;
  int count(String method) => sdk.calls.where((c) => c.method == method).length;
  dynamic respond(MethodCall call) {
    final args = call.arguments as Map;
    if (call.method == 'findMessageList') {
      final request = (args['searchParams'] as List).single as Map;
      return jsonEncode({
        'findResultItems': [
          {
            'conversationID': request['conversationID'],
            'messageList': sdk.messages
                .where(
                  (m) => (request['clientMsgIDList'] as List).contains(
                    m['clientMsgID'],
                  ),
                )
                .toList(),
          },
        ],
      });
    }
    if (call.method == 'deleteMessageFromLocalAndSvr') {
      sdk.messages.removeWhere((m) => m['clientMsgID'] == args['clientMsgID']);
      return null;
    }
    if (call.method == 'createMergerMessage') {
      return jsonEncode({
        ...SdkHarness.message(
          id: 'created-merge',
          text: '',
          sender: 'alice',
          status: 1,
        ),
        'contentType': 107,
        'mergeElem': {
          'title': args['title'],
          'abstractList': args['summaryList'],
          'multiMessage': args['messageList'],
        },
      });
    }
    return sdk.respond(call);
  }

  Future<void> start() async {
    await sdk.start();
    source = ChatRepository(
      gateway: sdk.gateway,
      session: sdk.session,
      conversationId: 'single-alice-bob',
    );
    chat = ChatController(source);
    await chat.start();
    selection = ChatBatchSelection(chat);
  }

  void selectAll() {
    selection.begin(chat.messages.first);
    for (final item in chat.messages.skip(1)) {
      selection.toggle(item);
    }
  }

  Future<void> dispose() async {
    selection.dispose();
    chat.dispose();
    await sdk.dispose();
  }
}
