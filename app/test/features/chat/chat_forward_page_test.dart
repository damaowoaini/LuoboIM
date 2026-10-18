import 'dart:convert';
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:imapp/features/chat/presentation/chat_page.dart';
import 'package:imapp/features/chat/presentation/chat_forward_page.dart';
import 'package:imapp/providers.dart';

import '../../core/im/capture_theme.dart';
import '../../core/im/sdk_harness.dart';

void main() {
  late SdkHarness harness;
  setUp(() {
    harness = SdkHarness();
    harness.conversations.addAll([
      SdkHarness.conversation(title: '原会话'),
      {
        ...SdkHarness.conversation(id: 'target-chat', title: '目标会话'),
        'userID': 'carol',
      },
    ]);
    harness.messages.add({
      ...SdkHarness.message(id: 'source-message', text: '可直接转发的原文'),
      'keyVersion': 8,
      'cloudCustomData': 'protocol-extension',
      'attachedInfoElem': {'isPrivateChat': false},
    });
    harness.overrideCall = (call) async {
      if (call.method == 'findMessageList') {
        return jsonEncode({
          'findResultItems': [
            {
              'conversationID': 'single-alice-bob',
              'messageList': harness.messages,
            },
          ],
        });
      }
      if (call.method == 'createForwardMessage') {
        return jsonEncode({
          ...Map<String, dynamic>.from(
            (call.arguments as Map)['message'] as Map,
          ),
          'clientMsgID': 'new-forward-message',
          'sendID': 'alice',
        });
      }
      return harness.respond(call);
    };
  });
  tearDown(() => harness.dispose());

  Future<void> mount(WidgetTester tester) async {
    await tester.runAsync(harness.start);
    final theme = await captureTheme(tester);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          imGatewayProvider.overrideWithValue(harness.gateway),
          appRuntimeProvider.overrideWith((_) => harness.runtime),
        ],
        child: MaterialApp(
          theme: theme,
          home: const ChatPage(conversationId: 'single-alice-bob'),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  Future<void> openConfirmation(WidgetTester tester) async {
    await tester.longPress(find.text('可直接转发的原文'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('转发到聊天'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('目标会话'));
    await tester.pumpAndSettle();
    expect(find.text('转发给 目标会话？'), findsOneWidget);
    expect(
      harness.calls.where((call) => call.method == 'sendMessage'),
      isEmpty,
    );
  }

  testWidgets(
    'forward chooser distinguishes duplicate names by stable account and group IDs through confirmation',
    (tester) async {
      harness.conversations[1]['showName'] = '同名收件人';
      harness.conversations.add({
        ...SdkHarness.conversation(
          id: 'group-target',
          title: '同名收件人',
          group: true,
        ),
        'groupID': 'team-red',
      });
      await mount(tester);
      await tester.longPress(find.text('可直接转发的原文'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('转发到聊天'));
      await tester.pumpAndSettle();
      expect(find.text('同名收件人'), findsNWidgets(2));
      expect(find.text('账号 ID：carol'), findsOneWidget);
      expect(find.text('群 ID：team-red'), findsOneWidget);

      await tester.enterText(find.byType(TextField), '  CAROL  ');
      await tester.pumpAndSettle();
      expect(find.text('同名收件人'), findsOneWidget);
      expect(find.text('群 ID：team-red'), findsNothing);
      await tester.enterText(find.byType(TextField), ' TEAM-RED ');
      await tester.pumpAndSettle();
      expect(find.text('同名收件人'), findsOneWidget);
      await tester.tap(find.text('同名收件人'));
      await tester.pumpAndSettle();
      expect(find.text('转发给 同名收件人？'), findsOneWidget);
      expect(find.text('群 ID：team-red\n\n确认转发这条消息。'), findsOneWidget);
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(
        harness.calls.where((call) => call.method == 'sendMessage'),
        isEmpty,
      );
      await tester.runAsync(harness.dispose);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets('forward recipient lookup excludes internal conversation IDs', (
    tester,
  ) async {
    await mount(tester);
    await tester.longPress(find.text('可直接转发的原文'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('转发到聊天'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), 'target-chat');
    await tester.pumpAndSettle();
    expect(find.text('目标会话'), findsNothing);
    await tester.enterText(find.byType(TextField), 'CAROL');
    await tester.pumpAndSettle();
    expect(find.text('目标会话'), findsOneWidget);
    expect(
      harness.calls.where((call) => call.method == 'sendMessage'),
      isEmpty,
    );
    await tester.runAsync(harness.dispose);
    await tester.pumpWidget(const SizedBox.shrink());
  });

  testWidgets(
    'known-expired body is hidden and its menu cannot copy reply collect or forward cached content',
    (tester) async {
      harness.messages.single['attachedInfoElem'] = {
        'isPrivateChat': true,
        'burnDuration': 30,
        'hasReadTime': 1700000000000,
      };
      await mount(tester);
      expect(find.text('可直接转发的原文'), findsNothing);
      expect(find.text('阅后即焚内容已到期'), findsOneWidget);
      await tester.longPress(find.text('阅后即焚内容已到期'));
      await tester.pumpAndSettle();
      expect(find.text('复制文字'), findsNothing);
      expect(find.text('引用回复'), findsNothing);
      expect(find.text('收藏'), findsNothing);
      expect(find.text('转发到聊天'), findsNothing);
      expect(find.text('删除消息'), findsOneWidget);
      await tester.runAsync(harness.dispose);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
  testWidgets(
    'forwarding stops when refreshed destination identity differs from the confirmed account',
    (tester) async {
      await mount(tester);
      await openConfirmation(tester);
      harness.conversations[1]['userID'] = 'replacement-account';
      await tester.tap(find.text('发送'));
      await tester.pumpAndSettle();
      expect(
        harness.calls.where((call) => call.method == 'createForwardMessage'),
        isEmpty,
      );
      expect(
        harness.calls.where((call) => call.method == 'sendMessage'),
        isEmpty,
      );
      await tester.runAsync(harness.dispose);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
  testWidgets(
    'chat long press forwards one original message after explicit destination confirmation',
    (tester) async {
      await mount(tester);
      await openConfirmation(tester);
      await tester.tap(find.text('发送'));
      await tester.pumpAndSettle();
      final sends = harness.calls.where((call) => call.method == 'sendMessage');
      expect(sends, hasLength(1));
      final args = sends.single.arguments as Map;
      expect(args['userID'], 'carol');
      expect(args['message']['clientMsgID'], 'new-forward-message');
      expect(args['message']['keyVersion'], 0);
      expect(args['message']['cloudCustomData'], 'protocol-extension');
      expect(find.text('消息已转发'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.runAsync(harness.dispose);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'source changing to private during confirmation is rejected before native creation or send',
    (tester) async {
      await mount(tester);
      await openConfirmation(tester);
      harness.messages.single['attachedInfoElem'] = {'isPrivateChat': true};
      await tester.tap(find.text('发送'));
      await tester.pumpAndSettle();
      expect(
        harness.calls.where((call) => call.method == 'createForwardMessage'),
        isEmpty,
      );
      expect(
        harness.calls.where((call) => call.method == 'sendMessage'),
        isEmpty,
      );
      expect(find.textContaining('阅后即焚内容不能收藏或转发'), findsOneWidget);
      await tester.runAsync(harness.dispose);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'account exit while selecting a destination cancels forwarding before any write dispatch',
    (tester) async {
      await mount(tester);
      await openConfirmation(tester);
      await tester.runAsync(harness.session.signOut);
      await tester.tap(find.text('发送'));
      await tester.pumpAndSettle();
      expect(
        harness.calls.where((call) => call.method == 'createForwardMessage'),
        isEmpty,
      );
      expect(
        harness.calls.where((call) => call.method == 'sendMessage'),
        isEmpty,
      );
      expect(find.text('登录已失效，请重新登录'), findsOneWidget);
      await tester.runAsync(harness.dispose);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'private message has an honest warning and no collection or forwarding action',
    (tester) async {
      harness.messages.single['attachedInfoElem'] = {
        'isPrivateChat': true,
        'burnDuration': 30,
      };
      await mount(tester);
      expect(find.textContaining('暂勿依赖本版本自动销毁'), findsOneWidget);
      await tester.longPress(find.text('可直接转发的原文'));
      await tester.pumpAndSettle();
      expect(find.text('收藏'), findsNothing);
      expect(find.text('转发到聊天'), findsNothing);
      expect(
        harness.calls.where((call) => call.method == 'sendMessage'),
        isEmpty,
      );
      await tester.runAsync(harness.dispose);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'tag903 text metadata forwards through the actual menu and preserves custom JSON',
    (tester) async {
      final customData = jsonEncode({
        'customType': 903,
        'data': {
          'textElem': {'content': '可直接转发的原文', 'language': 'zh'},
          'soundElem': null,
          'pictureElem': {
            'sourcePicture': {
              'url': 'https://assets.example.com/p.png',
              'width': 1,
              'height': 1,
            },
          },
          'vendorMetadata': {
            'revision': 2,
            'labels': ['legacy'],
          },
        },
      });
      final originalSource = harness.messages.single;
      originalSource
        ..['contentType'] = 110
        ..['customElem'] = {
          'data': customData,
          'extension': 'original-tag-extension',
          'description': 'original-description',
        };
      final originalJSON = jsonEncode(originalSource);
      await mount(tester);
      try {
        await openConfirmation(tester);
        await tester.tap(find.text('发送'));
        await tester.pumpAndSettle();
        final creates = harness.calls.where(
          (call) => call.method == 'createForwardMessage',
        );
        final sends = harness.calls.where(
          (call) => call.method == 'sendMessage',
        );
        expect(creates, hasLength(1));
        expect(sends, hasLength(1));
        final createdSource =
            (creates.single.arguments as Map)['message'] as Map;
        final send = sends.single.arguments as Map;
        expect(createdSource['contentType'], 110);
        expect(createdSource['customElem'], originalSource['customElem']);
        expect(createdSource['customElem']['data'], customData);
        expect(send['userID'], 'carol');
        expect(send['message']['clientMsgID'], 'new-forward-message');
        expect(send['message']['contentType'], 110);
        expect(send['message']['customElem'], originalSource['customElem']);
        expect(send['message']['customElem']['data'], customData);
        expect(jsonEncode(originalSource), originalJSON);
        expect(find.text('消息已转发'), findsOneWidget);
        expect(tester.takeException(), isNull);
      } finally {
        await tester.runAsync(harness.dispose);
        await tester.pumpWidget(const SizedBox.shrink());
      }
    },
  );

  testWidgets(
    'meeting905 card displays original fields and forwards the unchanged custom invitation',
    (tester) async {
      final start = DateTime(2024, 1, 2, 3, 4).millisecondsSinceEpoch ~/ 1000;
      final customData = jsonEncode({
        'customType': 905,
        'data': {
          'inviterUserID': 'original-inviter',
          'inviterNickname': '原邀请人',
          'inviterFaceURL': 'https://assets.example.com/inviter.png',
          'subject': '原会议主题',
          'id': 'original-meeting',
          'start': start,
          'duration': 3600,
          'unknownMetadata': {
            'revision': 7,
            'labels': ['retained'],
          },
          'url': 'https://untrusted.example.com/not-an-admission',
        },
      });
      final originalSource = harness.messages.single;
      originalSource
        ..['contentType'] = 110
        ..['customElem'] = {
          'data': customData,
          'extension': 'original-meeting-extension',
          'description': 'original-meeting-description',
        };
      final originalJSON = jsonEncode(originalSource);
      await mount(tester);
      try {
        expect(find.text('原会议主题'), findsOneWidget);
        expect(find.text('开始时间：2024-01-02 03:04'), findsOneWidget);
        expect(find.text('时长：1.0 小时'), findsOneWidget);
        expect(find.text('会议号：original-meeting'), findsOneWidget);
        expect(find.text('当前版本未开放从邀请加入会议'), findsOneWidget);
        await tester.tap(find.text('原会议主题'));
        await tester.pump();
        expect(
          harness.calls.where((call) => call.method.contains('Meeting')),
          isEmpty,
        );
        await tester.longPress(find.text('原会议主题'));
        await tester.pumpAndSettle();
        expect(find.text('复制文字'), findsNothing);
        expect(find.text('引用回复'), findsNothing);
        expect(find.text('转发到聊天'), findsOneWidget);
        await tester.tap(find.text('转发到聊天'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('目标会话'));
        await tester.pumpAndSettle();
        expect(find.text('转发给 目标会话？'), findsOneWidget);
        expect(
          harness.calls.where((call) => call.method == 'sendMessage'),
          isEmpty,
        );
        await tester.tap(find.text('发送'));
        await tester.pumpAndSettle();
        final creates = harness.calls.where(
          (call) => call.method == 'createForwardMessage',
        );
        final sends = harness.calls.where(
          (call) => call.method == 'sendMessage',
        );
        expect(creates, hasLength(1));
        expect(sends, hasLength(1));
        final createdSource =
            (creates.single.arguments as Map)['message'] as Map;
        final sent = sends.single.arguments as Map;
        expect(createdSource['contentType'], 110);
        expect(createdSource['customElem']['data'], customData);
        expect(sent['userID'], 'carol');
        expect(sent['message']['clientMsgID'], 'new-forward-message');
        expect(sent['message']['contentType'], 110);
        expect(sent['message']['customElem'], originalSource['customElem']);
        expect(sent['message']['customElem']['data'], customData);
        expect(jsonEncode(originalSource), originalJSON);
        expect(find.text('消息已转发'), findsOneWidget);
        expect(tester.takeException(), isNull);
      } finally {
        await tester.runAsync(harness.dispose);
        await tester.pumpWidget(const SizedBox.shrink());
      }
    },
  );

  testWidgets(
    'mixed tag903 text keeps copy and reply while raw voice still prevents forwarding',
    (tester) async {
      final customData = jsonEncode({
        'customType': 903,
        'data': {
          'textElem': {'content': '可直接转发的原文', 'language': 'zh'},
          'soundElem': {
            'sourceUrl': 'https://assets.example.com/a.m4a',
            'duration': 2,
          },
          'vendorMetadata': {'revision': 2},
        },
      });
      final originalSource = harness.messages.single;
      originalSource
        ..['contentType'] = 110
        ..['customElem'] = {
          'data': customData,
          'extension': 'original-mixed-extension',
          'description': 'original-mixed-description',
        };
      final originalJSON = jsonEncode(originalSource);
      await mount(tester);
      try {
        expect(find.text('标签语音'), findsNothing);
        await tester.longPress(find.text('可直接转发的原文'));
        await tester.pumpAndSettle();
        expect(find.text('复制文字'), findsOneWidget);
        expect(find.text('引用回复'), findsOneWidget);
        expect(find.text('转发到聊天'), findsNothing);
        expect(find.text('删除消息'), findsOneWidget);
        expect(
          harness.calls.where((call) => call.method == 'createForwardMessage'),
          isEmpty,
        );
        expect(
          harness.calls.where((call) => call.method == 'sendMessage'),
          isEmpty,
        );
        final dismissibleBarrier = find.byWidgetPredicate(
          (widget) => widget is ModalBarrier && widget.dismissible,
        );
        expect(dismissibleBarrier, findsOneWidget);
        final barrierRect = tester.getRect(dismissibleBarrier);
        final sheetRect = tester.getRect(find.byType(BottomSheet));
        expect(sheetRect.top, greaterThan(barrierRect.top));
        final dismissPoint = Offset(
          barrierRect.center.dx,
          (barrierRect.top + sheetRect.top) / 2,
        );
        expect(barrierRect.contains(dismissPoint), isTrue);
        expect(sheetRect.contains(dismissPoint), isFalse);
        await tester.tapAt(dismissPoint);
        await tester.pumpAndSettle();
        expect(find.byType(BottomSheet), findsNothing);
        expect(dismissibleBarrier, findsNothing);
        expect(jsonEncode(originalSource), originalJSON);
        expect(tester.takeException(), isNull);
      } finally {
        await tester.runAsync(harness.dispose);
        await tester.pumpWidget(const SizedBox.shrink());
      }
    },
  );

  Future<void> awaitNativeCreate(
    WidgetTester tester,
    Completer<void> entered,
  ) async {
    await tester.tap(find.widgetWithText(TextButton, '发送'));
    for (var i = 0; i < 20 && !entered.isCompleted; i++) {
      await tester.pump(const Duration(milliseconds: 50));
    }
    expect(entered.isCompleted, isTrue);
  }

  for (final boundary in ['body replacement', 'CID clear', 'owner exit']) {
    testWidgets(
      'single forward rejects $boundary after native creation without renewing its source intent',
      (tester) async {
        final entered = Completer<void>(), release = Completer<void>();
        final original = harness.overrideCall!;
        harness.overrideCall = (call) async {
          if (call.method == 'createForwardMessage') {
            entered.complete();
            await release.future;
          }
          return original(call);
        };
        await mount(tester);
        Future<void>? exiting;
        try {
          await openConfirmation(tester);
          await awaitNativeCreate(tester, entered);
          if (boundary == 'body replacement') {
            harness.messages.single['textElem'] = {'content': '后来替换的正文'};
          } else if (boundary == 'CID clear') {
            await tester.runAsync(
              () => harness.gateway.clearConversationMessages(
                harness.session.state.generation,
                'single-alice-bob',
              ),
            );
          } else {
            exiting = harness.session.signOut();
          }
          release.complete();
          await tester.pumpAndSettle();
          if (exiting case final exit?) {
            await tester.runAsync(() => exit);
          }
          expect(
            harness.calls.where((c) => c.method == 'createForwardMessage'),
            hasLength(1),
          );
          expect(
            harness.calls.where((c) => c.method == 'sendMessage'),
            isEmpty,
          );
          if (boundary == 'body replacement') {
            final createIndex = harness.calls.indexWhere(
              (c) => c.method == 'createForwardMessage',
            );
            expect(
              harness.calls
                  .skip(createIndex + 1)
                  .where((c) => c.method == 'findMessageList'),
              isNotEmpty,
            );
          }
          expect(find.textContaining('转发结果未确认'), findsNothing);
          expect(tester.takeException(), isNull);
        } finally {
          if (!release.isCompleted) {
            release.complete();
          }
          await tester.runAsync(harness.dispose);
          await tester.pumpWidget(const SizedBox.shrink());
        }
      },
    );
  }

  testWidgets(
    'single forward source route cannot revive after foreign coverage while native create waits',
    (tester) async {
      final entered = Completer<void>(), release = Completer<void>();
      final original = harness.overrideCall!;
      harness.overrideCall = (call) async {
        if (call.method == 'createForwardMessage') {
          if (!entered.isCompleted) {
            entered.complete();
          }
          await release.future;
        }
        return original(call);
      };
      await mount(tester);
      try {
        await openConfirmation(tester);
        await awaitNativeCreate(tester, entered);
        final navigator = Navigator.of(
          tester.element(find.byType(ChatForwardPage)),
        );
        final covered = navigator.push<void>(
          MaterialPageRoute<void>(
            builder: (_) => const Scaffold(body: Text('另一个页面')),
          ),
        );
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 350));
        navigator.pop();
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 350));
        await covered;
        release.complete();
        await tester.pumpAndSettle();
        expect(find.byType(ChatForwardPage), findsOneWidget);
        expect(find.text('此转发已失效，请重新确认收件人。'), findsOneWidget);
        expect(
          harness.calls.where((c) => c.method == 'createForwardMessage'),
          hasLength(1),
        );
        expect(harness.calls.where((c) => c.method == 'sendMessage'), isEmpty);
        expect(find.textContaining('转发结果未确认'), findsNothing);
        // A later explicit confirmation is a new attempt, never the old continuation.
        await tester.tap(find.text('目标会话'));
        await tester.pumpAndSettle();
        await tester.tap(find.widgetWithText(TextButton, '发送'));
        await tester.pumpAndSettle();
        expect(
          harness.calls.where((c) => c.method == 'createForwardMessage'),
          hasLength(2),
        );
        expect(
          harness.calls.where((c) => c.method == 'sendMessage'),
          hasLength(1),
        );
        expect(tester.takeException(), isNull);
      } finally {
        if (!release.isCompleted) {
          release.complete();
        }
        await tester.runAsync(harness.dispose);
        await tester.pumpWidget(const SizedBox.shrink());
      }
    },
  );

  testWidgets(
    'single unknown dispatch blocks replay and explicit review keeps the original confirmed destination',
    (tester) async {
      final original = harness.overrideCall!;
      harness.overrideCall = (call) async {
        if (call.method == 'sendMessage') {
          throw PlatformException(code: '10005');
        }
        return original(call);
      };
      await mount(tester);
      try {
        await openConfirmation(tester);
        final tile = tester.widget<ListTile>(
          find.ancestor(of: find.text('目标会话'), matching: find.byType(ListTile)),
        );
        final originalTap = tile.onTap!;
        await tester.tap(find.widgetWithText(TextButton, '发送'));
        await tester.pumpAndSettle();
        expect(find.textContaining('转发结果未确认'), findsWidgets);
        expect(
          tester.widget<TextField>(find.byType(TextField)).enabled,
          isFalse,
        );
        expect(
          tester
              .widget<ListTile>(
                find.ancestor(
                  of: find.text('目标会话'),
                  matching: find.byType(ListTile),
                ),
              )
              .onTap,
          isNull,
        );
        originalTap();
        await tester.pumpAndSettle();
        expect(
          harness.calls.where((c) => c.method == 'createForwardMessage'),
          hasLength(1),
        );
        expect(
          harness.calls.where((c) => c.method == 'sendMessage'),
          hasLength(1),
        );
        expect(find.text('转发给 目标会话？'), findsNothing);
        final review = find.text('前往核对 · 账号 ID：carol');
        expect(review, findsOneWidget);
        harness.conversations[1]['userID'] = 'replacement-account';
        await tester.tap(review);
        await tester.pumpAndSettle();
        expect(find.byType(ChatForwardPage), findsOneWidget);
        expect(find.byType(ChatPage), findsNothing);
        harness.conversations[1]['userID'] = 'carol';
        await tester.tap(review);
        await tester.pumpAndSettle();
        final reviewPage = tester.widget<ChatPage>(find.byType(ChatPage));
        expect(reviewPage.conversationId, 'target-chat');
        expect(reviewPage.sourceCurrent, isNotNull);
        expect(reviewPage.sourceCurrent!(), isTrue);
        await tester.runAsync(harness.session.signOut);
        expect(reviewPage.sourceCurrent!(), isFalse);
        expect(
          harness.calls.where((c) => c.method == 'sendMessage'),
          hasLength(1),
        );
        expect(tester.takeException(), isNull);
      } finally {
        await tester.runAsync(harness.dispose);
        await tester.pumpWidget(const SizedBox.shrink());
      }
    },
  );

  for (final code in [1302, 1303]) {
    testWidgets(
      'single forward preserves definitive $code rejection without an unknown replay lock',
      (tester) async {
        final original = harness.overrideCall!;
        harness.overrideCall = (call) async {
          if (call.method == 'sendMessage') {
            throw PlatformException(code: '$code');
          }
          return original(call);
        };
        await mount(tester);
        try {
          await openConfirmation(tester);
          await tester.tap(find.widgetWithText(TextButton, '发送'));
          await tester.pumpAndSettle();
          expect(
            harness.calls.where((c) => c.method == 'sendMessage'),
            hasLength(1),
          );
          expect(find.textContaining('转发结果未确认'), findsNothing);
          expect(find.textContaining('前往核对'), findsNothing);
          expect(
            tester
                .widget<ListTile>(
                  find.ancestor(
                    of: find.text('目标会话'),
                    matching: find.byType(ListTile),
                  ),
                )
                .onTap,
            isNotNull,
          );
          expect(
            harness.calls.where(
              (c) =>
                  c.method == 'createCustomMessage' ||
                  c.method == 'insertSingleMessageToLocalStorage',
            ),
            isEmpty,
          );
          expect(tester.takeException(), isNull);
        } finally {
          await tester.runAsync(harness.dispose);
          await tester.pumpWidget(const SizedBox.shrink());
        }
      },
    );
  }

  testWidgets(
    'merge picker route retirement also fences the original native dispatch callback',
    (tester) async {
      final entered = Completer<void>(), release = Completer<void>();
      final original = harness.overrideCall!;
      harness.overrideCall = (call) async {
        if (call.method == 'createMergerMessage') {
          entered.complete();
          await release.future;
          final args = call.arguments as Map;
          return jsonEncode({
            ...SdkHarness.message(
              id: 'new-merge',
              text: '',
              sender: 'alice',
              status: 1,
            ),
            'contentType': 107,
            'mergeElem': {
              'title': args['title'],
              'multiMessage': args['messageList'],
            },
          });
        }
        return original(call);
      };
      await mount(tester);
      try {
        await tester.longPress(find.text('可直接转发的原文'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('多选消息'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('合并转发'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('目标会话'));
        await tester.pumpAndSettle();
        await awaitNativeCreate(tester, entered);
        final navigator = Navigator.of(
          tester.element(find.byType(ChatForwardPage)),
        );
        final covered = navigator.push<void>(
          MaterialPageRoute<void>(
            builder: (_) => const Scaffold(body: Text('另一个页面')),
          ),
        );
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 350));
        navigator.pop();
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 350));
        await covered;
        release.complete();
        await tester.pumpAndSettle();
        expect(
          harness.calls.where((c) => c.method == 'createMergerMessage'),
          hasLength(1),
        );
        expect(harness.calls.where((c) => c.method == 'sendMessage'), isEmpty);
        expect(find.textContaining('转发结果未确认'), findsNothing);
        expect(find.text('此转发已失效，请重新确认收件人。'), findsOneWidget);
        expect(tester.takeException(), isNull);
      } finally {
        if (!release.isCompleted) {
          release.complete();
        }
        await tester.runAsync(harness.dispose);
        await tester.pumpWidget(const SizedBox.shrink());
      }
    },
  );

  testWidgets(
    'single review handoff rejects owner replacement after push before its first builder without new account SDK reads',
    (tester) async {
      final original = harness.overrideCall!;
      harness.overrideCall = (call) async {
        if (call.method == 'sendMessage') {
          throw PlatformException(code: '10005');
        }
        return original(call);
      };
      await mount(tester);
      final entered = Completer<void>(), release = Completer<void>();
      try {
        await openConfirmation(tester);
        await tester.tap(find.widgetWithText(TextButton, '发送'));
        await tester.pumpAndSettle();
        final sourceRoute = ModalRoute.of(
          tester.element(find.byType(ChatForwardPage)),
        )!;
        harness.overrideCall = (call) async {
          if (call.method == 'getMultipleConversation') {
            entered.complete();
            await release.future;
          }
          return original(call);
        };
        await tester.tap(find.text('前往核对 · 账号 ID：carol'));
        for (var i = 0; i < 20 && !entered.isCompleted; i++) {
          await tester.pump(const Duration(milliseconds: 50));
        }
        expect(entered.isCompleted, isTrue);
        release.complete();
        // Drain the real metadata continuation/push without building a frame.
        await tester.idle();
        expect(sourceRoute.isCurrent, isFalse);
        expect(find.byType(ChatPage), findsNothing);
        await tester.runAsync(() async {
          await harness.session.signOut();
          harness.userId = 'replacement-owner';
          await harness.session.signIn((_) async => harness.credentials);
        });
        final callsBeforeBuilder = harness.calls.length;
        await tester.pumpAndSettle();
        expect(find.text('原账号或会话已失效，请返回重新核对'), findsOneWidget);
        expect(find.byType(ChatPage), findsNothing);
        expect(
          harness.calls
              .skip(callsBeforeBuilder)
              .where(
                (c) =>
                    c.method == 'getMultipleConversation' ||
                    c.method == 'getAdvancedHistoryMessageList',
              ),
          isEmpty,
        );
        expect(
          harness.calls.where((c) => c.method == 'sendMessage'),
          hasLength(1),
        );
        expect(
          harness.calls.where((c) => c.method == 'createForwardMessage'),
          hasLength(1),
        );
        expect(tester.takeException(), isNull);
      } finally {
        if (!release.isCompleted) {
          release.complete();
        }
        await tester.runAsync(harness.dispose);
        await tester.pumpWidget(const SizedBox.shrink());
      }
    },
  );
}
